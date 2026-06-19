package engine

import (
	"context"
	"testing"
	"time"

	"github.com/cole/fetch/internal/config"
	"github.com/cole/fetch/internal/core"
	"github.com/cole/fetch/internal/providers/artifacts"
	"github.com/cole/fetch/internal/providers/fetch"
	"github.com/cole/fetch/internal/providers/search"
	"github.com/cole/fetch/internal/providers/store"
)

// queryAwareSearch fails until the query becomes "good" — used to drive the
// fallback/self-heal path.
type queryAwareSearch struct{ calls []string }

func (q *queryAwareSearch) Search(_ context.Context, query string, _ search.Options) ([]search.Result, error) {
	q.calls = append(q.calls, query)
	if query != "good" {
		return nil, nil // empty → engine treats as failure
	}
	return []search.Result{{Title: "T", URL: "https://x", Content: "body"}}, nil
}

type fakeReplanner struct {
	decisions []Decision
	calls     int
}

func (f *fakeReplanner) Replan(_ context.Context, _ ReplanRequest) (Decision, error) {
	d := f.decisions[min(f.calls, len(f.decisions)-1)]
	f.calls++
	return d, nil
}

func fixedDeps(d Deps) Deps {
	if d.Config.Search.MaxResults == 0 {
		d.Config = config.Default()
	}
	d.Now = func() time.Time { return time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC) }
	n := 0
	d.IDGen = func() string { n++; return "run-fixed" }
	return d
}

func storePipeline() core.Pipeline {
	return core.Pipeline{
		ID:     "p1",
		Schema: []core.Field{{Name: "x", Type: core.FieldString}},
		Plan: []core.Step{
			{ID: "store", Type: core.StepStore, Params: map[string]any{"rows": "{{input.rows}}"}},
		},
	}
}

func TestRunStoresRowsAndRecords(t *testing.T) {
	fs := store.NewFakeStore()
	e := New(fixedDeps(Deps{
		Store:     fs,
		Artifacts: artifacts.NewFakeArtifacts(),
	}))
	res, err := e.Run(context.Background(), storePipeline(),
		map[string]any{"rows": []any{map[string]any{"x": "a"}}}, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Run.Status != core.RunOK {
		t.Fatalf("status = %s", res.Run.Status)
	}
	if len(fs.Rows["p1"]) != 1 {
		t.Fatalf("rows = %d", len(fs.Rows["p1"]))
	}
	if len(fs.Traces) != 1 || fs.Traces[0].Status != "ok" {
		t.Fatalf("traces = %+v", fs.Traces)
	}
	// RecordRun called at start (running) and end (ok).
	if len(fs.Runs) != 2 || fs.Runs[1].Status != core.RunOK {
		t.Fatalf("runs = %+v", fs.Runs)
	}
}

func TestRunEmitsEventsInOrder(t *testing.T) {
	events := make(chan Event, 16)
	e := New(fixedDeps(Deps{Store: store.NewFakeStore(), Artifacts: artifacts.NewFakeArtifacts()}))
	_, err := e.Run(context.Background(), storePipeline(),
		map[string]any{"rows": []any{}}, events)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	close(events)
	var types []EventType
	for ev := range events {
		types = append(types, ev.Type)
	}
	want := []EventType{EventRunStarted, EventStepStarted, EventStepFinished, EventRunFinished}
	if len(types) != len(want) {
		t.Fatalf("events = %v", types)
	}
	for i := range want {
		if types[i] != want[i] {
			t.Fatalf("events = %v, want %v", types, want)
		}
	}
}

func TestSearchFetchHappyPath(t *testing.T) {
	srch := &queryAwareSearch{}
	ff := &fetch.FakeFetcher{Pages: map[string]fetch.Page{
		"https://x": {URL: "https://x", StatusCode: 200, ContentType: "text/html", Raw: []byte("<html>body</html>"), Text: "body"},
	}}
	fs := store.NewFakeStore()
	e := New(fixedDeps(Deps{Search: srch, Fetcher: ff, Store: fs, Artifacts: artifacts.NewFakeArtifacts()}))
	p := core.Pipeline{ID: "p2", Plan: []core.Step{
		{ID: "search", Type: core.StepSearch, Params: map[string]any{"query": "good"}},
		{ID: "fetch", Type: core.StepFetch, DependsOn: []string{"search"}, Params: map[string]any{"urls": "{{steps.search.urls}}"}},
	}}
	res, err := e.Run(context.Background(), p, nil, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Run.Status != core.RunOK {
		t.Fatalf("status = %s; traces=%+v", res.Run.Status, res.Traces)
	}
	if len(ff.URLs) != 1 || ff.URLs[0] != "https://x" {
		t.Fatalf("fetcher urls = %v", ff.URLs)
	}
}

func TestFallbackAdaptSelfHeal(t *testing.T) {
	srch := &queryAwareSearch{}
	repl := &fakeReplanner{decisions: []Decision{{
		Action: ActionAdapt,
		Step:   core.Step{ID: "search", Type: core.StepSearch, Params: map[string]any{"query": "good"}},
		Reason: "broaden",
	}}}
	repo := newTempRepo(t)
	e := New(fixedDeps(Deps{
		Search: srch, Store: store.NewFakeStore(), Artifacts: artifacts.NewFakeArtifacts(),
		Replanner: repl, Repo: repo, AutoPromote: true, MaxRetries: 2,
	}))
	p := core.Pipeline{ID: "p3", Version: 1, Plan: []core.Step{
		{ID: "search", Type: core.StepSearch, Params: map[string]any{"query": "bad"}},
	}}
	if err := repo.Save(p); err != nil {
		t.Fatalf("seed save: %v", err)
	}
	res, err := e.Run(context.Background(), p, nil, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Run.Status != core.RunOK {
		t.Fatalf("status = %s", res.Run.Status)
	}
	if len(res.Candidates) != 1 || res.Candidates[0].Adapted.Params["query"] != "good" {
		t.Fatalf("candidates = %+v", res.Candidates)
	}
	if !res.Traces[0].FallbackUsed {
		t.Fatal("expected FallbackUsed on trace")
	}
	// auto-promote saved a bumped pipeline with the adapted step.
	saved, err := repo.Load("p3")
	if err != nil {
		t.Fatalf("load promoted: %v", err)
	}
	if saved.Version != 2 || saved.Plan[0].Params["query"] != "good" {
		t.Fatalf("not promoted: %+v", saved)
	}
}

func TestFallbackRetryBudgetExhausted(t *testing.T) {
	srch := &queryAwareSearch{}
	repl := &fakeReplanner{decisions: []Decision{{
		Action: ActionAdapt,
		Step:   core.Step{ID: "search", Type: core.StepSearch, Params: map[string]any{"query": "still-bad"}},
	}}}
	e := New(fixedDeps(Deps{Search: srch, Store: store.NewFakeStore(), Artifacts: artifacts.NewFakeArtifacts(), Replanner: repl, MaxRetries: 2}))
	p := core.Pipeline{ID: "p4", Plan: []core.Step{
		{ID: "search", Type: core.StepSearch, Params: map[string]any{"query": "bad"}},
	}}
	res, err := e.Run(context.Background(), p, nil, nil)
	if err == nil {
		t.Fatal("expected run error after exhausting retries")
	}
	if res.Run.Status != core.RunFailed {
		t.Fatalf("status = %s", res.Run.Status)
	}
	// 1 initial + 2 retries = 3 search attempts.
	if len(srch.calls) != 3 {
		t.Fatalf("search attempts = %d", len(srch.calls))
	}
}

func TestFallbackSkip(t *testing.T) {
	srch := &queryAwareSearch{}
	repl := &fakeReplanner{decisions: []Decision{{Action: ActionSkip}}}
	e := New(fixedDeps(Deps{Search: srch, Store: store.NewFakeStore(), Artifacts: artifacts.NewFakeArtifacts(), Replanner: repl, MaxRetries: 2}))
	p := core.Pipeline{ID: "p5", Plan: []core.Step{
		{ID: "search", Type: core.StepSearch, Params: map[string]any{"query": "bad"}},
	}}
	res, err := e.Run(context.Background(), p, nil, nil)
	if err != nil {
		t.Fatalf("skip should not error: %v", err)
	}
	if res.Run.Status != core.RunPartial {
		t.Fatalf("status = %s", res.Run.Status)
	}
}

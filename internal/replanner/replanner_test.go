package replanner

import (
	"context"
	"testing"
	"time"

	"github.com/cole/fetch/internal/agent"
	"github.com/cole/fetch/internal/config"
	"github.com/cole/fetch/internal/core"
	"github.com/cole/fetch/internal/engine"
	"github.com/cole/fetch/internal/providers/artifacts"
	"github.com/cole/fetch/internal/providers/search"
	"github.com/cole/fetch/internal/providers/store"
)

var _ engine.Replanner = (*Replanner)(nil)

func req() engine.ReplanRequest {
	return engine.ReplanRequest{
		Step:    core.Step{ID: "search", Type: core.StepSearch, Params: map[string]any{"query": "bad"}},
		Params:  map[string]any{"query": "bad"},
		Attempt: 1,
		Err:     "search: no results",
	}
}

func TestReplanAdaptForcesSameIDAndType(t *testing.T) {
	llm := &agent.FakeLLM{Responses: []agent.ChatResponse{{
		Content: `{"action":"adapt","reason":"broaden","step":{"id":"WRONG","name":"s","type":"store","params":{"query":"good"},"depends_on":[]}}`,
	}}}
	d, err := New(llm, config.Default()).Replan(context.Background(), req())
	if err != nil {
		t.Fatalf("replan: %v", err)
	}
	if d.Action != engine.ActionAdapt {
		t.Fatalf("action = %q", d.Action)
	}
	if d.Step.ID != "search" || d.Step.Type != core.StepSearch {
		t.Fatalf("adapt must keep original id/type: %+v", d.Step)
	}
	if d.Step.Params["query"] != "good" {
		t.Fatalf("params = %+v", d.Step.Params)
	}
}

func TestReplanSkipAndAbort(t *testing.T) {
	for _, tc := range []struct {
		content string
		want    string
	}{
		{`{"action":"skip","reason":"optional"}`, engine.ActionSkip},
		{`{"action":"abort","reason":"dead"}`, engine.ActionAbort},
		{`not json`, engine.ActionAbort}, // malformed -> abort, no error
	} {
		llm := &agent.FakeLLM{Responses: []agent.ChatResponse{{Content: tc.content}}}
		d, err := New(llm, config.Default()).Replan(context.Background(), req())
		if err != nil {
			t.Fatalf("replan(%q): %v", tc.content, err)
		}
		if d.Action != tc.want {
			t.Fatalf("content %q -> action %q, want %q", tc.content, d.Action, tc.want)
		}
	}
}

// queryAwareSearch fails until the query becomes "good".
type queryAwareSearch struct{ calls []string }

func (q *queryAwareSearch) Search(_ context.Context, query string, _ search.Options) ([]search.Result, error) {
	q.calls = append(q.calls, query)
	if query != "good" {
		return nil, nil
	}
	return []search.Result{{Title: "T", URL: "https://x", Content: "body"}}, nil
}

func TestReplannerDrivesEngineSelfHeal(t *testing.T) {
	llm := &agent.FakeLLM{Responses: []agent.ChatResponse{{
		Content: `{"action":"adapt","reason":"broaden","step":{"id":"search","name":"search","type":"search","params":{"query":"good"},"depends_on":[]}}`,
	}}}
	e := engine.New(engine.Deps{
		Config:     config.Default(),
		Search:     &queryAwareSearch{},
		Store:      store.NewFakeStore(),
		Artifacts:  artifacts.NewFakeArtifacts(),
		Replanner:  New(llm, config.Default()),
		MaxRetries: 2,
		Now:        func() time.Time { return time.Unix(0, 0) },
		IDGen:      func() string { return "run-1" },
	})
	p := core.Pipeline{ID: "p", Plan: []core.Step{
		{ID: "search", Type: core.StepSearch, Params: map[string]any{"query": "bad"}},
	}}
	res, err := e.Run(context.Background(), p, nil, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Run.Status != core.RunOK {
		t.Fatalf("status = %s", res.Run.Status)
	}
	if len(res.Candidates) != 1 || res.Candidates[0].Adapted.Params["query"] != "good" {
		t.Fatalf("expected self-heal candidate: %+v", res.Candidates)
	}
}

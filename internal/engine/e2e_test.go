package engine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cole/fetch/internal/agent"
	"github.com/cole/fetch/internal/config"
	"github.com/cole/fetch/internal/core"
	"github.com/cole/fetch/internal/providers/artifacts"
	"github.com/cole/fetch/internal/providers/fetch"
	"github.com/cole/fetch/internal/providers/search"
	"github.com/cole/fetch/internal/providers/store"
)

// staticSearch returns one URL pointing at the test HTTP server.
type staticSearch struct{ url string }

func (s staticSearch) Search(_ context.Context, _ string, _ search.Options) ([]search.Result, error) {
	return []search.Result{{Title: "Doc", URL: s.url, Content: "snippet"}}, nil
}

func TestEndToEndSearchFetchExtractStore(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><article><h1>Parts</h1><p>Cross reference XYZ-999 for part A1.</p></article></body></html>`))
	}))
	defer srv.Close()

	llm := &agent.FakeLLM{Responses: []agent.ChatResponse{{
		Content: `{"rows":[{"part":"A1","cross_ref":"XYZ-999"}]}`,
	}}}
	fs := store.NewFakeStore()
	e := New(fixedDeps(Deps{
		Config:    config.Default(),
		LLM:       llm,
		Search:    staticSearch{url: srv.URL},
		Fetcher:   fetch.NewHTTP("test-agent", 10, 1<<20),
		Artifacts: artifacts.NewFakeArtifacts(),
		Store:     fs,
	}))
	p := core.Pipeline{
		ID:     "xref",
		Schema: []core.Field{{Name: "part", Type: core.FieldString}, {Name: "cross_ref", Type: core.FieldString}},
		Plan: []core.Step{
			{ID: "search", Type: core.StepSearch, Params: map[string]any{"query": "{{input.part}}"}},
			{ID: "fetch", Type: core.StepFetch, DependsOn: []string{"search"}, Params: map[string]any{"urls": "{{steps.search.urls}}"}},
			{ID: "extract", Type: core.StepExtract, DependsOn: []string{"fetch"}, Params: map[string]any{"pages": "{{steps.fetch.pages}}"}},
			{ID: "transform", Type: core.StepTransform, DependsOn: []string{"extract"}, Params: map[string]any{"op": "dedup", "rows": "{{steps.extract.rows}}"}},
			{ID: "store", Type: core.StepStore, DependsOn: []string{"transform"}, Params: map[string]any{"rows": "{{steps.transform.rows}}"}},
		},
	}
	res, err := e.Run(context.Background(), p, map[string]any{"part": "A1"}, nil)
	if err != nil {
		t.Fatalf("run: %v; traces=%+v", err, res.Traces)
	}
	if res.Run.Status != core.RunOK {
		t.Fatalf("status = %s", res.Run.Status)
	}
	rows := fs.Rows["xref"]
	if len(rows) != 1 || rows[0]["cross_ref"] != "XYZ-999" {
		t.Fatalf("rows = %+v", rows)
	}
	if len(res.Traces) != 5 {
		t.Fatalf("expected 5 traces, got %d", len(res.Traces))
	}
}

package engine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cole/fetch/internal/agent"
	"github.com/cole/fetch/internal/core"
	"github.com/cole/fetch/internal/providers/artifacts"
	"github.com/cole/fetch/internal/providers/fetch"
	"github.com/cole/fetch/internal/providers/store"
)

func TestBuildExtractSchema(t *testing.T) {
	raw := buildExtractSchema([]core.Field{
		{Name: "part", Type: core.FieldString},
		{Name: "price", Type: core.FieldFloat},
		{Name: "qty", Type: core.FieldInt},
	})
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("schema not valid json: %v", err)
	}
	if m["type"] != "object" {
		t.Fatalf("root type = %v", m["type"])
	}
	// drill: properties.rows.items.properties.price.type == "number"
	props := m["properties"].(map[string]any)
	rows := props["rows"].(map[string]any)
	items := rows["items"].(map[string]any)
	iprops := items["properties"].(map[string]any)
	if iprops["price"].(map[string]any)["type"] != "number" {
		t.Fatalf("price type = %v", iprops["price"])
	}
	if iprops["qty"].(map[string]any)["type"] != "integer" {
		t.Fatalf("qty type = %v", iprops["qty"])
	}
}

func TestExtractExecutorParsesRows(t *testing.T) {
	llm := &agent.FakeLLM{Responses: []agent.ChatResponse{{
		Content:          `{"rows":[{"part":"A1","price":12.5},{"part":"B2","price":3}]}`,
		PromptTokens:     10,
		CompletionTokens: 5,
	}}}
	ff := &fetch.FakeFetcher{Pages: map[string]fetch.Page{
		"https://x": {URL: "https://x", StatusCode: 200, ContentType: "text/html", Raw: []byte("<html>p</html>"), Text: "part A1 costs 12.5"},
	}}
	srch := &queryAwareSearch{}
	fs := store.NewFakeStore()
	e := New(fixedDeps(Deps{LLM: llm, Search: srch, Fetcher: ff, Store: fs, Artifacts: artifacts.NewFakeArtifacts()}))
	p := core.Pipeline{
		ID:     "xp",
		Schema: []core.Field{{Name: "part", Type: core.FieldString}, {Name: "price", Type: core.FieldFloat}},
		Plan: []core.Step{
			{ID: "search", Type: core.StepSearch, Params: map[string]any{"query": "good"}},
			{ID: "fetch", Type: core.StepFetch, DependsOn: []string{"search"}, Params: map[string]any{"urls": "{{steps.search.urls}}"}},
			{ID: "extract", Type: core.StepExtract, DependsOn: []string{"fetch"}, Params: map[string]any{"pages": "{{steps.fetch.pages}}"}},
			{ID: "store", Type: core.StepStore, DependsOn: []string{"extract"}, Params: map[string]any{"rows": "{{steps.extract.rows}}"}},
		},
	}
	res, err := e.Run(context.Background(), p, nil, nil)
	if err != nil {
		t.Fatalf("run: %v; traces=%+v", err, res.Traces)
	}
	if res.Run.Status != core.RunOK {
		t.Fatalf("status = %s", res.Run.Status)
	}
	if len(fs.Rows["xp"]) != 2 {
		t.Fatalf("stored rows = %d", len(fs.Rows["xp"]))
	}
	// the LLM was asked with a JSON-schema format and saw the page text.
	if len(llm.Calls) != 1 {
		t.Fatalf("llm calls = %d", len(llm.Calls))
	}
	if llm.Calls[0].Format == nil {
		t.Fatal("expected structured-output Format on the extract call")
	}
	joined := llm.Calls[0].Messages[len(llm.Calls[0].Messages)-1].Content
	if !strings.Contains(joined, "part A1 costs 12.5") {
		t.Fatalf("page text not in prompt: %q", joined)
	}
}

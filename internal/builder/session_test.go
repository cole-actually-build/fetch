package builder

import (
	"context"
	"testing"
	"time"

	"github.com/cole/fetch/internal/agent"
	"github.com/cole/fetch/internal/config"
	"github.com/cole/fetch/internal/pipeline"
	"github.com/cole/fetch/internal/providers/store"
)

func fixedSessionDeps(t *testing.T, llm agent.LLM) SessionDeps {
	t.Helper()
	return SessionDeps{
		LLM:   llm,
		Cfg:   config.Default(),
		Store: store.NewFakeStore(),
		Repo:  pipeline.NewRepository(t.TempDir()),
		Now:   func() time.Time { return time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC) },
		IDGen: func() string { return "x1" },
	}
}

// scripted responses for: interview(ready) -> design -> plan
func scriptedLLM() *agent.FakeLLM {
	return &agent.FakeLLM{Responses: []agent.ChatResponse{
		{Content: `{"question":"","ready":true,"facts":{"domain":"d","inputs":[],"output_fields":[],"source_hints":[]}}`},
		{Content: `{"name":"My Pipeline","description":"x","domain":"d","inputs":[{"name":"q","type":"string","required":true,"description":"q"}],"schema":[{"name":"title","type":"string","description":"t"}]}`},
		{Content: validPlanJSON},
	}}
}

func TestSessionCreateAndAccept(t *testing.T) {
	deps := fixedSessionDeps(t, scriptedLLM())
	fs := deps.Store.(*store.FakeStore)
	s := NewSession(deps)
	s.Start("build me a thing")

	q, ready, err := s.Reply(context.Background(), "")
	if err != nil {
		t.Fatalf("reply: %v", err)
	}
	if !ready || q != "" {
		t.Fatalf("expected ready: q=%q ready=%v", q, ready)
	}
	draft, err := s.Finalize(context.Background())
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if draft.Pipeline.Name != "My Pipeline" || len(draft.Pipeline.Plan) != 2 {
		t.Fatalf("draft = %+v", draft.Pipeline)
	}
	id, err := s.Accept(context.Background(), draft)
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if id != "my-pipeline" {
		t.Fatalf("id = %q", id)
	}
	if _, ok := fs.Tables[id]; !ok {
		t.Fatalf("table not ensured: %v", fs.Tables)
	}
	saved, err := deps.Repo.Load(id)
	if err != nil {
		t.Fatalf("load saved: %v", err)
	}
	if saved.Version != 1 || saved.CreatedAt.IsZero() {
		t.Fatalf("saved meta = %+v", saved)
	}
}

func TestSessionRedraft(t *testing.T) {
	llm := scriptedLLM()
	// add a second design+plan pair for the redraft.
	llm.Responses = append(llm.Responses,
		agent.ChatResponse{Content: `{"name":"Revised","description":"x","domain":"d","inputs":[{"name":"q","type":"string","required":true,"description":"q"}],"schema":[{"name":"title","type":"string","description":"t"}]}`},
		agent.ChatResponse{Content: validPlanJSON},
	)
	s := NewSession(fixedSessionDeps(t, llm))
	s.Start("thing")
	if _, _, err := s.Reply(context.Background(), ""); err != nil {
		t.Fatalf("reply: %v", err)
	}
	if _, err := s.Finalize(context.Background()); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	d2, err := s.Redraft(context.Background(), "rename it")
	if err != nil {
		t.Fatalf("redraft: %v", err)
	}
	if d2.Pipeline.Name != "Revised" {
		t.Fatalf("redraft name = %q", d2.Pipeline.Name)
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{"Truck Cross Ref": "truck-cross-ref", "  A/B  ": "a-b", "": "pipeline"}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Fatalf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

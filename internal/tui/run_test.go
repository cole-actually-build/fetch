package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/cole/fetch/internal/core"
	"github.com/cole/fetch/internal/engine"
	"github.com/cole/fetch/internal/pipeline"
	"github.com/cole/fetch/internal/providers/store"
)

func newFakeStore() store.Store { return store.NewFakeStore() }

func TestCoerceInput(t *testing.T) {
	params := []core.InputParam{
		{Name: "q", Type: core.FieldString, Required: true},
		{Name: "n", Type: core.FieldInt},
		{Name: "ok", Type: core.FieldBool},
	}
	out, err := coerceInput(params, map[string]string{"q": "hi", "n": "3", "ok": "true"})
	if err != nil {
		t.Fatal(err)
	}
	if out["q"] != "hi" || out["n"] != int64(3) || out["ok"] != true {
		t.Fatalf("coerced = %+v", out)
	}
	if _, err := coerceInput(params, map[string]string{"q": "", "n": "1"}); err == nil {
		t.Fatal("missing required q should error")
	}
	if _, err := coerceInput(params, map[string]string{"q": "x", "n": "notnum"}); err == nil {
		t.Fatal("bad int should error")
	}
}

func TestRunStreamsEventsAndPromotes(t *testing.T) {
	repo := pipeline.NewRepository(t.TempDir())
	p := core.Pipeline{ID: "p1", Name: "P", Version: 1, Plan: []core.Step{{ID: "s1", Type: core.StepSearch, Params: map[string]any{"q": "old"}}}}
	_ = repo.Save(p)

	eng := &fakeEngine{
		events: []engine.Event{
			{Type: engine.EventRunStarted, RunID: "r1"},
			{Type: engine.EventFallback, StepID: "s1", Message: "broadened query"},
			{Type: engine.EventRunFinished, RunID: "r1", Status: "ok"},
		},
		result: engine.RunResult{
			Run: core.Run{ID: "r1", PipelineID: "p1", Status: core.RunOK},
			Candidates: []engine.Revision{{
				StepID:  "s1",
				Adapted: core.Step{ID: "s1", Type: core.StepSearch, Params: map[string]any{"q": "new"}},
			}},
		},
	}
	svc := services{repo: repo, store: newFakeStore(), newEngine: func() EngineRunner { return eng }}
	m := newRunModel(svc, p)

	// no inputs -> submit immediately
	m, cmd := m.update(tea.KeyMsg{Type: tea.KeyCtrlS})
	if cmd == nil {
		t.Fatal("submit should start the run")
	}
	// drain events through the model
	msg := cmd()
	for {
		var next tea.Cmd
		m, next = m.update(msg)
		if next == nil {
			break
		}
		msg = next()
		if _, done := msg.(runDoneMsg); done {
			m, _ = m.update(msg)
			break
		}
	}
	if !strings.Contains(m.view(), "broadened query") {
		t.Fatalf("run log missing fallback line:\n%s", m.view())
	}
	if !strings.Contains(strings.ToLower(m.view()), "promote") {
		t.Fatalf("expected promote prompt:\n%s", m.view())
	}

	// press y -> promote saves v2
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	got, err := repo.Load("p1")
	if err != nil || got.Version != 2 || got.Plan[0].Params["q"] != "new" {
		t.Fatalf("promote did not save adapted pipeline: v=%d err=%v plan=%+v", got.Version, err, got.Plan)
	}
}

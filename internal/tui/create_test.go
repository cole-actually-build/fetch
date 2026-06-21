package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/cole/fetch/internal/builder"
	"github.com/cole/fetch/internal/core"
)

func newCreateTestModel(fs *fakeSession) createModel {
	svc := services{newSession: func() Session { return fs }}
	return newCreateModel(svc)
}

func TestCreateInterviewToDraftToAccept(t *testing.T) {
	fs := &fakeSession{
		questions: []string{"What domain?"},
		facts:     builder.Facts{Domain: "truck-parts", Inputs: []core.InputParam{{Name: "part", Type: core.FieldString}}},
		draft: builder.Draft{Pipeline: core.Pipeline{
			Name:   "Truck Parts",
			Schema: []core.Field{{Name: "cross_ref", Type: core.FieldString}},
			Plan:   []core.Step{{ID: "s1", Type: core.StepSearch}},
		}, Notes: "looks good"},
		acceptID: "truck-parts",
	}
	m := newCreateTestModel(fs)

	// enter the goal
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("trucks")})
	m, cmd := m.update(tea.KeyMsg{Type: tea.KeyEnter})
	if fs.started != "trucks" {
		t.Fatalf("Start not called with goal, got %q", fs.started)
	}
	// first replyCmd should have run (msg empty) -> question
	rm := cmd().(replyMsg)
	m, _ = m.update(rm)
	if !strings.Contains(m.view(), "What domain?") {
		t.Fatalf("view missing question:\n%s", m.view())
	}
	// live facts pane reflects Facts()
	if !strings.Contains(m.view(), "truck-parts") {
		t.Fatalf("view missing live facts:\n%s", m.view())
	}

	// answer the question; the fake is now exhausted so the next Reply is ready=true
	m = setInput(m, "ABC")
	m, cmd = m.update(tea.KeyMsg{Type: tea.KeyEnter})
	rm = cmd().(replyMsg)
	if !rm.ready {
		t.Fatalf("second reply should be ready")
	}
	// feeding the ready replyMsg auto-issues finalize
	m, cmd = m.update(rm)
	fm, ok := cmd().(finalizeMsg)
	if !ok {
		t.Fatalf("expected finalizeMsg, got %T", cmd())
	}
	m, _ = m.update(fm)
	if !strings.Contains(m.view(), "cross_ref") {
		t.Fatalf("draft review should show schema:\n%s", m.view())
	}

	// accept
	m, cmd = m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	am, ok := cmd().(acceptMsg)
	if !ok {
		t.Fatalf("expected acceptMsg, got %T", cmd())
	}
	if am.id != "truck-parts" {
		t.Fatalf("accept id = %q", am.id)
	}
	_, cmd = m.update(am)
	if nav, ok := cmd().(navMsg); !ok || nav.to != screenHome {
		t.Fatalf("accept should navigate home")
	}
}

func TestCreateCancelReturnsHome(t *testing.T) {
	fs := &fakeSession{}
	m := newCreateTestModel(fs)
	m = setInput(m, "/cancel")
	_, cmd := m.update(tea.KeyMsg{Type: tea.KeyEnter})
	if nav, ok := cmd().(navMsg); !ok || nav.to != screenHome {
		t.Fatalf("/cancel should navigate home")
	}
}

func setInput(m createModel, s string) createModel {
	m.input.SetValue(s)
	return m
}

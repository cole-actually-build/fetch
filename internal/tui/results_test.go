package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/cole/fetch/internal/core"
	"github.com/cole/fetch/internal/providers/store"
)

func TestResultsDrillDown(t *testing.T) {
	fs := store.NewFakeStore()
	ctx := context.Background()
	p := core.Pipeline{ID: "p1", Name: "P", Schema: []core.Field{{Name: "title", Type: core.FieldString}}}
	_ = fs.EnsureTable(ctx, "p1", p.Schema)
	_ = fs.RecordRun(ctx, core.Run{ID: "r1", PipelineID: "p1", Status: core.RunOK})
	_ = fs.AppendRows(ctx, "p1", p.Schema, "r1", []map[string]any{{"title": "Hello"}})
	_ = fs.RecordTrace(ctx, core.StepTrace{RunID: "r1", StepID: "s1", Status: "ok", FallbackUsed: true})

	svc := services{store: fs}
	m := newResultsModel(svc, p)

	// load runs
	m, _ = m.update(m.loadRuns()())
	if !strings.Contains(m.view(), "r1") {
		t.Fatalf("runs view missing run id:\n%s", m.view())
	}
	// enter -> rows
	m, cmd := m.update(tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = m.update(cmd())
	if !strings.Contains(m.view(), "Hello") {
		t.Fatalf("rows view missing result:\n%s", m.view())
	}
	// esc -> back to runs
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyEsc})
	if !strings.Contains(m.view(), "r1") {
		t.Fatalf("esc should return to runs:\n%s", m.view())
	}
	// t -> traces
	m, cmd = m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	m, _ = m.update(cmd())
	if !strings.Contains(m.view(), "s1") {
		t.Fatalf("traces view missing step:\n%s", m.view())
	}
}

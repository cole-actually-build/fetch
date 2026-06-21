package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/cole/fetch/internal/core"
	"github.com/cole/fetch/internal/pipeline"
)

func testServices(t *testing.T) (services, *pipeline.Repository) {
	t.Helper()
	repo := pipeline.NewRepository(t.TempDir())
	svc := services{repo: repo}
	return svc, repo
}

func TestHomeListsAndDeletes(t *testing.T) {
	svc, repo := testServices(t)
	if err := repo.Save(core.Pipeline{ID: "p1", Name: "Alpha", Domain: "d", Version: 1}); err != nil {
		t.Fatal(err)
	}
	m := newHomeModel(svc)
	// initial load
	loaded := m.reload()().(pipelinesLoadedMsg)
	if len(loaded.items) != 1 {
		t.Fatalf("expected 1 pipeline, got %d", len(loaded.items))
	}
	m, _ = m.update(loaded)
	if !strings.Contains(m.view(), "Alpha") {
		t.Fatalf("view missing pipeline name:\n%s", m.view())
	}

	// press d then y -> delete
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	if !strings.Contains(m.view(), "delete") {
		t.Fatalf("expected delete confirm, view:\n%s", m.view())
	}
	m, cmd := m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if cmd == nil {
		t.Fatal("delete should issue a reload cmd")
	}
	if _, err := repo.Load("p1"); err == nil {
		t.Fatal("pipeline should have been deleted")
	}
}

func TestHomeNavigationKeys(t *testing.T) {
	svc, repo := testServices(t)
	_ = repo.Save(core.Pipeline{ID: "p1", Name: "Alpha", Version: 1})
	m := newHomeModel(svc)
	m, _ = m.update(m.reload()())

	_, cmd := m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	if nav, ok := cmd().(navMsg); !ok || nav.to != screenCreate {
		t.Fatalf("c should navigate to create")
	}
	_, cmd = m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if nav, ok := cmd().(navMsg); !ok || nav.to != screenRun || nav.pipeline.ID != "p1" {
		t.Fatalf("r should navigate to run with selected pipeline")
	}
	_, cmd = m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	if nav, ok := cmd().(navMsg); !ok || nav.to != screenResults || nav.pipeline.ID != "p1" {
		t.Fatalf("v should navigate to results with selected pipeline")
	}
}

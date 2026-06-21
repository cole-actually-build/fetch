package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/cole/fetch/internal/core"
	"github.com/cole/fetch/internal/pipeline"
)

func TestRootNavigatesBetweenScreens(t *testing.T) {
	repo := pipeline.NewRepository(t.TempDir())
	_ = repo.Save(core.Pipeline{ID: "p1", Name: "Alpha", Version: 1})
	d := Deps{
		Repo:       repo,
		Store:      newFakeStore(),
		NewSession: func() Session { return &fakeSession{} },
		NewEngine:  func() EngineRunner { return &fakeEngine{} },
	}
	m := New(d)
	// Init loads the home list
	_ = m.Init()
	m2, _ := m.Update(pipelinesLoadedMsg{items: []core.Pipeline{{ID: "p1", Name: "Alpha"}}})
	rm := m2.(Model)
	if !strings.Contains(rm.View(), "Alpha") {
		t.Fatalf("home not rendered:\n%s", rm.View())
	}

	// navMsg switches to create
	m3, _ := rm.Update(navMsg{to: screenCreate})
	if m3.(Model).screen != screenCreate {
		t.Fatal("should be on create screen")
	}

	// window size propagates without panic
	m4, _ := m3.(Model).Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	if m4.(Model).width != 100 {
		t.Fatal("width not stored")
	}

	// ctrl+c quits
	_, cmd := m4.(Model).Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("ctrl+c should return a quit cmd")
	}
}

func TestRootSwallowsEscWhileRunStreaming(t *testing.T) {
	repo := pipeline.NewRepository(t.TempDir())
	d := Deps{
		Repo:       repo,
		Store:      newFakeStore(),
		NewSession: func() Session { return &fakeSession{} },
		NewEngine:  func() EngineRunner { return &fakeEngine{} },
	}
	m := New(d)
	m.screen = screenRun
	m.run = newRunModel(m.svc, core.Pipeline{ID: "p1", Name: "P"})
	m.run.phase = runStreaming
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if out.(Model).screen != screenRun {
		t.Fatalf("esc during streaming must not leave the run screen, got screen=%v", out.(Model).screen)
	}
}

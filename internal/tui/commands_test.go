package tui

import (
	"testing"

	"github.com/cole/fetch/internal/core"
	"github.com/cole/fetch/internal/engine"
)

func TestWaitEventDrainsPastBuffer(t *testing.T) {
	ch := make(chan engine.Event, 8)
	const n = 200
	go func() {
		for i := 0; i < n; i++ {
			ch <- engine.Event{Type: engine.EventStepStarted, StepID: "s"}
		}
		close(ch)
	}()
	got := 0
	for {
		msg := waitEvent(ch)()
		if _, closed := msg.(eventsClosedMsg); closed {
			break
		}
		if _, ok := msg.(eventMsg); !ok {
			t.Fatalf("unexpected msg %T", msg)
		}
		got++
	}
	if got != n {
		t.Fatalf("drained %d events, want %d", got, n)
	}
}

func TestPromoteSwapsStepAndBumpsVersion(t *testing.T) {
	p := core.Pipeline{
		Version: 3,
		Plan: []core.Step{
			{ID: "a", Type: core.StepSearch, Params: map[string]any{"q": "old"}},
			{ID: "b", Type: core.StepFetch},
		},
	}
	revs := []engine.Revision{{
		StepID:   "a",
		Original: p.Plan[0],
		Adapted:  core.Step{ID: "a", Type: core.StepSearch, Params: map[string]any{"q": "new"}},
	}}
	out := promote(p, revs)
	if out.Version != 4 {
		t.Fatalf("version = %d, want 4", out.Version)
	}
	if out.Plan[0].Params["q"] != "new" {
		t.Fatalf("step a not adapted: %+v", out.Plan[0])
	}
	if out.Plan[1].ID != "b" {
		t.Fatalf("step b mutated: %+v", out.Plan[1])
	}
	if p.Plan[0].Params["q"] != "old" || p.Version != 3 {
		t.Fatalf("promote mutated the original pipeline")
	}
}

package tui

import (
	"context"

	"github.com/cole/fetch/internal/builder"
	"github.com/cole/fetch/internal/core"
	"github.com/cole/fetch/internal/engine"
)

var (
	_ Session      = (*fakeSession)(nil)
	_ EngineRunner = (*fakeEngine)(nil)
)

// fakeSession scripts an interview: it returns questions[i] for the i-th Reply,
// reports ready once the script is exhausted, and yields canned facts/draft.
type fakeSession struct {
	questions  []string
	idx        int
	facts      builder.Facts
	draft      builder.Draft
	acceptID   string
	redraftErr error
	started    string
	replies    []string
}

func (f *fakeSession) Start(goal string) { f.started = goal }

func (f *fakeSession) Reply(_ context.Context, msg string) (string, bool, error) {
	if msg != "" {
		f.replies = append(f.replies, msg)
	}
	if f.idx >= len(f.questions) {
		return "", true, nil
	}
	q := f.questions[f.idx]
	f.idx++
	return q, false, nil
}

func (f *fakeSession) Facts() builder.Facts { return f.facts }

func (f *fakeSession) Finalize(_ context.Context) (builder.Draft, error) { return f.draft, nil }

func (f *fakeSession) Redraft(_ context.Context, _ string) (builder.Draft, error) {
	return f.draft, f.redraftErr
}

func (f *fakeSession) Accept(_ context.Context, _ builder.Draft) (string, error) {
	return f.acceptID, nil
}

// fakeEngine emits a scripted sequence of events then returns a canned result.
type fakeEngine struct {
	events []engine.Event
	result engine.RunResult
	err    error
}

func (f *fakeEngine) Run(_ context.Context, _ core.Pipeline, _ map[string]any, events chan<- engine.Event) (engine.RunResult, error) {
	for _, ev := range f.events {
		events <- ev
	}
	return f.result, f.err
}

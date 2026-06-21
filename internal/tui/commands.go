package tui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/cole/fetch/internal/builder"
	"github.com/cole/fetch/internal/core"
	"github.com/cole/fetch/internal/engine"
)

type replyMsg struct {
	question string
	ready    bool
	err      error
}

type finalizeMsg struct {
	draft builder.Draft
	err   error
}

type redraftMsg struct {
	draft builder.Draft
	err   error
}

type acceptMsg struct {
	id  string
	err error
}

type eventMsg struct{ ev engine.Event }

type eventsClosedMsg struct{}

type runDoneMsg struct {
	result engine.RunResult
	err    error
}

func replyCmd(s Session, msg string) tea.Cmd {
	return func() tea.Msg {
		q, ready, err := s.Reply(context.Background(), msg)
		return replyMsg{question: q, ready: ready, err: err}
	}
}

func finalizeCmd(s Session) tea.Cmd {
	return func() tea.Msg {
		d, err := s.Finalize(context.Background())
		return finalizeMsg{draft: d, err: err}
	}
}

func redraftCmd(s Session, comment string) tea.Cmd {
	return func() tea.Msg {
		d, err := s.Redraft(context.Background(), comment)
		return redraftMsg{draft: d, err: err}
	}
}

func acceptCmd(s Session, d builder.Draft) tea.Cmd {
	return func() tea.Msg {
		id, err := s.Accept(context.Background(), d)
		return acceptMsg{id: id, err: err}
	}
}

// waitEvent blocks on one engine event, returning eventsClosedMsg when the
// channel is closed. The Run screen re-issues it on each eventMsg to keep the
// engine's blocking-send channel continuously drained.
func waitEvent(ch <-chan engine.Event) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return eventsClosedMsg{}
		}
		return eventMsg{ev: ev}
	}
}

// waitDone blocks on the run goroutine's final result.
func waitDone(done <-chan runDoneMsg) tea.Cmd {
	return func() tea.Msg {
		return <-done
	}
}

// promote returns a copy of p with each adapted step swapped in by ID and the
// Version incremented. Steps not named by a revision are untouched. It lives in
// tui because it bridges engine.Revision and core.Pipeline (pipeline must not
// import engine).
func promote(p core.Pipeline, revs []engine.Revision) core.Pipeline {
	steps := make([]core.Step, len(p.Plan))
	copy(steps, p.Plan)
	for _, rev := range revs {
		for i := range steps {
			if steps[i].ID == rev.StepID {
				steps[i] = rev.Adapted
			}
		}
	}
	out := p
	out.Plan = steps
	out.Version = p.Version + 1
	return out
}

package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/cole/fetch/internal/core"
	"github.com/cole/fetch/internal/engine"
)

type runPhase int

const (
	runForm runPhase = iota
	runStreaming
	runDone
)

type runModel struct {
	svc      services
	pipeline core.Pipeline
	inputs   []textinput.Model
	focus    int
	phase    runPhase
	logVP    viewport.Model
	log      []string
	events   chan engine.Event
	done     chan runDoneMsg
	result   engine.RunResult
	rowCount int
	canProm  bool
	status   string
	width    int
	height   int
}

func newRunModel(svc services, p core.Pipeline) runModel {
	tis := make([]textinput.Model, len(p.Inputs))
	for i, in := range p.Inputs {
		ti := textinput.New()
		ti.Placeholder = string(in.Type)
		ti.Prompt = in.Name + ": "
		tis[i] = ti
	}
	if len(tis) > 0 {
		tis[0].Focus()
	}
	return runModel{svc: svc, pipeline: p, inputs: tis, logVP: viewport.New(70, 14)}
}

// coerceInput validates required inputs and converts each raw string to the
// param's type. int -> int64, float -> float64, bool -> bool, else string.
func coerceInput(params []core.InputParam, raw map[string]string) (map[string]any, error) {
	out := make(map[string]any, len(params))
	for _, p := range params {
		v := strings.TrimSpace(raw[p.Name])
		if v == "" {
			if p.Required {
				return nil, fmt.Errorf("input %q is required", p.Name)
			}
			continue
		}
		switch p.Type {
		case core.FieldInt:
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("input %q: not an integer", p.Name)
			}
			out[p.Name] = n
		case core.FieldFloat:
			f, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return nil, fmt.Errorf("input %q: not a number", p.Name)
			}
			out[p.Name] = f
		case core.FieldBool:
			b, err := strconv.ParseBool(v)
			if err != nil {
				return nil, fmt.Errorf("input %q: not a bool", p.Name)
			}
			out[p.Name] = b
		default:
			out[p.Name] = v
		}
	}
	return out, nil
}

func (m runModel) rawInputs() map[string]string {
	raw := make(map[string]string, len(m.inputs))
	for i, in := range m.pipeline.Inputs {
		raw[in.Name] = m.inputs[i].Value()
	}
	return raw
}

func (m runModel) startRun() (runModel, tea.Cmd) {
	input, err := coerceInput(m.pipeline.Inputs, m.rawInputs())
	if err != nil {
		m.status = err.Error()
		return m, nil
	}
	m.status = ""
	m.phase = runStreaming
	m.events = make(chan engine.Event, 128)
	m.done = make(chan runDoneMsg, 1)
	eng := m.svc.newEngine()
	p := m.pipeline
	events := m.events
	done := m.done
	go func() {
		res, rerr := eng.Run(context.Background(), p, input, events)
		close(events)
		done <- runDoneMsg{result: res, err: rerr}
	}()
	return m, waitEvent(m.events)
}

func (m runModel) appendLog(line string) runModel {
	m.log = append(m.log, line)
	m.logVP.SetContent(strings.Join(m.log, "\n"))
	m.logVP.GotoBottom()
	return m
}

func (m runModel) update(msg tea.Msg) (runModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.logVP.Width = msg.Width - 2
		if msg.Height > 6 {
			m.logVP.Height = msg.Height - 6
		}
		return m, nil

	case eventMsg:
		m = m.appendLog(formatEvent(msg.ev))
		return m, waitEvent(m.events)

	case eventsClosedMsg:
		return m, waitDone(m.done)

	case runDoneMsg:
		m.phase = runDone
		m.result = msg.result
		if msg.err != nil {
			m.status = "run error: " + msg.err.Error()
		}
		rows, _ := m.svc.store.ResultRows(context.Background(), m.pipeline.ID, msg.result.Run.ID)
		m.rowCount = len(rows)
		m.canProm = len(msg.result.Candidates) > 0
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	if m.phase == runForm {
		var cmd tea.Cmd
		m.inputs[m.focus], cmd = m.inputs[m.focus].Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m runModel) handleKey(msg tea.KeyMsg) (runModel, tea.Cmd) {
	switch m.phase {
	case runForm:
		switch msg.String() {
		case "ctrl+s":
			return m.startRun()
		case "tab", "down":
			m = m.cycleFocus(1)
			return m, nil
		case "shift+tab", "up":
			m = m.cycleFocus(-1)
			return m, nil
		case "enter":
			if m.focus == len(m.inputs)-1 || len(m.inputs) == 0 {
				return m.startRun()
			}
			m = m.cycleFocus(1)
			return m, nil
		}
		var cmd tea.Cmd
		m.inputs[m.focus], cmd = m.inputs[m.focus].Update(msg)
		return m, cmd

	case runDone:
		switch msg.String() {
		case "y":
			if m.canProm {
				np := promote(m.pipeline, m.result.Candidates)
				if err := m.svc.repo.Save(np); err != nil {
					m.status = "promote error: " + err.Error()
				} else {
					m.pipeline = np
					m.canProm = false
					m.status = fmt.Sprintf("promoted, now v%d", np.Version)
				}
			}
			return m, nil
		case "n":
			m.canProm = false
			return m, nil
		case "v":
			return m, func() tea.Msg { return navMsg{to: screenResults, pipeline: m.pipeline} }
		case "enter":
			return m, func() tea.Msg { return navMsg{to: screenHome} }
		}
	}
	return m, nil
}

func (m runModel) cycleFocus(d int) runModel {
	if len(m.inputs) == 0 {
		return m
	}
	m.inputs[m.focus].Blur()
	m.focus = (m.focus + d + len(m.inputs)) % len(m.inputs)
	m.inputs[m.focus].Focus()
	return m
}

func formatEvent(ev engine.Event) string {
	switch ev.Type {
	case engine.EventRunStarted:
		return headerStyle.Render("run " + ev.RunID + " started")
	case engine.EventStepStarted:
		return "▶ " + ev.StepID
	case engine.EventStepRetry:
		return warnStyle.Render("↻ retry " + ev.StepID + " — " + ev.Message)
	case engine.EventFallback:
		return warnStyle.Render("⚑ fallback " + ev.StepID + " — " + ev.Message)
	case engine.EventStepFinished:
		return okStyle.Render("✓ " + ev.StepID + " — " + ev.Status)
	case engine.EventRunFinished:
		return headerStyle.Render("run finished — " + ev.Status)
	default:
		return ev.Message
	}
}

func (m runModel) view() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Run: "+m.pipeline.Name) + "\n\n")
	switch m.phase {
	case runForm:
		if len(m.inputs) == 0 {
			b.WriteString(hintStyle.Render("no inputs — press ctrl+s or enter to run") + "\n")
		}
		for i := range m.inputs {
			b.WriteString(m.inputs[i].View() + "\n")
		}
		b.WriteString("\n" + hintStyle.Render("[tab] next  [ctrl+s] run  [esc] back"))
	default:
		b.WriteString(m.logVP.View() + "\n")
		if m.phase == runDone {
			b.WriteString(fmt.Sprintf("\nstatus: %s   rows: %d\n", m.result.Run.Status, m.rowCount))
			if m.canProm {
				b.WriteString(warnStyle.Render(fmt.Sprintf("Run adapted %d step(s) via fallback. Promote into saved pipeline? (y/n)", len(m.result.Candidates))) + "\n")
			}
			b.WriteString(hintStyle.Render("[v]iew results  [enter] home"))
		}
	}
	if m.status != "" {
		b.WriteString("\n" + errStyle.Render(m.status))
	}
	return b.String()
}

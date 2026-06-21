package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/cole/fetch/internal/builder"
)

type createPhase int

const (
	phaseGoal createPhase = iota
	phaseInterview
	phaseReview
	phaseThinking
)

type createModel struct {
	svc      services
	sess     Session
	input    textinput.Model
	chat     viewport.Model
	phase    createPhase
	facts    builder.Facts
	draft    builder.Draft
	hasDraft bool
	log      []string // transcript lines
	status   string
	width    int
	height   int
}

func newCreateModel(svc services) createModel {
	ti := textinput.New()
	ti.Placeholder = "describe what you want to build…"
	ti.Focus()
	vp := viewport.New(60, 12)
	return createModel{svc: svc, input: ti, chat: vp, phase: phaseGoal}
}

// chatContent word-wraps each transcript line to the chat pane width so long
// agent questions wrap instead of being truncated by the viewport.
func (m createModel) chatContent() string {
	w := m.chat.Width
	if w < 4 {
		w = 4
	}
	style := lipgloss.NewStyle().Width(w)
	wrapped := make([]string, len(m.log))
	for i, l := range m.log {
		wrapped[i] = style.Render(l)
	}
	return strings.Join(wrapped, "\n")
}

func (m createModel) appendChat(line string) createModel {
	m.log = append(m.log, line)
	m.chat.SetContent(m.chatContent())
	m.chat.GotoBottom()
	return m
}

func (m createModel) update(msg tea.Msg) (createModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.chat.Width = msg.Width/2 - 2
		if msg.Height > 6 {
			m.chat.Height = msg.Height - 6
		}
		// re-wrap existing transcript to the new width
		m.chat.SetContent(m.chatContent())
		m.chat.GotoBottom()
		return m, nil

	case replyMsg:
		m.phase = phaseInterview
		if msg.err != nil {
			m.status = "interview error: " + msg.err.Error()
			return m, nil
		}
		m.facts = m.sess.Facts()
		if msg.ready {
			m.phase = phaseThinking
			return m, finalizeCmd(m.sess)
		}
		if msg.question != "" {
			m = m.appendChat(hintStyle.Render("agent: ") + msg.question)
		}
		m.input.Focus()
		return m, nil

	case finalizeMsg:
		if msg.err != nil {
			m.status = "design error: " + msg.err.Error()
			m.phase = phaseInterview
			return m, nil
		}
		m.draft = msg.draft
		m.hasDraft = true
		m.phase = phaseReview
		m.status = ""
		return m, nil

	case redraftMsg:
		if msg.err != nil {
			m.status = "redraft error: " + msg.err.Error()
			m.phase = phaseReview
			return m, nil
		}
		m.draft = msg.draft
		m.phase = phaseReview
		m.status = ""
		return m, nil

	case acceptMsg:
		if msg.err != nil {
			m.status = "save error: " + msg.err.Error()
			m.phase = phaseReview
			return m, nil
		}
		return m, func() tea.Msg { return navMsg{to: screenHome} }

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m createModel) handleKey(msg tea.KeyMsg) (createModel, tea.Cmd) {
	switch m.phase {
	case phaseReview:
		switch msg.String() {
		case "a":
			m.phase = phaseThinking
			return m, acceptCmd(m.sess, m.draft)
		case "x":
			return m, func() tea.Msg { return navMsg{to: screenHome} }
		case "c":
			m.phase = phaseInterview // reuse input for the comment
			m.input.SetValue("")
			m.input.Focus()
			return m, nil
		}
		return m, nil

	case phaseThinking:
		return m, nil

	default: // phaseGoal / phaseInterview share the textinput
		if msg.Type != tea.KeyEnter {
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		}
		line := strings.TrimSpace(m.input.Value())
		m.input.SetValue("")
		switch line {
		case "/cancel":
			return m, func() tea.Msg { return navMsg{to: screenHome} }
		case "/done":
			m.phase = phaseThinking
			return m, finalizeCmd(m.sess)
		}
		if m.phase == phaseGoal {
			m.sess = m.svc.newSession()
			m.sess.Start(line)
			m = m.appendChat(okStyle.Render("you: ") + line)
			m.phase = phaseThinking
			return m, replyCmd(m.sess, "")
		}
		// interview turn (or a redraft comment if we just have a draft)
		m = m.appendChat(okStyle.Render("you: ") + line)
		m.phase = phaseThinking
		if m.hasDraft {
			return m, redraftCmd(m.sess, line)
		}
		return m, replyCmd(m.sess, line)
	}
}

func (m createModel) rightPane() string {
	if m.hasDraft && m.phase == phaseReview {
		var b strings.Builder
		b.WriteString(headerStyle.Render("Draft: "+m.draft.Pipeline.Name) + "\n\n")
		b.WriteString(titleStyle.Render("Schema") + "\n")
		for _, f := range m.draft.Pipeline.Schema {
			b.WriteString(fmt.Sprintf("  %s: %s\n", f.Name, f.Type))
		}
		b.WriteString("\n" + titleStyle.Render("Plan") + "\n")
		for _, s := range m.draft.Pipeline.Plan {
			b.WriteString(fmt.Sprintf("  %s (%s)\n", s.ID, s.Type))
		}
		if m.draft.Notes != "" {
			b.WriteString("\n" + hintStyle.Render(m.draft.Notes) + "\n")
		}
		return b.String()
	}
	// live facts pane
	var b strings.Builder
	b.WriteString(headerStyle.Render("Understanding") + "\n\n")
	b.WriteString("Domain: " + m.facts.Domain + "\n\n")
	b.WriteString(titleStyle.Render("Inputs") + "\n")
	for _, in := range m.facts.Inputs {
		req := ""
		if in.Required {
			req = " *"
		}
		b.WriteString(fmt.Sprintf("  %s: %s%s\n", in.Name, in.Type, req))
	}
	b.WriteString("\n" + titleStyle.Render("Output fields") + "\n")
	for _, f := range m.facts.OutputFields {
		b.WriteString(fmt.Sprintf("  %s: %s\n", f.Name, f.Type))
	}
	if len(m.facts.SourceHints) > 0 {
		b.WriteString("\n" + titleStyle.Render("Sources") + "\n  " + strings.Join(m.facts.SourceHints, ", ") + "\n")
	}
	return b.String()
}

func (m createModel) view() string {
	left := m.chat.View() + "\n"
	if m.phase == phaseReview {
		left += hintStyle.Render("[a]ccept  [c]omment  [x]cancel")
	} else if m.phase == phaseThinking {
		left += hintStyle.Render("…thinking")
	} else {
		left += m.input.View() + "\n" + hintStyle.Render("/done finalize · /cancel abort")
	}
	body := lipgloss.JoinHorizontal(lipgloss.Top, paneStyle.Render(left), paneStyle.Render(m.rightPane()))
	if m.status != "" {
		body += "\n" + errStyle.Render(m.status)
	}
	return titleStyle.Render("Create pipeline") + "\n\n" + body
}

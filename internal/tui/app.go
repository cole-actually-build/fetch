package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// Model is the root Bubble Tea model; it routes to one screen sub-model.
type Model struct {
	svc     services
	screen  screen
	home    homeModel
	create  createModel
	run     runModel
	results resultsModel
	status  string
	statErr bool
	width   int
	height  int
}

// New builds the root model from externally-constructed deps.
func New(d Deps) Model {
	svc := d.services()
	return Model{
		svc:    svc,
		screen: screenHome,
		home:   newHomeModel(svc),
	}
}

// Run starts the TUI program on the real terminal.
func Run(d Deps) error {
	p := tea.NewProgram(New(d), tea.WithAltScreen())
	_, err := p.Run()
	return err
}

func (m Model) Init() tea.Cmd {
	return m.home.reload()
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		// fan out the size to all screens
		m.home, _ = m.home.update(msg)
		m.create, _ = m.create.update(msg)
		m.run, _ = m.run.update(msg)
		m.results, _ = m.results.update(msg)
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "q":
			if m.screen == screenHome {
				return m, tea.Quit
			}
		case "esc":
			// global back-to-home from non-home screens (screens that consume
			// esc themselves handle it before this via their own update)
			if m.screen != screenHome && !m.screenConsumesEsc() {
				m.screen = screenHome
				return m, m.home.reload()
			}
		}

	case navMsg:
		return m.navigate(msg)

	case statusMsg:
		m.status, m.statErr = msg.text, msg.err
		return m, nil
	}

	return m.routeToScreen(msg)
}

// screenConsumesEsc reports whether the active screen handles esc internally.
// Results drills back with esc. The Run screen also consumes esc while
// streaming to avoid abandoning the event-drain goroutine; esc is harmlessly
// swallowed until the run completes. For all other non-home screens, esc
// returns to home globally.
func (m Model) screenConsumesEsc() bool {
	if m.screen == screenRun && m.run.phase == runStreaming {
		return true
	}
	return m.screen == screenResults
}

func (m Model) navigate(msg navMsg) (tea.Model, tea.Cmd) {
	m.screen = msg.to
	switch msg.to {
	case screenHome:
		return m, m.home.reload()
	case screenCreate:
		m.create = newCreateModel(m.svc)
		m.create, _ = m.create.update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
		return m, nil
	case screenRun:
		m.run = newRunModel(m.svc, msg.pipeline)
		m.run, _ = m.run.update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
		return m, nil
	case screenResults:
		m.results = newResultsModel(m.svc, msg.pipeline)
		m.results, _ = m.results.update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
		return m, m.results.loadRuns()
	}
	return m, nil
}

func (m Model) routeToScreen(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch m.screen {
	case screenHome:
		m.home, cmd = m.home.update(msg)
	case screenCreate:
		m.create, cmd = m.create.update(msg)
	case screenRun:
		m.run, cmd = m.run.update(msg)
	case screenResults:
		m.results, cmd = m.results.update(msg)
	}
	return m, cmd
}

func (m Model) View() string {
	var body string
	switch m.screen {
	case screenHome:
		body = m.home.view()
	case screenCreate:
		body = m.create.view()
	case screenRun:
		body = m.run.view()
	case screenResults:
		body = m.results.view()
	}
	if m.status != "" {
		style := okStyle
		if m.statErr {
			style = errStyle
		}
		body += "\n" + style.Render(m.status)
	}
	return body
}

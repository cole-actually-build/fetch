package tui

import (
	"fmt"
	"strconv"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/cole/fetch/internal/core"
)

type pipelinesLoadedMsg struct {
	items []core.Pipeline
	err   error
}

type homeModel struct {
	svc        services
	tbl        table.Model
	pipelines  []core.Pipeline
	confirming bool // delete confirmation active
	status     string
	width      int
	height     int
}

func newHomeModel(svc services) homeModel {
	t := table.New(
		table.WithColumns([]table.Column{
			{Title: "Name", Width: 24},
			{Title: "Domain", Width: 16},
			{Title: "Inputs", Width: 8},
			{Title: "Ver", Width: 5},
		}),
		table.WithFocused(true),
		table.WithHeight(12),
	)
	return homeModel{svc: svc, tbl: t}
}

func (m homeModel) reload() tea.Cmd {
	repo := m.svc.repo
	return func() tea.Msg {
		items, err := repo.List()
		return pipelinesLoadedMsg{items: items, err: err}
	}
}

func (m homeModel) setRows() homeModel {
	rows := make([]table.Row, 0, len(m.pipelines))
	for _, p := range m.pipelines {
		rows = append(rows, table.Row{p.Name, p.Domain, strconv.Itoa(len(p.Inputs)), strconv.Itoa(p.Version)})
	}
	m.tbl.SetRows(rows)
	return m
}

func (m homeModel) selected() (core.Pipeline, bool) {
	i := m.tbl.Cursor()
	if i < 0 || i >= len(m.pipelines) {
		return core.Pipeline{}, false
	}
	return m.pipelines[i], true
}

func (m homeModel) update(msg tea.Msg) (homeModel, tea.Cmd) {
	switch msg := msg.(type) {
	case pipelinesLoadedMsg:
		if msg.err != nil {
			m.status = "load error: " + msg.err.Error()
			return m, nil
		}
		m.pipelines = msg.items
		m.status = ""
		return m.setRows(), nil
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tea.KeyMsg:
		if m.confirming {
			switch msg.String() {
			case "y":
				m.confirming = false
				p, ok := m.selected()
				if !ok {
					return m, nil
				}
				if err := m.svc.repo.Delete(p.ID); err != nil {
					m.status = "delete error: " + err.Error()
					return m, nil
				}
				return m, m.reload()
			default:
				m.confirming = false
				return m, nil
			}
		}
		switch msg.String() {
		case "c":
			return m, func() tea.Msg { return navMsg{to: screenCreate} }
		case "r", "enter":
			if p, ok := m.selected(); ok {
				return m, func() tea.Msg { return navMsg{to: screenRun, pipeline: p} }
			}
		case "v":
			if p, ok := m.selected(); ok {
				return m, func() tea.Msg { return navMsg{to: screenResults, pipeline: p} }
			}
		case "d":
			if _, ok := m.selected(); ok {
				m.confirming = true
			}
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.tbl, cmd = m.tbl.Update(msg)
	return m, cmd
}

func (m homeModel) view() string {
	b := titleStyle.Render("fetch — pipelines") + "\n\n"
	if len(m.pipelines) == 0 {
		b += hintStyle.Render("No pipelines yet — press c to create one") + "\n"
	} else {
		b += m.tbl.View() + "\n"
	}
	if m.confirming {
		if p, ok := m.selected(); ok {
			b += warnStyle.Render(fmt.Sprintf("delete %q? (y/n)", p.Name)) + "\n"
		}
	}
	if m.status != "" {
		b += errStyle.Render(m.status) + "\n"
	}
	b += hintStyle.Render("[c]reate  [r]un  [v]iew results  [d]elete  [q]uit")
	return b
}

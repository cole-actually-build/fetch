package tui

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/cole/fetch/internal/core"
)

type resultsLevel int

const (
	levelRuns resultsLevel = iota
	levelRows
	levelTraces
)

type runsLoadedMsg struct {
	runs []core.Run
	err  error
}
type rowsLoadedMsg struct {
	rows []map[string]any
	err  error
}
type tracesLoadedMsg struct {
	traces []core.StepTrace
	err    error
}

type resultsModel struct {
	svc      services
	pipeline core.Pipeline
	level    resultsLevel
	runs     []core.Run
	runTbl   table.Model
	rowTbl   table.Model
	traces   []core.StepTrace
	curRun   string
	status   string
	width    int
	height   int
}

func newResultsModel(svc services, p core.Pipeline) resultsModel {
	rt := table.New(
		table.WithColumns([]table.Column{
			{Title: "Run", Width: 22}, {Title: "Status", Width: 10}, {Title: "Started", Width: 20},
		}),
		table.WithFocused(true), table.WithHeight(12),
	)
	return resultsModel{svc: svc, pipeline: p, runTbl: rt}
}

func (m resultsModel) loadRuns() tea.Cmd {
	svc, pid := m.svc, m.pipeline.ID
	return func() tea.Msg {
		runs, err := svc.store.ListRuns(context.Background(), pid)
		return runsLoadedMsg{runs: runs, err: err}
	}
}

func (m resultsModel) loadRows(runID string) tea.Cmd {
	svc, pid := m.svc, m.pipeline.ID
	return func() tea.Msg {
		rows, err := svc.store.ResultRows(context.Background(), pid, runID)
		return rowsLoadedMsg{rows: rows, err: err}
	}
}

func (m resultsModel) loadTraces(runID string) tea.Cmd {
	svc := m.svc
	return func() tea.Msg {
		tr, err := svc.store.RunTraces(context.Background(), runID)
		return tracesLoadedMsg{traces: tr, err: err}
	}
}

func (m resultsModel) selectedRun() (core.Run, bool) {
	i := m.runTbl.Cursor()
	if i < 0 || i >= len(m.runs) {
		return core.Run{}, false
	}
	return m.runs[i], true
}

func (m resultsModel) update(msg tea.Msg) (resultsModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case runsLoadedMsg:
		if msg.err != nil {
			m.status = "load error: " + msg.err.Error()
			return m, nil
		}
		m.runs = msg.runs
		rows := make([]table.Row, 0, len(m.runs))
		for _, r := range m.runs {
			rows = append(rows, table.Row{r.ID, string(r.Status), r.StartedAt.Format("2006-01-02 15:04:05")})
		}
		m.runTbl.SetRows(rows)
		m.level = levelRuns
		return m, nil

	case rowsLoadedMsg:
		if msg.err != nil {
			m.status = "load error: " + msg.err.Error()
			return m, nil
		}
		m.rowTbl = buildRowTable(m.pipeline.Schema, msg.rows)
		m.level = levelRows
		return m, nil

	case tracesLoadedMsg:
		if msg.err != nil {
			m.status = "load error: " + msg.err.Error()
			return m, nil
		}
		m.traces = msg.traces
		m.level = levelTraces
		return m, nil

	case tea.KeyMsg:
		switch m.level {
		case levelRuns:
			switch msg.String() {
			case "enter":
				if r, ok := m.selectedRun(); ok {
					m.curRun = r.ID
					return m, m.loadRows(r.ID)
				}
			case "t":
				if r, ok := m.selectedRun(); ok {
					m.curRun = r.ID
					return m, m.loadTraces(r.ID)
				}
			}
			var cmd tea.Cmd
			m.runTbl, cmd = m.runTbl.Update(msg)
			return m, cmd
		case levelRows, levelTraces:
			if msg.String() == "esc" {
				m.level = levelRuns
				return m, nil
			}
			if m.level == levelRows {
				var cmd tea.Cmd
				m.rowTbl, cmd = m.rowTbl.Update(msg)
				return m, cmd
			}
		}
	}
	return m, nil
}

func buildRowTable(schema []core.Field, rows []map[string]any) table.Model {
	cols := make([]table.Column, 0, len(schema))
	names := make([]string, 0, len(schema))
	for _, f := range schema {
		cols = append(cols, table.Column{Title: f.Name, Width: 18})
		names = append(names, f.Name)
	}
	trows := make([]table.Row, 0, len(rows))
	for _, r := range rows {
		tr := make(table.Row, 0, len(names))
		for _, n := range names {
			tr = append(tr, fmt.Sprintf("%v", r[n]))
		}
		trows = append(trows, tr)
	}
	return table.New(table.WithColumns(cols), table.WithRows(trows), table.WithFocused(true), table.WithHeight(12))
}

func (m resultsModel) view() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Results: "+m.pipeline.Name) + "\n\n")
	switch m.level {
	case levelRuns:
		if len(m.runs) == 0 {
			b.WriteString(hintStyle.Render("no runs yet") + "\n")
		} else {
			b.WriteString(m.runTbl.View() + "\n")
		}
		b.WriteString(hintStyle.Render("[enter] rows  [t] traces  [esc] back"))
	case levelRows:
		b.WriteString(m.rowTbl.View() + "\n")
		b.WriteString(hintStyle.Render("run " + m.curRun + "  ·  [esc] back to runs"))
	case levelTraces:
		b.WriteString(headerStyle.Render("traces for "+m.curRun) + "\n")
		sorted := append([]core.StepTrace(nil), m.traces...)
		sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].StepID < sorted[j].StepID })
		for _, tr := range sorted {
			fb := ""
			if tr.FallbackUsed {
				fb = warnStyle.Render(" [fallback]")
			}
			b.WriteString(fmt.Sprintf("  %s — %s%s  tokens=%s\n", tr.StepID, tr.Status, fb, strconv.Itoa(tr.Tokens)))
			if tr.OutputSummary != "" {
				b.WriteString(hintStyle.Render("      "+tr.OutputSummary) + "\n")
			}
			if tr.Error != "" {
				b.WriteString(errStyle.Render("      "+tr.Error) + "\n")
			}
			if len(tr.ArtifactRefs) > 0 {
				b.WriteString(hintStyle.Render("      artifacts: "+strings.Join(tr.ArtifactRefs, ", ")) + "\n")
			}
		}
		b.WriteString(hintStyle.Render("[esc] back to runs"))
	}
	if m.status != "" {
		b.WriteString("\n" + errStyle.Render(m.status))
	}
	return b.String()
}

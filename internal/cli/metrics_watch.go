package cli

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"
	"sliplane-cli/internal/api"
)

const metricsRefreshInterval = 5 * time.Second

type metricsRefreshMsg struct{}

type metricsResultMsg struct {
	history []timedMetric
	err     error
}

type metricsWatchModel struct {
	ctx     context.Context
	fetch   func(context.Context) ([]api.Metric, error)
	title   string
	period  string
	history []timedMetric
	updated time.Time
	width   int
	height  int
	err     error
}

func watchMetrics(command *cobra.Command, title, period string, fetch func(context.Context) ([]api.Metric, error)) error {
	ctx, cancel := context.WithCancel(command.Context())
	defer cancel()
	model := metricsWatchModel{ctx: ctx, fetch: fetch, title: title, period: period, width: 80, height: 24}
	program := tea.NewProgram(model, tea.WithInput(command.InOrStdin()), tea.WithOutput(command.OutOrStdout()), tea.WithoutSignalHandler())
	stop := context.AfterFunc(ctx, program.Quit)
	defer stop()
	final, err := program.Run()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return err
	}
	return final.(metricsWatchModel).err
}

func (m metricsWatchModel) Init() tea.Cmd {
	return m.refresh()
}

func (m metricsWatchModel) refresh() tea.Cmd {
	return func() tea.Msg {
		metrics, err := m.fetch(m.ctx)
		if err != nil {
			return metricsResultMsg{err: err}
		}
		history, err := metricHistory(metrics)
		return metricsResultMsg{history: history, err: err}
	}
}

func (m metricsWatchModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case tea.KeyPressMsg:
		switch message.String() {
		case "q":
			return m, tea.Quit
		case "ctrl+c":
			m.err = context.Canceled
			return m, tea.Quit
		}
	case tea.WindowSizeMsg:
		m.width, m.height = max(1, min(message.Width, 120)), message.Height
	case metricsRefreshMsg:
		return m, m.refresh()
	case metricsResultMsg:
		if message.err != nil {
			m.err = message.err
			return m, tea.Quit
		}
		m.history, m.updated = message.history, time.Now()
		// Schedule after the response so slow requests never overlap.
		return m, tea.Tick(metricsRefreshInterval, func(time.Time) tea.Msg { return metricsRefreshMsg{} })
	}
	return m, nil
}

func (m metricsWatchModel) View() tea.View {
	footer := m.period + " · refresh 5s · q / Ctrl+C exit"
	if !m.updated.IsZero() {
		footer += " · updated " + m.updated.Local().Format("15:04:05")
	}
	footer = lipgloss.NewStyle().Faint(true).Width(m.width).Render(footer)
	content := "Loading metrics…"
	if !m.updated.IsZero() {
		content = metricsView(m.title, m.history, m.width, 0)
		chartHeight := min(12, (m.height-lipgloss.Height(content)-lipgloss.Height(footer)-2)/2)
		if chartHeight >= 4 {
			content = metricsView(m.title, m.history, m.width, chartHeight)
		}
	}
	view := tea.NewView(content + "\n\n" + footer)
	view.AltScreen = true
	return view
}

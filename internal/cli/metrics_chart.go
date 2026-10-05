package cli

import (
	"fmt"
	"io"
	"math"
	"os"
	"slices"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/NimbleMarkets/ntcharts/v2/linechart/timeserieslinechart"
	"github.com/charmbracelet/colorprofile"
	"golang.org/x/term"
	"sliplane-cli/internal/api"
)

type timedMetric struct {
	time time.Time
	api.Metric
}

// metricHistory parses and orders samples without changing the API response.
func metricHistory(metrics []api.Metric) ([]timedMetric, error) {
	history := make([]timedMetric, 0, len(metrics))
	for _, metric := range metrics {
		timestamp, err := time.Parse(time.RFC3339Nano, metric.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("invalid metric timestamp %q: %w", metric.CreatedAt, err)
		}
		history = append(history, timedMetric{timestamp, metric})
	}
	slices.SortStableFunc(history, func(a, b timedMetric) int { return a.time.Compare(b.time) })
	return history, nil
}

func printMetrics(output io.Writer, title string, metrics []api.Metric) error {
	history, err := metricHistory(metrics)
	if err != nil {
		return err
	}
	width := 80
	if file, ok := output.(*os.File); ok {
		if columns, _, err := term.GetSize(int(file.Fd())); err == nil && columns > 0 {
			width = min(columns, 120)
		}
	}
	view := metricsView(title, history, width, 12)
	_, err = fmt.Fprintln(colorprofile.NewWriter(output, os.Environ()), view)
	return err
}

func metricsView(title string, history []timedMetric, width, chartHeight int) string {
	heading := lipgloss.NewStyle().Bold(true).Width(width).Render(terminalText(title))
	if len(history) == 0 {
		return heading + "\n\n" + lipgloss.NewStyle().Width(width).Render("No metrics available for this time range.")
	}
	cpu := make([]timeserieslinechart.TimePoint, 0, len(history))
	memory := make([]timeserieslinechart.TimePoint, 0, len(history))
	for _, sample := range history {
		cpu = append(cpu, timeserieslinechart.TimePoint{Time: sample.time, Value: sample.CPUUsage})
		memory = append(memory, timeserieslinechart.TimePoint{Time: sample.time, Value: sample.UsedMemory / (1024 * 1024)})
	}
	coverage := fmt.Sprintf("%s → %s · %d samples", formatLocalTime(history[0].time), formatLocalTime(history[len(history)-1].time), len(history))
	return strings.Join([]string{
		heading,
		lipgloss.NewStyle().Faint(true).Width(width).Render(coverage),
		"",
		metricChart("CPU", "%", cpu, width, chartHeight, lipgloss.NewStyle().Foreground(lipgloss.Color("6"))),
		"",
		metricChart("Memory", "MiB", memory, width, chartHeight, lipgloss.NewStyle().Foreground(lipgloss.Color("5"))),
	}, "\n")
}

// metricChart uses actual timestamps on the X axis and a zero baseline on Y.
func metricChart(title, unit string, points []timeserieslinechart.TimePoint, width, height int, style lipgloss.Style) string {
	var peak float64
	for _, point := range points {
		peak = max(peak, point.Value)
	}
	summary := fmt.Sprintf("%s  ·  latest %.2f %s  ·  peak %.2f %s", title, points[len(points)-1].Value, unit, peak, unit)
	heading := style.Bold(true).Width(width).Render(summary)
	if width < 30 || height < 4 {
		return heading
	}
	start, end := points[0].Time, points[len(points)-1].Time
	if start.Equal(end) {
		// Give an isolated sample room to appear as a point, not a fabricated line.
		start, end = start.Add(-30*time.Second), end.Add(30*time.Second)
	}
	labelFormat := "15:04"
	if end.Sub(start) >= 24*time.Hour {
		labelFormat = "Jan 02"
	}
	ceiling := 1.0
	if peak > 0 {
		magnitude := math.Pow(10, math.Floor(math.Log10(peak))-1)
		ceiling = math.Ceil(peak*1.1/magnitude) * magnitude
	}
	precision := max(0, int(1-math.Floor(math.Log10(ceiling))))
	axis := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	chart := timeserieslinechart.New(width, height,
		timeserieslinechart.WithTimeRange(start, end),
		timeserieslinechart.WithYRange(0, ceiling),
		timeserieslinechart.WithXYSteps(max(10, width/4), max(1, (height-2)/4)),
		timeserieslinechart.WithXLabelFormatter(func(_ int, value float64) string {
			return time.Unix(int64(value), 0).Local().Format(labelFormat)
		}),
		timeserieslinechart.WithYLabelFormatter(func(_ int, value float64) string {
			return fmt.Sprintf("%.*f", precision, value)
		}),
		timeserieslinechart.WithAxesStyles(axis, axis),
		timeserieslinechart.WithStyle(style),
	)
	for _, point := range points {
		chart.Push(point)
	}
	chart.DrawBraille()
	return heading + "\n" + chart.View()
}

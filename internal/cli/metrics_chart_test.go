package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/sliplane/cli/internal/api"
)

func TestMetricHistory(t *testing.T) {
	metrics := []api.Metric{
		{CreatedAt: "2026-10-05T12:00:00+02:00", CPUUsage: 2},
		{CreatedAt: "2026-10-05T09:00:00.123Z", CPUUsage: 1},
	}
	history, err := metricHistory(metrics)
	if err != nil || len(history) != 2 || history[0].CPUUsage != 1 || history[1].CPUUsage != 2 {
		t.Fatalf("history=%v err=%v", history, err)
	}
	if metrics[0].CPUUsage != 2 || history[0].time.Nanosecond() != 123000000 {
		t.Fatal("mutated input or lost timestamp precision")
	}
	if _, err := metricHistory([]api.Metric{{CreatedAt: "not a timestamp"}}); err == nil {
		t.Fatal("expected invalid timestamp error")
	}
}

func TestMetricsSummary(t *testing.T) {
	var output bytes.Buffer
	err := printMetrics(&output, "service · nginx\x1b[2J", []api.Metric{
		{CreatedAt: "2026-10-05T10:02:00Z", CPUUsage: 12.5, UsedMemory: 256 * 1024 * 1024},
		{CreatedAt: "2026-10-05T10:00:00Z", CPUUsage: 150, UsedMemory: 512 * 1024 * 1024},
		{CreatedAt: "2026-10-05T10:01:00Z", CPUUsage: 25, UsedMemory: 128 * 1024 * 1024},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"latest 12.50 %", "peak 150.00 %", "latest 256.00 MiB", "peak 512.00 MiB", "3 samples"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("missing %q in %s", want, output.String())
		}
	}
	if strings.Contains(output.String(), "\x1b") {
		t.Fatal("redirected output contains terminal escape sequences")
	}
}

func TestMetricsSparseHistory(t *testing.T) {
	for _, metrics := range [][]api.Metric{
		nil,
		{{CreatedAt: "2026-10-05T10:00:00Z"}},
		{{CreatedAt: "2026-10-05T10:00:00Z"}, {CreatedAt: "2026-10-05T10:00:00Z"}},
	} {
		var output bytes.Buffer
		if err := printMetrics(&output, "server · test", metrics); err != nil {
			t.Fatal(err)
		}
		if len(metrics) == 0 && !strings.Contains(output.String(), "No metrics available") {
			t.Fatal(output.String())
		}
		if len(metrics) > 0 && !strings.Contains(output.String(), "latest 0.00") {
			t.Fatal(output.String())
		}
	}
}

func TestMetricsFitTerminal(t *testing.T) {
	history := []timedMetric{
		{time.Unix(1700000000, 0), api.Metric{CPUUsage: 0.1, UsedMemory: 128, TotalMemory: 2048}},
		{time.Unix(1700086400, 0), api.Metric{CPUUsage: 99.9, UsedMemory: 512, TotalMemory: 2048}},
	}
	for _, width := range []int{20, 30, 40, 80, 120} {
		for _, line := range strings.Split(metricsView("service · a-long-service-name", history, width, 12), "\n") {
			if ansi.StringWidth(line) > width {
				t.Fatalf("line wider than %d: %q", width, line)
			}
		}
	}
}

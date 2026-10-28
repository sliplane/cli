package cli

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/sliplane/cli/internal/api"
)

func TestMetricsWatchRefresh(t *testing.T) {
	requests := 0
	model := metricsWatchModel{
		ctx: t.Context(),
		fetch: func(ctx context.Context) ([]api.Metric, error) {
			if ctx != t.Context() {
				t.Fatal("fetch lost command context")
			}
			requests++
			return []api.Metric{{CreatedAt: "2026-10-05T10:00:00Z", CPUUsage: float64(requests)}}, nil
		},
	}
	result := model.Init()()
	updated, next := model.Update(result)
	model = updated.(metricsWatchModel)
	if requests != 1 || next == nil || model.updated.IsZero() || model.history[0].CPUUsage != 1 {
		t.Fatalf("first refresh: requests=%d model=%+v", requests, model)
	}
	_, refresh := model.Update(metricsRefreshMsg{})
	updated, next = model.Update(refresh())
	model = updated.(metricsWatchModel)
	if requests != 2 || next == nil || len(model.history) != 1 || model.history[0].CPUUsage != 2 {
		t.Fatalf("second refresh: requests=%d model=%+v", requests, model)
	}
}

func TestMetricsWatchErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, test := range []struct {
		fetch func(context.Context) ([]api.Metric, error)
		want  string
	}{
		{func(context.Context) ([]api.Metric, error) { return nil, errors.New("API unavailable") }, "API unavailable"},
		{func(context.Context) ([]api.Metric, error) { return []api.Metric{{CreatedAt: "bad"}}, nil }, "invalid metric timestamp"},
		{func(ctx context.Context) ([]api.Metric, error) { return nil, ctx.Err() }, "context canceled"},
	} {
		model := metricsWatchModel{ctx: ctx, fetch: test.fetch}
		updated, quit := model.Update(model.Init()())
		err := updated.(metricsWatchModel).err
		if err == nil || !strings.Contains(err.Error(), test.want) || quit == nil {
			t.Fatalf("err=%v want=%s", err, test.want)
		}
		if _, ok := quit().(tea.QuitMsg); !ok {
			t.Fatal("watch did not exit on error")
		}
	}
}

func TestMetricsWatchQuit(t *testing.T) {
	for _, key := range []tea.Key{{Code: 'q'}, {Code: 'c', Mod: tea.ModCtrl}} {
		updated, quit := (metricsWatchModel{}).Update(tea.KeyPressMsg(key))
		if quit == nil {
			t.Fatalf("no quit for %v", key)
		}
		if _, ok := quit().(tea.QuitMsg); !ok {
			t.Fatal("expected quit message")
		}
		if key.Mod == tea.ModCtrl && !errors.Is(updated.(metricsWatchModel).err, context.Canceled) {
			t.Fatal("Ctrl+C did not report cancellation")
		}
	}
}

func TestMetricsWatchResize(t *testing.T) {
	model := metricsWatchModel{title: "service · nginx", period: "1h", updated: time.Now(), history: []timedMetric{
		{time.Unix(1700000000, 0), api.Metric{CPUUsage: 25, UsedMemory: 128 * 1024 * 1024}},
	}}
	for _, size := range []tea.WindowSizeMsg{{Width: 80, Height: 24}, {Width: 40, Height: 24}, {Width: 120, Height: 40}, {Width: 80, Height: 12}} {
		updated, _ := model.Update(size)
		view := updated.(metricsWatchModel).View()
		if !view.AltScreen || lipgloss.Height(view.Content) > size.Height || lipgloss.Width(view.Content) > size.Width {
			t.Fatalf("view does not fit %dx%d: %s", size.Width, size.Height, view.Content)
		}
	}
}

func TestMetricsWatchInvalidOptions(t *testing.T) {
	for _, flags := range [][]string{{"--watch"}, {"--watch", "-o", "json"}, {"--watch", "--from", "100", "--to", "200"}} {
		_, _, err := runCommand(t, "http://127.0.0.1:1", append([]string{"service", "metrics", "nginx"}, flags...)...)
		if err == nil || !strings.Contains(err.Error(), "--watch") {
			t.Fatalf("flags=%v err=%v", flags, err)
		}
	}
}

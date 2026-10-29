package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sliplane/cli/internal/api"
)

func TestMetricsRanges(t *testing.T) {
	for _, kind := range []string{"service", "server"} {
		for _, test := range []struct {
			flags []string
			query string
		}{
			{nil, "range=1h"},
			{[]string{"--range", "7d"}, "range=7d"},
			{[]string{"--from", "100", "--to", "200"}, "from=100&to=200"},
		} {
			t.Run(kind+test.query, func(t *testing.T) {
				requests := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch r.URL.Path {
					case "/projects":
						fmt.Fprint(w, `[{"id":"p1"}]`)
					case "/projects/p1/services":
						fmt.Fprint(w, `[{"id":"s1","name":"target","projectId":"p1"}]`)
					case "/servers":
						fmt.Fprint(w, `[{"id":"server1","name":"target"}]`)
					case "/projects/p1/services/s1/metrics", "/servers/server1/metrics":
						requests++
						if r.URL.RawQuery != test.query {
							t.Errorf("query=%s want=%s", r.URL.RawQuery, test.query)
						}
						fmt.Fprint(w, `[{"createdAt":"2026-10-04T10:00:00Z","cpuUsage":12.5,"usedMemory":512,"freeMemory":512,"totalMemory":1024}]`)
					default:
						t.Errorf("unexpected request %s", r.URL.Path)
						http.NotFound(w, r)
					}
				}))
				defer server.Close()
				stdout, _, err := runCommand(t, server.URL, append([]string{kind, "metrics", "target"}, test.flags...)...)
				if err != nil || requests != 1 || !strings.Contains(stdout, "12.50") {
					t.Fatalf("output=%s requests=%d err=%v", stdout, requests, err)
				}
				stdout, _, err = runCommand(t, server.URL, append([]string{kind, "metrics", "target", "-o", "json"}, test.flags...)...)
				var raw []api.Metric
				if err != nil || json.Unmarshal([]byte(stdout), &raw) != nil || len(raw) != 1 || raw[0].FreeMemory != 512 {
					t.Fatalf("JSON metrics=%s err=%v", stdout, err)
				}
			})
		}
	}
}

func TestMetricsInvalidRanges(t *testing.T) {
	for _, flags := range [][]string{{"--range", "2h"}, {"--from", "100"}, {"--to", "200"}, {"--from", "200", "--to", "100"}, {"--from", "100", "--to", "200", "--range", "1h"}} {
		_, _, err := runCommand(t, "http://127.0.0.1:1", append([]string{"service", "metrics", "api"}, flags...)...)
		if err == nil || strings.Contains(err.Error(), "connect") {
			t.Fatalf("flags=%v err=%v", flags, err)
		}
	}
}

func TestTopLevelMetricsResolvesServiceOrServer(t *testing.T) {
	for _, test := range []struct{ query, want, path string }{
		{"api", "service · api", "/projects/p1/services/s1/metrics"},
		{"node", "server · node", "/servers/server1/metrics"},
		{"shared", "matches both a service and a server", ""},
		{"missing", `no service or server named "missing"`, ""},
	} {
		t.Run(test.query, func(t *testing.T) {
			requested := ""
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/projects":
					fmt.Fprint(w, `[{"id":"p1"}]`)
				case "/projects/p1/services":
					fmt.Fprint(w, `[{"id":"s1","name":"api","projectId":"p1"},{"id":"s2","name":"shared","projectId":"p1"}]`)
				case "/servers":
					fmt.Fprint(w, `[{"id":"server1","name":"node"},{"id":"server2","name":"shared"}]`)
				default:
					requested = r.URL.Path
					fmt.Fprint(w, `[{"createdAt":"2026-10-04T10:00:00Z","cpuUsage":12.5,"usedMemory":512,"freeMemory":512,"totalMemory":1024}]`)
				}
			}))
			defer server.Close()
			stdout, _, err := runCommand(t, server.URL, "metrics", test.query)
			if test.path == "" {
				if err == nil || !strings.Contains(err.Error(), test.want) || requested != "" {
					t.Fatalf("err=%v requested=%s", err, requested)
				}
				return
			}
			if err != nil || requested != test.path || !strings.Contains(stdout, test.want) {
				t.Fatalf("output=%s requested=%s err=%v", stdout, requested, err)
			}
		})
	}
}

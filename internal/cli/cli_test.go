package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sliplane-cli/internal/api"
	"sliplane-cli/internal/config"
)

func runCommand(t *testing.T, serverURL string, args ...string) (string, string, error) {
	t.Helper()
	return runProfileCommand(t, serverURL, filepath.Join(t.TempDir(), "config.json"), append([]string{"--token", "test-token"}, args...)...)
}

func runProfileCommand(t *testing.T, serverURL, path string, args ...string) (string, string, error) {
	t.Helper()
	command := newCommand(serverURL, serverURL+"/presets")
	var stdout, stderr bytes.Buffer
	command.SetOut(&stdout)
	command.SetErr(&stderr)
	command.SetArgs(append([]string{"--config", path}, args...))
	err := command.ExecuteContext(t.Context())
	return stdout.String(), stderr.String(), err
}

func TestServiceDiscovery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/projects":
			fmt.Fprint(w, `[{"id":"p1","name":"production"},{"id":"p2","name":"staging"}]`)
		case "/projects/p1/services":
			fmt.Fprint(w, `[{"id":"s1","name":"api","projectId":"p1","serverId":"server_primary","status":"live"}]`)
		case "/projects/p2/services":
			fmt.Fprint(w, `[{"id":"s2","name":"api","projectId":"p2","serverId":"server_missing","status":"live"}]`)
		case "/servers":
			fmt.Fprint(w, `[{"id":"server_primary","name":"web-production"}]`)
		case "/projects/p1/services/s1":
			fmt.Fprint(w, `{"id":"s1","name":"api","projectId":"p1","status":"live"}`)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	stdout, stderr, err := runCommand(t, server.URL, "service", "list", "-o", "json")
	var services []serviceEntry
	if err != nil || stderr != "" || json.Unmarshal([]byte(stdout), &services) != nil || len(services) != 2 {
		t.Fatalf("bad list: %q %q %v", stdout, stderr, err)
	}
	if services[0].ID != "s1" || services[0].ServerID != "server_primary" {
		t.Fatalf("JSON lost resource IDs: %q", stdout)
	}
	stdout, _, err = runCommand(t, server.URL, "service", "show", "api", "-o", "json")
	if err == nil || !strings.Contains(err.Error(), "ambiguous") || stdout != "" {
		t.Fatalf("ambiguous lookup: %q %v", stdout, err)
	}
	for _, args := range [][]string{{"service", "show", "s1"}, {"service", "show", "api", "--project", "production"}} {
		stdout, stderr, err = runCommand(t, server.URL, append(args, "-o", "json")...)
		if err != nil || stderr != "" || !json.Valid([]byte(stdout)) || !strings.Contains(stdout, `"id":"s1"`) {
			t.Fatalf("lookup: %q %q %v", stdout, stderr, err)
		}
	}
	stdout, _, err = runCommand(t, server.URL, "service", "list")
	if err != nil || !strings.Contains(stdout, "production") || !strings.Contains(stdout, "staging") || !strings.Contains(stdout, "NAME") {
		t.Fatalf("text list: %q %v", stdout, err)
	}
	if !strings.Contains(stdout, "web-production") || strings.Contains(stdout, "server_primary") || strings.Contains(stdout, "s1") || strings.Contains(stdout, "ID") {
		t.Fatalf("text list should use names: %q", stdout)
	}
	if !strings.Contains(stdout, "server_missing") {
		t.Fatalf("missing server should retain its identifier: %q", stdout)
	}
}

func TestWriteDeniedHasNoStdout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		fmt.Fprint(w, `{"code":"read_only_token","message":"This token does not have write permissions"}`)
	}))
	defer server.Close()
	stdout, _, err := runCommand(t, server.URL, "project", "create", "demo", "-o", "json")
	if stdout != "" || err == nil || !strings.Contains(err.Error(), "read-write") {
		t.Fatalf("denied output %q %v", stdout, err)
	}
}

func TestLogSnapshotAndFollow(t *testing.T) {
	logRequests := 0
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/projects":
			fmt.Fprint(w, `[{"id":"p1","name":"prod"}]`)
		case "/projects/p1/services":
			fmt.Fprint(w, `[{"id":"s1","name":"api","projectId":"p1"}]`)
		case "/projects/p1/services/s1/logs":
			logRequests++
			if logRequests == 3 {
				cancel()
				return
			}
			fmt.Fprint(w, `[{"message":"second","createdAt":"2024-01-01T00:00:02Z"},{"message":"first","createdAt":"2024-01-01T00:00:01Z"}]`)
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	stdout, stderr, err := runCommand(t, server.URL, "logs", "api", "-o", "json")
	var logs []api.Log
	if err != nil || stderr != "" || json.Unmarshal([]byte(stdout), &logs) != nil || len(logs) != 2 || logs[0].Message != "first" {
		t.Fatalf("snapshot: %q %q %v", stdout, stderr, err)
	}
	command := newCommand(server.URL, server.URL+"/presets")
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs([]string{"--config", filepath.Join(t.TempDir(), "config.json"), "--token", "test-token", "logs", "api", "--follow", "--interval", "1s", "-o", "json"})
	err = command.ExecuteContext(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("follow cancellation: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("follow lines: %q", output.String())
	}
	for _, line := range lines {
		if !json.Valid([]byte(line)) {
			t.Fatalf("invalid JSON line %q", line)
		}
	}
}

func TestCursorOverlap(t *testing.T) {
	at := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	repeated := api.Log{CreatedAt: at, Message: "same"}
	cursor := logCursor{seen: map[logKey]int{}}
	if got := cursor.advance([]api.Log{repeated, repeated}); len(got) != 2 {
		t.Fatal(got)
	}
	if got := cursor.advance([]api.Log{repeated, repeated, repeated}); len(got) != 1 {
		t.Fatal(got)
	}
	later := api.Log{CreatedAt: at.Add(10 * time.Second), Message: "later"}
	cursor.advance([]api.Log{later})
	if len(cursor.seen) != 1 {
		t.Fatal("old overlap was retained")
	}
}

func TestConfigOverrides(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := config.Save(path, config.Config{Output: "json"}); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		args []string
		json bool
	}{
		{args: []string{"config", "show"}, json: true},
		{args: []string{"config", "show", "-o", "text"}},
	} {
		command := newCommand("", "/presets")
		var output bytes.Buffer
		command.SetOut(&output)
		command.SetArgs(append([]string{"--config", path}, test.args...))
		if err := command.Execute(); err != nil {
			t.Fatal(err)
		}
		if json.Valid(output.Bytes()) != test.json {
			t.Fatalf("wrong format %s", output.String())
		}
	}
}

func TestInvalidArgumentsFailBeforeNetwork(t *testing.T) {
	for _, args := range [][]string{
		{"project", "create"}, {"project", "create", ""}, {"service", "list", "unexpected"},
		{"service", "list", "-o", "yaml"}, {"logs", "api", "--interval", "0s"}, {"logs", "api", "--since", "-1m"},
	} {
		stdout, _, err := runCommand(t, "http://127.0.0.1:1", args...)
		if err == nil || stdout != "" {
			t.Fatalf("invalid args %v: %q %v", args, stdout, err)
		}
	}
}

func TestParseSince(t *testing.T) {
	now := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	since, err := parseSince("30m", now)
	if err != nil || !since.Equal(now.Add(-30*time.Minute)) {
		t.Fatalf("since %v %v", since, err)
	}
	if _, err := parseSince("2024-01-01T12:00:00.123Z", now); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"0s", "-1h", "yesterday"} {
		if _, err := parseSince(value, now); err == nil {
			t.Fatal("accepted", value)
		}
	}
}

func TestLogCapWarningKeepsJSONClean(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/projects":
			fmt.Fprint(w, `[{"id":"p1","name":"prod"}]`)
		case "/projects/p1/services":
			fmt.Fprint(w, `[{"id":"s1","name":"api","projectId":"p1"}]`)
		case "/projects/p1/services/s1/logs":
			lines := make([]api.Log, 500)
			for index := range lines {
				lines[index] = api.Log{CreatedAt: time.Unix(int64(index), 0), Message: "hello"}
			}
			if err := json.NewEncoder(w).Encode(lines); err != nil {
				t.Error(err)
			}
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
		}
	}))
	defer server.Close()
	stdout, stderr, err := runCommand(t, server.URL, "service", "logs", "api", "-o", "json")
	if err != nil || !json.Valid([]byte(stdout)) || !strings.Contains(stderr, "500-line limit") {
		t.Fatalf("cap handling: %q %v", stderr, err)
	}
}

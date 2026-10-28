package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sliplane/cli/internal/api"
	"github.com/spf13/cobra"
)

func useBerlinTime(t *testing.T) {
	t.Helper()
	location, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	previous := time.Local
	time.Local = location
	t.Cleanup(func() { time.Local = previous })
}

func TestFormatTimestamp(t *testing.T) {
	useBerlinTime(t)
	for _, test := range []struct {
		name, input, want string
	}{
		{"summer with nanoseconds", "2026-09-23T18:45:35.768321986Z", "2026-09-23 20:45:35 CEST"},
		{"winter", "2026-01-23T18:45:35Z", "2026-01-23 19:45:35 CET"},
		{"next day", "2026-09-23T23:45:35Z", "2026-09-24 01:45:35 CEST"},
		{"explicit offset", "2026-09-23T18:45:35-04:00", "2026-09-24 00:45:35 CEST"},
		{"empty", "", ""},
		{"unrecognized", "unknown", "unknown"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := formatTimestamp(test.input); got != test.want {
				t.Fatalf("formatTimestamp(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
	if got := formatLocalTime(time.Time{}); got != "" {
		t.Fatalf("missing timestamp = %q", got)
	}
}

func TestTimestampOutput(t *testing.T) {
	useBerlinTime(t)
	const timestamp = "2026-09-23T18:45:35.768321986Z"
	created, err := time.Parse(time.RFC3339Nano, timestamp)
	if err != nil {
		t.Fatal(err)
	}
	database := api.PostgresDatabase{Name: "primary", CreatedAt: timestamp}
	logs := []api.Log{{CreatedAt: created, Message: "ready"}}
	var output bytes.Buffer
	command := &cobra.Command{}
	command.SetOut(&output)
	options := &options{output: "text"}
	if err := options.printPostgres(command, database); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "2026-09-23 20:45:35 CEST") || strings.Contains(output.String(), timestamp) {
		t.Fatalf("database display: %q", output.String())
	}
	output.Reset()
	if err := options.printLogs(command, logs); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); got != "2026-09-23 20:45:35 CEST  ready\n" {
		t.Fatalf("log display: %q", got)
	}
	options.output = "json"
	output.Reset()
	if err := options.printPostgres(command, database); err != nil {
		t.Fatal(err)
	}
	var decoded api.PostgresDatabase
	if err := json.Unmarshal(output.Bytes(), &decoded); err != nil || decoded.CreatedAt != timestamp {
		t.Fatalf("database JSON: %q, %v", output.String(), err)
	}
	output.Reset()
	if err := options.printLogs(command, logs); err != nil {
		t.Fatal(err)
	}
	var decodedLogs []api.Log
	if err := json.Unmarshal(output.Bytes(), &decodedLogs); err != nil || len(decodedLogs) != 1 || !decodedLogs[0].CreatedAt.Equal(created) {
		t.Fatalf("log JSON: %q, %v", output.String(), err)
	}
}

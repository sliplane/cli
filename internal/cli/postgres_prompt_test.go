//go:build !windows

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sliplane/cli/internal/api"
	"github.com/spf13/cobra"
)

func TestPostgresAllowlistPrompt(t *testing.T) {
	for _, scenario := range []string{"approve", "IPv6", "decline", "enter", "already allowed", "concurrent rule", "read-only", "API failure", "piped input", "piped output", "piped error", "json"} {
		t.Run(scenario, func(t *testing.T) {
			address := netip.MustParseAddr("203.0.113.7")
			source := "203.0.113.7/32"
			if scenario == "IPv6" {
				address = netip.MustParseAddr("2001:db8::7")
				source = "2001:db8::7/128"
			}
			var reads, writes atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/postgres/pg_one" || r.Header.Get("Authorization") != "Bearer test-token" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				if r.Method == http.MethodGet {
					reads.Add(1)
					if scenario == "concurrent rule" {
						fmt.Fprintf(w, `{"ipAllowList":[{"source":%q}]}`, source)
						return
					}
					fmt.Fprint(w, `{"ipAllowList":[{"id":"fw_other","source":"198.51.100.0/24","description":"new office","createdAt":"yesterday"}]}`)
					return
				}
				writes.Add(1)
				var body map[string][]map[string]string
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				rules := body["ipAllowList"]
				if r.Method != http.MethodPatch || len(body) != 1 || len(rules) != 2 {
					t.Errorf("unexpected update: %s %+v", r.Method, body)
				} else if len(rules[0]) != 2 || rules[0]["source"] != "198.51.100.0/24" || rules[0]["description"] != "new office" || rules[1]["source"] != source {
					t.Errorf("lost existing rule or added incorrect address: %+v", rules)
				}
				if scenario == "read-only" {
					w.WriteHeader(http.StatusForbidden)
					fmt.Fprint(w, `{"code":"read_only_token"}`)
					return
				}
				if scenario == "API failure" {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				fmt.Fprint(w, `{}`)
			}))
			defer server.Close()
			inputMaster, input := testTerminal(t)
			outMaster, output := testTerminal(t)
			errMaster, diagnostics := testTerminal(t)
			command := &cobra.Command{}
			command.SetIn(input)
			command.SetOut(output)
			command.SetErr(diagnostics)
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			command.SetContext(ctx)
			answer := "yes\n"
			if scenario == "decline" {
				answer = "no\n"
			}
			if scenario == "enter" {
				answer = "\n"
			}
			if _, err := io.WriteString(inputMaster, answer); err != nil {
				t.Fatal(err)
			}
			var redirected bytes.Buffer
			opts := &options{output: "text"}
			switch scenario {
			case "piped input":
				command.SetIn(strings.NewReader("yes\n"))
			case "piped output":
				command.SetOut(&redirected)
			case "piped error":
				command.SetErr(&redirected)
			case "json":
				opts.output = "json"
			}
			database := api.PostgresDatabase{ID: "pg_one", Name: "main"}
			if scenario == "already allowed" {
				database.IPAllowList = []api.PostgresIPRange{{Source: "203.0.113.0/24"}}
			}
			err := opts.ensurePostgresAccess(command, api.New(server.URL, "test-token"), database, address)
			stdout := readTerminal(t, outMaster, output)
			stderr := readTerminal(t, errMaster, diagnostics)
			success := scenario == "approve" || scenario == "IPv6" || scenario == "already allowed" || scenario == "concurrent rule"
			if (err == nil) != success || stdout != "" || redirected.Len() != 0 {
				t.Fatalf("access: %v, stdout %q, stderr %q", err, stdout, stderr)
			}
			wantWrites := int32(0)
			if scenario == "approve" || scenario == "IPv6" || scenario == "read-only" || scenario == "API failure" {
				wantWrites = 1
			}
			if writes.Load() != wantWrites {
				t.Fatalf("created %d rules, want %d", writes.Load(), wantWrites)
			}
			if scenario == "read-only" && !strings.Contains(err.Error(), "read-write token") {
				t.Fatalf("missing permission guidance: %v", err)
			}
			if strings.HasPrefix(scenario, "piped") || scenario == "json" || scenario == "already allowed" {
				if stderr != "" || reads.Load() != 0 {
					t.Fatalf("unexpected prompt or API call: %q", stderr)
				}
			} else if !strings.Contains(stderr, "persistent allowlist rule") {
				t.Fatalf("missing scope of consent: %q", stderr)
			}
		})
	}
}

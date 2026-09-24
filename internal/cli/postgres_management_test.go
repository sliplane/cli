// Management tests exercise request bodies, target resolution, and credential output through a local API.
package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// postgresManagementResponse supplies credentials to ensure routine management output strips them.
const postgresManagementResponse = `{"id":"pg_main","name":"main","status":"updating","diskSizeGb":50,"databaseName":"app","databaseUser":"owner","connection":{"host":"db.example.test","port":5432,"database":"app","user":"owner","password":"rotated-secret","uri":"postgres://owner:rotated-secret@db.example.test/app"},"ipAllowList":[{"source":"203.0.113.0/24"}]}`

// TestPostgresManagementRequests verifies exact public routes and sparse payloads for every mutation.
func TestPostgresManagementRequests(t *testing.T) {
	for _, test := range []struct {
		name, method, path, body string
		args                     []string
		showPassword             bool
	}{
		{"create defaults", "POST", "/postgres", `{"name":"main","region":"ger","instanceType":"base","billingCycle":"hourly"}`, []string{"create", "main"}, false},
		{"create starter override", "POST", "/postgres", `{"name":"main","region":"ger","instanceType":"starter","billingCycle":"hourly"}`, []string{"create", "main", "--instance-type", "starter"}, false},
		{"create options", "POST", "/postgres", `{"name":"main","region":"fin","instanceType":"dedicated-base","billingCycle":"monthly","diskSizeGb":100,"version":18,"databaseName":"app_db","databaseUser":"app_user"}`, []string{"create", "main", "--instance-type", "dedicated-base", "--region", "fin", "--billing-cycle", "monthly", "--disk-size", "100", "--version", "18", "--database-name", "app_db", "--database-user", "app_user"}, false},
		{"rename", "PATCH", "/postgres/pg_main", `{"name":"production"}`, []string{"update", "main", "--name", "production"}, false},
		{"resize by ID", "PATCH", "/postgres/pg_main", `{"diskSizeGb":50,"instanceType":"medium"}`, []string{"update", "pg_main", "--disk-size", "50", "--instance-type", "medium"}, false},
		{"compute only", "PATCH", "/postgres/pg_main", `{"instanceType":"base"}`, []string{"update", "main", "--instance-type", "base"}, false},
		{"delete", "DELETE", "/postgres/pg_main", "", []string{"delete", "main", "--yes"}, false},
		{"restart", "POST", "/postgres/pg_main/restart", "", []string{"restart", "main"}, false},
		{"pause", "POST", "/postgres/pg_main/pause", "", []string{"pause", "main"}, false},
		{"unpause", "POST", "/postgres/pg_main/unpause", "", []string{"unpause", "main"}, false},
		{"rotate password", "POST", "/postgres/pg_main/credentials/rotate", "", []string{"rotate-password", "main"}, true},
	} {
		for _, output := range []string{"text", "json"} {
			t.Run(test.name+"/"+output, func(t *testing.T) {
				writes := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("Authorization") != "Bearer test-token" {
						t.Error("missing authentication")
					}
					if r.Method == "GET" {
						switch r.URL.Path {
						case "/postgres":
							fmt.Fprint(w, `[{"id":"pg_main","name":"main"},{"id":"pg_other","name":"other"}]`)
						case "/postgres/pg_main":
							fmt.Fprint(w, `{"id":"pg_main","name":"main","diskSizeGb":10}`)
						default:
							t.Errorf("unexpected lookup: %s", r.URL.Path)
							http.NotFound(w, r)
						}
						return
					}
					writes++
					if r.Method != test.method || r.URL.Path != test.path {
						t.Errorf("mutation = %s %s", r.Method, r.URL.Path)
					}
					body, err := io.ReadAll(r.Body)
					if err != nil {
						t.Error(err)
					}
					if test.body == "" {
						if len(body) != 0 {
							t.Errorf("unexpected body: %s", body)
						}
					} else {
						var got, want map[string]any
						if err := json.Unmarshal(body, &got); err != nil {
							t.Error(err)
						}
						if err := json.Unmarshal([]byte(test.body), &want); err != nil {
							t.Fatal(err)
						}
						if !reflect.DeepEqual(got, want) {
							t.Errorf("body = %s; want %s", body, test.body)
						}
					}
					if test.method == "DELETE" {
						w.WriteHeader(http.StatusNoContent)
						return
					}
					w.WriteHeader(http.StatusAccepted)
					fmt.Fprint(w, postgresManagementResponse)
				}))
				defer server.Close()
				args := append([]string{"postgres", "--output", output}, test.args...)
				stdout, stderr, err := runCommand(t, server.URL, args...)
				if err != nil || stderr != "" || writes != 1 || !strings.Contains(stdout, "pg_main") {
					t.Fatalf("stdout=%q stderr=%q error=%v writes=%d", stdout, stderr, err, writes)
				}
				if strings.Contains(stdout, "rotated-secret") != test.showPassword || strings.Contains(stdout, "postgres://") {
					t.Fatal("incorrect credential exposure")
				}
				if output == "json" && !json.Valid([]byte(stdout)) {
					t.Fatal("invalid JSON output")
				}
			})
		}
	}
}

// TestPostgresManagementValidation rejects incomplete or invalid changes before network access.
func TestPostgresManagementValidation(t *testing.T) {
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"create"}, "missing required argument"},
		{[]string{"create", " ", "--instance-type", "starter"}, "name must not be empty"},
		{[]string{"create", "main", "--instance-type="}, "--instance-type must not be empty"},
		{[]string{"create", "main", "--instance-type", "starter", "--region="}, "--region must not be empty"},
		{[]string{"create", "main", "--instance-type", "starter", "--disk-size=0"}, "--disk-size must be greater than zero"},
		{[]string{"create", "main", "--instance-type", "starter", "--version=-1"}, "--version must be greater than zero"},
		{[]string{"create", "main", "--instance-type", "starter", "--billing-cycle", "weekly"}, "--billing-cycle must be"},
		{[]string{"create", "main", "--instance-type", "starter", "--database-name="}, "--database-name must not be empty"},
		{[]string{"update", "main"}, "provide --name"},
		{[]string{"update", "main", "--name="}, "--name must not be empty"},
		{[]string{"update", "main", "--instance-type="}, "--instance-type must not be empty"},
		{[]string{"update", "main", "--disk-size=-1"}, "--disk-size must be greater than zero"},
		{[]string{"delete"}, "missing required argument"},
		{[]string{"rotate-password"}, "missing required argument"},
		{[]string{"restart"}, "missing required argument"},
		{[]string{"pause"}, "missing required argument"},
		{[]string{"unpause"}, "missing required argument"},
	} {
		stdout, stderr, err := runCommand(t, "http://127.0.0.1:1", append([]string{"postgres"}, test.args...)...)
		if err == nil || !strings.Contains(err.Error(), test.want) || stdout != "" || stderr != "" {
			t.Errorf("%v: stdout=%q stderr=%q err=%v", test.args, stdout, stderr, err)
		}
	}
}

// TestPostgresManagementFailures prevents destructive writes on ambiguous targets or missing consent.
func TestPostgresManagementFailures(t *testing.T) {
	for _, test := range []struct {
		name, want string
		args       []string
		writes     int
	}{
		{"delete without consent", "requires confirmation", []string{"delete", "main"}, 0},
		{"ambiguous", "ambiguous", []string{"delete", "main", "--yes"}, 0},
		{"missing", "not found", []string{"restart", "missing"}, 0},
		{"shrink", "greater than the current", []string{"update", "main", "--disk-size", "10"}, 0},
		{"read-only", "read-write token", []string{"update", "main", "--name", "new"}, 1},
		{"conflict", "server_invalid_state", []string{"restart", "main"}, 1},
		{"server failure", "503", []string{"delete", "main", "--yes"}, 1},
		{"credentials unavailable", "password rotated, but", []string{"rotate-password", "main"}, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			writes := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "GET" {
					if r.URL.Path == "/postgres" {
						if test.name == "ambiguous" {
							fmt.Fprint(w, `[{"id":"pg_one","name":"main"},{"id":"pg_two","name":"main"}]`)
						} else {
							fmt.Fprint(w, `[{"id":"pg_main","name":"main"}]`)
						}
					} else {
						fmt.Fprint(w, `{"id":"pg_main","name":"main","diskSizeGb":50}`)
					}
					return
				}
				writes++
				switch test.name {
				case "read-only":
					w.WriteHeader(http.StatusForbidden)
					fmt.Fprint(w, `{"code":"read_only_token"}`)
				case "conflict":
					w.WriteHeader(http.StatusConflict)
					fmt.Fprint(w, `{"code":"server_invalid_state"}`)
				case "credentials unavailable":
					fmt.Fprint(w, `{"id":"pg_main","name":"main"}`)
				default:
					w.WriteHeader(http.StatusServiceUnavailable)
				}
			}))
			defer server.Close()
			stdout, _, err := runCommand(t, server.URL, append([]string{"postgres"}, test.args...)...)
			if err == nil || !strings.Contains(err.Error(), test.want) || stdout != "" || writes != test.writes {
				t.Fatalf("stdout=%q err=%v writes=%d", stdout, err, writes)
			}
		})
	}
}

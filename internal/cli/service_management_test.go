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

func TestServiceManagementRequests(t *testing.T) {
	for _, test := range []struct {
		name, method, suffix, body string
		args                       []string
	}{
		{"rename", "PATCH", "", `{"name":"backend"}`, []string{"update", "api", "--name", "backend"}},
		{"healthcheck", "PATCH", "", `{"healthcheck":"/health"}`, []string{"update", "api", "--healthcheck", "/health"}},
		{"command", "PATCH", "", `{"cmd":"npm start"}`, []string{"update", "api", "--cmd", "npm start"}},
		{"clear command", "PATCH", "", `{"cmd":""}`, []string{"update", "api", "--cmd="}},
		{"combined update", "PATCH", "", `{"name":"backend","healthcheck":"/ready","cmd":""}`, []string{"update", "api", "--name", "backend", "--healthcheck", "/ready", "--cmd="}},
		{"redeploy by ID", "POST", "/deploy", `{}`, []string{"redeploy", "s1"}},
		{"redeploy tag", "POST", "/deploy", `{"tag":"v2.0.0"}`, []string{"redeploy", "api", "--tag", "v2.0.0"}},
		{"pause", "POST", "/pause", "", []string{"pause", "api"}},
		{"unpause", "POST", "/unpause", "", []string{"unpause", "api"}},
		{"delete", "DELETE", "", "", []string{"delete", "api", "--yes"}},
	} {
		for _, output := range []string{"text", "json"} {
			t.Run(test.name+"/"+output, func(t *testing.T) {
				writes := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("Authorization") != "Bearer test-token" {
						t.Error("missing authentication")
					}
					if r.Method == http.MethodGet {
						switch r.URL.Path {
						case "/projects":
							fmt.Fprint(w, `[{"id":"p1","name":"production"},{"id":"p2","name":"staging"}]`)
						case "/projects/p1/services":
							fmt.Fprint(w, `[{"id":"s1","name":"api","projectId":"p1","status":"live"}]`)
						default:
							t.Errorf("unexpected lookup: %s", r.URL.Path)
							http.NotFound(w, r)
						}
						return
					}
					writes++
					if r.Method != test.method || r.URL.Path != "/projects/p1/services/s1"+test.suffix {
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
							t.Error(err)
						}
						if !reflect.DeepEqual(got, want) {
							t.Errorf("body = %s; want %s", body, test.body)
						}
					}
					switch test.method {
					case "PATCH":
						fmt.Fprint(w, `{"id":"s1","name":"backend","projectId":"p1","status":"pending"}`)
					case "DELETE":
						w.WriteHeader(http.StatusNoContent)
					default:
						w.WriteHeader(http.StatusAccepted)
					}
				}))
				defer server.Close()
				args := append([]string{"service", "--project", "production", "--output", output}, test.args...)
				stdout, stderr, err := runCommand(t, server.URL, args...)
				if err != nil || stderr != "" || writes != 1 || !strings.Contains(stdout, "s1") {
					t.Fatalf("stdout=%q stderr=%q error=%v writes=%d", stdout, stderr, err, writes)
				}
				if output == "json" {
					var receipt map[string]any
					if err := json.Unmarshal([]byte(stdout), &receipt); err != nil {
						t.Fatal(err)
					}
					if receipt["id"] != "s1" || receipt["projectId"] != "p1" {
						t.Fatalf("receipt lost target: %s", stdout)
					}
					if test.method == "POST" && (receipt["accepted"] != true || receipt["action"] != test.args[0] || receipt["status"] != nil) {
						t.Fatalf("receipt must report acceptance without a stale status: %s", stdout)
					}
					if test.method == "DELETE" && receipt["deleted"] != true {
						t.Fatalf("missing deletion receipt: %s", stdout)
					}
				}
				if output == "text" && test.method == "POST" && !strings.Contains(stdout, test.args[0]+" accepted") {
					t.Fatalf("missing acceptance: %s", stdout)
				}
			})
		}
	}
}

func TestServiceManagementValidation(t *testing.T) {
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"update"}, "missing required argument"},
		{[]string{"update", "api"}, "provide --name"},
		{[]string{"update", "api", "--name="}, "--name must not be empty"},
		{[]string{"update", "api", "--name", " "}, "--name must not be empty"},
		{[]string{"update", "api", "--healthcheck="}, "--healthcheck must be a path"},
		{[]string{"update", "api", "--healthcheck", "https://example.test/health"}, "--healthcheck must be a path"},
		{[]string{"redeploy"}, "missing required argument"},
		{[]string{"redeploy", "api", "--tag="}, "--tag must not be empty"},
		{[]string{"redeploy", "api", "--tag", " "}, "--tag must not be empty"},
		{[]string{"pause"}, "missing required argument"},
		{[]string{"unpause"}, "missing required argument"},
		{[]string{"delete"}, "missing required argument"},
		{[]string{"delete", "api", "extra", "--yes"}, "too many arguments"},
	} {
		t.Run(strings.Join(test.args, " "), func(t *testing.T) {
			stdout, stderr, err := runCommand(t, "http://127.0.0.1:1", append([]string{"service"}, test.args...)...)
			if err == nil || !strings.Contains(err.Error(), test.want) || stdout != "" || stderr != "" {
				t.Fatalf("stdout=%q stderr=%q err=%v", stdout, stderr, err)
			}
		})
	}
}

func TestServiceManagementFailures(t *testing.T) {
	for _, args := range [][]string{
		{"update", "api", "--name", "backend"}, {"redeploy", "api"},
		{"pause", "api"}, {"unpause", "api"}, {"delete", "api", "--yes"},
	} {
		for _, test := range []struct {
			name, want string
			status     int
		}{
			{"ambiguous", "ambiguous", 0},
			{"missing", "not found", 0},
			{"read-only", "read-write token", 403},
			{"conflict", "service_invalid_state", 409},
			{"rate limit", "wait before retrying", 429},
			{"server error", "503", 503},
		} {
			t.Run(args[0]+"/"+test.name, func(t *testing.T) {
				writes := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == "GET" {
						switch r.URL.Path {
						case "/projects":
							fmt.Fprint(w, `[{"id":"p1","name":"production"},{"id":"p2","name":"staging"}]`)
						case "/projects/p1/services":
							if test.name == "missing" {
								fmt.Fprint(w, `[]`)
							} else {
								fmt.Fprint(w, `[{"id":"s1","name":"api","projectId":"p1"}]`)
							}
						case "/projects/p2/services":
							if test.name == "ambiguous" {
								fmt.Fprint(w, `[{"id":"s2","name":"api","projectId":"p2"}]`)
							} else {
								fmt.Fprint(w, `[]`)
							}
						default:
							t.Errorf("unexpected lookup: %s", r.URL.Path)
							http.NotFound(w, r)
						}
						return
					}
					writes++
					if test.status == 0 {
						t.Error("mutation on unresolved target")
						w.WriteHeader(500)
						return
					}
					w.WriteHeader(test.status)
					if test.status == 403 {
						fmt.Fprint(w, `{"code":"read_only_token"}`)
					}
					if test.status == 409 {
						fmt.Fprint(w, `{"code":"service_invalid_state"}`)
					}
				}))
				defer server.Close()
				stdout, stderr, err := runCommand(t, server.URL, append([]string{"service", "-o", "json"}, args...)...)
				wantWrites := 0
				if test.status != 0 {
					wantWrites = 1
				}
				if err == nil || !strings.Contains(err.Error(), test.want) || stdout != "" || stderr != "" || writes != wantWrites {
					t.Fatalf("stdout=%q stderr=%q err=%v writes=%d", stdout, stderr, err, writes)
				}
			})
		}
	}
}

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

func TestServiceConfigurationRequests(t *testing.T) {
	for _, test := range []struct {
		name, method, path, body, response string
		args                               []string
	}{
		{"secret default", "PUT", "/projects/p1/services/s1/env/TOKEN", `{"value":"sensitive","secret":true}`, `{"key":"TOKEN","value":"sensitive","secret":true}`, []string{"service", "env", "set", "api", "TOKEN=sensitive"}},
		{"public variable", "PUT", "/projects/p1/services/s1/env/LEVEL", `{"value":"debug","secret":false}`, `{}`, []string{"service", "env", "set", "api", "LEVEL=debug", "--public"}},
		{"delete variable", "DELETE", "/projects/p1/services/s1/env/OLD", "", "", []string{"service", "env", "delete", "api", "OLD"}},
		{"remove domain by hostname", "DELETE", "/projects/p1/services/s1/domains/d1", "", "", []string{"service", "domain", "remove", "api", "app.example.com"}},
		{"attach default mount", "POST", "/projects/p1/services/s1/volumes", `{"id":"v1","mountPath":"/data"}`, `{"id":"v1","name":"data","mountPath":"/data"}`, []string{"service", "volume", "attach", "api", "data"}},
		{"detach by name", "DELETE", "/projects/p1/services/s1/volumes/v1", "", "", []string{"service", "volume", "detach", "api", "data"}},
		{"change mount", "PATCH", "/projects/p1/services/s1/volumes/v1", `{"mountPath":"/new"}`, `{"id":"v1","name":"data","mountPath":"/new"}`, []string{"service", "volume", "update", "api", "data", "/new"}},
		{"single server default", "POST", "/servers/server1/volumes", `{"name":"backups"}`, `{"id":"v2","name":"backups"}`, []string{"server", "volume", "create", "backups"}},
		{"rename volume", "PATCH", "/servers/server1/volumes/v1", `{"name":"backups"}`, `{"id":"v1","name":"backups"}`, []string{"server", "volume", "rename", "data", "backups"}},
		{"partial repository update", "PATCH", "/projects/p1/services/s1", `{"deployment":{"url":"https://github.com/acme/api","branch":"release","autoDeploy":false,"dockerfilePath":"Dockerfile.prod","dockerContext":"backend","includePaths":[],"ignorePaths":["docs/**"]}}`, `{"id":"s1"}`, []string{"service", "update", "api", "--branch", "release", "--include-path="}},
	} {
		t.Run(test.name, func(t *testing.T) {
			writes := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "GET" {
					switch r.URL.Path {
					case "/projects":
						fmt.Fprint(w, `[{"id":"p1","name":"production"}]`)
					case "/projects/p1/services":
						fmt.Fprint(w, `[{"id":"s1","name":"api","projectId":"p1","serverId":"server1"}]`)
					case "/servers":
						fmt.Fprint(w, `[{"id":"server1","name":"main"}]`)
					case "/servers/server1/volumes":
						fmt.Fprint(w, `[{"id":"v1","name":"data"}]`)
					case "/projects/p1/services/s1":
						fmt.Fprint(w, `{"id":"s1","name":"api","projectId":"p1","serverId":"server1","network":{"customDomains":[{"id":"d1","domain":"app.example.com","status":"active"}]},"volumes":[{"id":"v1","name":"data","mountPath":"/data"}],"deployment":{"url":"https://github.com/acme/api","branch":"main","autoDeploy":false,"dockerfilePath":"Dockerfile.prod","dockerContext":"backend","includePaths":["src/**"],"ignorePaths":["docs/**"]}}`)
					default:
						t.Errorf("unexpected read %s", r.URL.Path)
						http.NotFound(w, r)
					}
					return
				}
				writes++
				if r.Method != test.method || r.URL.Path != test.path {
					t.Errorf("got %s %s", r.Method, r.URL.Path)
				}
				body, _ := io.ReadAll(r.Body)
				if test.body == "" {
					if len(body) != 0 {
						t.Errorf("unexpected body: %s", body)
					}
				} else {
					var got, want any
					if err := json.Unmarshal(body, &got); err != nil {
						t.Fatal(err)
					}
					if err := json.Unmarshal([]byte(test.body), &want); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(got, want) {
						t.Errorf("body = %s, want %s", body, test.body)
					}
				}
				if test.response == "" {
					w.WriteHeader(204)
				} else {
					fmt.Fprint(w, test.response)
				}
			}))
			defer server.Close()
			stdout, stderr, err := runCommand(t, server.URL, append(test.args, "-o", "json")...)
			if err != nil || stderr != "" || writes != 1 || !json.Valid([]byte(stdout)) {
				t.Fatalf("stdout=%s stderr=%s err=%v writes=%d", stdout, stderr, err, writes)
			}
			if strings.Contains(stdout, "sensitive") {
				t.Fatal("secret echoed in result")
			}
		})
	}
}

func TestServiceEnvironmentListRedactsSecrets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/projects":
			fmt.Fprint(w, `[{"id":"p1","name":"production"}]`)
		case "/projects/p1/services":
			fmt.Fprint(w, `[{"id":"s1","name":"api","projectId":"p1"}]`)
		case "/projects/p1/services/s1/env":
			fmt.Fprint(w, `[{"key":"TOKEN","value":"sensitive","secret":true},{"key":"LEVEL","value":"info","secret":false}]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	for _, output := range []string{"text", "json"} {
		stdout, _, err := runCommand(t, server.URL, "service", "env", "list", "api", "-o", output)
		if err != nil || strings.Contains(stdout, "sensitive") || !strings.Contains(stdout, "info") {
			t.Fatalf("stdout=%s err=%v", stdout, err)
		}
	}
}

func TestDeploymentTypeCannotChange(t *testing.T) {
	for _, test := range []struct{ current, flag, value string }{
		{`{"url":"docker.io/nginx:latest"}`, "branch", "main"},
		{`{"url":"docker.io/nginx:latest"}`, "repo", "https://github.com/acme/api"},
		{`{"url":"https://github.com/acme/api"}`, "image", "nginx:latest"},
		{`{"url":"https://github.com/acme/api"}`, "registry-auth", "credential1"},
	} {
		command := (&options{}).serviceUpdateCommand()
		if err := command.Flags().Set(test.flag, test.value); err != nil {
			t.Fatal(err)
		}
		if _, err := updatedDeployment(command, []byte(test.current)); err == nil {
			t.Fatalf("accepted --%s for %s", test.flag, test.current)
		}
	}
}

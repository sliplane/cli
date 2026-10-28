package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/sliplane/cli/internal/api"
)

func TestServiceCreateRequests(t *testing.T) {
	t.Setenv("CREATE_DATABASE_URL", "postgres://owner:local-secret@db/app")
	for _, test := range []struct {
		name, body string
		args       []string
	}{
		{"private image", `{"name":"api","serverId":"server1","deployment":{"url":"docker.io/library/nginx:1.28"},"network":{"public":false}}`, []string{"--image", "nginx:1.28"}},
		{"repository defaults", `{"name":"api","serverId":"server1","deployment":{"url":"https://github.com/acme/api"},"network":{"public":false}}`, []string{"--repo", "https://github.com/acme/api"}},
		{"full image", `{"name":"api","serverId":"server1","deployment":{"url":"ghcr.io/acme/api:v1","registryAuthenticationId":"credential1"},"network":{"public":true,"protocol":"http"},"healthcheck":"/health","cmd":"npm start","env":[{"key":"CREATE_DATABASE_URL","value":"postgres://owner:local-secret@db/app","secret":true},{"key":"EMPTY","value":"","secret":true},{"key":"PORT","value":"3000","secret":false}],"volumes":[{"name":"data","mountPath":"/data"},{"id":"volume1","mountPath":"/cache"}]}`, []string{"--image", "ghcr.io/acme/api:v1", "--registry-auth", "credential1", "--public", "--healthcheck", "/health", "--cmd", "npm start", "--env", "CREATE_DATABASE_URL", "--env", "EMPTY=", "--public-env", "PORT=3000", "--volume", "data:/data", "--mount", "volume1:/cache"}},
		{"full repository", `{"name":"api","serverId":"server1","deployment":{"url":"https://github.com/acme/api.git","branch":"develop","autoDeploy":false,"dockerfilePath":"apps/api/Dockerfile","dockerContext":"apps/api","includePaths":["apps/api/**","packages/{a,b}/**"],"ignorePaths":["**/*.md"]},"network":{"public":true,"protocol":"http"}}`, []string{"--repo", "https://github.com/acme/api.git", "--branch", "develop", "--auto-deploy=false", "--dockerfile", "apps/api/Dockerfile", "--docker-context", "apps/api", "--include-path", "apps/api/**", "--include-path", "packages/{a,b}/**", "--ignore-path", "**/*.md", "--public"}},
		{"automatic build", `{"name":"api","serverId":"server1","deployment":{"url":"https://github.com/acme/api","autoDeploy":true,"dockerfilePath":""},"network":{"public":false}}`, []string{"--repo", "https://github.com/acme/api", "--dockerfile=", "--auto-deploy=true"}},
		{"TCP", `{"name":"api","serverId":"server1","deployment":{"url":"docker.io/library/redis:7"},"network":{"public":true,"protocol":"tcp"}}`, []string{"--image", "redis:7", "--public", "--protocol", "tcp"}},
		{"UDP", `{"name":"api","serverId":"server1","deployment":{"url":"docker.io/acme/game:v1"},"network":{"public":true,"protocol":"udp"}}`, []string{"--image", "acme/game:v1", "--public", "--protocol", "udp"}},
	} {
		for _, output := range []string{"text", "json"} {
			t.Run(test.name+"/"+output, func(t *testing.T) {
				var expected api.CreateServiceInput
				if err := json.Unmarshal([]byte(test.body), &expected); err != nil {
					t.Fatal(err)
				}
				writes := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("Authorization") != "Bearer test-token" {
						t.Error("missing authentication")
					}
					switch {
					case r.Method == "GET" && r.URL.Path == "/projects":
						fmt.Fprint(w, `[{"id":"p1","name":"production"},{"id":"p2","name":"staging"}]`)
					case r.Method == "GET" && r.URL.Path == "/servers":
						fmt.Fprint(w, `[{"id":"server1","name":"main"},{"id":"server2","name":"backup"}]`)
					case r.Method == "POST" && r.URL.Path == "/projects/p1/services":
						writes++
						var got, want map[string]any
						if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
							t.Error(err)
						}
						if err := json.Unmarshal([]byte(test.body), &want); err != nil {
							t.Error(err)
						}
						if !reflect.DeepEqual(got, want) {
							t.Errorf("body = %#v; want %#v", got, want)
						}
						w.WriteHeader(http.StatusCreated)
						network := api.ServiceNetworkDetails{Public: expected.Network.Public, Protocol: expected.Network.Protocol, InternalDomain: "api.internal"}
						if network.Public {
							network.ManagedDomain = "api.sliplane.app"
						}
						if err := json.NewEncoder(w).Encode(map[string]any{"id": "s1", "name": "api", "status": "pending", "deployment": expected.Deployment, "network": network, "env": []api.EnvironmentVariable{{Key: "SECRET", Value: "response-secret", Secret: true}}}); err != nil {
							t.Error(err)
						}
					default:
						t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
						http.NotFound(w, r)
					}
				}))
				defer server.Close()
				args := append([]string{"service", "create", "api", "--project", "production", "--server", "server1", "-o", output}, test.args...)
				stdout, stderr, err := runCommand(t, server.URL, args...)
				if err != nil || stderr != "" || writes != 1 || !strings.Contains(stdout, "s1") || !strings.Contains(stdout, "pending") || !strings.Contains(stdout, "api.internal") || !strings.Contains(stdout, expected.Deployment.URL) {
					t.Fatalf("stdout=%q stderr=%q err=%v writes=%d", stdout, stderr, err, writes)
				}
				wantURL := expected.Network.Public && expected.Network.Protocol == "http"
				if strings.Contains(stdout, "https://api.sliplane.app") != wantURL {
					t.Fatalf("incorrect HTTP URL in %s", stdout)
				}
				if output == "text" && !strings.Contains(stdout, "Access") {
					t.Fatalf("missing access: %s", stdout)
				}
				for _, secret := range []string{"response-secret", "local-secret", "test-token"} {
					if strings.Contains(stdout+stderr, secret) {
						t.Fatal("creation exposed credentials")
					}
				}
				if output == "json" {
					var receipt map[string]any
					if json.Unmarshal([]byte(stdout), &receipt) != nil || receipt["projectId"] != "p1" || receipt["serverId"] != "server1" || receipt["preset"] != nil || receipt["env"] != nil || receipt["network"] == nil || receipt["deployment"] == nil {
						t.Fatalf("incorrect receipt: %s", stdout)
					}
				}
			})
		}
	}
}

func TestServiceCreateValidation(t *testing.T) {
	for _, test := range []struct {
		args []string
		want string
	}{
		{nil, "missing required argument"},
		{[]string{" "}, "service name must not be empty"},
		{[]string{"api"}, "exactly one"},
		{[]string{"api", "--image", "nginx", "--repo", "https://github.com/acme/api"}, "exactly one"},
		{[]string{"api", "--image="}, "--image must not be empty"},
		{[]string{"api", "--repo="}, "--repo must not be empty"},
		{[]string{"api", "--image", "https://docker.io/nginx"}, "without a URL scheme"},
		{[]string{"api", "--image", "nginx latest"}, "whitespace"},
		{[]string{"api", "--repo", "https://token-secret@github.com/acme/api"}, "without credentials"},
		{[]string{"api", "--repo", "https://github.com/acme/api/tree/main"}, "--repo must be"},
		{[]string{"api", "--repo", "https://github.com/acme/api", "--registry-auth", "credential1"}, "requires --image"},
		{[]string{"api", "--repo", "https://github.com/acme/api", "--branch="}, "--branch must not be empty"},
		{[]string{"api", "--repo", "https://github.com/acme/api", "--include-path="}, "must not be empty"},
		{[]string{"api", "--repo", "https://github.com/acme/api", "--docker-context="}, "--docker-context must not be empty"},
		{[]string{"api", "--image", "nginx", "--registry-auth="}, "--registry-auth must not be empty"},
		{[]string{"api", "--image", "nginx", "--server="}, "--server must not be empty"},
		{[]string{"api", "--image", "nginx", "--project="}, "--project must not be empty"},
		{[]string{"api", "--image", "nginx", "--protocol", "tcp"}, "requires --public"},
		{[]string{"api", "--image", "nginx", "--protocol", "invalid"}, "must be http, tcp, or udp"},
		{[]string{"api", "--image", "nginx", "--healthcheck", "/health"}, "requires --public"},
		{[]string{"api", "--image", "nginx", "--public", "--protocol", "tcp", "--healthcheck", "/health"}, "requires --public with --protocol http"},
		{[]string{"api", "--image", "nginx", "--public", "--healthcheck", "health"}, "path starting with /"},
		{[]string{"api", "--image", "nginx", "--volume", "data:relative"}, ":/absolute/path"},
		{[]string{"api", "--image", "nginx", "--mount", ":/data"}, "nonempty name or ID"},
		{[]string{"api", "--image", "nginx", "--volume", "data:/data", "--mount", "volume1:/data"}, "duplicate volume mount path"},
		{[]string{"api", "--image", "nginx", "--env", "BAD KEY=token-secret"}, "require KEY=value or KEY"},
		{[]string{"api", "--image", "nginx", "--env-file", "/does-not-exist/sliplane.env"}, "read environment file"},
	} {
		t.Run(strings.Join(test.args, " "), func(t *testing.T) {
			stdout, stderr, err := runCommand(t, "http://127.0.0.1:1", append([]string{"service", "create"}, test.args...)...)
			if err == nil || !strings.Contains(err.Error(), test.want) || strings.Contains(err.Error(), "token-secret") || stdout != "" || stderr != "" {
				t.Fatalf("stdout=%q stderr=%q err=%v", stdout, stderr, err)
			}
		})
	}
	for _, flag := range []string{"--branch=main", "--auto-deploy=false", "--dockerfile=", "--docker-context=.", "--include-path=src/**", "--ignore-path=docs/**"} {
		stdout, _, err := runCommand(t, "http://127.0.0.1:1", "service", "create", "api", "--image", "nginx", flag)
		if err == nil || !strings.Contains(err.Error(), "requires --repo") || stdout != "" {
			t.Fatalf("%s: stdout=%q err=%v", flag, stdout, err)
		}
	}
}

func TestServiceCreateFailures(t *testing.T) {
	for _, test := range []struct {
		name, projects, servers, want string
		args                          []string
		status, writes                int
	}{
		{"no projects", `[]`, `[]`, "no projects", nil, 0, 0},
		{"no servers", `[{"id":"p1","name":"prod"}]`, `[]`, "no servers", nil, 0, 0},
		{"multiple projects", `[{"id":"p1","name":"prod"},{"id":"p2","name":"stage"}]`, `[]`, "specify --project", nil, 0, 0},
		{"duplicate projects", `[{"id":"p1","name":"prod"},{"id":"p2","name":"prod"}]`, `[]`, "ambiguous", []string{"--project", "prod"}, 0, 0},
		{"multiple servers", `[{"id":"p1","name":"prod"}]`, `[{"id":"s1","name":"main"},{"id":"s2","name":"backup"}]`, "specify --server", nil, 0, 0},
		{"read-only", `[{"id":"p1","name":"prod"}]`, `[{"id":"s1","name":"main"}]`, "read-write token", nil, 403, 1},
		{"server failure", `[{"id":"p1","name":"prod"}]`, `[{"id":"s1","name":"main"}]`, "503", nil, 503, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			writes := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == "GET" && r.URL.Path == "/projects":
					fmt.Fprint(w, test.projects)
				case r.Method == "GET" && r.URL.Path == "/servers":
					fmt.Fprint(w, test.servers)
				case r.Method == "POST" && test.status != 0:
					writes++
					w.WriteHeader(test.status)
					if test.status == 403 {
						fmt.Fprint(w, `{"code":"read_only_token"}`)
					}
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			args := append([]string{"service", "create", "api", "--image", "nginx", "-o", "json"}, test.args...)
			stdout, stderr, err := runCommand(t, server.URL, args...)
			if err == nil || !strings.Contains(err.Error(), test.want) || stdout != "" || stderr != "" || writes != test.writes {
				t.Fatalf("stdout=%q stderr=%q err=%v writes=%d", stdout, stderr, err, writes)
			}
		})
	}
}

func TestServiceImageReferences(t *testing.T) {
	for _, test := range []struct{ input, want string }{
		{"nginx:alpine", "docker.io/library/nginx:alpine"},
		{"nginx", "docker.io/library/nginx:latest"},
		{"library/nginx", "docker.io/library/nginx:latest"},
		{"docker.io/nginx", "docker.io/library/nginx:latest"},
		{"docker.io/library/nginx", "docker.io/library/nginx:latest"},
		{"acme/web", "docker.io/acme/web:latest"},
		{"ghcr.io/acme/web", "ghcr.io/acme/web:latest"},
		{"registry.example:5000/team/web", "registry.example:5000/team/web:latest"},
		{"registry.example:5000/web", "registry.example:5000/web:latest"},
		{"library/nginx:alpine", "docker.io/library/nginx:alpine"},
		{"acme/web:v1", "docker.io/acme/web:v1"},
		{"docker.io/nginx:alpine", "docker.io/library/nginx:alpine"},
		{"docker.io/library/nginx:alpine", "docker.io/library/nginx:alpine"},
		{"ghcr.io/acme/web:v1", "ghcr.io/acme/web:v1"},
		{"localhost:5000/web:v1", "localhost:5000/web:v1"},
		{"localhost/web", "localhost/web:latest"},
		{"registry.example:5000/team/web:v1", "registry.example:5000/team/web:v1"},
		{"nginx@sha256:" + strings.Repeat("a", 64), "docker.io/library/nginx@sha256:" + strings.Repeat("a", 64)},
		{"registry.example:5000/web@sha256:" + strings.Repeat("a", 64), "registry.example:5000/web@sha256:" + strings.Repeat("a", 64)},
	} {
		t.Run(test.input, func(t *testing.T) {
			create := (&options{}).serviceCreateCommand()
			if err := create.Flags().Set("image", test.input); err != nil {
				t.Fatal(err)
			}
			deployment, err := (serviceCreateFlags{image: test.input}).serviceDeployment(create)
			if err != nil || deployment.URL != test.want {
				t.Fatalf("create: %s, %v", deployment.URL, err)
			}
			update := (&options{}).serviceUpdateCommand()
			if err := update.Flags().Set("image", test.input); err != nil {
				t.Fatal(err)
			}
			updated, err := updatedDeployment(update, []byte(`{"url":"docker.io/library/nginx:latest","registryAuthenticationId":"auth1"}`))
			if err != nil {
				t.Fatal(err)
			}
			var image string
			if err := json.Unmarshal(updated["url"], &image); err != nil {
				t.Fatal(err)
			}
			if image != test.want || string(updated["registryAuthenticationId"]) != `"auth1"` {
				t.Fatalf("update: %s", updated)
			}
		})
	}
}

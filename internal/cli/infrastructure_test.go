package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestInfrastructureRequests(t *testing.T) {
	for _, test := range []struct {
		name                         string
		args                         []string
		method, path, body, response string
	}{
		{"server defaults", []string{"server", "create", "web"}, "POST", "/servers", `{"name":"web","instanceType":"starter","location":"ger","billingCycle":"hourly"}`, `{"id":"srv","name":"web","status":"pending"}`},
		{"server overrides", []string{"server", "create", "web", "--instance-type", "base", "--location", "us-east", "--billing-cycle", "monthly", "--disk-size", "100"}, "POST", "/servers", `{"name":"web","instanceType":"base","location":"us-east","billingCycle":"monthly","diskSizeGb":100}`, `{"id":"srv","name":"web"}`},
		{"server compute scale", []string{"server", "scale", "web", "medium"}, "POST", "/servers/srv", `{"instanceType":"medium","billingCycle":"hourly"}`, ""},
		{"server disk growth", []string{"server", "resize-disk", "web", "100"}, "POST", "/servers/srv/disk", `{"diskSizeGb":100}`, ""},
		{"project rename", []string{"project", "rename", "production", "prod"}, "PATCH", "/projects/p1", `{"name":"prod"}`, `{"id":"p1","name":"prod"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			writes := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					switch r.URL.Path {
					case "/servers":
						fmt.Fprint(w, `[{"id":"srv","name":"web","diskSizeGb":50}]`)
					case "/projects":
						fmt.Fprint(w, `[{"id":"p1","name":"production"}]`)
					default:
						t.Errorf("unexpected lookup %s", r.URL.Path)
						w.WriteHeader(404)
					}
					return
				}
				writes++
				if r.Method != test.method || r.URL.Path != test.path {
					t.Errorf("unexpected mutation %s %s", r.Method, r.URL.Path)
				}
				var actual, expected any
				if err := json.NewDecoder(r.Body).Decode(&actual); err != nil {
					t.Error(err)
				}
				if err := json.Unmarshal([]byte(test.body), &expected); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(actual, expected) {
					t.Errorf("body = %#v, want %#v", actual, expected)
				}
				if test.response == "" {
					w.WriteHeader(http.StatusAccepted)
				} else {
					fmt.Fprint(w, test.response)
				}
			}))
			defer server.Close()
			output, _, err := runCommand(t, server.URL, append(test.args, "-o", "json")...)
			if err != nil || !json.Valid([]byte(output)) || writes != 1 {
				t.Fatalf("output %q, writes %d: %v", output, writes, err)
			}
		})
	}
}

func TestResourceDeletionRequiresConfirmation(t *testing.T) {
	for _, resource := range []string{"project", "server", "registry", "ssh-key"} {
		t.Run(resource, func(t *testing.T) {
			writes := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					fmt.Fprint(w, `[{"id":"resource_id","name":"example"}]`)
					return
				}
				if r.Method != http.MethodDelete || !strings.HasSuffix(r.URL.Path, "/resource_id") {
					t.Errorf("unexpected mutation %s %s", r.Method, r.URL.Path)
				}
				writes++
				w.WriteHeader(http.StatusNoContent)
			}))
			defer server.Close()
			output, _, err := runCommand(t, server.URL, resource, "delete", "example")
			if err == nil || !strings.Contains(err.Error(), "--yes") || output != "" || writes != 0 {
				t.Fatalf("unconfirmed deletion %q %v, writes=%d", output, err, writes)
			}
			output, _, err = runCommand(t, server.URL, resource, "delete", "example", "--yes", "-o", "json")
			if err != nil || !strings.Contains(output, `"deleted":true`) || writes != 1 {
				t.Fatalf("confirmed deletion %q %v, writes=%d", output, err, writes)
			}
		})
	}
}

func TestServerDiskCannotShrink(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected mutation %s", r.Method)
		}
		fmt.Fprint(w, `[{"id":"srv","name":"web","diskSizeGb":100}]`)
	}))
	defer server.Close()
	for _, size := range []string{"50", "100"} {
		output, _, err := runCommand(t, server.URL, "server", "resize-disk", "web", size)
		if err == nil || !strings.Contains(err.Error(), "larger than") || output != "" {
			t.Fatalf("resize to %s: %q %v", size, output, err)
		}
	}
}

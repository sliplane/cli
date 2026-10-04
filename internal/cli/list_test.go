package cli

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestListAliases keeps ls interchangeable with list throughout the command tree.
func TestListAliases(t *testing.T) {
	commands := NewCommand().Commands()
	for len(commands) > 0 {
		command := commands[0]
		commands = append(commands[1:], command.Commands()...)
		if command.Name() != "list" {
			continue
		}
		t.Run(command.CommandPath(), func(t *testing.T) {
			resolved, remaining, err := command.Parent().Find([]string{"ls"})
			if err != nil || resolved != command || len(remaining) != 0 {
				t.Fatalf("ls did not resolve to %s: command=%v remaining=%v error=%v", command.CommandPath(), resolved, remaining, err)
			}
		})
	}
}

func TestExpandedLists(t *testing.T) {
	t.Setenv("SLIPLANE_PROFILE", "")
	useBerlinTime(t)
	responses := map[string]string{
		"/projects":                        `[{"id":"project_prod","name":"production"},{"id":"project_stage","name":"staging"}]`,
		"/servers":                         `[{"id":"server_web","name":"web","status":"running","instanceType":"cx23","location":"fsn1","vcpu":2,"memoryMb":4096,"diskSizeGb":40,"ipv4":"203.0.113.1","ipv6":"2001:db8::1","createdAt":"2026-09-23T18:45:35Z"}]`,
		"/projects/project_prod/services":  `[{"id":"service_api","name":"api","projectId":"project_prod","serverId":"server_web","status":"running","createdAt":"2026-09-23T18:45:35Z"}]`,
		"/projects/project_stage/services": `[{"id":"service_stage","name":"preview","projectId":"project_stage","serverId":"server_missing"}]`,
		"/buckets":                         `[{"id":"bucket_uploads","name":"uploads","status":"active","region":"ger","endpoint":"https://s3.example.test","versioning":true,"objectLocking":false,"createdByUserId":"user_owner","createdAt":"2026-09-23T18:45:35Z"}]`,
		"/postgres":                        `[{"id":"database_primary","name":"primary","databaseName":"app","databaseUser":"app_user","region":"ger","version":18,"diskSizeGb":20,"status":"running","connection":{"host":"db.example.test","port":5432,"password":"database-secret","uri":"postgres://secret"},"ipAllowList":[{"source":"203.0.113.0/24"}],"createdByUserId":"user_owner","createdAt":"2026-09-23T18:45:35Z"},{"id":"database_pending","name":"pending","status":"provisioning"}]`,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, exists := responses[r.URL.Path]
		if !exists || r.Method != http.MethodGet {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		fmt.Fprint(w, body)
	}))
	defer server.Close()
	for _, test := range []struct {
		resource string
		metadata []string
	}{
		{"project", []string{"project_prod", "project_stage"}},
		{"server", []string{"server_web", "VCPU", "4096 MB", "40 GB", "2001:db8::1", "2026-09-23 20:45:35 CEST"}},
		{"service", []string{"service_api", "project_prod", "server_web", "2026-09-23 20:45:35 CEST", "service_stage", "server_missing"}},
		{"s3", []string{"bucket_uploads", "VERSIONING", "OBJECT LOCKING", "true", "false", "user_owner", "2026-09-23 20:45:35 CEST"}},
		{"postgres", []string{"database_primary", "database_pending", "app_user", "db.example.test", "5432", "203.0.113.0/24", "user_owner", "2026-09-23 20:45:35 CEST"}},
	} {
		t.Run(test.resource, func(t *testing.T) {
			compact, _, err := runCommand(t, server.URL, test.resource, "list")
			if err != nil || strings.Contains(compact, test.metadata[0]) {
				t.Fatalf("compact list: %q %v", compact, err)
			}
			for _, flag := range []string{"--all", "-a"} {
				output, stderr, err := runCommand(t, server.URL, test.resource, "list", flag)
				if err != nil || stderr != "" {
					t.Fatalf("expanded list: %q %q %v", output, stderr, err)
				}
				for _, want := range test.metadata {
					if !strings.Contains(output, want) {
						t.Errorf("missing %q in %q", want, output)
					}
				}
				if strings.Contains(output, "database-secret") || strings.Contains(output, "postgres://") {
					t.Fatal("expanded list exposed database credentials")
				}
			}
			original, _, err := runCommand(t, server.URL, test.resource, "list", "-o", "json")
			if err != nil {
				t.Fatal(err)
			}
			expanded, _, err := runCommand(t, server.URL, test.resource, "list", "--all", "-o", "json")
			if err != nil || expanded != original {
				t.Fatalf("--all changed JSON: %q %v", expanded, err)
			}
		})
	}
	output, _, err := runCommand(t, server.URL, "service", "list", "--all", "--project", "production")
	if err != nil || !strings.Contains(output, "service_api") || strings.Contains(output, "service_stage") {
		t.Fatalf("--all changed project scope: %q %v", output, err)
	}
}

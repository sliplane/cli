package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPostgresRestoreUsesBackupWindow(t *testing.T) {
	for _, test := range []struct {
		name, at, want, errorText string
	}{
		{name: "latest default", want: "2026-05-25T11:00:00Z"},
		{name: "explicit point", at: "2026-05-25T10:30:00Z", want: "2026-05-25T10:30:00Z"},
		{name: "outside window", at: "2026-05-25T09:00:00Z", errorText: "must be between"},
	} {
		t.Run(test.name, func(t *testing.T) {
			restored := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/postgres":
					fmt.Fprint(w, `[{"id":"pg_main","name":"main"}]`)
				case "/postgres/pg_main":
					fmt.Fprint(w, `{"id":"pg_main","name":"main"}`)
				case "/postgres/pg_main/restore":
					if r.Method == "GET" {
						fmt.Fprint(w, `{"min":"2026-05-25T10:00:00Z","max":"2026-05-25T11:00:00Z"}`)
						return
					}
					restored = true
					var input map[string]string
					if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input["timestamp"] != test.want {
						t.Errorf("restore input = %v, error = %v", input, err)
					}
					w.WriteHeader(http.StatusAccepted)
					fmt.Fprint(w, `{"id":"pg_restored","name":"main-restored"}`)
				default:
					t.Errorf("unexpected request %s", r.URL)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			args := []string{"postgres", "restore", "main", "-o", "json"}
			if test.at != "" {
				args = append(args, "--at", test.at)
			}
			output, _, err := runCommand(t, server.URL, args...)
			if test.errorText != "" {
				if err == nil || !strings.Contains(err.Error(), test.errorText) || restored {
					t.Fatalf("error = %v, restored = %v", err, restored)
				}
				return
			}
			if err != nil || !restored || !strings.Contains(output, "pg_restored") {
				t.Fatalf("output=%s err=%v restored=%v", output, err, restored)
			}
		})
	}
}

func TestPostgresAllowlistPreservesUnrelatedRules(t *testing.T) {
	for _, test := range []struct {
		name        string
		args        []string
		rules, want string
	}{
		{"add normalized host", []string{"add", "main", "203.0.113.8", "--description", "laptop"}, `[{"id":"pgfw_office","source":"198.51.100.0/24","description":"office"}]`, `[{"source":"198.51.100.0/24","description":"office"},{"source":"203.0.113.8/32","description":"laptop"}]`},
		{"remove by normalized CIDR", []string{"remove", "main", "203.0.113.8"}, `[{"id":"pgfw_host","source":"203.0.113.8/32"},{"source":"198.51.100.0/24","description":"office"}]`, `[{"source":"198.51.100.0/24","description":"office"}]`},
		{"remove last rule by ID", []string{"remove", "main", "pgfw_host"}, `[{"id":"pgfw_host","source":"203.0.113.8/32"}]`, `[]`},
		{"clear blocks all", []string{"clear", "main", "--yes"}, `[{"id":"pgfw_all","source":"0.0.0.0/0"}]`, `[]`},
	} {
		t.Run(test.name, func(t *testing.T) {
			writes := 0
			var saved []map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/postgres" {
					fmt.Fprint(w, `[{"id":"pg_main","name":"main"}]`)
					return
				}
				if r.URL.Path != "/postgres/pg_main" {
					t.Errorf("unexpected path %s", r.URL)
					http.NotFound(w, r)
					return
				}
				if r.Method == "GET" {
					fmt.Fprintf(w, `{"id":"pg_main","name":"main","ipAllowList":%s}`, test.rules)
					return
				}
				writes++
				var got map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
					t.Fatal(err)
				}
				var actual, want any
				if err := json.Unmarshal(got["ipAllowList"], &actual); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal([]byte(test.want), &want); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(actual, want) {
					t.Errorf("rules = %s; want %s", got["ipAllowList"], test.want)
				}
				// The API replaces every rule and returns new IDs.
				saved = []map[string]any{}
				for index, rule := range actual.([]any) {
					entry := rule.(map[string]any)
					entry["id"] = fmt.Sprintf("pgfw_saved%d", index)
					saved = append(saved, entry)
				}
				response, _ := json.Marshal(map[string]any{"id": "pg_main", "name": "main", "ipAllowList": saved})
				w.Write(response)
			}))
			defer server.Close()
			args := append([]string{"postgres", "allowlist"}, append(test.args, "-o", "json")...)
			output, _, err := runCommand(t, server.URL, args...)
			if err != nil || writes != 1 {
				t.Fatalf("err=%v writes=%d", err, writes)
			}
			var printed []map[string]any
			if err := json.Unmarshal([]byte(output), &printed); err != nil || len(printed) != len(saved) {
				t.Fatalf("output %q; want the saved rules %v", output, saved)
			}
			for index, entry := range printed {
				if entry["id"] != saved[index]["id"] {
					t.Errorf("printed ID %v; want saved ID %v", entry["id"], saved[index]["id"])
				}
			}
		})
	}
}

func TestBucketManagementDefaultsAndConfirmation(t *testing.T) {
	for _, test := range []struct {
		name                                      string
		args                                      []string
		method, suffix, body, response, errorText string
	}{
		{"CORS default read access", []string{"cors", "set", "uploads", "--origin", "https://app.example.com"}, "PUT", "/cors", `{"rules":[{"allowedOrigins":["https://app.example.com"],"allowedMethods":["GET","HEAD"],"maxAgeSeconds":3600}]}`, `{"rules":[]}`, ""},
		{"CORS upload", []string{"cors", "set", "uploads", "--origin", "https://app.example.com", "--method", "GET,HEAD,PUT", "--header", "*", "--expose-header", "ETag", "--max-age", "0"}, "PUT", "/cors", `{"rules":[{"allowedOrigins":["https://app.example.com"],"allowedMethods":["GET","HEAD","PUT"],"allowedHeaders":["*"],"exposeHeaders":["ETag"],"maxAgeSeconds":0}]}`, `{"rules":[]}`, ""},
		{"disable versioning", []string{"update", "uploads", "--versioning=false"}, "PATCH", "", `{"versioning":false}`, `{"name":"uploads","versioning":false}`, ""},
		{"delete confirmation", []string{"delete", "uploads"}, "", "", "", "", "requires confirmation"},
		{"schedule deletion", []string{"delete", "uploads", "--yes"}, "DELETE", "", "", `{"message":"Bucket scheduled for deletion","deletesAt":"2026-05-26T10:00:00Z"}`, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			writes := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/me":
					fmt.Fprint(w, `{"organizationId":"org_test","tokenType":"rw"}`)
					return
				case "/buckets":
					fmt.Fprint(w, `[{"id":"sb_uploads","name":"uploads"}]`)
					return
				}
				writes++
				if r.Method != test.method || r.URL.Path != "/buckets/sb_uploads"+test.suffix {
					t.Errorf("unexpected operation %s %s", r.Method, r.URL)
				}
				if test.body != "" {
					var got, want any
					if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
						t.Fatal(err)
					}
					if err := json.Unmarshal([]byte(test.body), &want); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(got, want) {
						t.Errorf("body = %v; want %v", got, want)
					}
				}
				fmt.Fprint(w, test.response)
			}))
			defer server.Close()
			args := append([]string{"s3"}, test.args...)
			output, _, err := runCommand(t, server.URL, args...)
			if test.errorText != "" {
				if err == nil || !strings.Contains(err.Error(), test.errorText) || writes != 0 {
					t.Fatalf("err=%v writes=%d", err, writes)
				}
				return
			}
			if err != nil || writes != 1 {
				t.Fatalf("output=%s err=%v writes=%d", output, err, writes)
			}
			if test.name == "schedule deletion" && !strings.Contains(output, "2026-05-26T10:00:00Z") {
				t.Errorf("missing scheduled time: %s", output)
			}
		})
	}
}

func TestCORSFileDoesNotInheritFlagDefaults(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "cors.json")
	if err := os.WriteFile(filename, []byte(`{"rules":[{"allowedOrigins":["https://app.example.com"]}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	_, _, err := runCommand(t, "http://127.0.0.1:1", "s3", "cors", "set", "uploads", "--file", filename)
	if err == nil || !strings.Contains(err.Error(), "requires at least one method") {
		t.Fatalf("error=%v", err)
	}
}

func TestPostgresLogsFiltersAndChronologicalOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/postgres":
			fmt.Fprint(w, `[{"id":"pg_main","name":"main"}]`)
		case "/postgres/pg_main":
			fmt.Fprint(w, `{"id":"pg_main","name":"main"}`)
		case "/postgres/pg_main/logs":
			query := r.URL.Query()
			if query.Get("limit") != "100" || query.Get("since") != "2026-05-25T10:00:00+02:00" || query.Get("until") != "2026-05-25T11:00:00+02:00" {
				t.Errorf("unexpected log filters: %s", r.URL.RawQuery)
			}
			fmt.Fprint(w, `[{"id":"new","createdAt":"2026-05-25T08:30:00Z","severity":"ERROR","message":"newer","detail":"kept in JSON"},{"id":"old","createdAt":"2026-05-25T08:00:00Z","severity":"LOG","message":"older"}]`)
		default:
			t.Errorf("unexpected request %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	output, _, err := runCommand(t, server.URL, "postgres", "logs", "main", "--since", "2026-05-25T10:00:00+02:00", "--until", "2026-05-25T11:00:00+02:00", "-o", "json")
	var logs []struct{ ID, Detail string }
	if err != nil || json.Unmarshal([]byte(output), &logs) != nil || len(logs) != 2 || logs[0].ID != "old" || logs[1].Detail != "kept in JSON" {
		t.Fatalf("output=%s error=%v", output, err)
	}
}

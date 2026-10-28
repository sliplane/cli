package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/sliplane/cli/internal/api"
)

func TestS3Create(t *testing.T) {
	t.Setenv("SLIPLANE_PROFILE", "")
	t.Setenv("SLIPLANE_S3_REGION", "us-east-1")
	for _, test := range []struct {
		name, region string
		flags        []string
		versioning   bool
		locking      bool
	}{
		{name: "defaults", region: "ger"},
		{name: "versioning", region: "us-east", flags: []string{"--region", "us-east", "--versioning"}, versioning: true},
		{name: "object lock", region: "ger", flags: []string{"--object-lock"}, versioning: true, locking: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodPost || r.URL.Path != "/buckets" || r.Header.Get("Authorization") != "Bearer test-token" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				var input api.CreateBucketInput
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input.Name != "my-uploads" || input.Region != test.region || input.Versioning != test.versioning || input.ObjectLocking != test.locking {
					t.Errorf("creation options: %+v %v", input, err)
				}
				w.WriteHeader(http.StatusCreated)
				fmt.Fprintf(w, `{"id":"sb_new","name":"my-uploads","status":"active","region":%q,"endpoint":"https://s3.example.test","versioning":%t,"objectLocking":%t}`, input.Region, input.Versioning, input.ObjectLocking)
			}))
			defer server.Close()
			for _, format := range []string{"text", "json"} {
				args := append([]string{"s3", "create", "my-uploads", "-o", format}, test.flags...)
				output, stderr, err := runCommand(t, server.URL, args...)
				if err != nil || stderr != "" || !strings.Contains(output, "my-uploads") || !strings.Contains(output, "s3.example.test") {
					t.Fatalf("create: %q %q %v", output, stderr, err)
				}
				if format == "json" {
					var bucket api.Bucket
					if json.Unmarshal([]byte(output), &bucket) != nil || bucket.ID != "sb_new" || bucket.Region != test.region || bucket.Versioning != test.versioning || bucket.ObjectLocking != test.locking {
						t.Fatalf("creation receipt: %q", output)
					}
				} else if strings.Contains(output, "sb_new") {
					t.Fatalf("human receipt contains ID: %q", output)
				}
			}
			if requests.Load() != 2 {
				t.Fatalf("expected one POST per command, got %d requests", requests.Load())
			}
		})
	}
}

func TestS3CreateErrors(t *testing.T) {
	t.Setenv("SLIPLANE_PROFILE", "")
	t.Setenv("SLIPLANE_S3_REGION", "ger")
	for _, test := range []struct {
		name, code, want string
		args             []string
		status           int
	}{
		{name: "empty region", args: []string{"uploads", "--region", ""}, want: "--region must be"},
		{name: "signing region", args: []string{"uploads", "--region", "us-east-1"}, want: "--region must be"},
		{name: "empty name", args: []string{" ", "--region", "ger"}, want: "name must not be empty"},
		{name: "locking without versioning", args: []string{"uploads", "--region", "ger", "--object-lock", "--versioning=false"}, want: "requires versioning"},
		{name: "read-only", args: []string{"uploads", "--region", "ger"}, status: 403, code: "read_only_token", want: "read-write token"},
		{name: "invalid name", args: []string{"INVALID", "--region", "ger"}, status: 400, code: "invalid_request", want: "invalid_request"},
		{name: "unavailable", args: []string{"uploads", "--region", "ger"}, status: 503, code: "unavailable", want: "503"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if test.status == 0 {
					t.Error("invalid arguments reached API")
					w.WriteHeader(500)
					return
				}
				w.WriteHeader(test.status)
				fmt.Fprintf(w, `{"code":%q,"message":"creation rejected"}`, test.code)
			}))
			defer server.Close()
			args := append([]string{"s3", "create", "-o", "json"}, test.args...)
			output, stderr, err := runCommand(t, server.URL, args...)
			if err == nil || !strings.Contains(err.Error(), test.want) || output != "" || stderr != "" {
				t.Fatalf("creation error: %q %q %v", output, stderr, err)
			}
			wantRequests := int32(0)
			if test.status != 0 {
				wantRequests = 1
			}
			if requests.Load() != wantRequests {
				t.Fatalf("expected %d requests, got %d", wantRequests, requests.Load())
			}
		})
	}
}

func TestS3Discovery(t *testing.T) {
	t.Setenv("SLIPLANE_PROFILE", "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/buckets" || r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
			return
		}
		fmt.Fprint(w, `[{"id":"sb_uploads","name":"uploads","status":"active","region":"ger","endpoint":"https://s3.example.test","versioning":true,"objectLocking":false,"createdByUserId":"user_one","createdAt":"2026-09-01T10:00:00Z"},{"id":"sb_backups","name":"backups","status":"active","region":"us-east","endpoint":"https://s3-us.example.test","versioning":false,"objectLocking":false}]`)
	}))
	defer server.Close()
	output, stderr, err := runCommand(t, server.URL, "s3", "list", "--project", "unrelated", "-o", "json")
	var buckets []api.Bucket
	if err != nil || stderr != "" || json.Unmarshal([]byte(output), &buckets) != nil || len(buckets) != 2 || buckets[0].Name != "backups" || buckets[1].ID != "sb_uploads" {
		t.Fatalf("bucket list: %q %q %v", output, stderr, err)
	}
	aliasOutput, aliasStderr, aliasErr := runCommand(t, server.URL, "s3", "ls", "-o", "json")
	if aliasErr != nil || aliasStderr != "" || aliasOutput != output {
		t.Fatalf("bucket list alias: %q %q %v", aliasOutput, aliasStderr, aliasErr)
	}
	for _, query := range []string{"uploads", "sb_uploads"} {
		output, _, err = runCommand(t, server.URL, "s3", "show", query, "-o", "json")
		var bucket api.Bucket
		if err != nil || json.Unmarshal([]byte(output), &bucket) != nil || bucket.ID != "sb_uploads" || !bucket.Versioning || bucket.Endpoint != "https://s3.example.test" {
			t.Fatalf("bucket details: %q %v", output, err)
		}
	}
	output, _, err = runCommand(t, server.URL, "s3", "list")
	if err != nil || !strings.Contains(output, "uploads") || !strings.Contains(output, "s3.example.test") || strings.Contains(output, "sb_uploads") {
		t.Fatalf("bucket table: %q %v", output, err)
	}
	output, _, err = runCommand(t, server.URL, "s3", "show", "uploads")
	if err != nil || !strings.Contains(output, "Versioning") || !strings.Contains(output, "true") {
		t.Fatalf("bucket text details: %q %v", output, err)
	}
	output, _, err = runCommand(t, server.URL, "s3", "show", "missing")
	if err == nil || output != "" || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing bucket: %q %v", output, err)
	}
}

func TestPostgresDiscovery(t *testing.T) {
	t.Setenv("SLIPLANE_PROFILE", "")
	ready := `{"id":"pg_primary","name":"primary","databaseName":"app","databaseUser":"app_user","region":"ger","version":18,"diskSizeGb":20,"status":"running","connection":{"host":"db.example.test","port":5432,"database":"app","user":"app_user","password":"database-secret","uri":"postgres://app_user:database-secret@db.example.test/app"},"ipAllowList":[{"id":"fw_one","source":"203.0.113.0/24","description":"office","createdAt":"2026-09-01T10:00:00Z"}],"createdByUserId":"user_one","createdAt":"2026-09-01T10:00:00Z"}`
	pending := `{"id":"pg_pending","name":"pending","databaseName":"app","databaseUser":"app_user","region":"fin","version":18,"diskSizeGb":10,"status":"provisioning","ipAllowList":[]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("unexpected request: %s", r.Method)
		}
		switch r.URL.Path {
		case "/postgres":
			fmt.Fprintf(w, "[%s,%s]", ready, pending)
		case "/postgres/pg_primary":
			fmt.Fprint(w, ready)
		case "/postgres/pg_pending":
			fmt.Fprint(w, pending)
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	for _, format := range []string{"text", "json"} {
		for _, args := range [][]string{{"postgres", "list"}, {"postgres", "show", "primary"}, {"postgres", "show", "pg_primary"}, {"postgres", "show", "pending"}} {
			output, stderr, err := runCommand(t, server.URL, append(args, "-o", format)...)
			if err != nil || stderr != "" {
				t.Fatalf("%v: %q %q %v", args, output, stderr, err)
			}
			for _, secret := range []string{"database-secret", "postgres://", `"password"`, `"uri"`} {
				if strings.Contains(output, secret) {
					t.Fatalf("%v exposed connection credentials", args)
				}
			}
			if format == "json" && !json.Valid([]byte(output)) {
				t.Fatalf("invalid JSON: %q", output)
			}
		}
	}
	output, _, err := runCommand(t, server.URL, "postgres", "list", "-o", "json")
	var databases []api.PostgresDatabase
	if err != nil || json.Unmarshal([]byte(output), &databases) != nil || len(databases) != 2 || databases[0].Name != "pending" || databases[0].Connection != nil || databases[1].Connection.Host != "db.example.test" || databases[1].ID != "pg_primary" {
		t.Fatalf("database JSON: %q %v", output, err)
	}
	output, _, err = runCommand(t, server.URL, "postgres", "list")
	if err != nil || !strings.Contains(output, "20 GB") || strings.Contains(output, "pg_primary") {
		t.Fatalf("database table: %q %v", output, err)
	}
	output, _, err = runCommand(t, server.URL, "postgres", "show", "primary")
	if err != nil || !strings.Contains(output, "db.example.test") || !strings.Contains(output, "203.0.113.0/24") {
		t.Fatalf("database details: %q %v", output, err)
	}
}

func TestStorageErrorsAndEmptyLists(t *testing.T) {
	t.Setenv("SLIPLANE_PROFILE", "")
	for _, resource := range []string{"s3", "postgres"} {
		for _, test := range []struct {
			name, body string
			status     int
			args       []string
			wantError  string
		}{
			{name: "empty", body: `[]`, status: 200, args: []string{"list"}},
			{name: "null", body: `null`, status: 200, args: []string{"list"}},
			{name: "disabled", body: `{"code":"feature_not_enabled","message":"Feature is not enabled for this organization"}`, status: 403, args: []string{"list"}, wantError: "feature_not_enabled"},
			{name: "malformed", body: `not json`, status: 200, args: []string{"list"}, wantError: "decode API response"},
			{name: "ambiguous", body: `[{"id":"one","name":"duplicate"},{"id":"two","name":"duplicate"}]`, status: 200, args: []string{"show", "duplicate"}, wantError: "ambiguous"},
			{name: "missing", body: `[]`, status: 200, args: []string{"show", "missing"}, wantError: "not found"},
		} {
			t.Run(resource+"/"+test.name, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(test.status); fmt.Fprint(w, test.body) }))
				defer server.Close()
				args := append([]string{resource}, test.args...)
				output, stderr, err := runCommand(t, server.URL, append(args, "-o", "json")...)
				if test.wantError != "" {
					if err == nil || !strings.Contains(err.Error(), test.wantError) || output != "" || stderr != "" {
						t.Fatalf("error contract: %q %q %v", output, stderr, err)
					}
					if test.name == "ambiguous" && strings.Contains(err.Error(), "--project") {
						t.Fatal("organization-wide resources suggested project filtering")
					}
					return
				}
				if err != nil || strings.TrimSpace(output) != "[]" || stderr != "" {
					t.Fatalf("empty list: %q %q %v", output, stderr, err)
				}
			})
		}
	}
}

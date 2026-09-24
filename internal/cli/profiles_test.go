// Profile tests exercise team selection, credential isolation, and local lifecycle changes.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/zalando/go-keyring"
	"sliplane-cli/internal/api"
	"sliplane-cli/internal/auth"
	"sliplane-cli/internal/config"
)

// runProfileCommand preserves one test's config across independent CLI invocations.
func runProfileCommand(t *testing.T, serverURL, path string, args ...string) (string, string, error) {
	t.Helper()
	command := newCommand(serverURL, serverURL+"/presets")
	var stdout, stderr bytes.Buffer
	command.SetOut(&stdout)
	command.SetErr(&stderr)
	command.SetArgs(append([]string{"--config", path}, args...))
	err := command.ExecuteContext(context.Background())
	return stdout.String(), stderr.String(), err
}

// TestProfileLifecycle keeps credentials and project defaults isolated between two teams.
func TestProfileLifecycle(t *testing.T) {
	t.Setenv("SLIPLANE_TOKEN", "")
	t.Setenv("SLIPLANE_PROFILE", "")
	keyring.MockInit()
	path := filepath.Join(t.TempDir(), "config.json")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		team := strings.TrimSuffix(token, "-token")
		switch r.URL.Path {
		case "/me":
			fmt.Fprintf(w, `{"authType":"apikey","organizationId":"org_%s","tokenType":"rw","user":{}}`, team)
		case "/projects":
			fmt.Fprintf(w, `[{"id":"p_%s","name":"%s-project"},{"id":"p_other","name":"other"}]`, team, team)
		case "/projects/p_work/services":
			fmt.Fprint(w, `[{"id":"s_work","name":"api","projectId":"p_work"}]`)
		case "/projects/p_other/services":
			fmt.Fprint(w, `[{"id":"s_other","name":"worker","projectId":"p_other"}]`)
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	for _, team := range []string{"work", "personal"} {
		output, stderr, err := runProfileCommand(t, server.URL, path, "auth", "login", "--profile", team, "--token", team+"-token", "-o", "json")
		if err != nil || stderr != "" || !json.Valid([]byte(output)) || strings.Contains(output, team+"-token") {
			t.Fatalf("login %s: %q %q %v", team, output, stderr, err)
		}
	}
	preferences, err := config.Load(path)
	if err != nil || preferences.Version != 1 || preferences.ActiveProfile != "work" || len(preferences.Profiles) != 2 {
		t.Fatalf("profiles: %+v %v", preferences, err)
	}
	if preferences.Profiles["work"].CredentialID == preferences.Profiles["personal"].CredentialID {
		t.Fatal("profiles share a credential")
	}
	contents, err := os.ReadFile(path)
	if err != nil || strings.Contains(string(contents), "work-token") || strings.Contains(string(contents), "personal-token") {
		t.Fatal("config exposed token", err)
	}
	for _, test := range []struct {
		environment string
		flag        []string
		want        string
	}{
		{want: "work"},
		{environment: "personal", want: "personal"},
		{environment: "personal", flag: []string{"--profile", "work"}, want: "work"},
	} {
		t.Setenv("SLIPLANE_PROFILE", test.environment)
		output, _, err := runProfileCommand(t, server.URL, path, append([]string{"auth", "status", "-o", "json"}, test.flag...)...)
		if err != nil || !strings.Contains(output, `"organizationId":"org_`+test.want+`"`) || !strings.Contains(output, `"source":"keychain"`) {
			t.Fatalf("profile precedence: %q %v", output, err)
		}
	}
	t.Setenv("SLIPLANE_PROFILE", "")
	for _, team := range []string{"work", "personal"} {
		if _, _, err := runProfileCommand(t, server.URL, path, "config", "set", "project", team+"-project", "--profile", team); err != nil {
			t.Fatal(err)
		}
	}
	preferences, err = config.Load(path)
	if err != nil || preferences.Profiles["work"].Project != "work-project" || preferences.Profiles["personal"].Project != "personal-project" {
		t.Fatal("project settings crossed profiles", err)
	}
	for _, test := range []struct {
		args []string
		want string
	}{
		{args: []string{"service", "list", "-o", "json"}, want: "s_work"},
		{args: []string{"service", "list", "--project", "other", "-o", "json"}, want: "s_other"},
	} {
		output, _, err := runProfileCommand(t, server.URL, path, test.args...)
		if err != nil || !strings.Contains(output, `"id":"`+test.want+`"`) {
			t.Fatalf("project selection: %q %v", output, err)
		}
	}
	if _, _, err := runProfileCommand(t, server.URL, path, "profile", "use", "personal"); err != nil {
		t.Fatal(err)
	}
	output, _, err := runProfileCommand(t, server.URL, path, "profile", "list", "-o", "json")
	var profiles []profileSummary
	if err != nil || json.Unmarshal([]byte(output), &profiles) != nil || len(profiles) != 2 || profiles[0].Name != "personal" || !profiles[0].Active || !profiles[0].Default {
		t.Fatalf("profile list: %q %v", output, err)
	}
	if _, _, err := runProfileCommand(t, server.URL, path, "auth", "logout", "--profile", "work"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runProfileCommand(t, server.URL, path, "project", "list", "--profile", "work"); err == nil {
		t.Fatal("logged-out profile used another credential")
	}
	if _, _, err := runProfileCommand(t, server.URL, path, "project", "list"); err != nil {
		t.Fatal("logout affected other profile", err)
	}
	if _, _, err := runProfileCommand(t, server.URL, path, "profile", "remove", "personal"); err != nil {
		t.Fatal(err)
	}
	saved, err := config.Load(path)
	if err != nil || saved.ActiveProfile != "" || len(saved.Profiles) != 1 {
		t.Fatal("removing active profile silently selected another", err)
	}
	if _, _, err := auth.Resolve("", false, "", preferences.Profiles["personal"].CredentialID); err == nil {
		t.Fatal("removed profile left its token")
	}
	if _, _, err := runProfileCommand(t, server.URL, path, "project", "list"); err == nil {
		t.Fatal("no active profile fell back to another credential")
	}
}

// TestProfileMismatchStopsResourceRequests guards both reads and writes against stale overrides.
func TestProfileMismatchStopsResourceRequests(t *testing.T) {
	t.Setenv("SLIPLANE_PROFILE", "")
	t.Setenv("SLIPLANE_TOKEN", "personal-token")
	keyring.MockInit()
	path := filepath.Join(t.TempDir(), "config.json")
	preferences := config.Config{Version: 1, ActiveProfile: "work", Profiles: map[string]config.Profile{"work": {OrganizationID: "org_work", CredentialID: "work-key"}}}
	if err := config.Save(path, preferences); err != nil {
		t.Fatal(err)
	}
	if err := auth.Save("work-key", "work-token"); err != nil {
		t.Fatal(err)
	}
	var resourceRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/me" {
			resourceRequests.Add(1)
			fmt.Fprint(w, `[]`)
			return
		}
		organization := "org_personal"
		if r.Header.Get("Authorization") == "Bearer work-token" {
			organization = "org_work"
		}
		fmt.Fprintf(w, `{"organizationId":%q,"tokenType":"rw","authType":"apikey","user":{}}`, organization)
	}))
	defer server.Close()
	for _, args := range [][]string{
		{"project", "list"}, {"project", "create", "example"}, {"deploy", "api"}, {"s3", "list"}, {"postgres", "list"}, {"auth", "status"}, {"auth", "login"},
	} {
		output, _, err := runProfileCommand(t, server.URL, path, append(args, "-o", "json")...)
		if err == nil || !strings.Contains(err.Error(), "bound to") || output != "" {
			t.Fatalf("mismatch %v: %q %v", args, output, err)
		}
	}
	if resourceRequests.Load() != 0 {
		t.Fatal("mismatched token reached resource endpoints")
	}
	if token, _, err := auth.Resolve("", false, "", "work-key"); err != nil || token != "work-token" {
		t.Fatal("mismatched login overwrote credential", err)
	}
	if _, _, err := runProfileCommand(t, server.URL, path, "project", "list", "--token", "work-token"); err != nil {
		t.Fatal("flag did not override stale environment", err)
	}
	before := resourceRequests.Load()
	if output, _, err := runProfileCommand(t, server.URL, path, "project", "list", "--profile", "typo", "--token", "work-token"); err == nil || output != "" {
		t.Fatal("unknown profile accepted")
	}
	if resourceRequests.Load() != before {
		t.Fatal("unknown profile made resource requests")
	}
}

// TestLegacyLoginMigration keeps existing preferences and moves the old token into a bound profile.
func TestLegacyLoginMigration(t *testing.T) {
	t.Setenv("SLIPLANE_PROFILE", "")
	t.Setenv("SLIPLANE_TOKEN", "")
	keyring.MockInit()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"project":"production","output":"json"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := auth.Save(auth.LegacyCredentialID, "legacy-token"); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer legacy-token" {
			t.Error("wrong migration token")
		}
		fmt.Fprint(w, `{"organizationId":"org_work","authType":"apikey","tokenType":"rw","user":{}}`)
	}))
	defer server.Close()
	if _, _, err := runProfileCommand(t, server.URL, path, "auth", "login"); err != nil {
		t.Fatal(err)
	}
	saved, err := config.Load(path)
	if err != nil || saved.ActiveProfile != "default" || saved.Project != "" || saved.Output != "json" || saved.Profiles["default"].Project != "production" {
		t.Fatalf("migration: %+v %v", saved, err)
	}
	if _, _, err := auth.Resolve("", false, "", auth.LegacyCredentialID); err == nil {
		t.Fatal("migrated legacy token was not removed")
	}
	if token, _, err := auth.Resolve("", false, "", saved.Profiles["default"].CredentialID); err != nil || token != "legacy-token" {
		t.Fatal("migration lost token", err)
	}
}

// TestTokenOnlyCIAndExplicitSelection keep CI independent of local credentials and reject empty selectors.
func TestTokenOnlyCIAndExplicitSelection(t *testing.T) {
	t.Setenv("SLIPLANE_TOKEN", "ci-token")
	t.Setenv("SLIPLANE_PROFILE", "")
	keyring.MockInitWithError(fmt.Errorf("no keychain"))
	path := filepath.Join(t.TempDir(), "config.json")
	if err := config.Save(path, config.Config{Version: 1}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/projects" || r.Header.Get("Authorization") != "Bearer ci-token" {
			t.Errorf("unexpected CI request: %s", r.URL.Path)
		}
		fmt.Fprint(w, `[]`)
	}))
	defer server.Close()
	if output, _, err := runProfileCommand(t, server.URL, path, "project", "list", "-o", "json"); err != nil || strings.TrimSpace(output) != "[]" {
		t.Fatalf("CI: %q %v", output, err)
	}
	for _, args := range [][]string{{"project", "list", "--profile", ""}, {"project", "list", "--token", ""}, {"auth", "login", "--profile", "bad name"}} {
		if _, _, err := runProfileCommand(t, server.URL, path, args...); err == nil {
			t.Fatalf("accepted invalid selection: %v", args)
		}
	}
}

// TestFailedProfileSaveRemovesNewCredential prevents orphaning a token when config cannot be saved.
func TestFailedProfileSaveRemovesNewCredential(t *testing.T) {
	keyring.MockInit()
	parent := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(parent, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	options := options{profileName: "work", configPath: filepath.Join(parent, "config.json"), preferences: config.Config{Profiles: map[string]config.Profile{}}}
	if err := options.saveLogin("new-token", api.Identity{OrganizationID: "org_work"}); err == nil {
		t.Fatal("save should fail")
	}
	credentialID := options.preferences.Profiles["work"].CredentialID
	if credentialID == "" {
		t.Fatal("test did not exercise credential save")
	}
	if _, _, err := auth.Resolve("", false, "", credentialID); err == nil {
		t.Fatal("failed config save left a credential")
	}
}

package cli

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/zalando/go-keyring"
	"sliplane-cli/internal/auth"
	"sliplane-cli/internal/config"
)

func TestLoginVerifiesBeforeSaving(t *testing.T) {
	t.Setenv("SLIPLANE_TOKEN", "")
	t.Setenv("SLIPLANE_PROFILE", "")
	keyring.MockInit()
	path := filepath.Join(t.TempDir(), "config.json")
	preferences := config.Config{Version: 1, ActiveProfile: "work", Profiles: map[string]config.Profile{"work": {OrganizationID: "org1", CredentialID: "credential_work"}}}
	if err := config.Save(path, preferences); err != nil {
		t.Fatal(err)
	}
	if err := auth.Save("credential_work", "previous-token"); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/me" {
			t.Errorf("unexpected auth endpoint %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"code":"invalid_token","message":"Invalid token"}`)
	}))
	stdout, _, err := runProfileCommand(t, server.URL, path, "auth", "login", "--token", "invalid", "-o", "json")
	server.Close()
	if err == nil || stdout != "" {
		t.Fatalf("failed login result: %q %v", stdout, err)
	}
	if token, _, err := auth.Resolve("", false, "", "credential_work"); err != nil || token != "previous-token" {
		t.Fatal("rejected token overwrote keychain", err)
	}
	validServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"authType":"apikey","organizationId":"org1","tokenType":"ro","user":{}}`)
	}))
	defer validServer.Close()
	if _, _, err := runProfileCommand(t, validServer.URL, path, "auth", "login", "--token", "verified-token", "-o", "json"); err != nil {
		t.Fatal(err)
	}
	if token, _, err := auth.Resolve("", false, "", "credential_work"); err != nil || token != "verified-token" {
		t.Fatal("verified token was not saved", err)
	}
	saved, err := config.Load(path)
	if err != nil || saved.Profiles["work"].CredentialID != "credential_work" {
		t.Fatal("rotation changed credential reference", err)
	}
}

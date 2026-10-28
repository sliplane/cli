package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sliplane/cli/internal/auth"
	"github.com/sliplane/cli/internal/config"
	"github.com/spf13/cobra"
	"github.com/zalando/go-keyring"
)

func TestLoginTokenSelection(t *testing.T) {
	for _, test := range []struct {
		name, flag, environment, want string
		flagSet                       bool
	}{
		{name: "flag wins", flag: "flag-token", flagSet: true, environment: "environment-token", want: "flag-token"},
		{name: "environment wins", environment: "environment-token", want: "environment-token"},
		{name: "saved profile requires browser"},
		{name: "empty explicit token fails", flagSet: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("SLIPLANE_TOKEN", test.environment)
			keyring.MockInit()
			if err := auth.Save("credential_work", "old-token"); err != nil {
				t.Fatal(err)
			}
			options := &options{}
			command := &cobra.Command{}
			command.Flags().StringVar(&options.token, "token", "", "")
			if test.flagSet {
				if err := command.Flags().Set("token", test.flag); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			command.SetContext(ctx)
			credential, err := options.loginToken(command)
			if credential.AccessToken != test.want || credential.RefreshToken != "" {
				t.Fatalf("got %+v, want token %q", credential, test.want)
			}
			if !test.flagSet && test.environment == "" && !errors.Is(err, context.Canceled) {
				t.Fatalf("browser login ignored cancellation: %v", err)
			}
			if test.want != "" && err != nil {
				t.Fatal(err)
			}
			if test.flagSet && test.flag == "" && err == nil {
				t.Fatal("empty explicit token fell back to another credential")
			}
			if saved, err := auth.Load("credential_work"); err != nil || saved != "old-token" {
				t.Fatal("selecting credentials changed the saved token", err)
			}
		})
	}
}

func TestLoginVerifiesBeforeSaving(t *testing.T) {
	t.Setenv("SLIPLANE_TOKEN", "")
	t.Setenv("SLIPLANE_PROFILE", "")
	keyring.MockInit()
	path := filepath.Join(t.TempDir(), "config.json")
	preferences := config.Config{ActiveProfile: "work", Profiles: map[string]config.Profile{"work": {OrganizationID: "org1", CredentialID: "credential_work", Project: "production", S3Credentials: map[string]string{"bucket": "s3_work"}}}}
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
		fmt.Fprint(w, `{"authType":"oauth","organizationId":"org1","tokenType":"ro","user":{},"availableOrganizations":[{"id":"org1","name":"Work Team"}]}`)
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
	profile := saved.Profiles["work"]
	if profile.OrganizationName != "Work Team" || profile.Project != "production" || profile.S3Credentials["bucket"] != "s3_work" {
		t.Fatalf("same-team login lost settings or did not save the team name: %+v", profile)
	}
}

func TestBrowserLoginRefresh(t *testing.T) {
	t.Setenv("SLIPLANE_TOKEN", "")
	t.Setenv("SLIPLANE_PROFILE", "")
	for _, test := range []struct {
		name, saved, wantAccess, wantError string
		refreshFails, rotatedElsewhere     bool
		wantRefreshes                      int
	}{
		{name: "refreshes once and saves the new pair", saved: `{"accessToken":"old-access","refreshToken":"old-refresh"}`, wantAccess: "new-access", wantRefreshes: 1},
		{name: "reuses a pair rotated by another process", saved: `{"accessToken":"old-access","refreshToken":"old-refresh"}`, rotatedElsewhere: true, wantAccess: "other-access"},
		{name: "expired refresh token asks to log in", saved: `{"accessToken":"old-access","refreshToken":"old-refresh"}`, refreshFails: true, wantAccess: "old-access", wantError: "run sliplane auth login", wantRefreshes: 1},
		{name: "API tokens are never refreshed", saved: "old-access", wantAccess: "old-access", wantError: "HTTP 401"},
	} {
		t.Run(test.name, func(t *testing.T) {
			keyring.MockInit()
			path := filepath.Join(t.TempDir(), "config.json")
			if err := config.Save(path, config.Config{ActiveProfile: "work", Profiles: map[string]config.Profile{"work": {OrganizationID: "org1", CredentialID: "credential_work"}}}); err != nil {
				t.Fatal(err)
			}
			if err := auth.Save("credential_work", test.saved); err != nil {
				t.Fatal(err)
			}
			refreshes := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/token":
					refreshes++
					if err := r.ParseForm(); err != nil || r.Form.Get("refresh_token") != "old-refresh" || test.refreshFails {
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					fmt.Fprint(w, `{"access_token":"new-access","token_type":"Bearer","refresh_token":"new-refresh"}`)
				case "/me":
					switch r.Header.Get("Authorization") {
					case "Bearer new-access", "Bearer other-access":
						fmt.Fprint(w, `{"organizationId":"org1"}`)
					default:
						if test.rotatedElsewhere {
							if err := auth.Save("credential_work", `{"accessToken":"other-access","refreshToken":"other-refresh"}`); err != nil {
								t.Error(err)
							}
						}
						w.WriteHeader(http.StatusUnauthorized)
					}
				case "/projects":
					fmt.Fprint(w, `[]`)
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
				}
			}))
			defer server.Close()
			previous := oauthClient
			oauthClient.TokenURL = server.URL + "/token"
			t.Cleanup(func() { oauthClient = previous })
			_, _, err := runProfileCommand(t, server.URL, path, "project", "list", "-o", "json")
			if test.wantError == "" && err != nil || test.wantError != "" && (err == nil || !strings.Contains(err.Error(), test.wantError)) {
				t.Fatalf("got error %v, want %q", err, test.wantError)
			}
			if refreshes != test.wantRefreshes {
				t.Fatalf("got %d refreshes, want %d", refreshes, test.wantRefreshes)
			}
			if credential, err := auth.LoadCredential("credential_work"); err != nil || credential.AccessToken != test.wantAccess {
				t.Fatalf("saved %+v, %v; want access token %q", credential, err, test.wantAccess)
			}
		})
	}
}

func TestLogoutRemovesBrowserLogin(t *testing.T) {
	t.Setenv("SLIPLANE_TOKEN", "")
	t.Setenv("SLIPLANE_PROFILE", "")
	keyring.MockInit()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := config.Save(path, config.Config{ActiveProfile: "work", Profiles: map[string]config.Profile{"work": {OrganizationID: "org1", CredentialID: "credential_work"}}}); err != nil {
		t.Fatal(err)
	}
	if err := auth.SaveCredential("credential_work", auth.Credential{AccessToken: "access", RefreshToken: "refresh"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runProfileCommand(t, "http://127.0.0.1:1", path, "auth", "logout"); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Load("credential_work"); !errors.Is(err, keyring.ErrNotFound) {
		t.Fatalf("logout kept the saved login: %v", err)
	}
}

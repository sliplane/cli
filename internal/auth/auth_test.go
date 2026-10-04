package auth

import (
	"errors"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestResolvePrecedence(t *testing.T) {
	keyring.MockInit()
	if err := Save("credential_work", "saved-token"); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, flag, environment, want, source string
		flagSet                               bool
	}{
		{name: "flag wins", flag: "flag-token", flagSet: true, environment: "env-token", want: "flag-token", source: "flag"},
		{name: "environment wins", environment: "env-token", want: "env-token", source: "environment"},
		{name: "keychain fallback", want: "saved-token", source: "keychain"},
	} {
		t.Run(test.name, func(t *testing.T) {
			token, source, err := Resolve(test.flag, test.flagSet, test.environment, "credential_work")
			if err != nil || token != test.want || source != test.source {
				t.Fatalf("got %q, %q, %v", token, source, err)
			}
		})
	}
	if _, _, err := Resolve("", true, "env-token", "credential_work"); err == nil {
		t.Fatal("empty explicit flag must not fall back")
	}
	if _, _, err := Resolve("secret\nvalue", true, "", "credential_work"); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("unsafe validation: %v", err)
	}
	keyring.MockInitWithError(errors.New("keychain unavailable"))
	if token, _, err := Resolve("", false, "ci-token", "credential_work"); err != nil || token != "ci-token" {
		t.Fatal("CI token tried keychain", err)
	}
	if _, _, err := Resolve("", false, "", "credential_work"); err == nil {
		t.Fatal("keychain error was hidden")
	}
	keyring.MockInit()
	if _, _, err := Resolve("", false, "", "credential_work"); err == nil {
		t.Fatal("missing token accepted")
	}
	if err := Delete("credential_work"); err != nil {
		t.Fatal(err)
	}
}

func TestCredentialsAreIsolated(t *testing.T) {
	keyring.MockInit()
	for id, token := range map[string]string{LegacyCredentialID: "legacy", "work": "work-token", "personal": "personal-token"} {
		if err := Save(id, token); err != nil {
			t.Fatal(err)
		}
	}
	if err := Delete("work"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Resolve("", false, "", "work"); err == nil {
		t.Fatal("missing profile fell back to another token")
	}
	if _, _, err := Resolve("", false, "", ""); err == nil {
		t.Fatal("unselected profile read a saved token")
	}
	if token, _, err := Resolve("", false, "", "personal"); err != nil || token != "personal-token" {
		t.Fatalf("other credential affected: %q %v", token, err)
	}
}

package config

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestConfigRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.json")
	if config, err := Load(path); err != nil || !reflect.DeepEqual(config, Config{}) {
		t.Fatalf("missing config: %+v %v", config, err)
	}
	want := Config{Project: "production", Output: "json"}
	if err := Save(path, want); err != nil {
		t.Fatal(err)
	}
	if got, err := Load(path); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("roundtrip: %+v %v", got, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatalf("permissions %o", info.Mode().Perm())
	}
	for _, contents := range []string{`{"token":"secret"}`, `{"output":"yaml"}`, `null`, `{} {}`, `{"project":3}`} {
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Fatalf("accepted invalid config %s", contents)
		}
	}
}

func TestProfileValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	want := Config{Version: 1, ActiveProfile: "work", Output: "json", Profiles: map[string]Profile{"work": {OrganizationID: "org_work", CredentialID: "credential_work", Project: "api"}}}
	if err := Save(path, want); err != nil {
		t.Fatal(err)
	}
	if got, err := Load(path); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("profile roundtrip: %+v %v", got, err)
	}
	for _, contents := range []string{
		`{"version":2}`,
		`{"version":1,"activeProfile":"missing"}`,
		`{"version":1,"profiles":{"work":{"credentialId":"work"}}}`,
		`{"version":1,"profiles":{"work":{"organizationId":"org","credentialId":"work","token":"secret"}}}`,
		`{"version":1,"profiles":{"work":{"organizationId":"org","credentialId":"api-token"}}}`,
		`{"version":1,"profiles":{"bad name":{"organizationId":"org","credentialId":"work"}}}`,
		`{"version":1,"profiles":{"work":{"organizationId":"org","credentialId":"same"},"personal":{"organizationId":"other","credentialId":"same"}}}`,
		`{"profiles":{"work":{"organizationId":"org","credentialId":"work"}}}`,
	} {
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Fatalf("accepted invalid profile config: %s", contents)
		}
	}
}

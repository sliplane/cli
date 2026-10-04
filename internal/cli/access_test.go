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

func TestOAuthClientUpdatePreservesOmittedSettings(t *testing.T) {
	for _, test := range []struct {
		args []string
		body string
	}{
		{[]string{"--name", "new"}, `{"name":"new"}`},
		{[]string{"--image-url="}, `{"imageUrl":""}`},
		{[]string{"--redirect-uri", "https://example.test/callback?a=1,b=2", "--redirect-uri", "http://localhost:8080/callback"}, `{"redirectUris":["https://example.test/callback?a=1,b=2","http://localhost:8080/callback"]}`},
	} {
		t.Run(test.body, func(t *testing.T) {
			writes := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					fmt.Fprint(w, `[{"id":"oc1","name":"app"}]`)
					return
				}
				writes++
				if r.Method != http.MethodPatch || r.URL.Path != "/oauth-clients/oc1" {
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
				fmt.Fprint(w, `{"id":"oc1","name":"app"}`)
			}))
			defer server.Close()
			_, _, err := runCommand(t, server.URL, append([]string{"oauth-client", "update", "app"}, test.args...)...)
			if err != nil || writes != 1 {
				t.Fatalf("writes=%d: %v", writes, err)
			}
		})
	}
}

func TestRegistryTokenNeverAppearsInOutput(t *testing.T) {
	t.Setenv("SLIPLANE_REGISTRY_TOKEN", "registry-private-secret")
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/registry-credentials" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				var input map[string]string
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
					t.Error(err)
				}
				if input["token"] != "registry-private-secret" || input["username"] != "user" || input["type"] != "dockerhub" {
					t.Errorf("incorrect credential payload")
				}
				if fail {
					w.WriteHeader(http.StatusBadRequest)
					fmt.Fprint(w, `{"message":"invalid registry-private-secret"}`)
					return
				}
				fmt.Fprint(w, `{"id":"cred1","name":"docker","type":"dockerhub","username":"user","token":"registry-private-secret"}`)
			}))
			defer server.Close()
			output, stderr, err := runCommand(t, server.URL, "registry", "create", "docker", "--username", "user", "-o", "json")
			if strings.Contains(output+stderr+fmt.Sprint(err), "registry-private-secret") {
				t.Fatal("registry token leaked")
			}
			if (err != nil) != fail {
				t.Fatalf("unexpected error %v", err)
			}
			if !fail && !strings.Contains(output, `"id":"cred1"`) {
				t.Fatalf("missing credential receipt %q", output)
			}
		})
	}
}

func TestPermanentSSHKeyOmitsExpiryAndRejectsPrivateKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "id_ed25519.pub")
	if err := os.WriteFile(path, []byte("ssh-ed25519 AAAA example\n"), 0600); err != nil {
		t.Fatal(err)
	}
	writes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writes++
		if r.Method != http.MethodPost || r.URL.Path != "/ssh-keys" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var input map[string]any
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		if _, exists := input["expiresAt"]; exists {
			t.Error("permanent key has an expiration")
		}
		if input["name"] != "laptop" || input["publicKey"] != "ssh-ed25519 AAAA example" {
			t.Errorf("incorrect key payload %#v", input)
		}
		fmt.Fprint(w, `{"id":"key1","name":"laptop"}`)
	}))
	defer server.Close()
	output, _, err := runCommand(t, server.URL, "ssh-key", "register", path, "--name", "laptop", "-o", "json")
	if err != nil || writes != 1 || !strings.Contains(output, `"id":"key1"`) {
		t.Fatalf("registration %q %v", output, err)
	}
	if err := os.WriteFile(path, []byte("-----BEGIN OPENSSH PRIVATE KEY-----\nsecret"), 0600); err != nil {
		t.Fatal(err)
	}
	output, _, err = runCommand(t, server.URL, "ssh-key", "register", path, "--name", "laptop")
	if err == nil || !strings.Contains(err.Error(), "public key") || writes != 1 || output != "" {
		t.Fatalf("private key registration %q %v", output, err)
	}
}

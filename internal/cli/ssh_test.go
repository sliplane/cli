// SSH tests exercise real key generation, subprocesses, and the HTTP lifecycle without a production service.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"sliplane-cli/internal/api"
)

// TestSSHSessionLifecycle verifies cleanup after success, setup failure, and cancellation.
func TestSSHSessionLifecycle(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("OpenSSH is not installed")
	}
	for _, scenario := range []string{"success", "attach failure", "invalid connection", "session failure", "canceled", "cleanup failure", "already expired"} {
		t.Run(scenario, func(t *testing.T) {
			workspace := t.TempDir()
			identities := filepath.Join(workspace, "identities")
			if err := os.Mkdir(identities, 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("TMPDIR", identities)
			trace := filepath.Join(workspace, "ssh-trace")
			t.Setenv("SSH_TEST_TRACE", trace)
			t.Setenv("SSH_TEST_SCENARIO", scenario)
			ssh := `#!/bin/sh
identity=
control=
master=
operation=
port=
username=
host=
while [ "$#" -gt 0 ]; do
  case "$1" in
    -i) identity=$2; shift ;;
    -S) control=$2; shift ;;
    -p) port=$2; shift ;;
    -l) username=$2; shift ;;
    --) host=$2; shift ;;
    -M) master=yes ;;
    -O) operation=$2; shift ;;
  esac
  shift
done
if [ "$port" != 2222 ] || [ "$username" != ssh-user ] || [ "$host" != ssh.example.com ]; then exit 91; fi
if [ "$operation" = exit ]; then
  echo close >> "$SSH_TEST_TRACE"
  exit 0
fi
if [ ! -f "$identity" ]; then exit 90; fi
if [ "$master" = yes ]; then
  echo master >> "$SSH_TEST_TRACE"
  touch "$control"
  exit 0
fi
echo shell >> "$SSH_TEST_TRACE"
echo 'remote shell output'
case "$SSH_TEST_SCENARIO" in
  'session failure') exit 23 ;;
  canceled) exec /bin/sleep 30 ;;
esac
`
			if err := os.WriteFile(filepath.Join(workspace, "ssh"), []byte(ssh), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", workspace+string(os.PathListSeparator)+os.Getenv("PATH"))
			var requests []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests = append(requests, r.Method+" "+r.URL.Path)
				switch r.Method + " " + r.URL.Path {
				case "GET /projects":
					fmt.Fprint(w, `[{"id":"p1","name":"prod"}]`)
				case "GET /projects/p1/services":
					fmt.Fprint(w, `[{"id":"s1","name":"api","projectId":"p1"}]`)
				case "POST /ssh-keys":
					var body struct {
						Name      string    `json:"name"`
						PublicKey string    `json:"publicKey"`
						ExpiresAt time.Time `json:"expiresAt"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if body.Name != "Sliplane CLI: api" || !strings.HasPrefix(body.PublicKey, "ssh-ed25519 ") || time.Until(body.ExpiresAt) < 119*time.Minute || time.Until(body.ExpiresAt) > 2*time.Hour {
						t.Errorf("invalid public key request: %+v", body)
					}
					entries, err := os.ReadDir(identities)
					if err != nil || len(entries) != 1 {
						t.Errorf("temporary identity directory: %v %v", entries, err)
						w.WriteHeader(500)
						return
					}
					for path, mode := range map[string]os.FileMode{filepath.Join(identities, entries[0].Name()): 0o700, filepath.Join(identities, entries[0].Name(), "key"): 0o600} {
						info, err := os.Stat(path)
						if err != nil || info.Mode().Perm() != mode {
							t.Errorf("incorrect permissions for %s: %v", path, err)
						}
					}
					w.WriteHeader(201)
					fmt.Fprint(w, `{"id":"pk1"}`)
				case "PUT /projects/p1/services/s1/ssh-keys/pk1":
					if scenario == "attach failure" {
						w.WriteHeader(503)
						return
					}
					if scenario == "invalid connection" {
						fmt.Fprint(w, `{"host":"-oProxyCommand=bad","port":2222,"username":"ssh-user"}`)
						return
					}
					fmt.Fprint(w, `{"host":"ssh.example.com","port":2222,"username":"ssh-user"}`)
				case "DELETE /projects/p1/services/s1/ssh-keys/pk1", "DELETE /ssh-keys/pk1":
					if scenario == "cleanup failure" {
						w.WriteHeader(500)
						return
					}
					if scenario == "already expired" {
						w.WriteHeader(404)
						return
					}
					w.WriteHeader(204)
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			command := newCommand(server.URL)
			var stdout, stderr bytes.Buffer
			command.SetOut(&stdout)
			if scenario == "canceled" {
				command.SetOut(&sshCancelWriter{cancel: cancel})
			}
			command.SetIn(strings.NewReader(""))
			command.SetErr(&stderr)
			command.SetArgs([]string{"--config", filepath.Join(workspace, "config.json"), "--token", "test-token", "ssh", "api", "--ttl", "2h"})
			err := command.ExecuteContext(ctx)
			switch scenario {
			case "success", "already expired":
				if err != nil || stdout.String() != "remote shell output\n" {
					t.Fatalf("session: %v, output %q", err, stdout.String())
				}
			case "canceled":
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation: %v", err)
				}
			case "session failure":
				var exit *exec.ExitError
				if !errors.As(err, &exit) || exit.ExitCode() != 23 {
					t.Fatalf("lost SSH exit status: %v", err)
				}
			case "cleanup failure":
				if err == nil || !strings.Contains(err.Error(), "automatic deletion scheduled") {
					t.Fatalf("cleanup failure: %v", err)
				}
			default:
				if err == nil {
					t.Fatal("expected setup error")
				}
			}
			want := []string{"GET /projects", "GET /projects/p1/services", "POST /ssh-keys", "PUT /projects/p1/services/s1/ssh-keys/pk1", "DELETE /projects/p1/services/s1/ssh-keys/pk1", "DELETE /ssh-keys/pk1"}
			if !reflect.DeepEqual(requests, want) {
				t.Fatalf("requests: %v", requests)
			}
			entries, err := os.ReadDir(identities)
			if err != nil || len(entries) != 0 {
				t.Fatalf("private identity remains: %v %v", entries, err)
			}
			if scenario != "attach failure" && scenario != "invalid connection" {
				data, err := os.ReadFile(trace)
				if err != nil || string(data) != "master\nshell\nclose\n" {
					t.Fatalf("SSH process lifecycle: %q %v", data, err)
				}
			}
		})
	}
}

type sshCancelWriter struct{ cancel context.CancelFunc }

// Write cancels a command once its child process has opened the shell.
func (w *sshCancelWriter) Write(data []byte) (int, error) {
	w.cancel()
	return len(data), nil
}

// TestSSHConnectionValidation prevents malformed API data from reaching OpenSSH.
func TestSSHConnectionValidation(t *testing.T) {
	for _, test := range []struct {
		connection api.SSHConnection
		valid      bool
	}{
		{connection: api.SSHConnection{}},
		{connection: api.SSHConnection{Host: "127.0.0.1", Port: 22222, Username: "service_1"}, valid: true},
		{connection: api.SSHConnection{Host: "2001:db8::1", Port: 22222, Username: "service_1"}, valid: true},
		{connection: api.SSHConnection{Host: "ssh.example.com", Port: 22, Username: "service_1"}, valid: true},
		{connection: api.SSHConnection{Host: "-oProxyCommand=bad", Port: 22, Username: "service_1"}},
		{connection: api.SSHConnection{Host: "user@host", Port: 22, Username: "service_1"}},
		{connection: api.SSHConnection{Host: "host", Port: 0, Username: "service_1"}},
		{connection: api.SSHConnection{Host: "host", Port: 22, Username: ""}},
	} {
		if err := validateSSHConnection(test.connection); (err == nil) != test.valid {
			t.Errorf("connection %+v: %v", test.connection, err)
		}
	}
}

// TestSSHArgumentsKeepHostVerification protects against silent host-key bypass or other saved identities.
func TestSSHArgumentsKeepHostVerification(t *testing.T) {
	arguments := sshArguments(api.SSHConnection{Host: "host", Port: 22222, Username: "service_1"}, "/private/key", "/private/control")
	for _, required := range []string{"StrictHostKeyChecking=accept-new", "IdentitiesOnly=yes", "IdentityAgent=none", "BatchMode=yes"} {
		if !strings.Contains(strings.Join(arguments, " "), required) {
			t.Errorf("missing %s", required)
		}
	}
}

// TestSSHUsageRejectsInvalidOptions fails before any credentials or remote changes are needed.
func TestSSHUsageRejectsInvalidOptions(t *testing.T) {
	for _, test := range []struct {
		args    []string
		message string
	}{
		{args: []string{"ssh"}, message: "missing required argument"},
		{args: []string{"ssh", "api", "extra"}, message: "too many arguments"},
		{args: []string{"ssh", "api", "--ttl", "0s"}, message: "--ttl must be longer than 5m and at most 720h"},
		{args: []string{"ssh", "api", "--ttl", "4m"}, message: "--ttl must be longer than 5m and at most 720h"},
		{args: []string{"ssh", "api", "--ttl", "5m"}, message: "use 6m or longer"},
		{args: []string{"ssh", "api", "--ttl", "721h"}, message: "--ttl must be longer than 5m and at most 720h"},
		{args: []string{"ssh", "api", "-o", "json"}, message: "require --output text"},
		{args: []string{"ssh", "api", "-N"}, message: "-N requires a local forward"},
		{args: []string{"ssh", "api", "-L", ""}, message: "-L requires"},
		{args: []string{"ssh", "api", "-L", "not-a-forward"}, message: "invalid SSH forwarding options"},
	} {
		stdout, _, err := runCommand(t, "http://127.0.0.1:1", test.args...)
		if stdout != "" || err == nil || !strings.Contains(err.Error(), test.message) {
			t.Errorf("%v: stdout %q, error %v", test.args, stdout, err)
		}
	}
}

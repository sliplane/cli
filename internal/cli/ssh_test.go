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

	"github.com/sliplane/cli/internal/api"
)

func TestSSHSessionLifecycle(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("OpenSSH is not installed")
	}
	for _, scenario := range []string{"success", "tunnel", "tunnel override", "tunnel guess", "tunnel guess failure", "attach failure", "invalid connection", "session failure", "canceled", "cleanup failure", "already expired"} {
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
forward=
no_shell=
probe=
while [ "$#" -gt 0 ]; do
  case "$1" in
    -i) identity=$2; shift ;;
    -S) control=$2; shift ;;
    -p) port=$2; shift ;;
    -l) username=$2; shift ;;
    --) host=$2; shift ;;
    -M) master=yes ;;
    -O) operation=$2; shift ;;
    -L) forward=$2; shift ;;
    -N) no_shell=yes ;;
    -W) probe=$2; shift ;;
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
if [ -n "$probe" ]; then
  if [ "$probe" != localhost:80 ]; then exit 94; fi
  echo probe >> "$SSH_TEST_TRACE"
  if [ "$SSH_TEST_SCENARIO" = 'tunnel guess failure' ]; then exit 1; fi
  printf 'HTTP/1.1 401 Unauthorized\r\nContent-Length: 0\r\n\r\n'
  exit 0
fi
case "$SSH_TEST_SCENARIO" in
  tunnel)
    case "$forward" in 127.0.0.1:*:localhost:5678) ;; *) exit 92 ;; esac
    if [ "$no_shell" != yes ]; then exit 93; fi ;;
  'tunnel override')
    case "$forward" in 127.0.0.1:*:localhost:8080) ;; *) exit 92 ;; esac
    if [ "$no_shell" != yes ]; then exit 93; fi ;;
  'tunnel guess')
    case "$forward" in 127.0.0.1:*:localhost:80) ;; *) exit 92 ;; esac ;;
esac
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
					port := 5678
					if strings.HasPrefix(scenario, "tunnel guess") || scenario == "tunnel override" {
						port = 0
					}
					fmt.Fprintf(w, `[{"id":"s1","name":"api","projectId":"p1","network":{"public":false,"internalPort":%d}}]`, port)
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
			command := newCommand(server.URL, server.URL+"/presets")
			var stdout, stderr bytes.Buffer
			command.SetOut(&stdout)
			if scenario == "canceled" {
				command.SetOut(&sshCancelWriter{cancel: cancel})
			}
			command.SetIn(strings.NewReader(""))
			command.SetErr(&stderr)
			command.SetArgs([]string{"--config", filepath.Join(workspace, "config.json"), "--token", "test-token", "ssh", "api", "--ttl", "2h"})
			if strings.HasPrefix(scenario, "tunnel") {
				args := []string{"--config", filepath.Join(workspace, "config.json"), "--token", "test-token", "tunnel", "api", "--ttl", "2h"}
				if scenario == "tunnel override" {
					args = append(args, "--remote-port", "8080")
				}
				command.SetArgs(args)
			}
			err := command.ExecuteContext(ctx)
			switch scenario {
			case "success", "already expired", "tunnel", "tunnel override", "tunnel guess":
				if err != nil || stdout.String() != "remote shell output\n" {
					t.Fatalf("session: %v, output %q", err, stdout.String())
				}
			case "canceled":
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation: %v", err)
				}
			case "tunnel guess failure":
				if err == nil || !strings.Contains(err.Error(), "specify --remote-port") || stdout.Len() != 0 {
					t.Fatalf("failed port guess: %v, stdout %q", err, stdout.String())
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
				want := "master\nshell\nclose\n"
				if scenario == "tunnel guess" {
					want = "master\nprobe\nshell\nclose\n"
				} else if scenario == "tunnel guess failure" {
					want = "master\nprobe\nclose\n"
				}
				if err != nil || string(data) != want {
					t.Fatalf("SSH process lifecycle: %q %v", data, err)
				}
			}
		})
	}
}

type sshCancelWriter struct{ cancel context.CancelFunc }

func (w *sshCancelWriter) Write(data []byte) (int, error) {
	w.cancel()
	return len(data), nil
}

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

func TestSSHArgumentsKeepHostVerification(t *testing.T) {
	arguments := sshArguments(api.SSHConnection{Host: "host", Port: 22222, Username: "service_1"}, "/private/key", "/private/control")
	for _, required := range []string{"StrictHostKeyChecking=accept-new", "IdentitiesOnly=yes", "IdentityAgent=none", "BatchMode=yes"} {
		if !strings.Contains(strings.Join(arguments, " "), required) {
			t.Errorf("missing %s", required)
		}
	}
}

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
		{args: []string{"tunnel"}, message: "missing required argument"},
		{args: []string{"tunnel", "api", "extra"}, message: "too many arguments"},
		{args: []string{"tunnel", "api", "--local-port", "-1"}, message: "--local-port must be"},
		{args: []string{"tunnel", "api", "--local-port", "65536"}, message: "--local-port must be"},
		{args: []string{"tunnel", "api", "--remote-port", "0"}, message: "--remote-port must be"},
		{args: []string{"tunnel", "api", "--remote-port", "65536"}, message: "--remote-port must be"},
		{args: []string{"tunnel", "api", "--ttl", "5m"}, message: "use 6m or longer"},
		{args: []string{"tunnel", "api", "-o", "json"}, message: "require --output text"},
	} {
		stdout, _, err := runCommand(t, "http://127.0.0.1:1", test.args...)
		if stdout != "" || err == nil || !strings.Contains(err.Error(), test.message) {
			t.Errorf("%v: stdout %q, error %v", test.args, stdout, err)
		}
	}
}

package cli

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/xo/dburl"
	"sliplane-cli/internal/api"
)

func TestPostgresConnectionURL(t *testing.T) {
	t.Setenv("PGHOSTADDR", "192.0.2.9")
	t.Setenv("PGSSLMODE", "disable")
	t.Setenv("PGPASSWORD", "wrong")
	t.Setenv("PGSERVICE", "wrong")
	connection := api.PostgresConnection{Host: "db.example.test", Port: 5433, User: "owner @'", Database: "app /'", Password: "p:a'\\ss\nword @?"}
	u, err := dburl.Parse(postgresConnectionURL(connection, "203.0.113.5"))
	if err != nil {
		t.Fatal(err)
	}
	dsn, err := pq.ParseURL(u.String())
	if err != nil {
		t.Fatal(err)
	}
	config, err := pq.NewConfig(dsn + " service='' passfile=''")
	if err != nil {
		t.Fatal(err)
	}
	if config.Host != connection.Host || config.Hostaddr.String() != "203.0.113.5" || config.Port != 5433 || config.User != connection.User || config.Database != connection.Database || config.Password != connection.Password || config.SSLMode != "verify-full" {
		t.Fatal("connection settings were changed or credentials were incorrectly escaped")
	}
}

func TestPostgresUSQL(t *testing.T) {
	connection := testPostgresServer(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Error("unexpected API write")
		}
		switch r.URL.Path {
		case "/postgres":
			fmt.Fprint(w, `[{"id":"pg_main","name":"main"}]`)
		case "/postgres/pg_main":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "pg_main", "name": "main", "connection": map[string]any{"host": connection.Host, "port": connection.Port, "database": connection.Database, "user": connection.User, "password": connection.Password}, "ipAllowList": []map[string]string{{"source": "0.0.0.0/0"}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("PATH", t.TempDir())
	t.Setenv("PGPASSWORD", "wrong")
	t.Setenv("PGSSLMODE", "disable")
	t.Setenv("PGSERVICE", "wrong")
	for _, test := range []struct {
		name, sql, want string
		fail            bool
	}{
		{"main", "SELECT 'built-in-query' AS result", "built-in-query", false},
		{"pg_main", "SELECT $$semi;colon$$ AS result; SELECT 42 AS answer;", "42", false},
		{"main", `CREATE TEMP TABLE shell_test(value text); INSERT INTO shell_test VALUES ('session-state'); SELECT * FROM shell_test;`, "session-state", false},
		{"main", `SELECT 1/0; SELECT 'must-not-run';`, "division by zero", true},
		{"main", `\conninfo`, "postgres", false},
		{"main", `\dt`, "", false},
	} {
		stdout, stderr, err := runCommand(t, server.URL, "postgres", "query", test.name, test.sql)
		if (err != nil) != test.fail {
			t.Fatalf("%s: stdout=%s stderr=%s err=%v", test.sql, stdout, stderr, err)
		}
		result := stdout + stderr
		if err != nil {
			result += err.Error()
		}
		if !strings.Contains(result, test.want) || strings.Contains(result, "must-not-run") || strings.Contains(result, connection.Password) {
			t.Fatalf("unexpected output for %s: %q", test.sql, result)
		}
	}
	for _, source := range []string{"file", "stdin", "explicit stdin"} {
		t.Run(source, func(t *testing.T) {
			script := "-- Multiline statements and dollar quotes must use usql's parser.\nCREATE TEMP TABLE automation(value text);\nINSERT INTO automation VALUES ($$multi\nline;value$$);\nSELECT count(*) AS script_rows FROM automation"
			path := filepath.Join(t.TempDir(), "script.sql")
			if err := os.WriteFile(path, []byte(script), 0600); err != nil {
				t.Fatal(err)
			}
			for _, failure := range []bool{false, true} {
				input := script
				if failure {
					input = "SELECT 1/0;\nSELECT 'should-never-execute';"
				}
				if err := os.WriteFile(path, []byte(input), 0600); err != nil {
					t.Fatal(err)
				}
				command := newCommand(server.URL, server.URL+"/presets")
				command.SetIn(strings.NewReader(input))
				var output bytes.Buffer
				command.SetOut(&output)
				command.SetErr(&output)
				args := []string{"--token", "test-token", "--config", filepath.Join(t.TempDir(), "config.json"), "postgres", "query", "main", "--timeout", "5s"}
				if source == "file" {
					args = append(args, "--file", path)
				}
				if source == "explicit stdin" {
					args = append(args, "--file", "-")
				}
				command.SetArgs(args)
				err := command.ExecuteContext(t.Context())
				if failure {
					if err == nil || !strings.Contains(err.Error(), "division by zero") || strings.Contains(output.String(), "should-never-execute") {
						t.Fatalf("script did not stop on error: %s %v", output.String(), err)
					}
				} else if err != nil || !strings.Contains(output.String(), "script_rows") || !strings.Contains(output.String(), "1") {
					t.Fatalf("script output: %s; error: %v", output.String(), err)
				}
			}
		})
	}
	t.Run("query timeout flag", func(t *testing.T) {
		start := time.Now()
		stdout, _, err := runCommand(t, server.URL, "postgres", "query", "main", "SELECT pg_sleep(30); SELECT 'after-timeout';", "--timeout", "500ms")
		if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "query timed out") || time.Since(start) > 3*time.Second || strings.Contains(stdout, "after-timeout") {
			t.Fatalf("timeout failed: %s %v", stdout, err)
		}
	})
	t.Run("piped shell", func(t *testing.T) {
		command := newCommand(server.URL, server.URL+"/presets")
		command.SetIn(strings.NewReader("BEGIN;\nSELECT 'shell-result';\nROLLBACK;\n\\conninfo\n\\q\n"))
		var output bytes.Buffer
		command.SetOut(&output)
		command.SetErr(&output)
		command.SetArgs([]string{"--token", "test-token", "--config", filepath.Join(t.TempDir(), "config.json"), "postgres", "connect", "main"})
		if err := command.ExecuteContext(t.Context()); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(output.String(), "shell-result") || strings.Contains(output.String(), connection.Password) {
			t.Fatalf("shell output: %q", output.String())
		}
	})
	t.Run("cancel running query", func(t *testing.T) {
		command := newCommand(server.URL, server.URL+"/presets")
		command.SetOut(&bytes.Buffer{})
		command.SetErr(&bytes.Buffer{})
		command.SetArgs([]string{"--token", "test-token", "--config", filepath.Join(t.TempDir(), "config.json"), "postgres", "query", "main", "SELECT pg_sleep(30)"})
		ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
		defer cancel()
		start := time.Now()
		err := command.ExecuteContext(ctx)
		if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 3*time.Second {
			t.Fatalf("cancellation failed: %v", err)
		}
	})
	t.Run("reject untrusted certificate", func(t *testing.T) {
		t.Setenv("PGSSLROOTCERT", filepath.Join(t.TempDir(), "missing.crt"))
		_, _, err := runCommand(t, server.URL, "postgres", "query", "main", "SELECT 1")
		if err == nil {
			t.Fatal("accepted an untrusted certificate")
		}
	})
}

// testPostgresServer starts an isolated TLS-enabled database when integration binaries are provided.
func testPostgresServer(t *testing.T) api.PostgresConnection {
	t.Helper()
	binaries := os.Getenv("SLIPLANE_TEST_POSTGRES_BIN")
	if binaries == "" {
		t.Skip("set SLIPLANE_TEST_POSTGRES_BIN to a PostgreSQL bin directory for integration tests")
	}
	workspace := t.TempDir()
	data := filepath.Join(workspace, "data")
	password := "test-secret'\\:@"
	passwordFile := filepath.Join(workspace, "password")
	if err := os.WriteFile(passwordFile, []byte(password), 0600); err != nil {
		t.Fatal(err)
	}
	initialize := exec.Command(filepath.Join(binaries, "initdb"), "-D", data, "-U", "owner", "-A", "scram-sha-256", "--pwfile", passwordFile, "--no-locale")
	if output, err := initialize.CombinedOutput(); err != nil {
		t.Fatalf("initdb: %s %v", output, err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	certificate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "localhost"}, DNSNames: []string{"localhost"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true, IsCA: true}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certFile := filepath.Join(data, "server.crt")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "server.key"), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	settings := fmt.Sprintf("\nlisten_addresses='127.0.0.1'\nport=%d\nunix_socket_directories=''\nssl=on\n", port)
	if err := os.WriteFile(filepath.Join(data, "postgresql.auto.conf"), []byte(settings), 0600); err != nil {
		t.Fatal(err)
	}
	control := filepath.Join(binaries, "pg_ctl")
	start := exec.Command(control, "-D", data, "-l", filepath.Join(workspace, "postgres.log"), "-w", "start")
	if output, err := start.CombinedOutput(); err != nil {
		t.Fatalf("start postgres: %s %v", output, err)
	}
	t.Cleanup(func() {
		if output, err := exec.Command(control, "-D", data, "-m", "immediate", "-w", "stop").CombinedOutput(); err != nil {
			t.Errorf("stop postgres: %s %v", output, err)
		}
	})
	t.Setenv("PGSSLROOTCERT", certFile)
	return api.PostgresConnection{Host: "localhost", Port: port, Database: "postgres", User: "owner", Password: password}
}

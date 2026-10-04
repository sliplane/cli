package cli

import (
	"encoding/json"
	"net/netip"
	"strings"
	"testing"

	"sliplane-cli/internal/api"
)

func TestPostgresAllowsIP(t *testing.T) {
	for _, test := range []struct {
		source, address string
		allowed         bool
	}{
		{"", "203.0.113.7", false},
		{"203.0.113.7", "203.0.113.7", true},
		{"203.0.113.7/32", "203.0.113.7", true},
		{"203.0.113.0/24", "203.0.113.7", true},
		{"203.0.113.0/24", "198.51.100.7", false},
		{"0.0.0.0/0", "203.0.113.7", true},
		{"0.0.0.0/0", "2001:db8::7", false},
		{"2001:db8::7", "2001:db8::7", true},
		{"2001:db8::/32", "2001:db8::7", true},
		{"2001:db8::7/128", "2001:db8::8", false},
		{"::/0", "2001:db8::7", true},
		{"203.0.113.0/24", "::ffff:203.0.113.7", true},
		{"invalid", "203.0.113.7", false},
	} {
		t.Run(test.source+"/"+test.address, func(t *testing.T) {
			entries := []api.PostgresIPRange{{Source: test.source}}
			if allowed := postgresAllowsIP(entries, netip.MustParseAddr(test.address)); allowed != test.allowed {
				t.Fatalf("allowed = %v, want %v", allowed, test.allowed)
			}
		})
	}
	if postgresAllowsIP(nil, netip.MustParseAddr("203.0.113.7")) {
		t.Fatal("empty allowlist permitted access")
	}
}

func TestPostgresCredentials(t *testing.T) {
	var connection api.PostgresConnection
	if err := json.Unmarshal([]byte(`{"host":"db.example.test","port":5432,"database":"app","user":"owner","password":"private-secret","uri":"postgres://owner:private-secret@db.example.test/app"}`), &connection); err != nil {
		t.Fatal(err)
	}
	if connection.Password != "private-secret" || connection.Host != "db.example.test" {
		t.Fatal("API credentials were not decoded")
	}
	encoded, err := json.Marshal(connection)
	if err != nil || strings.Contains(string(encoded), "secret") || strings.Contains(string(encoded), "password") || strings.Contains(string(encoded), "uri") {
		t.Fatal("credentials were included in metadata output")
	}
	if err := validatePostgresConnection(&connection); err != nil {
		t.Fatal(err)
	}
	for _, test := range []string{"missing password", "invalid port", "null byte", "missing host"} {
		invalid := connection
		switch test {
		case "missing password":
			invalid.Password = ""
		case "invalid port":
			invalid.Port = 65536
		case "null byte":
			invalid.Database = "app\x00"
		case "missing host":
			invalid.Host = ""
		}
		if err := validatePostgresConnection(&invalid); err == nil {
			t.Errorf("accepted %s", test)
		}
	}
	if err := validatePostgresConnection(nil); err == nil {
		t.Fatal("accepted unavailable database")
	}
}

func TestPostgresCommandValidation(t *testing.T) {
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"connect"}, "missing required argument"},
		{[]string{"query"}, "missing required argument"},
		{[]string{"query", "main", "SELECT 1", "--file", "script.sql"}, "either a SQL argument or --file"},
		{[]string{"query", "main", "--file="}, "--file requires a path"},
		{[]string{"query", "main", "SELECT 1", "--timeout=-1s"}, "--timeout must not be negative"},
		{[]string{"query", "main", "select", "1"}, "too many arguments"},
		{[]string{"query", "main", " "}, "SQL must not be empty"},
		{[]string{"query", "main", "select 1", "-o", "json"}, "require --output text"},
		{[]string{"connect", "main", "-o", "json"}, "require --output text"},
	} {
		stdout, stderr, err := runCommand(t, "http://127.0.0.1:1", append([]string{"postgres"}, test.args...)...)
		if err == nil || !strings.Contains(err.Error(), test.want) || stdout != "" || stderr != "" {
			t.Fatalf("%v: %q %q %v", test.args, stdout, stderr, err)
		}
	}
}

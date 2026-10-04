package cli

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"sliplane-cli/internal/api"
)

func (o *options) postgresConnectCommand() *cobra.Command {
	return &cobra.Command{
		Use: "connect <name-or-id>", Short: "Open the built-in PostgreSQL shell", Args: validateArguments,
		Long:    "Open an interactive SQL shell without installing psql.\nIf your IP is blocked, ask before saving a single-IP allowlist rule.\nThe rule remains after the session. Use \\? for help and \\q to exit.",
		Example: "  sliplane postgres connect main\n  sliplane postgres connect main --profile work",
		RunE: func(command *cobra.Command, args []string) error {
			dsn, err := o.postgresDSN(command, args[0])
			if err != nil {
				return err
			}
			return o.runPostgres(command, dsn, "")
		},
	}
}

func (o *options) postgresDSN(command *cobra.Command, name string) (string, error) {
	if o.output != "text" {
		return "", errors.New("PostgreSQL commands require --output text")
	}
	client, _, err := o.client(command)
	if err != nil {
		return "", err
	}
	database, err := resolvePostgres(command.Context(), client, name)
	if err != nil {
		return "", err
	}
	if err := validatePostgresConnection(database.Connection); err != nil {
		return "", fmt.Errorf("cannot connect to database %q: %w", database.Name, err)
	}
	addresses, err := net.DefaultResolver.LookupIP(command.Context(), "ip", database.Connection.Host)
	if err != nil {
		return "", fmt.Errorf("resolve PostgreSQL host %q: %w", database.Connection.Host, err)
	}
	if len(addresses) == 0 {
		return "", fmt.Errorf("PostgreSQL host %q has no IP addresses", database.Connection.Host)
	}
	address := addresses[0]
	// Keep the database connection in the same address family used for public-IP detection.
	for _, candidate := range addresses {
		if candidate.To4() != nil {
			address = candidate
			break
		}
	}
	if err := o.checkPostgresAccess(command, client, database, address); err != nil {
		return "", err
	}
	return postgresConnectionURL(*database.Connection, address.String()), nil
}

// validatePostgresConnection rejects unavailable credentials and invalid protocol values.
func validatePostgresConnection(connection *api.PostgresConnection) error {
	if connection == nil {
		return errors.New("connection details are unavailable; the database must be running")
	}
	if connection.Port < 1 || connection.Port > 65535 {
		return errors.New("API returned an invalid PostgreSQL port")
	}
	for _, value := range []string{connection.Host, connection.Database, connection.User, connection.Password} {
		if value == "" || strings.ContainsRune(value, '\x00') {
			return errors.New("API returned missing or invalid PostgreSQL connection details")
		}
	}
	return nil
}

// postgresConnectionURL escapes credentials and pins the checked address while verifying the hostname.
func postgresConnectionURL(connection api.PostgresConnection, address string) string {
	parameters := url.Values{
		"hostaddr": {address}, "sslmode": {"verify-full"}, "ssl_min_protocol_version": {"TLSv1.2"},
		"connect_timeout": {"10"}, "application_name": {"sliplane-cli"},
		"target_session_attrs": {"any"},
	}
	dsn := url.URL{
		Scheme: "postgres", Host: net.JoinHostPort(connection.Host, strconv.Itoa(connection.Port)),
		Path: "/" + connection.Database, User: url.UserPassword(connection.User, connection.Password),
		RawQuery: parameters.Encode(),
	}
	return dsn.String()
}

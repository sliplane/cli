// PostgreSQL sessions connect directly with API credentials and verified TLS.
package cli

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgconn/ctxwatch"
	"github.com/spf13/cobra"
	"sliplane-cli/internal/api"
)

// postgresConnectCommand opens the built-in SQL shell.
func (o *options) postgresConnectCommand() *cobra.Command {
	return &cobra.Command{
		Use: "connect <name-or-id>", Short: "Open the built-in PostgreSQL shell", Args: validateArguments,
		Long:    "Open an interactive SQL shell without installing psql.\nIf your IP is blocked, ask before saving a single-IP allowlist rule.\nThe rule remains after the session. Use \\? for help and \\q to exit.",
		Example: "  sliplane postgres connect main\n  sliplane postgres connect main --profile work",
		RunE: func(command *cobra.Command, args []string) error {
			connection, err := o.openPostgres(command, args[0])
			if err != nil {
				return err
			}
			defer closePostgres(connection)
			return o.postgresShell(command, connection)
		},
	}
}

// postgresQueryCommand executes SQL once and returns SQL errors to the caller.
func (o *options) postgresQueryCommand() *cobra.Command {
	return &cobra.Command{
		Use: "query <name-or-id> <sql>", Short: "Execute SQL once with the built-in PostgreSQL client", Args: validateArguments,
		Long:    "Execute quoted SQL without installing psql.\nIf your IP is blocked, ask before saving a single-IP allowlist rule.\nRedirected commands never prompt; allow your IP in the dashboard first.",
		Example: "  sliplane postgres query main 'SELECT now();'\n  sliplane postgres query main 'SELECT count(*) FROM users;' --profile work",
		RunE: func(command *cobra.Command, args []string) error {
			if strings.TrimSpace(args[1]) == "" {
				return errors.New("SQL must not be empty")
			}
			connection, err := o.openPostgres(command, args[0])
			if err != nil {
				return err
			}
			defer closePostgres(connection)
			return executePostgresSQL(command.Context(), connection, command.OutOrStdout(), args[1])
		},
	}
}

// openPostgres resolves the database and checks access before opening a verified TLS connection.
func (o *options) openPostgres(command *cobra.Command, name string) (*pgconn.PgConn, error) {
	if o.output != "text" {
		return nil, errors.New("PostgreSQL commands require --output text")
	}
	client, _, err := o.client(command)
	if err != nil {
		return nil, err
	}
	database, err := resolvePostgres(command.Context(), client, name)
	if err != nil {
		return nil, err
	}
	if err := validatePostgresConnection(database.Connection); err != nil {
		return nil, fmt.Errorf("cannot connect to database %q: %w", database.Name, err)
	}
	addresses, err := net.DefaultResolver.LookupIP(command.Context(), "ip", database.Connection.Host)
	if err != nil {
		return nil, fmt.Errorf("resolve PostgreSQL host %q: %w", database.Connection.Host, err)
	}
	if len(addresses) == 0 {
		return nil, fmt.Errorf("PostgreSQL host %q has no IP addresses", database.Connection.Host)
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
		return nil, err
	}
	config, err := postgresClientConfig(*database.Connection, address.String())
	if err != nil {
		return nil, err
	}
	config.OnNotice = func(_ *pgconn.PgConn, notice *pgconn.Notice) {
		fmt.Fprintln(command.ErrOrStderr(), terminalText(notice.Severity+": "+notice.Message))
	}
	return pgconn.ConnectConfig(command.Context(), config)
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

// postgresClientConfig keeps credentials in memory and pins TLS verification to the API hostname.
func postgresClientConfig(connection api.PostgresConnection, address string) (*pgconn.Config, error) {
	// ParseConfig initializes driver internals; real connection values are assigned below, never interpolated.
	config, err := pgconn.ParseConfig("host=localhost port=5432 user=sliplane password=unused dbname=postgres sslmode=disable connect_timeout=10 target_session_attrs=any min_protocol_version=3.0 max_protocol_version=3.0 channel_binding=prefer require_auth='' sslnegotiation=postgres")
	if err != nil {
		return nil, fmt.Errorf("initialize PostgreSQL client: %w", err)
	}
	config.Host = address
	config.Port = uint16(connection.Port)
	config.Database = connection.Database
	config.User = connection.User
	config.Password = connection.Password
	config.TLSConfig = &tls.Config{ServerName: connection.Host, MinVersion: tls.VersionTLS12}
	config.Fallbacks = nil
	config.RuntimeParams = map[string]string{"application_name": "sliplane-cli", "standard_conforming_strings": "on"}
	config.BuildContextWatcherHandler = func(connection *pgconn.PgConn) ctxwatch.Handler {
		return &pgconn.CancelRequestContextWatcherHandler{Conn: connection, DeadlineDelay: 5 * time.Second}
	}
	return config, nil
}

// closePostgres closes even when the command's context has already been canceled.
func closePostgres(connection *pgconn.PgConn) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = connection.Close(ctx)
}

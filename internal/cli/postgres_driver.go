// The usql PostgreSQL driver keeps its metadata support while honoring command cancellation.
package cli

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"time"

	"github.com/lib/pq"
	"github.com/xo/dburl"
	"github.com/xo/usql/drivers"
	_ "github.com/xo/usql/drivers/postgres"
)

// installPostgresDriver scopes connections to the CLI context; usql handles query-level Ctrl-C itself.
func installPostgresDriver(ctx context.Context) drivers.Driver {
	previous := drivers.Available()["postgres"]
	driver := previous
	driver.Open = func(_ context.Context, connectionURL *dburl.URL, stdout, stderr func() io.Writer) (func(string, string) (*sql.DB, error), error) {
		return func(_, dsn string) (*sql.DB, error) {
			if connectionURL.Scheme != "" {
				// dburl's DSN conversion loses escaping for some database names and passwords.
				var err error
				dsn, err = pq.ParseURL(connectionURL.String())
				if err != nil {
					return nil, err
				}
			}
			// Explicit API credentials must not be redirected by an inherited service or passfile.
			dsn += " service='' passfile=''"
			config, err := pq.NewConfig(dsn)
			if err != nil {
				return nil, err
			}
			connector, err := pq.NewConnectorConfig(config)
			if err != nil {
				return nil, err
			}
			connector.Dialer(postgresDialer{ctx: ctx})
			// usql's \conninfo prints DSN verbatim. Retain secrets only in the driver configuration.
			display := url.URL{Scheme: "postgres", Host: net.JoinHostPort(config.Host, strconv.Itoa(int(config.Port))), Path: "/" + config.Database, User: url.User(config.User)}
			connectionURL.DSN = display.String()
			connectionURL.User = display.User
			notices := pq.ConnectorWithNoticeHandler(connector, func(notice *pq.Error) {
				fmt.Fprintln(stderr(), terminalText(notice.Severity+": "+notice.Message))
				if notice.Hint != "" {
					fmt.Fprintln(stderr(), terminalText("HINT: "+notice.Hint))
				}
			})
			notifications := pq.ConnectorWithNotificationHandler(notices, func(notification *pq.Notification) {
				fmt.Fprintf(stdout(), "Notification on %q from PID %d: %s\n", notification.Channel, notification.BePid, terminalText(notification.Extra))
			})
			database := sql.OpenDB(notifications)
			// A shell's transactions, temporary tables, and SET commands belong to one connection.
			database.SetMaxOpenConns(1)
			return database, nil
		}, nil
	}
	drivers.Available()["postgres"] = driver
	return previous
}

// postgresDialer closes sockets when the command ends, including queries started by usql's own contexts.
type postgresDialer struct {
	ctx context.Context
}

// Dial opens a connection tied to the session lifetime.
func (d postgresDialer) Dial(network, address string) (net.Conn, error) {
	return d.DialContext(d.ctx, network, address)
}

// DialTimeout retains the driver's connection timeout.
func (d postgresDialer) DialTimeout(network, address string, timeout time.Duration) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(d.ctx, timeout)
	defer cancel()
	return d.DialContext(ctx, network, address)
}

// DialContext combines the driver's connection deadline with the command's lifetime.
func (d postgresDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(d.ctx, cancel)
	defer stop()
	connection, err := (&net.Dialer{}).DialContext(ctx, network, address)
	if err != nil {
		return nil, err
	}
	closeOnCancel := context.AfterFunc(d.ctx, func() { _ = connection.Close() })
	return &postgresSocket{Conn: connection, stop: closeOnCancel}, nil
}

// postgresSocket releases cancellation callbacks when usql closes or reconnects a connection.
type postgresSocket struct {
	net.Conn
	stop func() bool
}

// Close releases both the socket and its session cancellation callback.
func (c *postgresSocket) Close() error {
	c.stop()
	return c.Conn.Close()
}

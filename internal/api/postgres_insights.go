// PostgreSQL backup and diagnostic endpoints return snapshots from the managed instance.
package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type PostgresRestoreWindow struct {
	Min time.Time `json:"min"`
	Max time.Time `json:"max"`
}

type PostgresQueryStats struct {
	Rank         int       `json:"rank"`
	ReportedAt   time.Time `json:"reportedAt"`
	DatabaseID   int64     `json:"databaseId"`
	UserID       int64     `json:"userId"`
	UserName     string    `json:"userName,omitempty"`
	QueryID      *int64    `json:"queryId,omitempty"`
	Calls        int64     `json:"calls"`
	TotalExecMS  float64   `json:"totalExecMs"`
	MeanExecMS   float64   `json:"meanExecMs"`
	MaxExecMS    float64   `json:"maxExecMs"`
	StddevExecMS float64   `json:"stddevExecMs"`
	RowsTouched  int64     `json:"rowsTouched"`
	TopLevel     bool      `json:"topLevel"`
	Query        string    `json:"query"`
}

type PostgresActiveConnection struct {
	PID              int        `json:"pid"`
	ReportedAt       time.Time  `json:"reportedAt"`
	BackendType      string     `json:"backendType"`
	ApplicationName  string     `json:"applicationName,omitempty"`
	BackendStart     *time.Time `json:"backendStart,omitempty"`
	ClientAddr       string     `json:"clientAddr,omitempty"`
	ClientHostname   string     `json:"clientHostname,omitempty"`
	ClientPort       int        `json:"clientPort,omitempty"`
	Database         string     `json:"dbname,omitempty"`
	Query            string     `json:"query,omitempty"`
	QueryStart       *time.Time `json:"queryStart,omitempty"`
	State            string     `json:"state,omitempty"`
	StateChange      *time.Time `json:"stateChange,omitempty"`
	User             string     `json:"user,omitempty"`
	WaitEvent        string     `json:"waitEvent,omitempty"`
	WaitEventType    string     `json:"waitEventType,omitempty"`
	TransactionStart *time.Time `json:"xactStart,omitempty"`
}

type PostgresRelationSize struct {
	ReportedAt time.Time `json:"reportedAt"`
	SchemaName string    `json:"schemaName"`
	TableName  string    `json:"tableName"`
	TableBytes int64     `json:"tableBytes"`
	IndexBytes int64     `json:"indexBytes"`
	TotalBytes int64     `json:"totalBytes"`
}

type PostgresLog struct {
	ID          string    `json:"id"`
	CreatedAt   time.Time `json:"createdAt"`
	Severity    string    `json:"severity"`
	Message     string    `json:"message"`
	BackendType string    `json:"backendType,omitempty"`
	Database    string    `json:"dbname,omitempty"`
	Detail      string    `json:"detail,omitempty"`
	Hint        string    `json:"hint,omitempty"`
	PID         int       `json:"pid,omitempty"`
	Query       string    `json:"query,omitempty"`
	Statement   string    `json:"statement,omitempty"`
	User        string    `json:"user,omitempty"`
}

func (c *Client) PostgresRestoreWindow(ctx context.Context, id string) (PostgresRestoreWindow, error) {
	var window PostgresRestoreWindow
	err := c.request(ctx, http.MethodGet, "/postgres/"+url.PathEscape(id)+"/restore", nil, &window)
	return window, err
}

func (c *Client) RestorePostgres(ctx context.Context, id string, at time.Time) (PostgresDatabase, error) {
	var database PostgresDatabase
	err := c.request(ctx, http.MethodPost, "/postgres/"+url.PathEscape(id)+"/restore", map[string]time.Time{"timestamp": at}, &database)
	return database, err
}

func (c *Client) PostgresQueries(ctx context.Context, id, kind string) ([]PostgresQueryStats, error) {
	return list[PostgresQueryStats](ctx, c, "/postgres/"+url.PathEscape(id)+"/"+kind)
}

func (c *Client) PostgresActiveConnections(ctx context.Context, id string) ([]PostgresActiveConnection, error) {
	return list[PostgresActiveConnection](ctx, c, "/postgres/"+url.PathEscape(id)+"/active-connections")
}

func (c *Client) PostgresRelationSizes(ctx context.Context, id string) ([]PostgresRelationSize, error) {
	return list[PostgresRelationSize](ctx, c, "/postgres/"+url.PathEscape(id)+"/relation-sizes")
}

func (c *Client) PostgresLogs(ctx context.Context, id string, since, until *time.Time, limit int) ([]PostgresLog, error) {
	query := url.Values{"limit": {strconv.Itoa(limit)}}
	if since != nil {
		query.Set("since", since.Format(time.RFC3339Nano))
	}
	if until != nil {
		query.Set("until", until.Format(time.RFC3339Nano))
	}
	return list[PostgresLog](ctx, c, "/postgres/"+url.PathEscape(id)+"/logs?"+query.Encode())
}

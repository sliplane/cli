// These handwritten types cover the public API surface used by the CLI.
package api

import (
	"encoding/json"
	"time"
)

type Identity struct {
	AuthType       string          `json:"authType"`
	OrganizationID string          `json:"organizationId"`
	TokenType      string          `json:"tokenType"`
	User           json.RawMessage `json:"user"`
}

type Project struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Server struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Status       string `json:"status"`
	InstanceType string `json:"instanceType"`
	Location     string `json:"location"`
	VCPU         int    `json:"vcpu"`
	MemoryMB     int    `json:"memoryMb"`
	DiskSizeGB   int    `json:"diskSizeGb"`
	IPv4         string `json:"ipv4,omitempty"`
	IPv6         string `json:"ipv6,omitempty"`
	CreatedAt    string `json:"createdAt"`
}

type Service struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	ProjectID   string          `json:"projectId"`
	ServerID    string          `json:"serverId"`
	Status      string          `json:"status"`
	CreatedAt   string          `json:"createdAt"`
	Deployment  json.RawMessage `json:"deployment"`
	Network     json.RawMessage `json:"network"`
	Env         json.RawMessage `json:"env,omitempty"`
	Volumes     json.RawMessage `json:"volumes,omitempty"`
	Healthcheck string          `json:"healthcheck,omitempty"`
	Cmd         string          `json:"cmd,omitempty"`
}

type Log struct {
	CreatedAt time.Time `json:"createdAt"`
	Message   string    `json:"message"`
}

type Bucket struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Status          string `json:"status"`
	Region          string `json:"region"`
	Endpoint        string `json:"endpoint"`
	Versioning      bool   `json:"versioning"`
	ObjectLocking   bool   `json:"objectLocking"`
	CreatedByUserID string `json:"createdByUserId"`
	CreatedAt       string `json:"createdAt"`
}

type PostgresDatabase struct {
	ID              string              `json:"id"`
	Name            string              `json:"name"`
	DatabaseName    string              `json:"databaseName"`
	DatabaseUser    string              `json:"databaseUser"`
	Region          string              `json:"region"`
	Version         int                 `json:"version"`
	DiskSizeGB      int                 `json:"diskSizeGb"`
	Status          string              `json:"status"`
	Connection      *PostgresConnection `json:"connection,omitempty"`
	IPAllowList     []PostgresIPRange   `json:"ipAllowList"`
	CreatedByUserID string              `json:"createdByUserId"`
	CreatedAt       string              `json:"createdAt"`
}

// PostgresConnection keeps credentials available for psql but excludes them from output.
type PostgresConnection struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Database string `json:"database"`
	User     string `json:"user"`
	Password string `json:"-"`
}

// UnmarshalJSON accepts the API password without making it serializable or retaining the URI.
func (connection *PostgresConnection) UnmarshalJSON(data []byte) error {
	type metadata PostgresConnection
	var response struct {
		metadata
		Password string `json:"password"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return err
	}
	*connection = PostgresConnection(response.metadata)
	connection.Password = response.Password
	return nil
}

type PostgresIPRange struct {
	ID          string `json:"id"`
	Source      string `json:"source"`
	Description string `json:"description,omitempty"`
	CreatedAt   string `json:"createdAt"`
}

// BucketKey contains listable metadata, never the secret returned during creation.
type BucketKey struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	AccessKeyID string `json:"accessKeyId"`
	CreatedAt   string `json:"createdAt"`
}

// CreatedBucketKey includes the one-time secret for explicit key creation or private storage.
type CreatedBucketKey struct {
	BucketKey
	SecretAccessKey string `json:"secretAccessKey"`
}

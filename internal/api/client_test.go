package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"sliplane-cli/internal/buildinfo"
)

func TestClientContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("missing bearer token")
		}
		if r.Header.Get("Accept") != "application/json" {
			t.Error("missing accept header")
		}
		if want := "sliplane-cli/" + buildinfo.Version; r.UserAgent() != want {
			t.Errorf("user agent %q; want %q", r.UserAgent(), want)
		}
		switch r.URL.Path {
		case "/projects":
			if r.Method == http.MethodPost {
				var body map[string]string
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["name"] != "demo" {
					t.Errorf("wrong project body: %v %v", body, err)
				}
				if r.Header.Get("Content-Type") != "application/json" {
					t.Error("missing content type")
				}
				w.WriteHeader(http.StatusCreated)
				fmt.Fprint(w, `{"id":"p1","name":"demo"}`)
				return
			}
			fmt.Fprint(w, `[]`)
		case "/projects/p1/services":
			var body CreateServiceInput
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name != "n8n" || body.Deployment.URL != "docker.io/n8nio/n8n:test" {
				t.Errorf("wrong deployment body: %v %v", body, err)
			}
			if r.Method != http.MethodPost {
				t.Error("deployment must POST")
			}
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"id":"s1","name":"n8n","status":"pending"}`)
		case "/projects/p1/services/s1/logs":
			if r.URL.Query().Get("from") != "1700000000" {
				t.Errorf("wrong timestamp: %s", r.URL.RawQuery)
			}
			fmt.Fprint(w, `[{"createdAt":"2024-01-01T00:00:00Z","message":"hello"}]`)
		default:
			t.Errorf("unexpected request %s", r.URL)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	client := New(server.URL, "test-token")
	if projects, err := client.Projects(context.Background()); err != nil || len(projects) != 0 {
		t.Fatalf("projects: %v %v", projects, err)
	}
	if project, err := client.CreateProject(context.Background(), "demo"); err != nil || project.ID != "p1" {
		t.Fatalf("create: %v %v", project, err)
	}
	if service, err := client.CreateService(context.Background(), "p1", CreateServiceInput{Name: "n8n", ServerID: "server1", Deployment: ServiceDeployment{URL: "docker.io/n8nio/n8n:test"}}); err != nil || service.ID != "s1" {
		t.Fatal(err)
	}
	from := time.Unix(1700000000, 0)
	if logs, err := client.Logs(context.Background(), "p1", "s1", &from); err != nil || len(logs) != 1 {
		t.Fatalf("logs: %v %v", logs, err)
	}
}

func TestAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"Status":200,"code":"read_only_token","message":"denied test-secret"}`)
	}))
	defer server.Close()
	_, err := New(server.URL, "test-secret").Projects(context.Background())
	var apiError *Error
	if !errors.As(err, &apiError) || apiError.Status != 403 || apiError.Code != "read_only_token" {
		t.Fatalf("wrong error: %v", err)
	}
	if strings.Contains(err.Error(), "test-secret") || !strings.Contains(err.Error(), "read-write") {
		t.Fatalf("unsafe error: %v", err)
	}
}

func TestClientRejectsRedirects(t *testing.T) {
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("redirect followed") }))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, http.StatusFound) }))
	defer source.Close()
	if _, err := New(source.URL, "test-token").Projects(context.Background()); err == nil {
		t.Fatal("redirect accepted")
	}
}

func TestClientCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { cancel(); <-r.Context().Done() }))
	defer server.Close()
	defer cancel()
	_, err := New(server.URL, "test-token").Projects(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation: %v", err)
	}
}

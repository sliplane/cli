// Local S3 protocol tests cover signing, pagination, and streaming without real credentials.
package objectstore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"sliplane-cli/internal/buildinfo"
)

// TestWaitReadyStopsAtDeadline bounds activation polling and preserves cancellation.
func TestWaitReadyStopsAtDeadline(t *testing.T) {
	for _, test := range []struct {
		code          string
		cancelRequest bool
	}{
		{"InvalidAccessKeyId", false}, {"InvalidAccessKeyId", true},
		{"AccessDenied", false}, {"AccessDenied", true},
	} {
		t.Run(fmt.Sprintf("%s/cancel=%t", test.code, test.cancelRequest), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()
			var attempts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempts.Add(1)
				if r.Method != http.MethodGet || r.URL.Query().Get("max-keys") != "1" {
					t.Error("activation probe must be a small read")
				}
				w.WriteHeader(http.StatusForbidden)
				fmt.Fprintf(w, `<Error><Code>%s</Code><Message>not active yet</Message></Error>`, test.code)
				if test.cancelRequest {
					cancel()
				}
			}))
			defer server.Close()
			client, err := New(server.URL, "us-east-1", Credentials{AccessKeyID: "access", SecretAccessKey: "secret"})
			if err != nil {
				t.Fatal(err)
			}
			err = client.WaitReady(ctx, "bucket")
			want := context.DeadlineExceeded
			if test.cancelRequest {
				want = context.Canceled
			}
			if !errors.Is(err, want) || attempts.Load() != 1 {
				t.Fatalf("activation wait: %v, %d requests", err, attempts.Load())
			}
			if !test.cancelRequest && !strings.Contains(err.Error(), test.code) {
				t.Fatalf("deadline lost the provider's error: %v", err)
			}
		})
	}
}

// TestListPagination preserves prefixes and keys while following continuation tokens.
func TestListPagination(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=access/") || strings.Contains(r.Header.Get("Authorization"), "secret") {
			t.Error("missing or unsafe S3 signature")
		}
		if r.URL.Path != "/bucket" || r.URL.Query().Get("prefix") != "photos/" || r.URL.Query().Get("delimiter") != "/" {
			t.Errorf("wrong list request: %s", r.URL)
		}
		w.Header().Set("Content-Type", "application/xml")
		if r.URL.Query().Get("continuation-token") == "" {
			fmt.Fprint(w, `<ListBucketResult><IsTruncated>true</IsTruncated><NextContinuationToken>next+token</NextContinuationToken><CommonPrefixes><Prefix>photos/2026/</Prefix></CommonPrefixes><Contents><Key>photos/a + b.txt</Key><Size>3</Size><LastModified>2026-09-22T00:00:00Z</LastModified></Contents></ListBucketResult>`)
			return
		}
		if r.URL.Query().Get("continuation-token") != "next+token" {
			t.Error("continuation token corrupted")
		}
		fmt.Fprint(w, `<ListBucketResult><IsTruncated>false</IsTruncated><Contents><Key>photos/z.txt</Key><Size>5</Size></Contents></ListBucketResult>`)
	}))
	defer server.Close()
	client, err := New(server.URL, "us-east-1", Credentials{AccessKeyID: "access", SecretAccessKey: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	entries, err := client.List(context.Background(), "bucket", "photos/", "/")
	if err != nil || len(entries) != 3 || entries[0].Type != "directory" || entries[1].Key != "photos/a + b.txt" || entries[2].Size != 5 {
		t.Fatalf("entries: %+v %v", entries, err)
	}
}

// TestBinaryTransfer checks object-key encoding and streamed binary bodies.
func TestBinaryTransfer(t *testing.T) {
	payload := []byte{0, 1, 2, 255, 10, 0}
	key := "folder/a + ü.bin"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if want := "sliplane-cli/" + buildinfo.Version; !slices.Contains(strings.Fields(r.UserAgent()), want) {
			t.Errorf("user agent %q; want token %q", r.UserAgent(), want)
		}
		if r.URL.Path != "/bucket/"+key {
			t.Errorf("key changed: %s", r.URL.Path)
		}
		switch r.Method {
		case http.MethodPut:
			data, err := io.ReadAll(r.Body)
			if err != nil || !bytes.Equal(data, payload) {
				t.Errorf("upload corrupted: %v %v", data, err)
			}
			w.Header().Set("ETag", `"etag"`)
		case http.MethodGet:
			w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
			w.Write(payload)
		default:
			t.Errorf("unexpected method %s", r.Method)
		}
	}))
	defer server.Close()
	client, err := New(server.URL, "us-east-1", Credentials{AccessKeyID: "access", SecretAccessKey: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(t.TempDir(), "source.bin")
	if err := os.WriteFile(filename, payload, 0600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	result, err := client.Upload(context.Background(), "bucket", key, file)
	if err != nil || result.Size != int64(len(payload)) {
		t.Fatalf("upload: %+v %v", result, err)
	}
	var downloaded bytes.Buffer
	if size, err := client.Download(context.Background(), "bucket", key, &downloaded, nil); err != nil || size != int64(len(payload)) || !bytes.Equal(downloaded.Bytes(), payload) {
		t.Fatalf("download: %d %v", size, err)
	}
}

// TestDownloadProgress reports streamed bytes and handles responses without a known length.
func TestDownloadProgress(t *testing.T) {
	for _, scenario := range []string{"known", "unknown", "empty", "truncated", "progress failure"} {
		t.Run(scenario, func(t *testing.T) {
			payload := "download body"
			total := int64(len(payload))
			switch scenario {
			case "unknown":
				total = -1
			case "empty":
				payload, total = "", 0
			case "truncated":
				total = 100
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if total >= 0 {
					w.Header().Set("Content-Length", fmt.Sprint(total))
				}
				w.(http.Flusher).Flush()
				fmt.Fprint(w, payload)
			}))
			defer server.Close()
			client, err := New(server.URL, "us-east-1", Credentials{AccessKeyID: "access", SecretAccessKey: "secret"})
			if err != nil {
				t.Fatal(err)
			}
			var destination bytes.Buffer
			var updates []int64
			size, err := client.Download(context.Background(), "bucket", "key", &destination, func(written, expected int64) error {
				if expected != total || written != int64(destination.Len()) {
					t.Errorf("progress %d/%d does not match destination length %d or total %d", written, expected, destination.Len(), total)
				}
				updates = append(updates, written)
				if scenario == "progress failure" {
					return errors.New("display unavailable")
				}
				return nil
			})
			if len(updates) == 0 || updates[0] != 0 {
				t.Fatalf("missing initial progress: %v", updates)
			}
			if scenario == "truncated" || scenario == "progress failure" {
				if err == nil {
					t.Fatal("failed download reported success")
				}
				return
			}
			if err != nil || size != int64(len(payload)) || destination.String() != payload || updates[len(updates)-1] != size {
				t.Fatalf("download: size=%d, progress=%v, err=%v", size, updates, err)
			}
		})
	}
}

// TestS3Errors protects secret values and rejects incomplete pagination.
func TestS3Errors(t *testing.T) {
	for _, test := range []struct {
		name, body string
		status     int
		want       string
	}{
		{"denied", `<Error><Code>AccessDenied</Code><Message>access secret denied</Message></Error>`, 403, "AccessDenied"},
		{"invalid pagination", `<ListBucketResult><IsTruncated>true</IsTruncated></ListBucketResult>`, 200, "pagination"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/xml")
				w.WriteHeader(test.status)
				fmt.Fprint(w, test.body)
			}))
			defer server.Close()
			client, err := New(server.URL, "us-east-1", Credentials{AccessKeyID: "access", SecretAccessKey: "secret"})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.List(context.Background(), "bucket", "", "")
			if err == nil || !strings.Contains(err.Error(), test.want) || strings.Contains(err.Error(), "secret") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// TestExistsDoesNotConfuseForbiddenWithMissing prevents failed permission checks from allowing uploads.
func TestExistsDoesNotConfuseForbiddenWithMissing(t *testing.T) {
	for _, status := range []int{200, 404, 403} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodHead {
					t.Error("existence check must use HEAD")
				}
				w.WriteHeader(status)
			}))
			defer server.Close()
			client, err := New(server.URL, "us-east-1", Credentials{AccessKeyID: "access", SecretAccessKey: "secret"})
			if err != nil {
				t.Fatal(err)
			}
			exists, err := client.Exists(context.Background(), "bucket", "key")
			if status == 403 {
				if err == nil {
					t.Fatal("permission failure treated as absence")
				}
				return
			}
			if err != nil || exists != (status == 200) {
				t.Fatalf("exists: %v %v", exists, err)
			}
		})
	}
}

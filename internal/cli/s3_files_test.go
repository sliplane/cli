// Transfer commands are tested against separate control-plane and S3 HTTP servers.
package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// TestFileCommands preserves output contracts and never replaces a destination after a failed download.
func TestFileCommands(t *testing.T) {
	t.Setenv("SLIPLANE_PROFILE", "")
	t.Setenv("SLIPLANE_S3_ACCESS_KEY_ID", "s3-access")
	t.Setenv("SLIPLANE_S3_SECRET_ACCESS_KEY", "s3-secret")
	var writes atomic.Int32
	objects := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.Header.Get("Authorization"), "test-token") {
			t.Error("API token sent to S3")
		}
		if r.Method == http.MethodHead {
			return
		}
		if r.Method == http.MethodPut {
			writes.Add(1)
			payload, _ := io.ReadAll(r.Body)
			if string(payload) != "upload body" {
				t.Error("wrong upload body")
			}
			w.Header().Set("ETag", `"etag"`)
			return
		}
		if r.URL.Path == "/uploads/broken.bin" {
			w.Header().Set("Content-Length", "100")
			fmt.Fprint(w, "short")
			return
		}
		if r.URL.Path == "/uploads" {
			w.Header().Set("Content-Type", "application/xml")
			fmt.Fprint(w, `<ListBucketResult><IsTruncated>false</IsTruncated></ListBucketResult>`)
			return
		}
		fmt.Fprint(w, "download body")
	}))
	defer objects.Close()
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/me":
			fmt.Fprint(w, `{"organizationId":"org_work","tokenType":"rw","user":{}}`)
		case "/buckets":
			fmt.Fprintf(w, `[{"id":"sb_one","name":"uploads","endpoint":%q}]`, objects.URL)
		default:
			t.Errorf("unexpected API request %s", r.URL.Path)
		}
	}))
	defer control.Close()
	source := filepath.Join(t.TempDir(), "source.txt")
	if err := os.WriteFile(source, []byte("upload body"), 0600); err != nil {
		t.Fatal(err)
	}
	output, _, err := runCommand(t, control.URL, "s3", "upload", "uploads", source, "existing.txt", "-o", "json")
	if err == nil || !strings.Contains(err.Error(), "--force") || output != "" {
		t.Fatalf("overwrite: %q %v", output, err)
	}
	output, _, err = runCommand(t, control.URL, "s3", "upload", "uploads", source, "existing.txt", "--force", "-o", "json")
	if err != nil || !json.Valid([]byte(output)) || writes.Load() != 1 {
		t.Fatalf("upload: %q %v", output, err)
	}
	directory := t.TempDir()
	destination := filepath.Join(directory, "download.txt")
	output, _, err = runCommand(t, control.URL, "s3", "download", "uploads", "remote.txt", destination, "-o", "json")
	data, readErr := os.ReadFile(destination)
	if err != nil || readErr != nil || string(data) != "download body" || !json.Valid([]byte(output)) {
		t.Fatalf("download: %q %v %v", output, err, readErr)
	}
	output, _, err = runCommand(t, control.URL, "s3", "download", "uploads", "broken.bin", destination, "--force", "-o", "json")
	data, _ = os.ReadFile(destination)
	leftovers, _ := filepath.Glob(filepath.Join(directory, ".sliplane-download-*"))
	if err == nil || output != "" || string(data) != "download body" || len(leftovers) != 0 {
		t.Fatalf("failed download replaced destination: %q %v %v", output, err, leftovers)
	}
	output, _, err = runCommand(t, control.URL, "s3", "download", "uploads", "remote.txt", destination, "-o", "json")
	if err == nil || output != "" || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("local overwrite: %q %v", output, err)
	}
	output, _, err = runCommand(t, control.URL, "s3", "ls", "uploads", "--recursive", "-o", "json")
	if err != nil || strings.TrimSpace(output) != "[]" {
		t.Fatalf("empty objects: %q %v", output, err)
	}
}

// TestS3ListAliases keeps list and ls identical for files, prefixes, and recursive listings.
func TestS3ListAliases(t *testing.T) {
	t.Setenv("SLIPLANE_PROFILE", "")
	t.Setenv("SLIPLANE_S3_ACCESS_KEY_ID", "s3-access")
	t.Setenv("SLIPLANE_S3_SECRET_ACCESS_KEY", "s3-secret")
	for _, recursive := range []bool{false, true} {
		t.Run(fmt.Sprint(recursive), func(t *testing.T) {
			objects := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				delimiter := "/"
				if recursive {
					delimiter = ""
				}
				if r.URL.Path != "/uploads" || r.URL.Query().Get("prefix") != "photos/" || r.URL.Query().Get("delimiter") != delimiter {
					t.Errorf("unexpected file list: %s", r.URL)
				}
				fmt.Fprint(w, `<ListBucketResult><IsTruncated>false</IsTruncated><Contents><Key>photos/a.jpg</Key><Size>42</Size></Contents></ListBucketResult>`)
			}))
			defer objects.Close()
			control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/me":
					fmt.Fprint(w, `{"organizationId":"org_work","tokenType":"rw"}`)
				case "/buckets":
					fmt.Fprintf(w, `[{"id":"sb_one","name":"uploads","endpoint":%q}]`, objects.URL)
				default:
					t.Errorf("unexpected API request: %s", r.URL)
				}
			}))
			defer control.Close()
			for _, format := range []string{"text", "json"} {
				var previous string
				for _, name := range []string{"list", "ls"} {
					args := []string{"s3", name, "uploads", "photos/", "-o", format}
					if recursive {
						args = append(args, "--recursive")
					}
					output, stderr, err := runCommand(t, control.URL, args...)
					if err != nil || stderr != "" || !strings.Contains(output, "photos/a.jpg") || (format == "json" && !json.Valid([]byte(output))) {
						t.Fatalf("%s: %q %q %v", name, output, stderr, err)
					}
					if name == "ls" && output != previous {
						t.Fatalf("aliases differ: %q != %q", output, previous)
					}
					previous = output
				}
			}
		})
	}
}

// TestS3ReadOnlyTokenCannotUpload rejects writes before reading S3 credentials or calling its endpoint.
func TestS3ReadOnlyTokenCannotUpload(t *testing.T) {
	t.Setenv("SLIPLANE_PROFILE", "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/me" {
			fmt.Fprint(w, `{"organizationId":"org_work","tokenType":"ro","user":{}}`)
			return
		}
		fmt.Fprint(w, `[{"id":"sb_one","name":"uploads","endpoint":"https://s3.example.test"}]`)
	}))
	defer server.Close()
	filename := filepath.Join(t.TempDir(), "file")
	os.WriteFile(filename, []byte("data"), 0600)
	output, _, err := runCommand(t, server.URL, "s3", "upload", "uploads", filename, "-o", "json")
	if err == nil || !strings.Contains(err.Error(), "read-write") || output != "" {
		t.Fatalf("read-only upload: %q %v", output, err)
	}
}

//go:build unix

package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestInstall(t *testing.T) {
	archiveName := fmt.Sprintf("sliplane_1.2.3_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	for _, test := range []struct {
		name, failure, wantError string
	}{
		{name: "update"},
		{name: "unsigned release", failure: "unsigned", wantError: "checksums.txt.sig: 404"},
		{name: "untrusted signer", failure: "untrusted", wantError: "not from a trusted release key"},
		{name: "checksums changed after signing", failure: "tampered", wantError: "signature does not match"},
		{name: "bad checksum", failure: "checksum", wantError: "checksum verification failed"},
		{name: "missing checksum", failure: "missing", wantError: "exactly one checksum"},
		{name: "invalid archive", failure: "archive", wantError: "invalid release archive"},
		{name: "symlink in archive", failure: "symlink", wantError: "regular Sliplane executable"},
		{name: "unrunnable executable", failure: "execution", wantError: "could not run"},
		{name: "wrong reported version", failure: "wrong version", wantError: "reports version 9.9.9 instead of 1.2.3"},
	} {
		t.Run(test.name, func(t *testing.T) {
			binary := "#!/bin/sh\necho 'sliplane 1.2.3 (commit test)'\n"
			switch test.failure {
			case "execution":
				binary = "#!/bin/sh\nexit 1\n"
			case "wrong version":
				binary = "#!/bin/sh\necho 'sliplane 9.9.9 (commit test)'\n"
			}
			archive := releaseArchive(t, binary, test.failure == "symlink")
			if test.failure == "archive" {
				archive = []byte("not an archive")
			}
			checksum := fmt.Sprintf("%x  %s\n", sha256.Sum256(archive), archiveName)
			switch test.failure {
			case "checksum":
				checksum = fmt.Sprintf("%x  %s\n", sha256.Sum256([]byte("other")), archiveName)
			case "missing":
				checksum = "0000  other.tar.gz\n"
			}
			signer := newTestSigner(t)
			signature := signer.sign([]byte(checksum), signatureNamespace, "sha512", userPresent)
			if test.failure == "untrusted" {
				signature = newTestSigner(t).sign([]byte(checksum), signatureNamespace, "sha512", userPresent)
			}
			if test.failure == "tampered" {
				checksum += "0000  extra.tar.gz\n"
			}
			files := map[string][]byte{"checksums.txt": []byte(checksum), "checksums.txt.sig": signature, archiveName: archive}
			if test.failure == "unsigned" {
				delete(files, "checksums.txt.sig")
			}
			server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				contents, ok := files[strings.TrimPrefix(request.URL.Path, "/releases/download/v1.2.3/")]
				if !ok {
					http.NotFound(writer, request)
					return
				}
				writer.Write(contents)
			}))
			defer server.Close()

			executable := filepath.Join(t.TempDir(), "sliplane")
			if err := os.WriteFile(executable, []byte("old"), 0755); err != nil {
				t.Fatal(err)
			}
			updater := &Updater{Releases: server.URL + "/releases", Client: server.Client(), Signers: signer.allowedSigners(), Executable: executable}
			err := updater.Install(context.Background(), "1.2.3")
			installed, readErr := os.ReadFile(executable)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(executable), ".sliplane.*")); len(leftovers) != 0 {
				t.Fatalf("staged files left behind: %v", leftovers)
			}
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("error %v; want %q", err, test.wantError)
				}
				if string(installed) != "old" {
					t.Fatal("a failed update changed the executable")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if string(installed) != binary {
				t.Fatalf("installed %q; want the release executable", installed)
			}
		})
	}
}

func TestLatest(t *testing.T) {
	for _, test := range []struct {
		name, location, want, wantError string
	}{
		{name: "release", location: "/releases/tag/v1.2.3", want: "1.2.3"},
		{name: "no release", wantError: "did not return a published release"},
		{name: "other page", location: "/releases", wantError: "did not return a published release"},
		{name: "invalid tag", location: "/releases/tag/v1.2.3%2F..%2F..", wantError: "did not return a published release"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.URL.Path != "/releases/latest" || test.location == "" {
					http.NotFound(writer, request)
					return
				}
				http.Redirect(writer, request, test.location, http.StatusFound)
			}))
			defer server.Close()
			updater := &Updater{Releases: server.URL + "/releases", Client: server.Client()}
			version, err := updater.Latest(context.Background())
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("version %q, error %v; want %q", version, err, test.wantError)
				}
				return
			}
			if err != nil || version != test.want {
				t.Fatalf("version %q, error %v; want %q", version, err, test.want)
			}
		})
	}
}

func TestNewer(t *testing.T) {
	for _, test := range []struct {
		release, current string
		want             bool
	}{
		{"1.2.4", "1.2.3", true},
		{"1.10.0", "1.9.9", true},
		{"2.0.0", "1.99.99", true},
		{"1.2.3", "1.2.3-rc.1", true},
		{"1.2.3", "1.2.3", false},
		{"1.2.2", "1.2.3", false},
		{"1.2.3", "1.3.0-rc.1", false},
		{"1.3.0-rc.1", "1.2.3", false},
		{"1.2.4", "dev", false},
		{"v1.2.4", "1.2.3", false},
	} {
		if got := Newer(test.release, test.current); got != test.want {
			t.Errorf("Newer(%q, %q) = %v; want %v", test.release, test.current, got, test.want)
		}
	}
}

func TestBackgroundState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sliplane", "update.json")
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	if !Claim(path, now) {
		t.Fatal("first check was not due")
	}
	if Claim(path, now.Add(23*time.Hour)) {
		t.Fatal("checked again within a day")
	}
	if !Claim(path, now.Add(-time.Hour)) {
		t.Fatal("a check from the future blocked updates")
	}
	if !Claim(path, now.Add(25*time.Hour)) {
		t.Fatal("check was not due after a day")
	}

	if _, ok := Replaced(path, "1.2.3"); ok {
		t.Fatal("announced an update that did not happen")
	}
	if err := Record(path, "1.2.2"); err != nil {
		t.Fatal(err)
	}
	if replaced, ok := Replaced(path, "1.2.3"); !ok || replaced != "1.2.2" {
		t.Fatalf("Replaced = %q, %v; want 1.2.2", replaced, ok)
	}
	if _, ok := Replaced(path, "1.2.3"); ok {
		t.Fatal("announced the same update twice")
	}
	if Claim(path, now.Add(25*time.Hour)) {
		t.Fatal("recording an update reset the last check")
	}

	if err := os.WriteFile(path, []byte("damaged"), 0600); err != nil {
		t.Fatal(err)
	}
	if !Claim(path, now) {
		t.Fatal("a damaged state file blocked updates")
	}
}

func releaseArchive(t *testing.T, binary string, symlink bool) []byte {
	t.Helper()
	var archive bytes.Buffer
	compressed := gzip.NewWriter(&archive)
	files := tar.NewWriter(compressed)
	header := &tar.Header{Name: "sliplane", Mode: 0755, Size: int64(len(binary)), Typeflag: tar.TypeReg}
	if symlink {
		header = &tar.Header{Name: "sliplane", Linkname: "/bin/sh", Typeflag: tar.TypeSymlink}
	}
	if err := files.WriteHeader(header); err != nil {
		t.Fatal(err)
	}
	if !symlink {
		if _, err := files.Write([]byte(binary)); err != nil {
			t.Fatal(err)
		}
	}
	if err := files.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	return archive.Bytes()
}

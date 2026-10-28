// Package update replaces the running executable with the latest signed release,
// verifying it the same way scripts/install.sh does.
package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const releases = "https://github.com/sliplane/cli/releases"

var versionPattern = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)(-[0-9A-Za-z.-]+)?$`)

type Updater struct {
	Releases   string       // GitHub releases page of the repository
	Client     *http.Client // HTTP client for release downloads
	Signers    string       // allowed_signers lines trusted to sign checksums.txt
	Executable string       // file that Install replaces
}

func New(executable string) *Updater {
	return &Updater{
		Releases: releases,
		Client: &http.Client{Timeout: 5 * time.Minute, CheckRedirect: func(request *http.Request, _ []*http.Request) error {
			if request.URL.Scheme != "https" {
				return errors.New("refusing a redirect away from HTTPS")
			}
			return nil
		}},
		Signers:    allowedSigners,
		Executable: executable,
	}
}

// Executable returns the file behind the running process, following symlinks
// so that an update replaces the installed binary rather than a link to it.
func Executable() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(executable)
}

// Latest returns the version GitHub marks as the latest release. Releases stay
// drafts until a maintainer signs them, so this is always a signed release.
func (u *Updater) Latest(ctx context.Context) (string, error) {
	client := *u.Client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, u.Releases+"/latest", nil)
	if err != nil {
		return "", err
	}
	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("find the latest release: %w", err)
	}
	response.Body.Close()
	location, err := response.Location()
	if err != nil {
		return "", errors.New("GitHub did not return a published release")
	}
	tag, found := strings.CutPrefix(location.String(), u.Releases+"/tag/")
	version := strings.TrimPrefix(tag, "v")
	if !found || !versionPattern.MatchString(version) {
		return "", errors.New("GitHub did not return a published release")
	}
	return version, nil
}

// Install downloads version, verifies its signature, checksum, and reported
// version, and only then atomically replaces the executable.
func (u *Updater) Install(ctx context.Context, version string) error {
	if !versionPattern.MatchString(version) {
		return fmt.Errorf("invalid version %q", version)
	}
	keys, err := trustedKeys(u.Signers)
	if err != nil {
		return err
	}
	release := u.Releases + "/download/v" + version + "/"
	archive := fmt.Sprintf("sliplane_%s_%s_%s.tar.gz", version, runtime.GOOS, runtime.GOARCH)
	checksums, err := u.download(ctx, release+"checksums.txt", 1<<20)
	if err != nil {
		return err
	}
	signature, err := u.download(ctx, release+"checksums.txt.sig", 1<<16)
	if err != nil {
		return err
	}
	if err := verifySignature(checksums, signature, keys); err != nil {
		return fmt.Errorf("verify release signature: %w", err)
	}
	expected, err := checksum(checksums, archive)
	if err != nil {
		return err
	}
	contents, err := u.download(ctx, release+archive, 256<<20)
	if err != nil {
		return err
	}
	if sha256.Sum256(contents) != expected {
		return fmt.Errorf("checksum verification failed for %s", archive)
	}
	binary, err := extract(contents)
	if err != nil {
		return err
	}
	return u.replace(ctx, binary, version)
}

func (u *Updater) download(ctx context.Context, url string, limit int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	response, err := u.Client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", path.Base(url), err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: %s", path.Base(url), response.Status)
	}
	contents, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", path.Base(url), err)
	}
	if int64(len(contents)) > limit {
		return nil, fmt.Errorf("download %s: file is too large", path.Base(url))
	}
	return contents, nil
}

// checksum returns the SHA-256 that checksums.txt lists for name, which must appear exactly once.
func checksum(checksums []byte, name string) ([sha256.Size]byte, error) {
	var sum [sha256.Size]byte
	count := 0
	for line := range strings.Lines(string(checksums)) {
		fields := strings.Fields(line)
		if len(fields) != 2 || fields[1] != name {
			continue
		}
		count++
		decoded, err := hex.DecodeString(fields[0])
		if err != nil || len(decoded) != sha256.Size {
			return sum, fmt.Errorf("invalid checksum for %s", name)
		}
		copy(sum[:], decoded)
	}
	if count != 1 {
		return sum, fmt.Errorf("the release must contain exactly one checksum for %s", name)
	}
	return sum, nil
}

// extract returns the sliplane executable from a release archive.
func extract(archive []byte) ([]byte, error) {
	decompressed, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, errors.New("invalid release archive")
	}
	files := tar.NewReader(decompressed)
	for {
		header, err := files.Next()
		if err == io.EOF {
			return nil, errors.New("the release does not contain a Sliplane executable")
		}
		if err != nil {
			return nil, errors.New("invalid release archive")
		}
		if header.Name != "sliplane" {
			continue
		}
		if header.Typeflag != tar.TypeReg {
			return nil, errors.New("the release does not contain a regular Sliplane executable")
		}
		binary, err := io.ReadAll(files)
		if err != nil {
			return nil, errors.New("invalid release archive")
		}
		return binary, nil
	}
}

// replace stages binary next to the executable, checks that it runs and reports
// version, and renames it into place so no command ever sees a partial file.
func (u *Updater) replace(ctx context.Context, binary []byte, version string) error {
	staged, err := os.CreateTemp(filepath.Dir(u.Executable), ".sliplane.*")
	if err != nil {
		return fmt.Errorf("cannot write next to %s: %w", u.Executable, err)
	}
	defer os.Remove(staged.Name())
	_, err = staged.Write(binary)
	if closeErr := staged.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Chmod(staged.Name(), 0755)
	}
	if err != nil {
		return fmt.Errorf("stage the update: %w", err)
	}
	reported, err := reportedVersion(ctx, staged.Name())
	if err != nil {
		return errors.New("the downloaded executable could not run or returned invalid version information")
	}
	if reported != version {
		return fmt.Errorf("the downloaded executable reports version %s instead of %s", reported, version)
	}
	if err := os.Rename(staged.Name(), u.Executable); err != nil {
		return fmt.Errorf("install the update: %w", err)
	}
	return nil
}

func reportedVersion(ctx context.Context, executable string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable, "version", "-o", "text")
	command.Env = append(os.Environ(), disableVariable+"=1")
	output, err := command.Output()
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(output))
	if len(fields) != 4 || fields[0] != "sliplane" || fields[2] != "(commit" || !strings.HasSuffix(fields[3], ")") {
		return "", errors.New("unexpected version output")
	}
	return fields[1], nil
}

// Newer reports whether release is a later version than current.
// GitHub never marks a prerelease as latest, so release has no prerelease part.
func Newer(release, current string) bool {
	releaseParts := versionPattern.FindStringSubmatch(release)
	currentParts := versionPattern.FindStringSubmatch(current)
	if releaseParts == nil || currentParts == nil || releaseParts[4] != "" {
		return false
	}
	for index := 1; index <= 3; index++ {
		releaseNumber, _ := strconv.Atoi(releaseParts[index])
		currentNumber, _ := strconv.Atoi(currentParts[index])
		if releaseNumber != currentNumber {
			return releaseNumber > currentNumber
		}
	}
	// A release follows its own prereleases.
	return currentParts[4] != ""
}

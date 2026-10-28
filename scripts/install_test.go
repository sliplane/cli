//go:build !windows

package scripts

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

func TestInstall(t *testing.T) {
	for _, test := range []struct {
		name, system, architecture, version, failure, state, wantMessage string
	}{
		{name: "first installation", system: "Linux", architecture: "x86_64", state: "fresh", wantMessage: "Installed Sliplane 1.2.3"},
		{name: "update existing installation", system: "Linux", architecture: "x86_64", state: "older", wantMessage: "Updated Sliplane 1.1.0 → 1.2.3"},
		{name: "identical installation stays untouched", system: "Linux", architecture: "x86_64", state: "current", wantMessage: "Already up to date: Sliplane 1.2.3"},
		{name: "same version damaged executable is repaired", system: "Linux", architecture: "x86_64", state: "damaged", wantMessage: "Reinstalled Sliplane 1.2.3"},
		{name: "missing executable permission is repaired", system: "Linux", architecture: "x86_64", state: "not executable", wantMessage: "Reinstalled Sliplane 1.2.3"},
		{name: "unrunnable release preserves installation", system: "Linux", architecture: "x86_64", failure: "execution"},
		{name: "wrong release version preserves installation", system: "Linux", architecture: "x86_64", failure: "wrong version"},
		{name: "invalid version output preserves installation", system: "Linux", architecture: "x86_64", failure: "metadata"},
		{name: "latest macOS ARM", system: "Darwin", architecture: "arm64"},
		{name: "pinned macOS Intel", system: "Darwin", architecture: "x86_64", version: "v1.2.3"},
		{name: "pinned Linux ARM", system: "Linux", architecture: "aarch64", version: "1.2.3"},
		{name: "Linux Intel", system: "Linux", architecture: "x86_64"},
		{name: "bad checksum", system: "Linux", architecture: "x86_64", failure: "checksum"},
		{name: "missing checksum", system: "Linux", architecture: "x86_64", failure: "missing"},
		{name: "duplicate checksum", system: "Linux", architecture: "x86_64", failure: "duplicate"},
		{name: "unsigned release", system: "Linux", architecture: "x86_64", failure: "unsigned", wantMessage: "Could not download the release signature"},
		{name: "signature from untrusted key", system: "Linux", architecture: "x86_64", failure: "untrusted", wantMessage: "Release signature verification failed"},
		{name: "signature for another purpose", system: "Linux", architecture: "x86_64", failure: "namespace", wantMessage: "Release signature verification failed"},
		{name: "checksums changed after signing", system: "Linux", architecture: "x86_64", failure: "tampered", wantMessage: "Release signature verification failed"},
		{name: "failed download", system: "Linux", architecture: "x86_64", failure: "download"},
		{name: "invalid archive", system: "Linux", architecture: "x86_64", failure: "archive"},
		{name: "unsupported system", system: "FreeBSD", architecture: "x86_64", failure: "platform"},
		{name: "invalid version", system: "Linux", architecture: "x86_64", version: "../../bad", failure: "version"},
		{name: "unknown option", system: "Linux", architecture: "x86_64", version: "--unknown", failure: "argument"},
		{name: "symlink destination", system: "Linux", architecture: "x86_64", failure: "symlink"},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			platform := strings.ToLower(test.system)
			architecture := "amd64"
			if test.architecture == "arm64" || test.architecture == "aarch64" {
				architecture = "arm64"
			}
			archiveName := fmt.Sprintf("sliplane_1.2.3_%s_%s.tar.gz", platform, architecture)
			binary := []byte("#!/bin/sh\nprintf 'sliplane 1.2.3 (commit test)\\n'\n")
			switch test.failure {
			case "execution":
				binary = []byte("#!/bin/sh\nexit 1\n")
			case "wrong version":
				binary = []byte("#!/bin/sh\necho 'sliplane 9.9.9 (commit test)'\n")
			case "metadata":
				binary = []byte("#!/bin/sh\necho unrelated-program\n")
			}
			archive := releaseArchive(t, binary)
			if test.failure == "archive" {
				archive.Reset()
				archive.WriteString("not an archive")
			}
			checksum := fmt.Sprintf("%x  %s\n", sha256.Sum256(archive.Bytes()), archiveName)
			switch test.failure {
			case "checksum":
				checksum = strings.Repeat("0", 64) + "  " + archiveName + "\n"
			case "missing":
				checksum = ""
			case "duplicate":
				checksum += checksum
			}
			writeFile(t, filepath.Join(directory, archiveName), archive.Bytes(), 0600)
			writeFile(t, filepath.Join(directory, "checksums.txt"), []byte(checksum), 0600)
			if test.failure == "download" {
				if err := os.Remove(filepath.Join(directory, archiveName)); err != nil {
					t.Fatal(err)
				}
			}
			installDirectory := filepath.Join(directory, "user's bin")
			destination := filepath.Join(installDirectory, "sliplane")
			original := []byte("previous working binary")
			switch test.state {
			case "older":
				original = []byte("#!/bin/sh\necho 'sliplane 1.1.0 (commit old)'\n")
			case "current", "not executable":
				original = binary
			case "damaged":
				original = append([]byte("# damaged installation\n"), binary...)
			}
			writeFile(t, destination, original, 0755)
			if test.state == "fresh" {
				if err := os.Remove(destination); err != nil {
					t.Fatal(err)
				}
			}
			if test.state == "not executable" {
				if err := os.Chmod(destination, 0644); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.Stat(destination)
			if test.failure == "symlink" {
				if err := os.Rename(destination, destination+".original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(destination+".original", destination); err != nil {
					t.Fatal(err)
				}
			}
			command := installerCommand(t, directory, installDirectory, "1.2.3", test.system, test.architecture, test.version)
			switch test.failure {
			case "unsigned":
				if err := os.Remove(filepath.Join(directory, "checksums.txt.sig")); err != nil {
					t.Fatal(err)
				}
			case "untrusted":
				signChecksums(t, directory, newSigningKey(t, t.TempDir()), "sliplane-release")
			case "namespace":
				signChecksums(t, directory, filepath.Join(command.Dir, "trusted-key"), "git")
			case "tampered":
				checksums, err := os.OpenFile(filepath.Join(directory, "checksums.txt"), os.O_APPEND|os.O_WRONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
				fmt.Fprintf(checksums, "%s  other.tar.gz\n", strings.Repeat("0", 64))
				if err := checksums.Close(); err != nil {
					t.Fatal(err)
				}
			}
			home := t.TempDir()
			if test.failure != "" {
				command.Args = append(command.Args, "--setup-path")
				command.Env = append(command.Env, "HOME="+home)
			}
			output, err := command.CombinedOutput()
			installed, readError := os.ReadFile(destination)
			if readError != nil {
				t.Fatal(readError)
			}
			if test.failure != "" {
				if err == nil || !bytes.Equal(installed, original) {
					t.Fatalf("failed install changed the binary or succeeded: %v\n%s", err, output)
				}
				if !strings.Contains(string(output), test.wantMessage) {
					t.Fatalf("missing error %q: %s", test.wantMessage, output)
				}
				if _, err := os.Stat(filepath.Join(home, ".bashrc")); !os.IsNotExist(err) {
					t.Fatal("failed installation changed shell configuration")
				}
				return
			}
			if err != nil || !bytes.Equal(installed, binary) {
				t.Fatalf("install failed: %v\n%s", err, output)
			}
			if output, err := exec.Command(destination, "version").CombinedOutput(); err != nil || string(output) != "sliplane 1.2.3 (commit test)\n" {
				t.Fatalf("installed executable: %v\n%s", err, output)
			}
			if test.wantMessage != "" && !strings.Contains(string(output), test.wantMessage) {
				t.Fatalf("missing status %q: %s", test.wantMessage, output)
			}
			wantLoginHints := 0
			if test.state == "fresh" {
				wantLoginHints = 1
			}
			if strings.Count(string(output), "sliplane auth login") != wantLoginHints {
				t.Fatalf("unexpected login instructions: %s", output)
			}
			if test.state == "current" {
				after, err := os.Stat(destination)
				if err != nil || !os.SameFile(before, after) {
					t.Fatal("replaced identical executable")
				}
			}
			for _, line := range strings.Split(string(output), "\n") {
				if strings.HasPrefix(line, "  export PATH=") {
					check := exec.Command("sh", "-c", line+"\nprintf '%s' \"$PATH\"")
					path, err := check.Output()
					if err != nil || !strings.HasPrefix(string(path), installDirectory+":") {
						t.Fatalf("invalid shell instructions %q: %v", line, err)
					}
				}
			}
		})
	}
}

func TestInstallPath(t *testing.T) {
	for _, test := range []struct {
		name, shell, profile, reply                                                                   string
		arguments                                                                                     []string
		terminal, wantPrompt, wantSetup, existingProfile, customZdotdir, brokenProfile, alreadyOnPath bool
	}{
		{name: "redirected output skips setup", shell: "/bin/bash", profile: ".bashrc"},
		{name: "bash setup creates profile", shell: "/bin/bash", profile: ".bashrc", arguments: []string{"--setup-path"}, wantSetup: true},
		{name: "zsh setup preserves profile", shell: "/bin/zsh", profile: ".zshrc", arguments: []string{"--setup-path", "1.2.3"}, wantSetup: true, existingProfile: true},
		{name: "bash login profile fallback", shell: "/bin/bash", profile: ".bash_profile", arguments: []string{"--setup-path"}, wantSetup: true, existingProfile: true},
		{name: "zsh custom config directory", shell: "/bin/zsh", profile: ".zshrc", arguments: []string{"--setup-path"}, wantSetup: true, customZdotdir: true},
		{name: "unsupported shell gets instructions", shell: "/usr/bin/fish", profile: ".bashrc", arguments: []string{"--setup-path"}},
		{name: "explicit setup in automation", shell: "/bin/bash", profile: ".bashrc", arguments: []string{"--non-interactive", "--setup-path"}, wantSetup: true},
		{name: "unwritable profile keeps install", shell: "/bin/bash", profile: ".bashrc", arguments: []string{"--setup-path"}, brokenProfile: true},
		{name: "existing PATH needs no manual setup", shell: "/bin/bash", profile: ".bashrc", alreadyOnPath: true},
		{name: "explicit setup persists an existing PATH", shell: "/bin/bash", profile: ".bashrc", arguments: []string{"--setup-path"}, alreadyOnPath: true, wantSetup: true},
		{name: "interactive default accepts", shell: "/bin/zsh", profile: ".zshrc", terminal: true, reply: "\n", wantPrompt: true, wantSetup: true},
		{name: "interactive decline preserves profile", shell: "/bin/bash", profile: ".bashrc", terminal: true, reply: "n\n", wantPrompt: true, existingProfile: true},
		{name: "noninteractive flag skips terminal prompt", shell: "/bin/bash", profile: ".bashrc", arguments: []string{"--non-interactive"}, terminal: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			archiveName := "sliplane_1.2.3_linux_amd64.tar.gz"
			// Profiles run `sliplane completion`, which must print a valid script.
			binary := []byte("#!/bin/sh\ncase \"$1\" in\n  version) echo 'sliplane 1.2.3 (commit test)' ;;\n  completion) echo \"sliplane_completion=$2\" ;;\nesac\n")
			archive := releaseArchive(t, binary)
			writeFile(t, filepath.Join(directory, archiveName), archive.Bytes(), 0600)
			checksum := fmt.Sprintf("%x  %s\n", sha256.Sum256(archive.Bytes()), archiveName)
			writeFile(t, filepath.Join(directory, "checksums.txt"), []byte(checksum), 0600)
			installDirectory := filepath.Join(directory, "user's $literal `bin`")
			command := installerCommand(t, directory, installDirectory, "1.2.3", "Linux", "x86_64", test.arguments...)
			home := t.TempDir()
			configDirectory := home
			command.Env = append(command.Env, "HOME="+home, "SHELL="+test.shell)
			if test.customZdotdir {
				configDirectory = t.TempDir()
				command.Env = append(command.Env, "ZDOTDIR="+configDirectory)
			}
			if test.alreadyOnPath {
				for _, entry := range command.Environ() {
					if strings.HasPrefix(entry, "PATH=") {
						command.Env = append(command.Env, "PATH="+installDirectory+":"+strings.TrimPrefix(entry, "PATH="))
					}
				}
			}
			profile := filepath.Join(configDirectory, test.profile)
			original := []byte("# existing user configuration\nexport KEEP_ME=yes\n")
			if test.existingProfile {
				writeFile(t, profile, original, 0600)
			}
			if test.brokenProfile {
				if err := os.Mkdir(profile, 0700); err != nil {
					t.Fatal(err)
				}
			}
			var output []byte
			var err error
			if test.terminal {
				// stdin is the script pipe; answers must come from /dev/tty.
				command.Args = append([]string{"sh", "-c", `cat install.sh | sh -s -- "$@"`, "sh"}, test.arguments...)
				terminal, startError := pty.Start(command)
				if startError != nil {
					t.Fatal(startError)
				}
				defer terminal.Close()
				if _, err := terminal.WriteString(test.reply); err != nil {
					t.Fatal(err)
				}
				output, _ = io.ReadAll(terminal) // PTYs can report EIO when the child exits.
				err = command.Wait()
			} else {
				output, err = command.CombinedOutput()
			}
			if err != nil {
				t.Fatalf("install: %v\n%s", err, output)
			}
			if strings.Contains(string(output), "(Y/n)") != test.wantPrompt {
				t.Fatalf("unexpected prompt behavior: %s", output)
			}
			installed, err := os.ReadFile(filepath.Join(installDirectory, "sliplane"))
			if err != nil || !bytes.Equal(installed, binary) {
				t.Fatalf("binary not installed: %v", err)
			}
			contents, readError := os.ReadFile(profile)
			if !test.wantSetup {
				if test.existingProfile && !bytes.Equal(contents, original) {
					t.Fatal("changed existing profile")
				}
				if !test.existingProfile && readError == nil {
					t.Fatal("unexpected profile created")
				}
				return
			}
			if readError != nil {
				t.Fatal(readError)
			}
			if !test.wantPrompt && strings.Contains(string(output), "export PATH=") {
				t.Fatalf("automatic setup printed unnecessary PATH instructions: %s", output)
			}
			if test.existingProfile && !bytes.HasPrefix(contents, original) {
				t.Fatal("lost existing configuration")
			}
			check := exec.Command("sh", "-c", `. "$1"; printf '%s' "$PATH"`, "sh", profile)
			path, err := check.Output()
			if err != nil || !strings.HasPrefix(string(path), installDirectory+":") {
				t.Fatalf("profile PATH does not select installation: %v %s", err, path)
			}
			shell := filepath.Base(test.shell)
			if strings.Count(string(contents), "eval \"$(sliplane completion "+shell+")\"") != 1 {
				t.Fatalf("profile does not load %s completion:\n%s", shell, contents)
			}
			if _, err := exec.LookPath(shell); err == nil {
				// bash completion also needs bash-completion; stand in for it.
				load := exec.Command(shell, "-c", `_get_comp_words_by_ref() { :; }; . "$1"; printf '%s' "$sliplane_completion"`, shell, profile)
				load.Env = append(command.Env, "ZDOTDIR="+t.TempDir())
				if loaded, err := load.CombinedOutput(); err != nil || string(loaded) != shell {
					t.Fatalf("%s did not load completion: %v %s", shell, err, loaded)
				}
			}
			repeat := exec.Command("sh", "install.sh", "--setup-path")
			repeat.Dir = command.Dir
			repeat.Env = command.Env
			repeatOutput, err := repeat.CombinedOutput()
			if err != nil {
				t.Fatalf("repeat install: %v\n%s", err, repeatOutput)
			}
			if !strings.Contains(string(repeatOutput), "Already up to date") || strings.Contains(string(repeatOutput), "export PATH=") || strings.Contains(string(repeatOutput), "auth login") {
				t.Fatalf("unexpected repeat install output: %s", repeatOutput)
			}
			afterRepeat, err := os.ReadFile(profile)
			if err != nil || !bytes.Equal(afterRepeat, contents) {
				t.Fatal("repeat installation changed profile")
			}
		})
	}
}

func releaseArchive(t *testing.T, binary []byte) *bytes.Buffer {
	t.Helper()
	var archive bytes.Buffer
	compressed := gzip.NewWriter(&archive)
	writer := tar.NewWriter(compressed)
	if err := writer.WriteHeader(&tar.Header{Name: "sliplane", Mode: 0755, Size: int64(len(binary))}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(binary); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	return &archive
}

func TestReleaseInstall(t *testing.T) {
	directory := os.Getenv("SLIPLANE_TEST_RELEASE_DIR")
	if directory == "" {
		t.Skip("set SLIPLANE_TEST_RELEASE_DIR to test built release archives")
	}
	contents, err := os.ReadFile(filepath.Join(directory, "metadata.json"))
	if err != nil {
		t.Fatal(err)
	}
	var metadata struct {
		Version string `json:"version"`
		Commit  string `json:"commit"`
	}
	if err := json.Unmarshal(contents, &metadata); err != nil {
		t.Fatal(err)
	}
	installDirectory := filepath.Join(t.TempDir(), "bin")
	system := "Linux"
	if runtime.GOOS == "darwin" {
		system = "Darwin"
	}
	command := installerCommand(t, directory, installDirectory, metadata.Version, system, runtime.GOARCH, metadata.Version)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("release install failed: %v\n%s", err, output)
	}
	output, err := exec.Command(filepath.Join(installDirectory, "sliplane"), "version", "-o", "json").CombinedOutput()
	if err != nil {
		t.Fatalf("release cannot run: %v\n%s", err, output)
	}
	var actual map[string]string
	if err := json.Unmarshal(output, &actual); err != nil {
		t.Fatal(err)
	}
	if actual["version"] != metadata.Version || actual["commit"] != metadata.Commit {
		t.Fatalf("release metadata mismatch: got %s, expected %+v", output, metadata)
	}
}

// TestReleaseSigners keeps the installer's embedded keys identical to the
// repository's list and accepts only hardware-backed release keys.
func TestReleaseSigners(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join("..", ".github", "allowed_signers"))
	if err != nil {
		t.Fatal(err)
	}
	var signers []string
	for _, line := range strings.Split(string(contents), "\n") {
		if line != "" && !strings.HasPrefix(line, "#") {
			signers = append(signers, line+"\n")
		}
	}
	if len(signers) == 0 {
		t.Fatal(".github/allowed_signers lists no release keys")
	}
	for _, signer := range signers {
		fields := strings.Fields(signer)
		if len(fields) != 5 || fields[0] != "sliplane-release" || fields[1] != `namespaces="git,sliplane-release"` ||
			fields[2] != "sk-ssh-ed25519@openssh.com" {
			t.Fatalf("each release key must be a named FIDO2 key for the sliplane-release principal: %q", signer)
		}
	}
	installer, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	match := releaseSigners.FindSubmatch(installer)
	if match == nil || string(match[1]) != strings.Join(signers, "") {
		t.Fatal("install.sh release signers differ from .github/allowed_signers")
	}
}

var releaseSigners = regexp.MustCompile(`(?s)<<'SIGNERS'\n(.*?)SIGNERS\n`)

func newSigningKey(t *testing.T, directory string) string {
	t.Helper()
	key := filepath.Join(directory, "trusted-key")
	if output, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "test", "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("generate signing key: %v\n%s", err, output)
	}
	return key
}

func signChecksums(t *testing.T, releaseDirectory, key, namespace string) {
	t.Helper()
	checksums := filepath.Join(releaseDirectory, "checksums.txt")
	if err := os.Remove(checksums + ".sig"); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if output, err := exec.Command("ssh-keygen", "-q", "-Y", "sign", "-f", key, "-n", namespace, checksums).CombinedOutput(); err != nil {
		t.Fatalf("sign checksums: %v\n%s", err, output)
	}
}

// installerCommand signs the release with a temporary key and runs a copy of
// install.sh that trusts that key alongside an unrelated one, so any listed key
// is enough.
func installerCommand(t *testing.T, releaseDirectory, installDirectory, version, system, architecture string, arguments ...string) *exec.Cmd {
	t.Helper()
	commands := t.TempDir()
	var signers string
	for _, key := range []string{newSigningKey(t, t.TempDir()), newSigningKey(t, commands)} {
		public, err := os.ReadFile(key + ".pub")
		if err != nil {
			t.Fatal(err)
		}
		fields := strings.Fields(string(public))
		signers += fmt.Sprintf("sliplane-release namespaces=\"git,sliplane-release\" %s %s test\n", fields[0], fields[1])
	}
	signChecksums(t, releaseDirectory, filepath.Join(commands, "trusted-key"), "sliplane-release")
	installer, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !releaseSigners.Match(installer) {
		t.Fatal("install.sh has no release signers block")
	}
	installer = releaseSigners.ReplaceAllLiteral(installer, []byte("<<'SIGNERS'\n"+signers+"SIGNERS\n"))
	writeFile(t, filepath.Join(commands, "install.sh"), installer, 0600)
	writeFile(t, filepath.Join(commands, "uname"), []byte("#!/bin/sh\ncase \"$1\" in\n-s) echo \"$TEST_SYSTEM\";;\n-m) echo \"$TEST_ARCH\";;\nesac\n"), 0755)
	writeFile(t, filepath.Join(commands, "curl"), []byte(`#!/bin/sh
set -eu
output=
while [ "$#" -gt 0 ]; do
  case "$1" in
    --output) output=$2; shift ;;
    https://*) url=$1 ;;
  esac
  shift
done
case "$url" in
  https://github.com/sliplane/cli/releases/latest)
    printf 'https://github.com/sliplane/cli/releases/tag/v%s' "$TEST_VERSION" ;;
  https://github.com/sliplane/cli/releases/download/v"$TEST_VERSION"/*)
    cp "$TEST_RELEASE_DIR/${url##*/}" "$output" ;;
  *) exit 22 ;;
esac
`), 0755)
	shellArguments := []string{"install.sh"}
	for _, argument := range arguments {
		if argument != "" {
			shellArguments = append(shellArguments, argument)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	command := exec.CommandContext(ctx, "sh", shellArguments...)
	command.Dir = commands
	command.Env = append(os.Environ(), "PATH="+commands+":"+os.Getenv("PATH"), "SLIPLANE_INSTALL_DIR="+installDirectory,
		"HOME="+t.TempDir(), "SHELL=/bin/bash", "ZDOTDIR=",
		"TEST_RELEASE_DIR="+releaseDirectory, "TEST_VERSION="+version, "TEST_SYSTEM="+system, "TEST_ARCH="+architecture)
	return command
}

func writeFile(t *testing.T, path string, contents []byte, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, contents, mode); err != nil {
		t.Fatal(err)
	}
}

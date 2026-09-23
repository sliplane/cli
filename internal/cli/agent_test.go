// Agent tests protect existing project instructions and verify offline, noninteractive command behavior.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAgentSectionPreservesInstructions covers append, refresh, line endings, and repeated initialization.
func TestAgentSectionPreservesInstructions(t *testing.T) {
	for _, test := range []struct {
		name     string
		original string
		prefix   string
		suffix   string
	}{
		{"empty", "", "", "\n"},
		{"no final newline", "# Team rules", "# Team rules\n\n", "\n"},
		{"final newline", "# Team rules\n", "# Team rules\n\n", "\n"},
		{"blank line", "# Team rules\n\n", "# Team rules\n\n", "\n"},
		{"refresh", "before\n" + agentSectionStart + "\nold guidance\n" + agentSectionEnd + "\nafter\n", "before\n", "\nafter\n"},
		{"CRLF", "before\r\n" + agentSectionStart + "\r\nold guidance\r\n" + agentSectionEnd + "\r\nafter", "before\r\n", "\r\nafter"},
	} {
		t.Run(test.name, func(t *testing.T) {
			updated, err := updateAgentSection(test.original)
			if err != nil {
				t.Fatal(err)
			}
			start := strings.Index(updated, agentSectionStart)
			end := strings.Index(updated, agentSectionEnd)
			if start < 0 || end < start || updated[:start] != test.prefix || updated[end+len(agentSectionEnd):] != test.suffix {
				t.Fatalf("instructions outside the section changed: %q", updated)
			}
			if strings.Contains(updated, "old guidance") || !strings.Contains(updated, "sliplane deploy") {
				t.Fatal("guidance was not refreshed")
			}
			if test.name == "CRLF" && strings.Contains(strings.ReplaceAll(updated, "\r\n", ""), "\n") {
				t.Fatal("introduced mixed line endings")
			}
			repeated, err := updateAgentSection(updated)
			if err != nil || repeated != updated {
				t.Fatalf("repeated init changed the file: %v", err)
			}
		})
	}
}

// TestAgentInitOffline verifies target selection, output, permissions, and no-op updates without credentials.
func TestAgentInitOffline(t *testing.T) {
	t.Setenv("SLIPLANE_PROFILE", "")
	t.Setenv("SLIPLANE_TOKEN", "")
	for _, test := range []struct {
		name       string
		claudeCode string
		filename   string
		existing   bool
		status     string
	}{
		{"create agents", "", "AGENTS.md", false, "created"},
		{"create claude", "1", "CLAUDE.md", false, "created"},
		{"disabled claude", "0", "AGENTS.md", false, "created"},
		{"append agents", "", "AGENTS.md", true, "updated"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			t.Setenv("CLAUDECODE", test.claudeCode)
			configPath := filepath.Join(t.TempDir(), "config.json")
			if test.existing {
				if err := os.WriteFile(test.filename, []byte("# Existing instructions\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			stdout, stderr, err := runProfileCommand(t, "http://127.0.0.1:1", configPath, "agent", "init", "-y", "-o", "json")
			var receipt struct{ Path, Status string }
			if err != nil || stderr != "" || json.Unmarshal([]byte(stdout), &receipt) != nil || receipt.Path != test.filename || receipt.Status != test.status {
				t.Fatalf("init: %q %q %v", stdout, stderr, err)
			}
			contents, err := os.ReadFile(test.filename)
			if err != nil || !strings.Contains(string(contents), "sliplane auth status") {
				t.Fatalf("missing guidance: %v", err)
			}
			before, err := os.Stat(test.filename)
			if err != nil {
				t.Fatal(err)
			}
			if test.existing && (before.Mode().Perm() != 0600 || !strings.HasPrefix(string(contents), "# Existing instructions\n\n")) {
				t.Fatal("existing instructions or permissions changed")
			}
			// No confirmation is needed when there is nothing to write.
			stdout, stderr, err = runProfileCommand(t, "http://127.0.0.1:1", configPath, "agent", "init", "-o", "json")
			if err != nil || stderr != "" || json.Unmarshal([]byte(stdout), &receipt) != nil || receipt.Status != "unchanged" {
				t.Fatalf("repeat: %q %q %v", stdout, stderr, err)
			}
			after, err := os.Stat(test.filename)
			if err != nil || !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
				t.Fatalf("unchanged file was rewritten: %v", err)
			}
			files, err := os.ReadDir(".")
			if err != nil || len(files) != 1 || files[0].Name() != test.filename {
				t.Fatalf("unexpected files left after init: %v %v", files, err)
			}
		})
	}
}

// TestAgentInitRejectsBrokenSections ensures malformed markers never cause loss of user content.
func TestAgentInitRejectsBrokenSections(t *testing.T) {
	t.Setenv("SLIPLANE_PROFILE", "")
	t.Setenv("CLAUDECODE", "")
	for _, contents := range []string{
		agentSectionStart + "\nuser instructions",
		"user instructions\n" + agentSectionEnd,
		agentSectionEnd + "\nuser instructions\n" + agentSectionStart,
		agentSectionStart + agentSectionStart + agentSectionEnd,
		agentSectionStart + agentSectionEnd + agentSectionEnd,
		agentSectionStart + agentSectionEnd + agentSectionStart + agentSectionEnd,
	} {
		t.Run(contents, func(t *testing.T) {
			t.Chdir(t.TempDir())
			if err := os.WriteFile("AGENTS.md", []byte(contents), 0600); err != nil {
				t.Fatal(err)
			}
			stdout, stderr, err := runCommand(t, "", "agent", "init", "--yes", "-o", "json")
			if err == nil || !strings.Contains(err.Error(), "markers") || stdout != "" || stderr != "" {
				t.Fatalf("bad markers accepted: %q %q %v", stdout, stderr, err)
			}
			preserved, err := os.ReadFile("AGENTS.md")
			if err != nil || string(preserved) != contents {
				t.Fatalf("original file changed: %v", err)
			}
		})
	}
}

// TestAgentInitRejectsUnapprovedWrites ensures pipes, invalid arguments, and cancellation cannot write guidance.
func TestAgentInitRejectsUnapprovedWrites(t *testing.T) {
	t.Setenv("SLIPLANE_PROFILE", "")
	t.Setenv("CLAUDECODE", "")
	for _, args := range [][]string{
		{"agent", "init"}, {"agent", "init", "-o", "json"},
		{"agent", "init", "--yes=false"}, {"agent", "init", "--yes", "unexpected"},
		{"agent", "unknown"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Chdir(t.TempDir())
			stdout, _, err := runCommand(t, "", args...)
			if err == nil || stdout != "" {
				t.Fatalf("accepted invalid or unconfirmed init: %q %v", stdout, err)
			}
			if _, err := os.Stat("AGENTS.md"); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("created guidance without approval: %v", err)
			}
		})
	}
	t.Chdir(t.TempDir())
	command := newCommand("")
	command.SetArgs([]string{"--config", filepath.Join(t.TempDir(), "config.json"), "agent", "init", "--yes"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := command.ExecuteContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("ignored cancellation: %v", err)
	}
	if _, err := os.Stat("AGENTS.md"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("created guidance after cancellation: %v", err)
	}
}

// TestAgentFilePreservesConcurrentEdits prevents stale confirmation from discarding newer instructions.
func TestAgentFilePreservesConcurrentEdits(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "AGENTS.md")
	if err := os.WriteFile(filename, []byte("new instructions"), 0600); err != nil {
		t.Fatal(err)
	}
	err := writeAgentFile(filename, []byte("old instructions"), []byte("replacement"), 0600)
	if err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("overwrote concurrent edit: %v", err)
	}
	contents, err := os.ReadFile(filename)
	if err != nil || string(contents) != "new instructions" {
		t.Fatalf("lost concurrent edit: %q %v", contents, err)
	}
}

// TestAgentFileRejectsSpecialTargets protects linked instructions and directories from replacement.
func TestAgentFileRejectsSpecialTargets(t *testing.T) {
	directory := t.TempDir()
	if _, _, err := readAgentFile(directory); err == nil {
		t.Fatal("accepted a directory")
	}
	target := filepath.Join(directory, "instructions.md")
	if err := os.WriteFile(target, []byte("shared instructions"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(directory, "AGENTS.md")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, _, err := readAgentFile(link); err == nil {
		t.Fatal("accepted a symbolic link")
	}
}

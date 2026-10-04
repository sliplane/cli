package cli

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

const agentSectionStart = "<!-- SLIPLANE BEST PRACTICES START -->"
const agentSectionEnd = "<!-- SLIPLANE BEST PRACTICES END -->"

//go:embed agent_guidance.md
var agentGuidance string

func (o *options) agentCommand() *cobra.Command {
	var yes bool
	command := &cobra.Command{
		Use: "agent", Short: "Set up Sliplane guidance for coding agents", Args: validateArguments,
		RunE: func(command *cobra.Command, _ []string) error { return command.Help() },
	}
	initialize := &cobra.Command{
		Use: "init", Short: "Add or update Sliplane best practices in the current directory", Args: validateArguments,
		Long:    "Add or update a marked Sliplane section in AGENTS.md (CLAUDE.md when CLAUDECODE=1). Existing instructions outside the section are preserved. Requires --yes in JSON mode or a non-interactive shell.",
		Example: "  sliplane agent init\n  sliplane agent init --yes",
		RunE: func(command *cobra.Command, _ []string) error {
			filename := "AGENTS.md"
			if os.Getenv("CLAUDECODE") == "1" {
				filename = "CLAUDE.md"
			}
			return o.initAgent(command, filename, yes)
		},
	}
	initialize.Flags().BoolVarP(&yes, "yes", "y", false, "Skip confirmation before writing agent guidance")
	command.AddCommand(initialize)
	return command
}

func (o *options) initAgent(command *cobra.Command, filename string, yes bool) error {
	original, mode, err := readAgentFile(filename)
	if err != nil {
		return err
	}
	updated, err := updateAgentSection(string(original))
	if err != nil {
		return fmt.Errorf("update %s: %w", filename, err)
	}
	status := "unchanged"
	if updated == string(original) {
		return o.print(command, map[string]any{"path": filename, "status": status}, [][]string{{filename, status}})
	}
	if !yes {
		if !o.canPrompt(command) {
			return errors.New("agent init requires --yes in JSON mode or a non-interactive shell")
		}
		confirmed, err := confirm(command.Context(), command.InOrStdin(), command.ErrOrStderr(), "Add or update Sliplane best practices in "+filename+"?")
		if err != nil {
			return err
		}
		if !confirmed {
			return o.print(command, map[string]any{"path": filename, "status": "cancelled"}, [][]string{{filename, "cancelled"}})
		}
	}
	if err := command.Context().Err(); err != nil {
		return err
	}
	if err := writeAgentFile(filename, original, []byte(updated), mode); err != nil {
		return err
	}
	status = "updated"
	if original == nil {
		status = "created"
	}
	return o.print(command, map[string]any{"path": filename, "status": status}, [][]string{{filename, status}})
}

// updateAgentSection preserves all bytes outside a single valid pair of markers.
func updateAgentSection(contents string) (string, error) {
	newline := "\n"
	if strings.Contains(contents, "\r\n") {
		newline = "\r\n"
	}
	section := agentSectionStart + newline + strings.ReplaceAll(strings.TrimSpace(agentGuidance), "\n", newline) + newline + agentSectionEnd
	starts := strings.Count(contents, agentSectionStart)
	ends := strings.Count(contents, agentSectionEnd)
	if starts == 0 && ends == 0 {
		separator := ""
		if contents != "" {
			separator = newline + newline
			if strings.HasSuffix(contents, newline) {
				separator = newline
			}
			if strings.HasSuffix(contents, newline+newline) {
				separator = ""
			}
		}
		return contents + separator + section + newline, nil
	}
	start := strings.Index(contents, agentSectionStart)
	end := strings.Index(contents, agentSectionEnd)
	if starts != 1 || ends != 1 || end < start {
		return "", errors.New("Sliplane section markers are incomplete, duplicated, or out of order; fix the markers before retrying")
	}
	return contents[:start] + section + contents[end+len(agentSectionEnd):], nil
}

// readAgentFile refuses links and special files so an update cannot affect another instruction file.
func readAgentFile(filename string) ([]byte, os.FileMode, error) {
	info, err := os.Lstat(filename)
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0644, nil
	}
	if err != nil {
		return nil, 0, fmt.Errorf("inspect %s: %w", filename, err)
	}
	if !info.Mode().IsRegular() {
		return nil, 0, fmt.Errorf("%s must be a regular file, not a directory or symbolic link", filename)
	}
	contents, err := os.ReadFile(filename)
	if err != nil {
		return nil, 0, fmt.Errorf("read %s: %w", filename, err)
	}
	return contents, info.Mode().Perm(), nil
}

// writeAgentFile stages the complete update and checks for edits made during confirmation before replacing it.
func writeAgentFile(filename string, original, updated []byte, mode os.FileMode) error {
	file, err := os.CreateTemp(filepath.Dir(filename), ".sliplane-agent-*")
	if err != nil {
		return fmt.Errorf("create temporary guidance for %s: %w", filename, err)
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, err := file.Write(updated); err != nil {
		return fmt.Errorf("write guidance for %s: %w", filename, err)
	}
	if err := file.Chmod(mode); err != nil {
		return fmt.Errorf("preserve permissions for %s: %w", filename, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close guidance for %s: %w", filename, err)
	}
	current, currentMode, err := readAgentFile(filename)
	if err != nil {
		return err
	}
	if !bytes.Equal(current, original) || (current == nil) != (original == nil) || currentMode != mode {
		return fmt.Errorf("%s changed during agent init; retry to preserve the latest instructions", filename)
	}
	if original == nil {
		// Linking prevents a concurrently created file from being overwritten.
		err = os.Link(file.Name(), filename)
	} else {
		err = os.Rename(file.Name(), filename)
	}
	if err != nil {
		return fmt.Errorf("save guidance to %s: %w", filename, err)
	}
	return nil
}

package update

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// disableVariable turns off automatic updates when set to any value.
const disableVariable = "SLIPLANE_NO_AUTO_UPDATE"

const checkInterval = 24 * time.Hour

// state is what automatic updates remember between commands.
type state struct {
	CheckedAt time.Time `json:"checkedAt"`
	Replaced  string    `json:"replaced,omitempty"` // version a background update replaced, until announced
}

// Disabled reports whether the environment opts out of automatic updates.
// CI runs keep the version they installed.
func Disabled() bool {
	return os.Getenv(disableVariable) != "" || os.Getenv("CI") != ""
}

// StatePath returns the automatic update state file in the user cache directory.
func StatePath() (string, error) {
	directory, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(directory, "sliplane", "update.json"), nil
}

// Claim reports whether a background check is due and, if so, records it as
// started so that commands running in parallel do not start another.
func Claim(path string, now time.Time) bool {
	current := load(path)
	if now.Sub(current.CheckedAt) < checkInterval && !current.CheckedAt.After(now) {
		return false
	}
	current.CheckedAt = now
	return save(path, current) == nil
}

// Record remembers the version a background update replaced, to announce it on the next command.
func Record(path, replaced string) error {
	current := load(path)
	current.Replaced = replaced
	return save(path, current)
}

// Replaced returns, once, the version that a background update replaced with
// current. A different executable on PATH can still run the replaced version;
// then there is nothing to announce.
func Replaced(path, current string) (string, bool) {
	saved := load(path)
	if saved.Replaced == "" {
		return "", false
	}
	replaced := saved.Replaced
	saved.Replaced = ""
	if save(path, saved) != nil || replaced == current {
		return "", false
	}
	return replaced, true
}

// load treats a missing or damaged state file as empty, so a bad cache only causes an early check.
func load(path string) state {
	var saved state
	if contents, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(contents, &saved)
	}
	return saved
}

func save(path string, saved state) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	contents, err := json.Marshal(saved)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".update.*")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	_, err = temporary.Write(contents)
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(temporary.Name(), path)
}

// Start runs `sliplane update --background` in a detached process that outlives the current command.
func Start(executable string) error {
	command := exec.Command(executable, "update", "--background")
	detach(command)
	if err := command.Start(); err != nil {
		return err
	}
	return command.Process.Release()
}

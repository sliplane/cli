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
	claimed := false
	err := modify(path, func(saved *state) bool {
		if now.Sub(saved.CheckedAt) < checkInterval && !saved.CheckedAt.After(now) {
			return false
		}
		saved.CheckedAt = now
		claimed = true
		return true
	})
	return err == nil && claimed
}

// Record remembers the version a background update replaced, to announce it on the next command.
func Record(path, replaced string) error {
	return modify(path, func(saved *state) bool {
		saved.Replaced = replaced
		return true
	})
}

// Replaced returns, once, the version that a background update replaced with
// current. A different executable on PATH can still run the replaced version;
// then there is nothing to announce.
func Replaced(path, current string) (string, bool) {
	var replaced string
	err := modify(path, func(saved *state) bool {
		replaced, saved.Replaced = saved.Replaced, ""
		return replaced != ""
	})
	if err != nil || replaced == "" || replaced == current {
		return "", false
	}
	return replaced, true
}

// modify applies edit to the saved state while holding a lock shared by all
// sliplane processes, and saves the state if edit changed it.
func modify(path string, edit func(*state) bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	unlock, err := lock(path + ".lock")
	if err != nil {
		return err
	}
	defer unlock()
	saved := load(path)
	if !edit(&saved) {
		return nil
	}
	return save(path, saved)
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

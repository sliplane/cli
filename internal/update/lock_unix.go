//go:build unix

package update

import (
	"os"
	"syscall"
)

// lock waits for an exclusive lock on path that other processes respect, and
// returns the function that releases it.
func lock(path string) (func(), error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		file.Close()
		return nil, err
	}
	// Closing the file releases the lock.
	return func() { file.Close() }, nil
}

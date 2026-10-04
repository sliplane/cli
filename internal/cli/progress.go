package cli

import (
	"fmt"
	"io"
	"time"
)

const progressInterval = 100 * time.Millisecond // Limit terminal redraws to ten per second.

type downloadProgress struct {
	output     io.Writer
	lastUpdate time.Time
	lastTotal  int64
}

func (p *downloadProgress) update(written, total int64) error {
	now := time.Now()
	if total == p.lastTotal && written != total && now.Sub(p.lastUpdate) < progressInterval {
		return nil
	}
	p.lastUpdate = now
	p.lastTotal = total
	message := "Downloading  " + formatBytes(written)
	if total >= 0 {
		percent := 100.0
		if total > 0 {
			percent = 100 * float64(written) / float64(total)
		}
		message += fmt.Sprintf(" / %s  (%.0f%%)", formatBytes(total), percent)
	}
	_, err := fmt.Fprint(p.output, "\r\x1b[2K"+message)
	return err
}

func (p *downloadProgress) finish() error {
	_, err := fmt.Fprint(p.output, "\r\x1b[2K")
	return err
}

func formatBytes(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%d B", size)
	}
	value := float64(size)
	for _, unit := range []string{"KiB", "MiB", "GiB", "TiB", "PiB", "EiB"} {
		value /= 1024
		if value < 1024 || unit == "EiB" {
			return fmt.Sprintf("%.1f %s", value, unit)
		}
	}
	return fmt.Sprintf("%d B", size)
}

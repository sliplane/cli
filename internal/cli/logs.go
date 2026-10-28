package cli

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/sliplane/cli/internal/api"
	"github.com/spf13/cobra"
)

const logPageLimit = 500 // Public API maximum; a full page may hide additional lines.

type logKey struct {
	timestamp time.Time
	message   string
}

type logCursor struct {
	latest time.Time
	seen   map[logKey]int
}

func (o *options) logsCommand() *cobra.Command {
	var follow bool
	var since string
	var interval time.Duration
	command := &cobra.Command{
		Use: "logs <name-or-id>", Aliases: []string{"log"}, Short: "Read service logs; --follow polls for new lines", Args: validateArguments,
		Example: "  sliplane logs api --follow\n  sliplane logs api --since 30m -o json",
		RunE: func(command *cobra.Command, args []string) error {
			if interval < time.Second {
				return errors.New("--interval must be at least 1s")
			}
			from, err := parseSince(since, time.Now())
			if err != nil {
				return err
			}
			client, _, err := o.client(command)
			if err != nil {
				return err
			}
			service, err := o.resolveService(command.Context(), client, args[0])
			if err != nil {
				return err
			}
			return o.readLogs(command, client, service, from, follow, interval)
		},
	}
	command.Flags().BoolVarP(&follow, "follow", "f", false, "Poll for new logs until interrupted")
	command.Flags().StringVar(&since, "since", "", "Start time as RFC3339 or lookback duration (e.g. 30m)")
	command.Flags().DurationVar(&interval, "interval", 2*time.Second, "Polling interval with --follow (minimum 1s)")
	return command
}

func parseSince(value string, now time.Time) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}
	if duration, err := time.ParseDuration(value); err == nil {
		if duration <= 0 {
			return nil, errors.New("--since duration must be positive")
		}
		timestamp := now.Add(-duration)
		return &timestamp, nil
	}
	timestamp, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return nil, errors.New("--since must be a positive duration (e.g. 30m) or RFC3339 timestamp")
	}
	return &timestamp, nil
}

// readLogs writes an array for snapshots and individual JSON objects while following.
func (o *options) readLogs(command *cobra.Command, client *api.Client, service api.Service, from *time.Time, follow bool, interval time.Duration) error {
	cursor := logCursor{seen: map[logKey]int{}}
	for {
		logs, err := client.Logs(command.Context(), service.ProjectID, service.ID, from)
		if err != nil {
			return err
		}
		sort.SliceStable(logs, func(i, j int) bool { return logs[i].CreatedAt.Before(logs[j].CreatedAt) })
		if len(logs) >= logPageLimit {
			fmt.Fprintln(command.ErrOrStderr(), "Warning: Reached the 500-line limit; additional logs may be missing.")
		}
		if !follow {
			return o.printLogs(command, logs)
		}
		fresh := cursor.advance(logs)
		for _, line := range fresh {
			if o.output == "json" {
				if err := writeJSON(command.OutOrStdout(), line); err != nil {
					return err
				}
			} else if err := printLog(command, line); err != nil {
				return err
			}
		}
		if !cursor.latest.IsZero() {
			// A one-second overlap covers the API's coarse timestamp boundary.
			overlap := time.Unix(cursor.latest.Unix()-1, 0)
			from = &overlap
		}
		timer := time.NewTimer(interval)
		select {
		case <-command.Context().Done():
			timer.Stop()
			return command.Context().Err()
		case <-timer.C:
		}
	}
}

// advance preserves repeated identical entries within a batch while removing poll overlap.
func (c *logCursor) advance(logs []api.Log) []api.Log {
	fresh := []api.Log{}
	counts := map[logKey]int{}
	for _, line := range logs {
		key := logKey{line.CreatedAt.UTC(), line.Message}
		counts[key]++
		if counts[key] > c.seen[key] {
			fresh = append(fresh, line)
		}
		if line.CreatedAt.After(c.latest) {
			c.latest = line.CreatedAt
		}
	}
	for key, count := range counts {
		if count > c.seen[key] {
			c.seen[key] = count
		}
	}
	cutoff := time.Unix(c.latest.Unix()-1, 0)
	for key := range c.seen {
		if key.timestamp.Before(cutoff) {
			delete(c.seen, key)
		}
	}
	return fresh
}

func (o *options) printLogs(command *cobra.Command, logs []api.Log) error {
	if o.output == "json" {
		return writeJSON(command.OutOrStdout(), logs)
	}
	for _, line := range logs {
		if err := printLog(command, line); err != nil {
			return err
		}
	}
	return nil
}

func printLog(command *cobra.Command, line api.Log) error {
	_, err := fmt.Fprintf(command.OutOrStdout(), "%s  %s\n", formatLocalTime(line.CreatedAt), terminalText(line.Message))
	return err
}

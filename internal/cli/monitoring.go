package cli

import (
	"context"
	"errors"
	"net/url"
	"strconv"

	"github.com/spf13/cobra"
	"sliplane-cli/internal/api"
)

func (o *options) metricsCommand(kind string) *cobra.Command {
	var period string
	var watch bool
	var from, to int64
	command := &cobra.Command{
		Use: "metrics <name-or-id>", Aliases: []string{"metric"}, Short: "Show " + kind + " CPU and memory usage", Args: validateArguments,
		RunE: func(command *cobra.Command, args []string) error {
			if watch && (command.Flags().Changed("from") || command.Flags().Changed("to")) {
				return errors.New("--watch uses a rolling --range; omit --from and --to")
			}
			if watch && !o.canPrompt(command) {
				return errors.New("--watch requires an interactive terminal with text output")
			}
			query := url.Values{}
			if command.Flags().Changed("from") || command.Flags().Changed("to") {
				if command.Flags().Changed("range") || !command.Flags().Changed("from") || !command.Flags().Changed("to") || from <= 0 || to <= from {
					return errors.New("provide --from and --to as increasing Unix seconds, without --range")
				}
				query.Set("from", strconv.FormatInt(from, 10))
				query.Set("to", strconv.FormatInt(to, 10))
			} else {
				switch period {
				case "10min", "1h", "24h", "7d":
				default:
					return errors.New("--range must be 10min, 1h, 24h, or 7d")
				}
				query.Set("range", period)
			}
			client, _, err := o.client(command)
			if err != nil {
				return err
			}
			var fetch func(context.Context) ([]api.Metric, error)
			name := args[0]
			if kind == "service" {
				service, err := o.resolveService(command.Context(), client, args[0])
				if err != nil {
					return err
				}
				name = service.Name
				fetch = func(ctx context.Context) ([]api.Metric, error) {
					return client.ServiceMetrics(ctx, service.ProjectID, service.ID, query)
				}
			} else {
				server, err := o.selectServer(command, client, args[0])
				if err != nil {
					return err
				}
				name = server.name
				fetch = func(ctx context.Context) ([]api.Metric, error) {
					return client.ServerMetrics(ctx, server.id, query)
				}
			}
			title := kind + " · " + name
			if watch {
				return watchMetrics(command, title, period, fetch)
			}
			metrics, err := fetch(command.Context())
			if err != nil {
				return err
			}
			if o.output == "json" {
				return writeJSON(command.OutOrStdout(), metrics)
			}
			return printMetrics(command.OutOrStdout(), title, metrics)
		},
	}
	command.Flags().StringVar(&period, "range", "1h", "Time range: 10min, 1h, 24h, or 7d")
	command.Flags().BoolVarP(&watch, "watch", "w", false, "Refresh charts every 5s (q or Ctrl+C to exit)")
	command.Flags().Int64Var(&from, "from", 0, "Start time in Unix seconds (requires --to)")
	command.Flags().Int64Var(&to, "to", 0, "End time in Unix seconds (requires --from)")
	return command
}

func (o *options) serviceEventsCommand() *cobra.Command {
	return &cobra.Command{
		Use: "events <service>", Aliases: []string{"event"}, Short: "Show deployment and system events, newest first", Args: validateArguments,
		RunE: func(command *cobra.Command, args []string) error {
			client, _, err := o.client(command)
			if err != nil {
				return err
			}
			service, err := o.resolveService(command.Context(), client, args[0])
			if err != nil {
				return err
			}
			events, err := client.ServiceEvents(command.Context(), service.ProjectID, service.ID)
			if err != nil {
				return err
			}
			rows := [][]string{{"TIME", "TYPE", "TRIGGER", "MESSAGE"}}
			for _, event := range events {
				rows = append(rows, []string{formatTimestamp(event.CreatedAt), event.Type, event.Trigger, event.Message})
			}
			return o.print(command, events, rows)
		},
	}
}

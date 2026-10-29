package cli

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"

	"github.com/sliplane/cli/internal/api"
	"github.com/spf13/cobra"
)

// metricsCommand serves services, servers, or both when kind is empty.
func (o *options) metricsCommand(kind string) *cobra.Command {
	short := "Show " + kind + " CPU and memory usage"
	if kind == "" {
		short = "Show service or server CPU and memory usage"
	}
	var period string
	var watch bool
	var from, to int64
	command := &cobra.Command{
		Use: "metrics <name-or-id>", Aliases: []string{"metric"}, Short: short, Args: validateArguments,
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
			title, fetch, err := o.metricsSource(command, client, kind, args[0], query)
			if err != nil {
				return err
			}
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

type metricsFetch func(context.Context) ([]api.Metric, error)

// metricsSource resolves the target; without a kind, a name must match exactly one service or server.
func (o *options) metricsSource(command *cobra.Command, client *api.Client, kind, query string, values url.Values) (string, metricsFetch, error) {
	serviceSource := func(service api.Service) (string, metricsFetch, error) {
		return "service · " + service.Name, func(ctx context.Context) ([]api.Metric, error) {
			return client.ServiceMetrics(ctx, service.ProjectID, service.ID, values)
		}, nil
	}
	serverSource := func(server choice) (string, metricsFetch, error) {
		return "server · " + server.name, func(ctx context.Context) ([]api.Metric, error) {
			return client.ServerMetrics(ctx, server.id, values)
		}, nil
	}
	switch kind {
	case "service":
		service, err := o.resolveService(command.Context(), client, query)
		if err != nil {
			return "", nil, err
		}
		return serviceSource(service)
	case "server":
		server, err := o.selectServer(command, client, query)
		if err != nil {
			return "", nil, err
		}
		return serverSource(server)
	}
	service, serviceErr := o.resolveService(command.Context(), client, query)
	server, serverErr := o.selectServer(command, client, query)
	switch {
	case serviceErr == nil && serverErr == nil:
		return "", nil, fmt.Errorf("%q matches both a service and a server; use \"sliplane service metrics\" or \"sliplane server metrics\"", query)
	case serviceErr == nil && errors.Is(serverErr, errNotFound):
		return serviceSource(service)
	case serverErr == nil && errors.Is(serviceErr, errNotFound):
		return serverSource(server)
	case errors.Is(serviceErr, errNotFound) && errors.Is(serverErr, errNotFound):
		return "", nil, fmt.Errorf("no service or server named %q", query)
	case serviceErr != nil && !errors.Is(serviceErr, errNotFound):
		return "", nil, serviceErr
	}
	return "", nil, serverErr
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

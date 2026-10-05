package cli

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"

	"github.com/spf13/cobra"
	"sliplane-cli/internal/api"
)

func (o *options) metricsCommand(kind string) *cobra.Command {
	var period string
	var from, to int64
	command := &cobra.Command{
		Use: "metrics <name-or-id>", Aliases: []string{"metric"}, Short: "Show " + kind + " CPU and memory usage", Args: validateArguments,
		RunE: func(command *cobra.Command, args []string) error {
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
			var metrics []api.Metric
			if kind == "service" {
				service, err := o.resolveService(command.Context(), client, args[0])
				if err != nil {
					return err
				}
				metrics, err = client.ServiceMetrics(command.Context(), service.ProjectID, service.ID, query)
				if err != nil {
					return err
				}
			} else {
				server, err := o.selectServer(command, client, args[0])
				if err != nil {
					return err
				}
				metrics, err = client.ServerMetrics(command.Context(), server.id, query)
				if err != nil {
					return err
				}
			}
			rows := [][]string{{"TIME", "CPU %", "USED MB", "FREE MB", "TOTAL MB"}}
			for _, metric := range metrics {
				rows = append(rows, []string{formatTimestamp(metric.CreatedAt), fmt.Sprintf("%.2f", metric.CPUUsage), fmt.Sprintf("%.2f", metric.UsedMemory), fmt.Sprintf("%.2f", metric.FreeMemory), fmt.Sprintf("%.2f", metric.TotalMemory)})
			}
			return o.print(command, metrics, rows)
		},
	}
	command.Flags().StringVar(&period, "range", "1h", "Time range: 10min, 1h, 24h, or 7d")
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

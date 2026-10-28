// CORS flags cover a single common browser rule; JSON files support multiple rules.
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/sliplane/cli/internal/api"
	"github.com/spf13/cobra"
)

func (o *options) s3CORSCommand() *cobra.Command {
	command := &cobra.Command{Use: "cors", Short: "Manage bucket cross-origin access"}
	command.AddCommand(&cobra.Command{Use: "show <bucket>", Short: "Show bucket CORS rules", Args: validateArguments,
		RunE: func(command *cobra.Command, args []string) error {
			client, bucket, err := o.s3Access(command, args[0])
			if err != nil {
				return err
			}
			cors, err := client.BucketCORS(command.Context(), bucket.ID)
			if err != nil {
				return err
			}
			return o.printBucketCORS(command, cors)
		},
	}, o.s3CORSSetCommand(), &cobra.Command{Use: "delete <bucket>", Short: "Remove all CORS rules (requires a read-write token)", Args: validateArguments,
		RunE: func(command *cobra.Command, args []string) error {
			client, bucket, err := o.s3Access(command, args[0])
			if err != nil {
				return err
			}
			if err := client.DeleteBucketCORS(command.Context(), bucket.ID); err != nil {
				return err
			}
			return o.printBucketCORS(command, api.BucketCORS{Rules: []api.BucketCORSRule{}})
		},
	})
	return command
}

func (o *options) s3CORSSetCommand() *cobra.Command {
	var rule api.BucketCORSRule
	var filename string
	var maxAge int
	command := &cobra.Command{Use: "set <bucket>", Short: "Replace all CORS rules (requires a read-write token)", Args: validateArguments,
		Long:    "Replace all existing CORS rules. Supply --origin for a single rule, or --file for a JSON configuration.\nThe default methods are GET and HEAD. Add PUT to allow browser uploads.\nJSON files have the form {\"rules\":[{\"allowedOrigins\":[\"https://app.example.com\"],\"allowedMethods\":[\"GET\",\"HEAD\"]}]}.",
		Example: "  sliplane s3 cors set uploads --origin https://app.example.com\n  sliplane s3 cors set uploads --origin https://app.example.com --method GET,HEAD,PUT --header '*' --expose-header ETag\n  sliplane s3 cors set uploads --file cors.json",
		RunE: func(command *cobra.Command, args []string) error {
			rule.MaxAgeSeconds = &maxAge
			cors := api.BucketCORS{Rules: []api.BucketCORSRule{rule}}
			if filename != "" {
				for _, flag := range []string{"origin", "method", "header", "expose-header", "max-age"} {
					if command.Flags().Changed(flag) {
						return fmt.Errorf("--file cannot be combined with --%s", flag)
					}
				}
				content, err := os.ReadFile(filename)
				if err != nil {
					return fmt.Errorf("read CORS file: %w", err)
				}
				cors = api.BucketCORS{}
				if err := json.Unmarshal(content, &cors); err != nil {
					return fmt.Errorf("decode CORS file: %w", err)
				}
			}
			if err := validateBucketCORS(cors); err != nil {
				return err
			}
			client, bucket, err := o.s3Access(command, args[0])
			if err != nil {
				return err
			}
			cors, err = client.SetBucketCORS(command.Context(), bucket.ID, cors)
			if err != nil {
				return err
			}
			return o.printBucketCORS(command, cors)
		},
	}
	command.Flags().StringSliceVar(&rule.AllowedOrigins, "origin", nil, "Allowed origin (repeat or comma-separate; use '*' for all origins)")
	command.Flags().StringSliceVar(&rule.AllowedMethods, "method", []string{"GET", "HEAD"}, "Allowed HTTP methods (repeat or comma-separate)")
	command.Flags().StringSliceVar(&rule.AllowedHeaders, "header", nil, "Allowed request headers (repeat or comma-separate)")
	command.Flags().StringSliceVar(&rule.ExposeHeaders, "expose-header", nil, "Response headers browsers may read (repeat or comma-separate)")
	command.Flags().IntVar(&maxAge, "max-age", 3600, "Preflight cache lifetime in seconds (0–86400)")
	command.Flags().StringVar(&filename, "file", "", "JSON configuration file for one or more rules")
	return command
}

// validateBucketCORS catches missing input and unsupported methods before accessing credentials.
func validateBucketCORS(cors api.BucketCORS) error {
	if len(cors.Rules) < 1 || len(cors.Rules) > 10 {
		return errors.New("CORS requires 1–10 rules; use cors delete to remove all rules")
	}
	for index, rule := range cors.Rules {
		if len(rule.AllowedOrigins) == 0 {
			return fmt.Errorf("CORS rule %d requires an allowed origin; supply --origin or --file", index+1)
		}
		for _, origin := range rule.AllowedOrigins {
			if strings.TrimSpace(origin) == "" {
				return fmt.Errorf("CORS rule %d has an empty origin", index+1)
			}
		}
		if len(rule.AllowedMethods) == 0 {
			return fmt.Errorf("CORS rule %d requires at least one method", index+1)
		}
		for _, method := range rule.AllowedMethods {
			switch strings.ToUpper(method) {
			case "GET", "HEAD", "PUT", "POST", "DELETE":
			default:
				return fmt.Errorf("unsupported CORS method %q; use GET, HEAD, PUT, POST, or DELETE", method)
			}
		}
		if rule.MaxAgeSeconds != nil && (*rule.MaxAgeSeconds < 0 || *rule.MaxAgeSeconds > 86400) {
			return errors.New("CORS max age must be between 0 and 86400 seconds")
		}
	}
	return nil
}

func (o *options) printBucketCORS(command *cobra.Command, cors api.BucketCORS) error {
	rows := [][]string{{"ORIGINS", "METHODS", "HEADERS", "EXPOSE HEADERS", "MAX AGE"}}
	for _, rule := range cors.Rules {
		maxAge := ""
		if rule.MaxAgeSeconds != nil {
			maxAge = strconv.Itoa(*rule.MaxAgeSeconds)
		}
		rows = append(rows, []string{strings.Join(rule.AllowedOrigins, ", "), strings.Join(rule.AllowedMethods, ", "), strings.Join(rule.AllowedHeaders, ", "), strings.Join(rule.ExposeHeaders, ", "), maxAge})
	}
	return o.print(command, cors, rows)
}

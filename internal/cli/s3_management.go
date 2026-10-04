// Bucket lifecycle commands use the control API and retain its deletion grace period.
package cli

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/spf13/cobra"
)

func (o *options) s3DeleteCommand() *cobra.Command {
	var yes bool
	command := &cobra.Command{Use: "delete <bucket>", Short: "Schedule bucket deletion in 24 hours (requires a read-write token)", Args: validateArguments,
		Long: "Schedule the bucket and all its access keys for deletion after a 24-hour grace period.\nRequires confirmation in a terminal, or --yes for noninteractive use.",
		RunE: func(command *cobra.Command, args []string) error {
			client, bucket, err := o.s3Access(command, args[0])
			if err != nil {
				return err
			}
			if !yes {
				if !o.canPrompt(command) {
					return errors.New("bucket deletion requires confirmation; use --yes or rerun in a terminal")
				}
				approved, err := confirm(command.Context(), command.InOrStdin(), command.ErrOrStderr(), fmt.Sprintf("Schedule bucket %q and all its access keys for deletion in 24 hours?", bucket.Name))
				if err != nil {
					return err
				}
				if !approved {
					return errors.New("bucket deletion canceled")
				}
			}
			deletion, err := client.DeleteBucket(command.Context(), bucket.ID)
			if err != nil {
				return err
			}
			return o.print(command, deletion, [][]string{{"Bucket", bucket.Name}, {"Status", deletion.Message}, {"Deletes at", deletion.DeletesAt.Format(time.RFC3339)}})
		},
	}
	command.Flags().BoolVarP(&yes, "yes", "y", false, "Confirm scheduled bucket deletion")
	return command
}

func (o *options) s3UpdateCommand() *cobra.Command {
	var versioning bool
	command := &cobra.Command{Use: "update <bucket>", Short: "Change bucket versioning (requires a read-write token)", Args: validateArguments,
		Example: "  sliplane s3 update uploads --versioning\n  sliplane s3 update uploads --versioning=false",
		RunE: func(command *cobra.Command, args []string) error {
			if !command.Flags().Changed("versioning") {
				return errors.New("provide --versioning or --versioning=false")
			}
			client, bucket, err := o.s3Access(command, args[0])
			if err != nil {
				return err
			}
			if bucket.ObjectLocking && !versioning {
				return errors.New("cannot disable versioning on a bucket with object locking enabled")
			}
			bucket, err = client.SetBucketVersioning(command.Context(), bucket.ID, versioning)
			if err != nil {
				return err
			}
			return o.print(command, bucket, [][]string{{"Bucket", bucket.Name}, {"Versioning", strconv.FormatBool(bucket.Versioning)}})
		},
	}
	command.Flags().BoolVar(&versioning, "versioning", false, "Keep every version of each object (use =false to disable)")
	return command
}

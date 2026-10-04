package cli

import (
	"errors"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"sliplane-cli/internal/api"
)

func (o *options) s3Command() *cobra.Command {
	command := &cobra.Command{Use: "s3", Short: "Manage buckets and transfer files"}
	command.AddCommand(&cobra.Command{
		Use: "show <name-or-id>", Short: "Show an S3 bucket", Args: validateArguments,
		RunE: func(command *cobra.Command, args []string) error {
			client, _, err := o.client(command)
			if err != nil {
				return err
			}
			buckets, err := client.Buckets(command.Context())
			if err != nil {
				return err
			}
			choices := make([]choice, len(buckets))
			for index, bucket := range buckets {
				choices[index] = choice{bucket.ID, bucket.Name}
			}
			index, err := resolve("bucket", args[0], choices)
			if err != nil {
				return err
			}
			bucket := buckets[index]
			return o.print(command, bucket, [][]string{{"Name", bucket.Name}, {"Status", bucket.Status}, {"Region", bucket.Region}, {"Endpoint", bucket.Endpoint}, {"Versioning", strconv.FormatBool(bucket.Versioning)}, {"Object locking", strconv.FormatBool(bucket.ObjectLocking)}, {"Created", formatTimestamp(bucket.CreatedAt)}, {"ID", bucket.ID}})
		},
	})
	defaultRegion := os.Getenv("SLIPLANE_S3_REGION")
	if defaultRegion == "" {
		defaultRegion = "us-east-1"
	}
	command.PersistentFlags().StringVar(&o.s3Region, "region", defaultRegion, "S3 signing region")
	command.AddCommand(o.s3DeleteCommand(), o.s3UpdateCommand(), o.s3CORSCommand(), o.s3CreateCommand(), o.s3KeyCommand(), o.s3ListCommand(), o.s3UploadCommand(), o.s3DownloadCommand())
	return command
}

func (o *options) s3CreateCommand() *cobra.Command {
	var input api.CreateBucketInput
	command := &cobra.Command{Use: "create <bucket-name>", Short: "Create an S3 bucket (requires a read-write token)", Args: validateArguments, Example: "  sliplane s3 create my-bucket", RunE: func(command *cobra.Command, args []string) error {
		if strings.TrimSpace(args[0]) == "" {
			return errors.New("bucket name must not be empty")
		}
		if input.Region != "ger" && input.Region != "us-east" {
			return errors.New("--region must be ger (Frankfurt) or us-east (New York)")
		}
		if input.ObjectLocking {
			if command.Flags().Changed("versioning") && !input.Versioning {
				return errors.New("object locking requires versioning; remove --versioning=false")
			}
			input.Versioning = true
		}
		input.Name = args[0]
		client, _, err := o.client(command)
		if err != nil {
			return err
		}
		bucket, err := client.CreateBucket(command.Context(), input)
		if err != nil {
			return err
		}
		return o.print(command, bucket, [][]string{{"Name", bucket.Name}, {"Status", bucket.Status}, {"Region", bucket.Region}, {"Endpoint", bucket.Endpoint}, {"Versioning", strconv.FormatBool(bucket.Versioning)}, {"Object locking", strconv.FormatBool(bucket.ObjectLocking)}})
	}}
	// Creation uses the bucket's location, independently of the inherited S3 signing region.
	command.Flags().StringVar(&input.Region, "region", "ger", "Bucket region: ger (Frankfurt) or us-east (New York)")
	command.Flags().BoolVar(&input.Versioning, "versioning", false, "Keep every version of each object")
	command.Flags().BoolVar(&input.ObjectLocking, "object-lock", false, "Enable Object Lock retention support (also enables versioning)")
	return command
}

func (o *options) listBuckets(command *cobra.Command) error {
	all, err := command.Flags().GetBool("all")
	if err != nil {
		return err
	}
	client, _, err := o.client(command)
	if err != nil {
		return err
	}
	buckets, err := client.Buckets(command.Context())
	if err != nil {
		return err
	}
	sort.Slice(buckets, func(i, j int) bool {
		if buckets[i].Name != buckets[j].Name {
			return buckets[i].Name < buckets[j].Name
		}
		return buckets[i].ID < buckets[j].ID
	})
	rows := [][]string{{"NAME", "STATUS", "REGION", "ENDPOINT"}}
	if all {
		rows[0] = append(rows[0], "ID", "VERSIONING", "OBJECT LOCKING", "CREATED BY", "CREATED")
	}
	for _, bucket := range buckets {
		row := []string{bucket.Name, bucket.Status, bucket.Region, bucket.Endpoint}
		if all {
			row = append(row, bucket.ID, strconv.FormatBool(bucket.Versioning), strconv.FormatBool(bucket.ObjectLocking), bucket.CreatedByUserID, formatTimestamp(bucket.CreatedAt))
		}
		rows = append(rows, row)
	}
	return o.print(command, buckets, rows)
}

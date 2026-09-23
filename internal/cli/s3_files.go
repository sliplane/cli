// File commands leave object keys intact and commit downloads only after a complete transfer.
package cli

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"sliplane-cli/internal/objectstore"
)

// s3ListCommand lists buckets or browses files when a bucket is supplied.
func (o *options) s3ListCommand() *cobra.Command {
	var recursive bool
	command := &cobra.Command{Use: "list [bucket] [prefix]", Aliases: []string{"ls"}, Short: "List buckets or files in a bucket", Args: validateArguments, RunE: func(command *cobra.Command, args []string) error {
		if len(args) == 0 {
			if recursive {
				return errors.New("specify a bucket when using --recursive")
			}
			return o.listBuckets(command)
		}
		apiClient, bucket, err := o.s3Access(command, args[0])
		if err != nil {
			return err
		}
		client, err := o.objectClient(command, apiClient, bucket)
		if err != nil {
			return err
		}
		prefix := ""
		if len(args) == 2 {
			prefix = args[1]
		}
		delimiter := "/"
		if recursive {
			delimiter = ""
		}
		entries, err := client.List(command.Context(), bucket.Name, prefix, delimiter)
		if err != nil {
			return err
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Key < entries[j].Key })
		rows := [][]string{{"KEY", "SIZE", "MODIFIED"}}
		for _, entry := range entries {
			size := strconv.FormatInt(entry.Size, 10) + " B"
			if entry.Type == "directory" {
				size = "-"
			}
			modified := ""
			if entry.LastModified != nil {
				modified = entry.LastModified.UTC().Format(time.RFC3339)
			}
			rows = append(rows, []string{entry.Key, size, modified})
		}
		return o.print(command, entries, rows)
	}}
	command.Flags().BoolVarP(&recursive, "recursive", "r", false, "Include files in all subdirectories")
	return command
}

// s3UploadCommand uploads one regular file, protecting existing objects unless --force is supplied.
func (o *options) s3UploadCommand() *cobra.Command {
	var force bool
	command := &cobra.Command{Use: "upload <bucket> <file> [key]", Short: "Upload a file to a bucket", Args: validateArguments, RunE: func(command *cobra.Command, args []string) error {
		file, err := os.Open(args[1])
		if err != nil {
			return fmt.Errorf("open upload file: %w", err)
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("upload source must be a regular file")
		}
		if info.Size() > objectstore.MaxUploadSize {
			return errors.New("uploads currently support files up to 5 GiB")
		}
		key := filepath.Base(args[1])
		if len(args) == 3 {
			key = args[2]
		}
		if key == "" {
			return errors.New("object key must not be empty")
		}
		apiClient, bucket, err := o.s3Access(command, args[0])
		if err != nil {
			return err
		}
		if err := o.requireS3Write(); err != nil {
			return err
		}
		client, err := o.objectClient(command, apiClient, bucket)
		if err != nil {
			return err
		}
		if !force {
			exists, err := client.Exists(command.Context(), bucket.Name, key)
			if err != nil {
				return err
			}
			if exists {
				return errors.New("object already exists; use --force to replace it")
			}
		}
		result, err := client.Upload(command.Context(), bucket.Name, key, file)
		if err != nil {
			return err
		}
		return o.print(command, result, [][]string{{"Uploaded", result.Path}, {"Bucket", result.Bucket}, {"Key", result.Key}, {"Size", strconv.FormatInt(result.Size, 10) + " B"}})
	}}
	command.Flags().BoolVar(&force, "force", false, "Replace an existing object")
	return command
}

// s3DownloadCommand writes through a private temporary file so interrupted downloads leave no partial result.
func (o *options) s3DownloadCommand() *cobra.Command {
	var force bool
	command := &cobra.Command{Use: "download <bucket> <key> [file]", Short: "Download a file from a bucket", Args: validateArguments, RunE: func(command *cobra.Command, args []string) error {
		if args[1] == "" {
			return errors.New("object key must not be empty")
		}
		destination := path.Base(strings.ReplaceAll(args[1], "\\", "/"))
		if destination == "." || destination == ".." || destination == "/" {
			return errors.New("specify a local filename for this object")
		}
		if len(args) == 3 {
			destination = args[2]
		}
		if destination == "-" {
			return errors.New("specify a local filename for the download")
		}
		info, err := os.Stat(destination)
		if err == nil && info.IsDir() {
			destination = filepath.Join(destination, path.Base(strings.ReplaceAll(args[1], "\\", "/")))
		}
		if !force {
			if _, err := os.Lstat(destination); err == nil {
				return errors.New("destination already exists; use --force to replace it")
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		file, err := os.CreateTemp(filepath.Dir(destination), ".sliplane-download-*")
		if err != nil {
			return fmt.Errorf("create download file: %w", err)
		}
		defer os.Remove(file.Name())
		defer file.Close()
		apiClient, bucket, err := o.s3Access(command, args[0])
		if err != nil {
			return err
		}
		client, err := o.objectClient(command, apiClient, bucket)
		if err != nil {
			return err
		}
		var progress *downloadProgress
		var reportProgress func(int64, int64) error
		if o.canShowProgress(command) {
			progress = &downloadProgress{output: command.ErrOrStderr()}
			reportProgress = progress.update
			if err := progress.update(0, -1); err != nil {
				return err
			}
		}
		size, err := client.Download(command.Context(), bucket.Name, args[1], file, reportProgress)
		if progress != nil {
			err = errors.Join(err, progress.finish())
		}
		if err != nil {
			return err
		}
		if err := file.Close(); err != nil {
			return fmt.Errorf("finish download: %w", err)
		}
		if force {
			err = os.Rename(file.Name(), destination)
		} else {
			err = os.Link(file.Name(), destination)
		}
		if err != nil {
			return fmt.Errorf("save download: %w", err)
		}
		return o.print(command, objectstore.Transfer{Bucket: bucket.Name, Key: args[1], Path: destination, Size: size}, [][]string{{"Downloaded", destination}, {"Size", strconv.FormatInt(size, 10) + " B"}})
	}}
	command.Flags().BoolVar(&force, "force", false, "Replace an existing local file")
	return command
}

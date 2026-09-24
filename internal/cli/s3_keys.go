// Bucket key commands manage remote credentials; automatic file access setup remains internal.
package cli

import (
	"errors"
	"fmt"
	"maps"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"sliplane-cli/internal/api"
	"sliplane-cli/internal/auth"
	"sliplane-cli/internal/config"
)

// s3KeyCommand exposes the key operations supported by the bucket API.
func (o *options) s3KeyCommand() *cobra.Command {
	command := &cobra.Command{Use: "key", Short: "List, create, and delete bucket access keys"}
	command.AddCommand(o.s3KeyListCommand(), o.s3KeyCreateCommand(), o.s3KeyDeleteCommand())
	return command
}

// s3KeyListCommand lists key metadata without loading or exposing saved secrets.
func (o *options) s3KeyListCommand() *cobra.Command {
	return &cobra.Command{Use: "list <bucket>", Short: "List bucket access keys", Args: validateArguments, RunE: func(command *cobra.Command, args []string) error {
		client, bucket, err := o.s3Access(command, args[0])
		if err != nil {
			return err
		}
		keys, err := client.BucketKeys(command.Context(), bucket.ID)
		if err != nil {
			return err
		}
		sort.Slice(keys, func(i, j int) bool {
			if keys[i].Name != keys[j].Name {
				return keys[i].Name < keys[j].Name
			}
			return keys[i].ID < keys[j].ID
		})
		rows := [][]string{{"NAME", "ID", "ACCESS KEY ID", "CREATED"}}
		for _, key := range keys {
			rows = append(rows, []string{key.Name, key.ID, key.AccessKeyID, formatTimestamp(key.CreatedAt)})
		}
		return o.print(command, keys, rows)
	}}
}

// s3KeyCreateCommand returns the one-time secret for use in other clients, without changing the CLI's saved key.
func (o *options) s3KeyCreateCommand() *cobra.Command {
	return &cobra.Command{
		Use: "create <bucket> <name>", Short: "Create a bucket key and show its credentials once (requires a read-write token)",
		Long: "Create a named bucket access key. The secret is returned only at creation; save it securely. This does not replace the CLI's saved credentials.",
		Args: validateArguments, Example: "  sliplane s3 key create uploads ci -o json",
		RunE: func(command *cobra.Command, args []string) error {
			if strings.TrimSpace(args[1]) == "" {
				return errors.New("key name must not be empty")
			}
			client, bucket, err := o.s3Access(command, args[0])
			if err != nil {
				return err
			}
			if err := o.requireS3Write(); err != nil {
				return err
			}
			key, err := createBucketKey(command, client, bucket.ID, args[1])
			if err != nil {
				return err
			}
			return o.print(command, key, [][]string{{"Name", key.Name}, {"ID", key.ID}, {"Access key ID", key.AccessKeyID}, {"Secret access key (shown once)", key.SecretAccessKey}})
		},
	}
}

// s3KeyDeleteCommand resolves an exact name or ID before revoking only the selected key.
func (o *options) s3KeyDeleteCommand() *cobra.Command {
	return &cobra.Command{Use: "delete <bucket> <name-or-id>", Short: "Revoke a bucket access key (requires a read-write token)", Args: validateArguments, RunE: func(command *cobra.Command, args []string) error {
		client, bucket, err := o.s3Access(command, args[0])
		if err != nil {
			return err
		}
		if err := o.requireS3Write(); err != nil {
			return err
		}
		keys, err := client.BucketKeys(command.Context(), bucket.ID)
		if err != nil {
			return err
		}
		choices := make([]choice, len(keys))
		for index, key := range keys {
			choices[index] = choice{key.ID, key.Name}
		}
		index, err := resolve("bucket key", args[1], choices)
		if err != nil {
			return err
		}
		key := keys[index]
		err = client.DeleteBucketKey(command.Context(), bucket.ID, key.ID)
		var apiError *api.Error
		if err != nil && (!errors.As(err, &apiError) || apiError.Status != 404) {
			return err
		}
		if err := o.forgetBucketKey(bucket.ID, key.ID); err != nil {
			return fmt.Errorf("key %q was revoked, but its saved credentials could not be removed: %w", key.ID, err)
		}
		return o.print(command, map[string]any{"bucket": bucket.Name, "keyId": key.ID, "deleted": true}, [][]string{{"Key deleted", key.Name}, {"ID", key.ID}, {"Bucket", bucket.Name}})
	}}
}

// forgetBucketKey clears only a matching saved key in the selected profile.
func (o *options) forgetBucketKey(bucketID, keyID string) error {
	profile := o.preferences.Profiles[o.profileName]
	reference := profile.S3Credentials[bucketID]
	if reference == "" {
		return nil
	}
	credentials, err := o.savedS3Credentials(bucketID)
	if err != nil {
		return err
	}
	if credentials.KeyID != keyID {
		return nil
	}
	profile.S3Credentials = maps.Clone(profile.S3Credentials)
	delete(profile.S3Credentials, bucketID)
	preferences := o.preferences
	preferences.Profiles = maps.Clone(preferences.Profiles)
	preferences.Profiles[o.profileName] = profile
	if err := config.Save(o.configPath, preferences); err != nil {
		return err
	}
	o.preferences = preferences
	return auth.Delete(reference)
}

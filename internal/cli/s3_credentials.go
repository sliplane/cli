package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"time"

	"github.com/spf13/cobra"
	"sliplane-cli/internal/api"
	"sliplane-cli/internal/auth"
	"sliplane-cli/internal/config"
	"sliplane-cli/internal/objectstore"
)

func (o *options) s3Access(command *cobra.Command, query string) (*api.Client, api.Bucket, error) {
	client, _, err := o.client(command)
	if err != nil {
		return nil, api.Bucket{}, err
	}
	if o.identity == nil {
		if err := o.verifyIdentity(command.Context(), client); err != nil {
			return nil, api.Bucket{}, err
		}
	}
	buckets, err := client.Buckets(command.Context())
	if err != nil {
		return nil, api.Bucket{}, err
	}
	choices := make([]choice, len(buckets))
	for index, bucket := range buckets {
		choices[index] = choice{bucket.ID, bucket.Name}
	}
	index, err := resolve("bucket", query, choices)
	if err != nil {
		return nil, api.Bucket{}, err
	}
	return client, buckets[index], nil
}

// requireS3Write preserves read-only API token restrictions even when an S3 key allows writes.
func (o *options) requireS3Write() error {
	if o.identity == nil || o.identity.TokenType != "rw" {
		return errors.New("this command requires a read-write Sliplane API token")
	}
	return nil
}

// savedS3Credentials reads only the selected profile's bucket credential.
func (o *options) savedS3Credentials(bucketID string) (objectstore.Credentials, error) {
	reference := o.preferences.Profiles[o.profileName].S3Credentials[bucketID]
	if reference == "" {
		return objectstore.Credentials{}, errors.New("no saved S3 key for this bucket; run sliplane s3 ls <bucket> in a terminal to set up access, or supply SLIPLANE_S3_ACCESS_KEY_ID and SLIPLANE_S3_SECRET_ACCESS_KEY (create credentials with sliplane s3 key create <bucket> <name>)")
	}
	encoded, err := auth.Load(reference)
	if err != nil {
		return objectstore.Credentials{}, fmt.Errorf("read saved S3 credential: %w", err)
	}
	var credentials objectstore.Credentials
	if err := json.Unmarshal([]byte(encoded), &credentials); err != nil || credentials.KeyID == "" || credentials.AccessKeyID == "" || credentials.SecretAccessKey == "" {
		return objectstore.Credentials{}, errors.New("saved S3 credential is invalid")
	}
	return credentials, nil
}

func (o *options) objectClient(command *cobra.Command, client *api.Client, bucket api.Bucket) (*objectstore.Client, error) {
	accessKey, accessSet := os.LookupEnv("SLIPLANE_S3_ACCESS_KEY_ID")
	secretKey, secretSet := os.LookupEnv("SLIPLANE_S3_SECRET_ACCESS_KEY")
	credentials := objectstore.Credentials{AccessKeyID: accessKey, SecretAccessKey: secretKey}
	if accessSet || secretSet {
		if accessKey == "" || secretKey == "" {
			return nil, errors.New("set both SLIPLANE_S3_ACCESS_KEY_ID and SLIPLANE_S3_SECRET_ACCESS_KEY")
		}
	} else {
		if o.preferences.Profiles[o.profileName].S3Credentials[bucket.ID] == "" && o.canPrompt(command) {
			if o.profileName == "" {
				return nil, errors.New("select a profile with --profile before saving bucket credentials")
			}
			if err := o.requireS3Write(); err != nil {
				return nil, err
			}
			question := fmt.Sprintf("Create an S3 access key for bucket %q and save it in your keychain for profile %q?", bucket.Name, o.profileName)
			confirmed, err := confirm(command.Context(), command.InOrStdin(), command.ErrOrStderr(), question)
			if err != nil {
				return nil, err
			}
			if !confirmed {
				return nil, errors.New("bucket access setup canceled")
			}
			if err := o.connectBucket(command, client, bucket); err != nil {
				return nil, err
			}
		}
		saved, err := o.savedS3Credentials(bucket.ID)
		if err != nil {
			return nil, err
		}
		credentials = saved
	}
	return objectstore.New(bucket.Endpoint, o.s3Region, credentials)
}

// connectBucket saves a newly created bucket key, revoking it if local storage fails.
func (o *options) connectBucket(command *cobra.Command, client *api.Client, bucket api.Bucket) error {
	ctx := command.Context()
	if err := o.requireS3Write(); err != nil {
		return err
	}
	profile := o.preferences.Profiles[o.profileName]
	profile.S3Credentials = maps.Clone(profile.S3Credentials)
	if profile.S3Credentials[bucket.ID] != "" {
		return fmt.Errorf("bucket %q already has a saved key; use sliplane s3 key list and sliplane s3 key delete to manage it", bucket.Name)
	}
	random := make([]byte, 16)
	rand.Read(random)
	reference := "s3_" + hex.EncodeToString(random)
	key, err := createBucketKey(command, client, bucket.ID, "Sliplane CLI ("+o.profileName+")")
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(objectstore.Credentials{KeyID: key.ID, AccessKeyID: key.AccessKeyID, SecretAccessKey: key.SecretAccessKey})
	if err != nil {
		return errors.Join(err, revokeBucketKey(ctx, client, bucket.ID, key.ID))
	}
	if err := auth.Save(reference, string(encoded)); err != nil {
		return errors.Join(err, revokeBucketKey(ctx, client, bucket.ID, key.ID))
	}
	if profile.S3Credentials == nil {
		profile.S3Credentials = map[string]string{}
	}
	profile.S3Credentials[bucket.ID] = reference
	preferences := o.preferences
	preferences.Profiles = maps.Clone(preferences.Profiles)
	preferences.Profiles[o.profileName] = profile
	if err := config.Save(o.configPath, preferences); err != nil {
		return errors.Join(err, revokeBucketKey(ctx, client, bucket.ID, key.ID), auth.Delete(reference))
	}
	o.preferences = preferences
	objects, err := objectstore.New(bucket.Endpoint, o.s3Region, objectstore.Credentials{KeyID: key.ID, AccessKeyID: key.AccessKeyID, SecretAccessKey: key.SecretAccessKey})
	if err != nil {
		return fmt.Errorf("bucket access key was saved, but could not verify access: %w", err)
	}
	if _, err := fmt.Fprintln(command.ErrOrStderr(), "Waiting for bucket access key and permissions to become active (up to 1 minute)..."); err != nil {
		return err
	}
	ready, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	if err := objects.WaitReady(ready, bucket.Name); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("bucket access key is saved, but access could not be confirmed within 1 minute; retry your original command shortly (the key is already saved). If AccessDenied persists, contact Sliplane support to check this key's bucket permissions: %w", err)
		}
		return fmt.Errorf("bucket access key was saved, but could not verify access: %w", err)
	}
	return nil
}

// createBucketKey validates the one-time credentials and revokes an unusable key when possible.
func createBucketKey(command *cobra.Command, client *api.Client, bucketID, name string) (api.CreatedBucketKey, error) {
	key, err := client.CreateBucketKey(command.Context(), bucketID, name)
	if err != nil {
		return api.CreatedBucketKey{}, err
	}
	if key.ID == "" {
		return api.CreatedBucketKey{}, errors.New("API created a bucket key without returning its ID; inspect the bucket's access keys with sliplane s3 key list before retrying")
	}
	if key.AccessKeyID == "" || key.SecretAccessKey == "" {
		return api.CreatedBucketKey{}, errors.Join(errors.New("API returned an incomplete bucket credential"), revokeBucketKey(command.Context(), client, bucketID, key.ID))
	}
	return key, nil
}

// revokeBucketKey uses a fresh deadline so cancellation cannot prevent credential cleanup.
func revokeBucketKey(ctx context.Context, client *api.Client, bucketID, keyID string) error {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if err := client.DeleteBucketKey(cleanup, bucketID, keyID); err != nil {
		return fmt.Errorf("could not revoke bucket access key %s: %w", keyID, err)
	}
	return nil
}

package objectstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sliplane/cli/internal/buildinfo"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsmiddleware "github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

const MaxUploadSize = 5 * 1024 * 1024 * 1024 // Single-request S3 uploads are limited to 5 GiB.

type Credentials struct {
	KeyID           string `json:"keyId,omitempty"`
	AccessKeyID     string `json:"accessKeyId"`
	SecretAccessKey string `json:"secretAccessKey"`
}

type Entry struct {
	Key          string     `json:"key"`
	Type         string     `json:"type"`
	Size         int64      `json:"size"`
	LastModified *time.Time `json:"lastModified,omitempty"`
}

type Transfer struct {
	Bucket string `json:"bucket"`
	Key    string `json:"key"`
	Path   string `json:"path"`
	Size   int64  `json:"size"`
}

type Client struct {
	s3          *s3.Client
	credentials Credentials
}

func New(endpoint, region string, credentials Credentials) (*Client, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("invalid S3 endpoint")
	}
	if parsed.Scheme != "https" {
		ip := net.ParseIP(parsed.Hostname())
		if parsed.Scheme != "http" || ip == nil || !ip.IsLoopback() {
			return nil, errors.New("S3 endpoint must use HTTPS")
		}
	}
	if strings.TrimSpace(region) == "" {
		return nil, errors.New("S3 region must not be empty")
	}
	if credentials.AccessKeyID == "" || credentials.SecretAccessKey == "" {
		return nil, errors.New("both S3 access key ID and secret access key are required")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 30 * time.Second
	httpClient := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	sdk := s3.New(s3.Options{
		APIOptions: []func(*middleware.Stack) error{
			awsmiddleware.AddUserAgentKeyValue("sliplane-cli", buildinfo.Version),
		},
		BaseEndpoint: aws.String(endpoint), Region: region, UsePathStyle: true, HTTPClient: httpClient, RetryMaxAttempts: 1,
		Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
			return aws.Credentials{AccessKeyID: credentials.AccessKeyID, SecretAccessKey: credentials.SecretAccessKey, Source: "sliplane"}, nil
		}),
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
	})
	return &Client{s3: sdk, credentials: credentials}, nil
}

// requestError retains useful S3 error codes without printing signing credentials.
func (c *Client) requestError(action string, err error) error {
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	message := err.Error()
	var apiError smithy.APIError
	if errors.As(err, &apiError) {
		message = apiError.ErrorCode() + ": " + apiError.ErrorMessage()
	}
	message = strings.ReplaceAll(message, c.credentials.SecretAccessKey, "[redacted]")
	message = strings.ReplaceAll(message, c.credentials.AccessKeyID, "[redacted]")
	return fmt.Errorf("%s: %s", action, message)
}

// WaitReady probes a new key with a small read while its key and permissions propagate.
// The caller supplies a deadline; existing keys and object writes do not use this retry loop.
func (c *Client) WaitReady(ctx context.Context, bucket string) error {
	for {
		_, err := c.s3.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(bucket), MaxKeys: aws.Int32(1)})
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return errors.Join(ctx.Err(), c.requestError("verify bucket access", err))
		}
		var apiError smithy.APIError
		if !errors.As(err, &apiError) {
			return c.requestError("verify bucket access", err)
		}
		if apiError.ErrorCode() != "InvalidAccessKeyId" && apiError.ErrorCode() != "AccessDenied" {
			return c.requestError("verify bucket access", err)
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return errors.Join(ctx.Err(), c.requestError("verify bucket access", err))
		case <-timer.C:
		}
	}
}

// List follows every page; delimiter controls folder-style versus recursive listing.
func (c *Client) List(ctx context.Context, bucket, prefix, delimiter string) ([]Entry, error) {
	entries := []Entry{}
	input := &s3.ListObjectsV2Input{Bucket: aws.String(bucket), Prefix: aws.String(prefix), Delimiter: aws.String(delimiter)}
	seen := map[string]bool{}
	for {
		page, err := c.s3.ListObjectsV2(ctx, input)
		if err != nil {
			return nil, c.requestError("list objects", err)
		}
		for _, directory := range page.CommonPrefixes {
			entries = append(entries, Entry{Key: aws.ToString(directory.Prefix), Type: "directory"})
		}
		for _, object := range page.Contents {
			entries = append(entries, Entry{Key: aws.ToString(object.Key), Type: "file", Size: aws.ToInt64(object.Size), LastModified: object.LastModified})
		}
		if !aws.ToBool(page.IsTruncated) {
			return entries, nil
		}
		token := aws.ToString(page.NextContinuationToken)
		if token == "" || seen[token] {
			return nil, errors.New("S3 returned an invalid pagination token")
		}
		seen[token] = true
		input.ContinuationToken = aws.String(token)
	}
}

func (c *Client) Upload(ctx context.Context, bucket, key string, file *os.File) (Transfer, error) {
	info, err := file.Stat()
	if err != nil {
		return Transfer{}, fmt.Errorf("inspect upload file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return Transfer{}, errors.New("upload source must be a regular file")
	}
	if info.Size() > MaxUploadSize {
		return Transfer{}, errors.New("uploads currently support files up to 5 GiB")
	}
	contentType := mime.TypeByExtension(filepath.Ext(file.Name()))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	input := &s3.PutObjectInput{Bucket: aws.String(bucket), Key: aws.String(key), Body: file, ContentLength: aws.Int64(info.Size()), ContentType: aws.String(contentType)}
	if _, err := c.s3.PutObject(ctx, input); err != nil {
		return Transfer{}, c.requestError("upload object", err)
	}
	return Transfer{Bucket: bucket, Key: key, Path: file.Name(), Size: info.Size()}, nil
}

// Download streams into a caller-owned destination, leaving final file replacement to the caller.
// Progress receives bytes written and the response length (-1 when unknown); nil disables reporting.
func (c *Client) Download(ctx context.Context, bucket, key string, writer io.Writer, progress func(int64, int64) error) (int64, error) {
	response, err := c.s3.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		return 0, c.requestError("download object", err)
	}
	defer response.Body.Close()
	if progress != nil {
		total := int64(-1)
		if response.ContentLength != nil {
			total = *response.ContentLength
		}
		if err := progress(0, total); err != nil {
			return 0, fmt.Errorf("report download progress: %w", err)
		}
		writer = &downloadWriter{destination: writer, progress: progress, total: total}
	}
	size, err := io.Copy(writer, response.Body)
	if err != nil {
		return 0, c.requestError("read object body", err)
	}
	if response.ContentLength != nil && size != *response.ContentLength {
		return 0, errors.New("download size does not match the object length")
	}
	return size, nil
}

// downloadWriter reports only bytes accepted by the destination, preserving write failures.
type downloadWriter struct {
	destination io.Writer
	progress    func(int64, int64) error
	written     int64
	total       int64
}

func (w *downloadWriter) Write(data []byte) (int, error) {
	n, err := w.destination.Write(data)
	w.written += int64(n)
	if progressErr := w.progress(w.written, w.total); progressErr != nil {
		return n, errors.Join(err, fmt.Errorf("report download progress: %w", progressErr))
	}
	return n, err
}

// Exists treats only a confirmed 404 as absence; permission or transport failures stop uploads.
func (c *Client) Exists(ctx context.Context, bucket, key string) (bool, error) {
	_, err := c.s3.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err == nil {
		return true, nil
	}
	var responseError *smithyhttp.ResponseError
	if errors.As(err, &responseError) && responseError.HTTPStatusCode() == http.StatusNotFound {
		return false, nil
	}
	return false, c.requestError("inspect object", err)
}

// Delete removes an object; in a versioned bucket, S3 keeps earlier versions behind a delete marker.
func (c *Client) Delete(ctx context.Context, bucket, key string) error {
	if _, err := c.s3.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)}); err != nil {
		return c.requestError("delete object", err)
	}
	return nil
}

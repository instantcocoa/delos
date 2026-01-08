package datasets

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// Source is the interface for reading data from various locations.
type Source interface {
	// Read returns a reader for the data.
	Read(ctx context.Context) (io.ReadCloser, error)
}

// NewSource creates a Source from a DataSource.
func NewSource(ds DataSource) (Source, error) {
	switch {
	case ds.LocalFile != nil:
		return NewLocalFileSource(ds.LocalFile.Path), nil
	case ds.S3 != nil:
		return NewS3Source(ds.S3), nil
	case ds.URL != nil:
		return NewURLSource(ds.URL.URL, ds.URL.Headers), nil
	case ds.Inline != nil:
		return NewInlineSource(ds.Inline.Data), nil
	case ds.GCS != nil:
		return nil, fmt.Errorf("GCS source not yet implemented")
	default:
		return nil, fmt.Errorf("no data source specified")
	}
}

// LocalFileSourceImpl reads data from the local filesystem.
type LocalFileSourceImpl struct {
	path string
}

// NewLocalFileSource creates a new local file source.
func NewLocalFileSource(path string) *LocalFileSourceImpl {
	return &LocalFileSourceImpl{path: path}
}

// Read opens the file and returns a reader.
func (l *LocalFileSourceImpl) Read(ctx context.Context) (io.ReadCloser, error) {
	return os.Open(l.path)
}

// InlineSourceImpl provides data directly from memory.
type InlineSourceImpl struct {
	data []byte
}

// NewInlineSource creates a new inline source.
func NewInlineSource(data []byte) *InlineSourceImpl {
	return &InlineSourceImpl{data: data}
}

// Read returns a reader for the inline data.
func (i *InlineSourceImpl) Read(ctx context.Context) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(i.data)), nil
}

// URLSourceImpl reads data from an HTTP(S) URL.
type URLSourceImpl struct {
	url     string
	headers map[string]string
}

// NewURLSource creates a new URL source.
func NewURLSource(url string, headers map[string]string) *URLSourceImpl {
	return &URLSourceImpl{
		url:     url,
		headers: headers,
	}
}

// Read fetches the URL and returns a reader.
func (u *URLSourceImpl) Read(ctx context.Context) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	for k, v := range u.headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch URL: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	return resp.Body, nil
}

// S3SourceImpl reads data from Amazon S3 or S3-compatible storage.
type S3SourceImpl struct {
	bucket          string
	key             string
	region          string
	endpoint        string
	accessKeyID     string
	secretAccessKey string
}

// NewS3Source creates a new S3 source.
func NewS3Source(src *S3Source) *S3SourceImpl {
	return &S3SourceImpl{
		bucket:          src.Bucket,
		key:             src.Key,
		region:          src.Region,
		endpoint:        src.Endpoint,
		accessKeyID:     src.AccessKeyID,
		secretAccessKey: src.SecretAccessKey,
	}
}

// Read fetches the object from S3 and returns a reader.
func (s *S3SourceImpl) Read(ctx context.Context) (io.ReadCloser, error) {
	client, err := s.createClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create S3 client: %w", err)
	}

	result, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.key),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get S3 object: %w", err)
	}

	return result.Body, nil
}

func (s *S3SourceImpl) createClient(ctx context.Context) (*s3.Client, error) {
	var opts []func(*config.LoadOptions) error

	// Set region if specified
	if s.region != "" {
		opts = append(opts, config.WithRegion(s.region))
	}

	// Set credentials if specified
	if s.accessKeyID != "" && s.secretAccessKey != "" {
		opts = append(opts, config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(s.accessKeyID, s.secretAccessKey, ""),
		))
	}

	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, err
	}

	// Create S3 client with optional custom endpoint
	var s3Opts []func(*s3.Options)
	if s.endpoint != "" {
		s3Opts = append(s3Opts, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(s.endpoint)
			o.UsePathStyle = true // Required for most S3-compatible stores
		})
	}

	return s3.NewFromConfig(cfg, s3Opts...), nil
}

// Write uploads data to S3 (for export).
func (s *S3SourceImpl) Write(ctx context.Context, data io.Reader) error {
	client, err := s.createClient(ctx)
	if err != nil {
		return fmt.Errorf("failed to create S3 client: %w", err)
	}

	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.key),
		Body:   data,
	})
	if err != nil {
		return fmt.Errorf("failed to put S3 object: %w", err)
	}

	return nil
}

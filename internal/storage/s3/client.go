// internal/storage/s3/client.go
package s3

import (
	"context"
	"errors"
	"fmt"
	"github.com/kkuzar/blog_system/internal/config"
	"github.com/kkuzar/blog_system/internal/storage"
	"io"
	"log"
	"net/url"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

type S3Client struct {
	client *s3.Client
	bucket string
}

// NewS3Client creates a new S3 storage client.
func NewS3Client(cfg *config.StorageConfig) (*S3Client, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(context.TODO(),
		awsconfig.WithRegion(cfg.S3Region),
		// Optionally provide static credentials (useful for local dev/testing)
		// In production, prefer IAM roles or environment variables recognized by the SDK
		awsconfig.WithCredentialsProvider(aws.NewCredentialsCache(
			credentials.NewStaticCredentialsProvider(cfg.S3AccessKey, cfg.S3SecretKey, ""),
		)),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	s3Opts := []func(*s3.Options){}

	// Custom endpoint resolver for S3-compatible storage like MinIO
	if cfg.S3Endpoint != "" {
		// Ensure endpoint has scheme
		endpointURL, err := url.Parse(cfg.S3Endpoint)
		if err != nil {
			return nil, fmt.Errorf("invalid S3 endpoint URL: %w", err)
		}
		if endpointURL.Scheme == "" {
			// Default to http if no scheme provided, adjust if needed
			cfg.S3Endpoint = "http://" + cfg.S3Endpoint
			log.Printf("S3 Endpoint missing scheme, defaulting to http: %s", cfg.S3Endpoint)
		}

		resolver := s3.EndpointResolverFunc(func(region string, options s3.EndpointResolverOptions) (aws.Endpoint, error) {
			return aws.Endpoint{
				URL:           cfg.S3Endpoint,
				SigningRegion: region, // Use the configured region for signing
			}, nil
		})
		s3Opts = append(s3Opts, s3.WithEndpointResolver(resolver))
	}

	// Use path-style addressing if configured (needed for MinIO)
	if cfg.S3UsePathStyle {
		s3Opts = append(s3Opts, func(o *s3.Options) {
			o.UsePathStyle = true
		})
	}

	client := s3.NewFromConfig(awsCfg, s3Opts...)

	return &S3Client{
		client: client,
		bucket: cfg.S3Bucket,
	}, nil
}

func (s *S3Client) UploadFile(ctx context.Context, key string, body io.Reader, contentType string) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(key),
		Body:        body,
		ContentType: aws.String(contentType),
	})
	if err != nil {
		return fmt.Errorf("failed to upload to S3 (bucket: %s, key: %s): %w", s.bucket, key, err)
	}
	return nil
}

func (s *S3Client) DownloadFile(ctx context.Context, key string) (io.ReadCloser, error) {
	output, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		var nsk *types.NoSuchKey
		if errors.As(err, &nsk) {
			return nil, storage.ErrFileNotFound
		}
		return nil, fmt.Errorf("failed to download from S3 (bucket: %s, key: %s): %w", s.bucket, key, err)
	}
	return output.Body, nil
}

func (s *S3Client) DeleteFile(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		// Check if the error is because the file doesn't exist, which might be okay for delete
		var nsk *types.NoSuchKey
		if errors.As(err, &nsk) {
			log.Printf("Attempted to delete non-existent S3 key: %s", key)
			return nil // Or return storage.ErrFileNotFound if needed upstream
		}
		return fmt.Errorf("failed to delete from S3 (bucket: %s, key: %s): %w", s.bucket, key, err)
	}
	return nil
}

func (s *S3Client) FileExists(ctx context.Context, key string) (bool, error) {
	_, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		var nfk *types.NotFound // HeadObject returns 404 NotFound, not NoSuchKey
		if errors.As(err, &nfk) {
			return false, nil
		}
		// Handle other potential errors like access denied
		return false, fmt.Errorf("failed to check S3 file existence (bucket: %s, key: %s): %w", s.bucket, key, err)
	}
	return true, nil
}

func (s *S3Client) Close() error {
	// S3 client doesn't typically require explicit closing unless managing underlying resources manually.
	return nil
}

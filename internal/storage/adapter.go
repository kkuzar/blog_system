// internal/storage/adapter.go
package storage

import (
	"context"
	"errors"
	"github.com/kkuzar/blog_system/internal/config"
	"github.com/kkuzar/blog_system/internal/storage/s3"
	"io"
)

var ErrFileNotFound = errors.New("file not found")
var ErrStorageConfig = errors.New("invalid storage configuration")

// StorageAdapter defines the interface for file storage operations.
type StorageAdapter interface {
	UploadFile(ctx context.Context, key string, body io.Reader, contentType string) error
	DownloadFile(ctx context.Context, key string) (io.ReadCloser, error)
	DeleteFile(ctx context.Context, key string) error
	FileExists(ctx context.Context, key string) (bool, error)
	// GetPresignedURL(ctx context.Context, key string, duration time.Duration) (string, error) // Optional: for direct browser uploads/downloads
	Close() error // For any cleanup needed
}

// NewStorageAdapter creates a storage adapter based on the configuration.
func NewStorageAdapter(cfg *config.StorageConfig) (StorageAdapter, error) {
	switch cfg.Type {
	case "s3":
		if cfg.S3Bucket == "" || cfg.S3Region == "" {
			// Only return error if S3 is selected but improperly configured
			return nil, errors.New("S3 storage selected but S3_BUCKET_NAME or AWS_REGION is missing")
		}
		return s3.NewS3Client(cfg)
	// Add other storage types here (e.g., "local", "gcs")
	default:
		// Only return error if a type is specified but not supported
		if cfg.Type != "" {
			return nil, errors.New("unsupported storage type: " + cfg.Type)
		}
		// If no storage type is configured, maybe return a nil adapter or a no-op one?
		// For this project, storage is essential, so let's require it.
		return nil, errors.New("STORAGE_TYPE must be configured (e.g., 's3')")
	}
}

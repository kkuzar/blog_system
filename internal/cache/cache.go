// internal/cache/cache.go
package cache

import (
	"context"
	"errors"
	"github.com/kkuzar/blog_system/internal/models"
	"time"
)

var ErrNotFound = errors.New("cache: key not found")

// Cache defines the interface for caching operations.
type Cache interface {
	GetUser(ctx context.Context, userID string) (*models.User, error)
	SetUser(ctx context.Context, user *models.User, expiration time.Duration) error
	DeleteUser(ctx context.Context, userID string) error

	GetItemMeta(ctx context.Context, itemID string, itemType models.ItemType) (interface{}, error) // Returns *models.Post or *models.CodeFile
	SetItemMeta(ctx context.Context, itemID string, itemType models.ItemType, meta interface{}, expiration time.Duration) error
	DeleteItemMeta(ctx context.Context, itemID string, itemType models.ItemType) error

	GetItemContent(ctx context.Context, itemID string, itemType models.ItemType, version int) (string, error)
	SetItemContent(ctx context.Context, itemID string, itemType models.ItemType, version int, content string, expiration time.Duration) error
	DeleteItemContent(ctx context.Context, itemID string, itemType models.ItemType, version int) error // Delete specific version
	InvalidateItemContent(ctx context.Context, itemID string, itemType models.ItemType) error          // Delete all versions for item

	Ping(ctx context.Context) error
	Close() error
}

// NoOpCache provides a cache implementation that does nothing.
// Useful when caching is disabled or for testing.
type NoOpCache struct{}

func NewNoOpCache() *NoOpCache { return &NoOpCache{} }

func (c *NoOpCache) GetUser(ctx context.Context, userID string) (*models.User, error) {
	return nil, ErrNotFound
}
func (c *NoOpCache) SetUser(ctx context.Context, user *models.User, expiration time.Duration) error {
	return nil
}
func (c *NoOpCache) DeleteUser(ctx context.Context, userID string) error { return nil }
func (c *NoOpCache) GetItemMeta(ctx context.Context, itemID string, itemType models.ItemType) (interface{}, error) {
	return nil, ErrNotFound
}
func (c *NoOpCache) SetItemMeta(ctx context.Context, itemID string, itemType models.ItemType, meta interface{}, expiration time.Duration) error {
	return nil
}
func (c *NoOpCache) DeleteItemMeta(ctx context.Context, itemID string, itemType models.ItemType) error {
	return nil
}
func (c *NoOpCache) GetItemContent(ctx context.Context, itemID string, itemType models.ItemType, version int) (string, error) {
	return "", ErrNotFound
}
func (c *NoOpCache) SetItemContent(ctx context.Context, itemID string, itemType models.ItemType, version int, content string, expiration time.Duration) error {
	return nil
}
func (c *NoOpCache) DeleteItemContent(ctx context.Context, itemID string, itemType models.ItemType, version int) error {
	return nil
}
func (c *NoOpCache) InvalidateItemContent(ctx context.Context, itemID string, itemType models.ItemType) error {
	return nil
}
func (c *NoOpCache) Ping(ctx context.Context) error { return nil }
func (c *NoOpCache) Close() error                   { return nil }

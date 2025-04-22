// internal/cache/redis/client.go
package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/kkuzar/blog_system/internal/cache"
	"github.com/kkuzar/blog_system/internal/config"
	"github.com/kkuzar/blog_system/internal/models"
	"log"
	"time"

	"github.com/go-redis/redis/v8"
)

type RedisCache struct {
	client *redis.Client
	prefix string // Optional prefix for keys
}

// NewRedisCache creates a new Redis cache client.
func NewRedisCache(cfg *config.RedisConfig) (*RedisCache, error) {
	if !cfg.Enabled {
		log.Println("Redis is disabled in config.")
		// Return nil or a NoOpCache? Let's return nil and handle in main.
		return nil, errors.New("redis disabled")
	}

	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	})

	// Ping to check connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to redis: %w", err)
	}

	log.Printf("Connected to Redis at %s, DB %d", cfg.Addr, cfg.DB)
	return &RedisCache{client: rdb, prefix: "gbc:"}, nil // Example prefix
}

func (c *RedisCache) Ping(ctx context.Context) error {
	return c.client.Ping(ctx).Err()
}

func (c *RedisCache) Close() error {
	if c.client != nil {
		return c.client.Close()
	}
	return nil
}

// --- Key Generation ---
func (c *RedisCache) userKey(userID string) string {
	return fmt.Sprintf("%suser:%s", c.prefix, userID)
}
func (c *RedisCache) itemMetaKey(itemID string, itemType models.ItemType) string {
	return fmt.Sprintf("%sitem:meta:%s:%s", c.prefix, itemType, itemID)
}
func (c *RedisCache) itemContentKey(itemID string, itemType models.ItemType, version int) string {
	return fmt.Sprintf("%sitem:content:%s:%s:v%d", c.prefix, itemType, itemID, version)
}
func (c *RedisCache) itemContentPattern(itemID string, itemType models.ItemType) string {
	return fmt.Sprintf("%sitem:content:%s:%s:v*", c.prefix, itemType, itemID) // Pattern for invalidation
}

// --- User Methods ---
func (c *RedisCache) GetUser(ctx context.Context, userID string) (*models.User, error) {
	key := c.userKey(userID)
	val, err := c.client.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return nil, cache.ErrNotFound
	} else if err != nil {
		log.Printf("Redis GET error for key %s: %v", key, err)
		return nil, err
	}

	var user models.User
	if err := json.Unmarshal(val, &user); err != nil {
		log.Printf("Redis JSON unmarshal error for key %s: %v", key, err)
		return nil, err
	}
	return &user, nil
}

func (c *RedisCache) SetUser(ctx context.Context, user *models.User, expiration time.Duration) error {
	key := c.userKey(user.ID)
	val, err := json.Marshal(user)
	if err != nil {
		log.Printf("Redis JSON marshal error for user %s: %v", user.ID, err)
		return err
	}
	if err := c.client.Set(ctx, key, val, expiration).Err(); err != nil {
		log.Printf("Redis SET error for key %s: %v", key, err)
		return err
	}
	return nil
}

func (c *RedisCache) DeleteUser(ctx context.Context, userID string) error {
	key := c.userKey(userID)
	if err := c.client.Del(ctx, key).Err(); err != nil && err != redis.Nil {
		log.Printf("Redis DEL error for key %s: %v", key, err)
		return err
	}
	return nil
}

// --- Item Meta Methods ---
func (c *RedisCache) GetItemMeta(ctx context.Context, itemID string, itemType models.ItemType) (interface{}, error) {
	key := c.itemMetaKey(itemID, itemType)
	val, err := c.client.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return nil, cache.ErrNotFound
	} else if err != nil {
		log.Printf("Redis GET error for key %s: %v", key, err)
		return nil, err
	}

	// Need to unmarshal into the correct type
	var meta interface{}
	switch itemType {
	case models.ItemTypePost:
		var post models.Post
		if err := json.Unmarshal(val, &post); err != nil {
			log.Printf("Redis JSON unmarshal error for post key %s: %v", key, err)
			return nil, err
		}
		meta = &post
	case models.ItemTypeCodeFile:
		var codeFile models.CodeFile
		if err := json.Unmarshal(val, &codeFile); err != nil {
			log.Printf("Redis JSON unmarshal error for codefile key %s: %v", key, err)
			return nil, err
		}
		meta = &codeFile
	default:
		return nil, errors.New("invalid item type for cache")
	}
	return meta, nil
}

func (c *RedisCache) SetItemMeta(ctx context.Context, itemID string, itemType models.ItemType, meta interface{}, expiration time.Duration) error {
	key := c.itemMetaKey(itemID, itemType)
	// Ensure meta is the correct type before marshalling
	switch itemType {
	case models.ItemTypePost:
		if _, ok := meta.(*models.Post); !ok {
			return errors.New("invalid meta type for post")
		}
	case models.ItemTypeCodeFile:
		if _, ok := meta.(*models.CodeFile); !ok {
			return errors.New("invalid meta type for codefile")
		}
	default:
		return errors.New("invalid item type for cache")
	}

	val, err := json.Marshal(meta)
	if err != nil {
		log.Printf("Redis JSON marshal error for item %s (%s): %v", itemID, itemType, err)
		return err
	}
	if err := c.client.Set(ctx, key, val, expiration).Err(); err != nil {
		log.Printf("Redis SET error for key %s: %v", key, err)
		return err
	}
	return nil
}

func (c *RedisCache) DeleteItemMeta(ctx context.Context, itemID string, itemType models.ItemType) error {
	key := c.itemMetaKey(itemID, itemType)
	if err := c.client.Del(ctx, key).Err(); err != nil && err != redis.Nil {
		log.Printf("Redis DEL error for key %s: %v", key, err)
		return err
	}
	return nil
}

// --- Item Content Methods ---
func (c *RedisCache) GetItemContent(ctx context.Context, itemID string, itemType models.ItemType, version int) (string, error) {
	key := c.itemContentKey(itemID, itemType, version)
	val, err := c.client.Get(ctx, key).Result()
	if err == redis.Nil {
		return "", cache.ErrNotFound
	} else if err != nil {
		log.Printf("Redis GET error for key %s: %v", key, err)
		return "", err
	}
	return val, nil
}

func (c *RedisCache) SetItemContent(ctx context.Context, itemID string, itemType models.ItemType, version int, content string, expiration time.Duration) error {
	key := c.itemContentKey(itemID, itemType, version)
	if err := c.client.Set(ctx, key, content, expiration).Err(); err != nil {
		log.Printf("Redis SET error for key %s: %v", key, err)
		return err
	}
	return nil
}

func (c *RedisCache) DeleteItemContent(ctx context.Context, itemID string, itemType models.ItemType, version int) error {
	key := c.itemContentKey(itemID, itemType, version)
	if err := c.client.Del(ctx, key).Err(); err != nil && err != redis.Nil {
		log.Printf("Redis DEL error for key %s: %v", key, err)
		return err
	}
	return nil
}

// InvalidateItemContent deletes all cached versions for a given item.
func (c *RedisCache) InvalidateItemContent(ctx context.Context, itemID string, itemType models.ItemType) error {
	pattern := c.itemContentPattern(itemID, itemType)
	iter := c.client.Scan(ctx, 0, pattern, 0).Iterator()
	keysToDelete := []string{}
	for iter.Next(ctx) {
		keysToDelete = append(keysToDelete, iter.Val())
	}
	if err := iter.Err(); err != nil {
		log.Printf("Redis SCAN error for pattern %s: %v", pattern, err)
		// Continue to delete keys found so far, but return error
		// return err
	}

	if len(keysToDelete) > 0 {
		if err := c.client.Del(ctx, keysToDelete...).Err(); err != nil && err != redis.Nil {
			log.Printf("Redis DEL error for keys matching %s: %v", pattern, err)
			return err
		}
		log.Printf("Invalidated %d content cache entries for %s %s", len(keysToDelete), itemType, itemID)
	}
	return iter.Err() // Return scan error if any
}

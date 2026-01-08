// Package cache provides Redis-based caching utilities.
package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

// Config holds Redis connection configuration.
type Config struct {
	Addr     string
	Password string
	DB       int

	PoolSize     int
	MinIdleConns int
	MaxRetries   int

	ReadTimeout  time.Duration
	WriteTimeout time.Duration
}

// DefaultConfig returns sensible defaults for Redis configuration.
func DefaultConfig() *Config {
	return &Config{
		Addr:         "localhost:6379",
		Password:     "",
		DB:           0,
		PoolSize:     10,
		MinIdleConns: 2,
		MaxRetries:   3,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
	}
}

// Client wraps redis.Client with additional functionality.
type Client struct {
	*redis.Client
	logger    *slog.Logger
	keyPrefix string
}

// Connect creates a new Redis connection.
func Connect(ctx context.Context, cfg *Config) (*Client, error) {
	client := redis.NewClient(&redis.Options{
		Addr:         cfg.Addr,
		Password:     cfg.Password,
		DB:           cfg.DB,
		PoolSize:     cfg.PoolSize,
		MinIdleConns: cfg.MinIdleConns,
		MaxRetries:   cfg.MaxRetries,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
	})

	// Verify connection
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to ping Redis: %w", err)
	}

	return &Client{
		Client: client,
		logger: slog.Default(),
	}, nil
}

// WithLogger sets the logger for the client.
func (c *Client) WithLogger(logger *slog.Logger) *Client {
	c.logger = logger
	return c
}

// WithKeyPrefix sets a prefix for all keys.
func (c *Client) WithKeyPrefix(prefix string) *Client {
	c.keyPrefix = prefix
	return c
}

// prefixedKey returns the key with the configured prefix.
func (c *Client) prefixedKey(key string) string {
	if c.keyPrefix == "" {
		return key
	}
	return c.keyPrefix + ":" + key
}

// Get retrieves a value from the cache.
func (c *Client) Get(ctx context.Context, key string) (string, error) {
	result, err := c.Client.Get(ctx, c.prefixedKey(key)).Result()
	if err == redis.Nil {
		return "", nil
	}
	return result, err
}

// Set stores a value in the cache with an expiration.
func (c *Client) Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
	var data string
	switch v := value.(type) {
	case string:
		data = v
	case []byte:
		data = string(v)
	default:
		bytes, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("failed to marshal value: %w", err)
		}
		data = string(bytes)
	}

	return c.Client.Set(ctx, c.prefixedKey(key), data, expiration).Err()
}

// GetJSON retrieves a JSON value from the cache and unmarshals it.
func (c *Client) GetJSON(ctx context.Context, key string, dest interface{}) error {
	data, err := c.Get(ctx, key)
	if err != nil {
		return err
	}
	if data == "" {
		return nil
	}
	return json.Unmarshal([]byte(data), dest)
}

// Delete removes a key from the cache.
func (c *Client) Delete(ctx context.Context, keys ...string) error {
	prefixedKeys := make([]string, len(keys))
	for i, k := range keys {
		prefixedKeys[i] = c.prefixedKey(k)
	}
	return c.Client.Del(ctx, prefixedKeys...).Err()
}

// Exists checks if a key exists.
func (c *Client) Exists(ctx context.Context, key string) (bool, error) {
	n, err := c.Client.Exists(ctx, c.prefixedKey(key)).Result()
	return n > 0, err
}

// Expire sets an expiration on a key.
func (c *Client) Expire(ctx context.Context, key string, expiration time.Duration) error {
	return c.Client.Expire(ctx, c.prefixedKey(key), expiration).Err()
}

// TTL returns the remaining time to live for a key.
func (c *Client) TTL(ctx context.Context, key string) (time.Duration, error) {
	return c.Client.TTL(ctx, c.prefixedKey(key)).Result()
}

// Incr increments a counter.
func (c *Client) Incr(ctx context.Context, key string) (int64, error) {
	return c.Client.Incr(ctx, c.prefixedKey(key)).Result()
}

// IncrBy increments a counter by a value.
func (c *Client) IncrBy(ctx context.Context, key string, value int64) (int64, error) {
	return c.Client.IncrBy(ctx, c.prefixedKey(key), value).Result()
}

// SetNX sets a value only if it doesn't exist (for distributed locks).
func (c *Client) SetNX(ctx context.Context, key string, value interface{}, expiration time.Duration) (bool, error) {
	var data string
	switch v := value.(type) {
	case string:
		data = v
	case []byte:
		data = string(v)
	default:
		bytes, err := json.Marshal(v)
		if err != nil {
			return false, fmt.Errorf("failed to marshal value: %w", err)
		}
		data = string(bytes)
	}

	return c.Client.SetNX(ctx, c.prefixedKey(key), data, expiration).Result()
}

// Close closes the Redis connection.
func (c *Client) Close() error {
	return c.Client.Close()
}

// CacheAside implements the cache-aside pattern.
type CacheAside[T any] struct {
	client     *Client
	defaultTTL time.Duration
	keyFunc    func(key string) string
}

// NewCacheAside creates a new cache-aside helper.
func NewCacheAside[T any](client *Client, ttl time.Duration) *CacheAside[T] {
	return &CacheAside[T]{
		client:     client,
		defaultTTL: ttl,
		keyFunc:    func(k string) string { return k },
	}
}

// WithKeyFunc sets a custom key transformation function.
func (ca *CacheAside[T]) WithKeyFunc(fn func(string) string) *CacheAside[T] {
	ca.keyFunc = fn
	return ca
}

// Get retrieves a value from cache, or calls the loader function if not found.
func (ca *CacheAside[T]) Get(ctx context.Context, key string, loader func(ctx context.Context) (T, error)) (T, error) {
	cacheKey := ca.keyFunc(key)

	// Try cache first
	var result T
	data, err := ca.client.Get(ctx, cacheKey)
	if err != nil {
		return result, fmt.Errorf("cache get error: %w", err)
	}

	if data != "" {
		if err := json.Unmarshal([]byte(data), &result); err == nil {
			return result, nil
		}
	}

	// Cache miss - load from source
	result, err = loader(ctx)
	if err != nil {
		return result, err
	}

	// Store in cache (ignore errors)
	_ = ca.client.Set(ctx, cacheKey, result, ca.defaultTTL)

	return result, nil
}

// Invalidate removes a key from the cache.
func (ca *CacheAside[T]) Invalidate(ctx context.Context, key string) error {
	return ca.client.Delete(ctx, ca.keyFunc(key))
}

// RateLimiter provides simple rate limiting using Redis.
type RateLimiter struct {
	client     *Client
	keyPrefix  string
	limit      int
	windowSecs int
}

// NewRateLimiter creates a new rate limiter.
func NewRateLimiter(client *Client, keyPrefix string, limit int, windowSecs int) *RateLimiter {
	return &RateLimiter{
		client:     client,
		keyPrefix:  keyPrefix,
		limit:      limit,
		windowSecs: windowSecs,
	}
}

// Allow checks if a request is allowed under the rate limit.
func (rl *RateLimiter) Allow(ctx context.Context, key string) (bool, error) {
	fullKey := fmt.Sprintf("%s:%s", rl.keyPrefix, key)

	// Use sliding window with Redis INCR + EXPIRE
	count, err := rl.client.Incr(ctx, fullKey)
	if err != nil {
		return false, err
	}

	if count == 1 {
		// First request in this window, set expiration
		_ = rl.client.Expire(ctx, fullKey, time.Duration(rl.windowSecs)*time.Second)
	}

	return count <= int64(rl.limit), nil
}

// Remaining returns the number of requests remaining in the current window.
func (rl *RateLimiter) Remaining(ctx context.Context, key string) (int, error) {
	fullKey := fmt.Sprintf("%s:%s", rl.keyPrefix, key)
	data, err := rl.client.Get(ctx, fullKey)
	if err != nil {
		return rl.limit, err
	}
	if data == "" {
		return rl.limit, nil
	}

	var count int
	fmt.Sscanf(data, "%d", &count)
	remaining := rl.limit - count
	if remaining < 0 {
		return 0, nil
	}
	return remaining, nil
}

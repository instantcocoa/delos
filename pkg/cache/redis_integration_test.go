package cache

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"
)

func getRedisAddr() string {
	if addr := os.Getenv("REDIS_ADDR"); addr != "" {
		return addr
	}
	return "localhost:6379"
}

func setupRedis(t *testing.T) *Client {
	t.Helper()

	cfg := &Config{
		Addr:         getRedisAddr(),
		Password:     "",
		DB:           15, // Use DB 15 for tests to avoid conflicts
		PoolSize:     5,
		MinIdleConns: 1,
		MaxRetries:   3,
		ReadTimeout:  time.Second,
		WriteTimeout: time.Second,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	client, err := Connect(ctx, cfg)
	if err != nil {
		t.Skipf("Redis not available: %v", err)
	}

	// Clean up test database
	client.Client.FlushDB(ctx)

	t.Cleanup(func() {
		client.Client.FlushDB(context.Background())
		client.Close()
	})

	return client
}

func TestClient_GetSet_Integration(t *testing.T) {
	client := setupRedis(t)
	ctx := context.Background()

	// Test Set and Get with string
	err := client.Set(ctx, "test-key", "test-value", time.Minute)
	if err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	val, err := client.Get(ctx, "test-key")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if val != "test-value" {
		t.Errorf("Get() = %q, want %q", val, "test-value")
	}
}

func TestClient_GetSet_ByteSlice_Integration(t *testing.T) {
	client := setupRedis(t)
	ctx := context.Background()

	data := []byte("binary data here")
	err := client.Set(ctx, "bytes-key", data, time.Minute)
	if err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	val, err := client.Get(ctx, "bytes-key")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if val != string(data) {
		t.Errorf("Get() = %q, want %q", val, string(data))
	}
}

func TestClient_GetSet_JSON_Integration(t *testing.T) {
	client := setupRedis(t)
	ctx := context.Background()

	type TestStruct struct {
		Name  string `json:"name"`
		Value int    `json:"value"`
	}

	input := TestStruct{Name: "test", Value: 42}
	err := client.Set(ctx, "json-key", input, time.Minute)
	if err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	var output TestStruct
	err = client.GetJSON(ctx, "json-key", &output)
	if err != nil {
		t.Fatalf("GetJSON() error = %v", err)
	}
	if output.Name != input.Name || output.Value != input.Value {
		t.Errorf("GetJSON() = %+v, want %+v", output, input)
	}
}

func TestClient_GetJSON_NotFound_Integration(t *testing.T) {
	client := setupRedis(t)
	ctx := context.Background()

	var output map[string]string
	err := client.GetJSON(ctx, "nonexistent-key", &output)
	if err != nil {
		t.Fatalf("GetJSON() error = %v", err)
	}
	// output should remain nil/empty for nonexistent key
	if output != nil {
		t.Errorf("GetJSON() = %v, want nil for nonexistent key", output)
	}
}

func TestClient_Get_NotFound_Integration(t *testing.T) {
	client := setupRedis(t)
	ctx := context.Background()

	val, err := client.Get(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if val != "" {
		t.Errorf("Get() = %q, want empty string for nonexistent key", val)
	}
}

func TestClient_Delete_Integration(t *testing.T) {
	client := setupRedis(t)
	ctx := context.Background()

	// Set a key
	err := client.Set(ctx, "delete-me", "value", time.Minute)
	if err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	// Delete it
	err = client.Delete(ctx, "delete-me")
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	// Verify it's gone
	val, err := client.Get(ctx, "delete-me")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if val != "" {
		t.Errorf("Get() after Delete() = %q, want empty", val)
	}
}

func TestClient_Delete_Multiple_Integration(t *testing.T) {
	client := setupRedis(t)
	ctx := context.Background()

	// Set multiple keys
	keys := []string{"key1", "key2", "key3"}
	for _, k := range keys {
		if err := client.Set(ctx, k, "value", time.Minute); err != nil {
			t.Fatalf("Set(%s) error = %v", k, err)
		}
	}

	// Delete all
	err := client.Delete(ctx, keys...)
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	// Verify all gone
	for _, k := range keys {
		val, _ := client.Get(ctx, k)
		if val != "" {
			t.Errorf("Get(%s) after Delete() = %q, want empty", k, val)
		}
	}
}

func TestClient_Exists_Integration(t *testing.T) {
	client := setupRedis(t)
	ctx := context.Background()

	// Key doesn't exist
	exists, err := client.Exists(ctx, "exists-test")
	if err != nil {
		t.Fatalf("Exists() error = %v", err)
	}
	if exists {
		t.Error("Exists() = true for nonexistent key")
	}

	// Set the key
	_ = client.Set(ctx, "exists-test", "value", time.Minute)

	// Now it exists
	exists, err = client.Exists(ctx, "exists-test")
	if err != nil {
		t.Fatalf("Exists() error = %v", err)
	}
	if !exists {
		t.Error("Exists() = false for existing key")
	}
}

func TestClient_Expire_Integration(t *testing.T) {
	client := setupRedis(t)
	ctx := context.Background()

	_ = client.Set(ctx, "expire-test", "value", time.Hour)

	// Set a short expiration (minimum 1 second for Redis)
	err := client.Expire(ctx, "expire-test", 1*time.Second)
	if err != nil {
		t.Fatalf("Expire() error = %v", err)
	}

	// Wait for expiration
	time.Sleep(1500 * time.Millisecond)

	val, _ := client.Get(ctx, "expire-test")
	if val != "" {
		t.Errorf("key should have expired, got %q", val)
	}
}

func TestClient_TTL_Integration(t *testing.T) {
	client := setupRedis(t)
	ctx := context.Background()

	_ = client.Set(ctx, "ttl-test", "value", 10*time.Second)

	ttl, err := client.TTL(ctx, "ttl-test")
	if err != nil {
		t.Fatalf("TTL() error = %v", err)
	}
	if ttl < 9*time.Second || ttl > 10*time.Second {
		t.Errorf("TTL() = %v, want ~10s", ttl)
	}
}

func TestClient_Incr_Integration(t *testing.T) {
	client := setupRedis(t)
	ctx := context.Background()

	// Incr on nonexistent key starts at 1
	val, err := client.Incr(ctx, "counter")
	if err != nil {
		t.Fatalf("Incr() error = %v", err)
	}
	if val != 1 {
		t.Errorf("Incr() = %d, want 1", val)
	}

	// Incr again
	val, err = client.Incr(ctx, "counter")
	if err != nil {
		t.Fatalf("Incr() error = %v", err)
	}
	if val != 2 {
		t.Errorf("Incr() = %d, want 2", val)
	}
}

func TestClient_IncrBy_Integration(t *testing.T) {
	client := setupRedis(t)
	ctx := context.Background()

	val, err := client.IncrBy(ctx, "counter-by", 10)
	if err != nil {
		t.Fatalf("IncrBy() error = %v", err)
	}
	if val != 10 {
		t.Errorf("IncrBy() = %d, want 10", val)
	}

	val, err = client.IncrBy(ctx, "counter-by", 5)
	if err != nil {
		t.Fatalf("IncrBy() error = %v", err)
	}
	if val != 15 {
		t.Errorf("IncrBy() = %d, want 15", val)
	}
}

func TestClient_SetNX_Integration(t *testing.T) {
	client := setupRedis(t)
	ctx := context.Background()

	// First SetNX should succeed
	ok, err := client.SetNX(ctx, "setnx-test", "first", time.Minute)
	if err != nil {
		t.Fatalf("SetNX() error = %v", err)
	}
	if !ok {
		t.Error("SetNX() = false, want true for new key")
	}

	// Second SetNX should fail
	ok, err = client.SetNX(ctx, "setnx-test", "second", time.Minute)
	if err != nil {
		t.Fatalf("SetNX() error = %v", err)
	}
	if ok {
		t.Error("SetNX() = true, want false for existing key")
	}

	// Value should be "first"
	val, _ := client.Get(ctx, "setnx-test")
	if val != "first" {
		t.Errorf("Get() = %q, want %q", val, "first")
	}
}

func TestClient_SetNX_Struct_Integration(t *testing.T) {
	client := setupRedis(t)
	ctx := context.Background()

	data := struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}{ID: 1, Name: "test"}

	ok, err := client.SetNX(ctx, "setnx-struct", data, time.Minute)
	if err != nil {
		t.Fatalf("SetNX() error = %v", err)
	}
	if !ok {
		t.Error("SetNX() = false, want true")
	}

	val, _ := client.Get(ctx, "setnx-struct")
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(val), &result); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if result["name"] != "test" {
		t.Errorf("name = %v, want test", result["name"])
	}
}

func TestClient_WithKeyPrefix_Integration(t *testing.T) {
	client := setupRedis(t)
	ctx := context.Background()

	client.WithKeyPrefix("myapp")

	_ = client.Set(ctx, "key", "value", time.Minute)

	// Key should be stored with prefix
	val, _ := client.Get(ctx, "key")
	if val != "value" {
		t.Errorf("Get() = %q, want %q", val, "value")
	}

	// Direct access without prefix should fail
	directVal, _ := client.Client.Get(ctx, "key").Result()
	if directVal == "value" {
		t.Error("key stored without prefix")
	}

	// Direct access with prefix should work
	prefixedVal, _ := client.Client.Get(ctx, "myapp:key").Result()
	if prefixedVal != "value" {
		t.Errorf("prefixed key = %q, want %q", prefixedVal, "value")
	}
}

type cacheTestData struct {
	Value string `json:"value"`
}

func TestCacheAside_Integration(t *testing.T) {
	client := setupRedis(t)
	ctx := context.Background()

	cache := NewCacheAside[cacheTestData](client, time.Minute)

	loadCount := 0
	loader := func(ctx context.Context) (cacheTestData, error) {
		loadCount++
		return cacheTestData{Value: "loaded"}, nil
	}

	// First call should invoke loader
	val, err := cache.Get(ctx, "aside-key", loader)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if val.Value != "loaded" {
		t.Errorf("Get() = %q, want %q", val.Value, "loaded")
	}
	if loadCount != 1 {
		t.Errorf("loadCount = %d, want 1", loadCount)
	}

	// Second call should use cache
	val, err = cache.Get(ctx, "aside-key", loader)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if val.Value != "loaded" {
		t.Errorf("Get() = %q, want %q", val.Value, "loaded")
	}
	if loadCount != 1 {
		t.Errorf("loadCount = %d, want 1 (cached)", loadCount)
	}
}

func TestCacheAside_WithKeyFunc_Integration(t *testing.T) {
	client := setupRedis(t)
	ctx := context.Background()

	cache := NewCacheAside[int](client, time.Minute).
		WithKeyFunc(func(k string) string { return "user:" + k })

	loader := func(ctx context.Context) (int, error) {
		return 42, nil
	}

	val, err := cache.Get(ctx, "123", loader)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if val != 42 {
		t.Errorf("Get() = %d, want 42", val)
	}

	// Verify key was stored with transformed name
	exists, _ := client.Exists(ctx, "user:123")
	if !exists {
		t.Error("key should exist with transformed name")
	}
}

func TestCacheAside_Invalidate_Integration(t *testing.T) {
	client := setupRedis(t)
	ctx := context.Background()

	cache := NewCacheAside[cacheTestData](client, time.Minute)

	loadCount := 0
	loader := func(ctx context.Context) (cacheTestData, error) {
		loadCount++
		return cacheTestData{Value: "value"}, nil
	}

	// Load
	_, _ = cache.Get(ctx, "inv-key", loader)
	if loadCount != 1 {
		t.Errorf("loadCount = %d, want 1", loadCount)
	}

	// Invalidate
	err := cache.Invalidate(ctx, "inv-key")
	if err != nil {
		t.Fatalf("Invalidate() error = %v", err)
	}

	// Should reload
	_, _ = cache.Get(ctx, "inv-key", loader)
	if loadCount != 2 {
		t.Errorf("loadCount = %d, want 2", loadCount)
	}
}

func TestRateLimiter_Integration(t *testing.T) {
	client := setupRedis(t)
	ctx := context.Background()

	limiter := NewRateLimiter(client, "test-limit", 3, 60)

	// First 3 requests should be allowed
	for i := 0; i < 3; i++ {
		allowed, err := limiter.Allow(ctx, "user1")
		if err != nil {
			t.Fatalf("Allow() error = %v", err)
		}
		if !allowed {
			t.Errorf("request %d should be allowed", i+1)
		}
	}

	// 4th request should be denied
	allowed, err := limiter.Allow(ctx, "user1")
	if err != nil {
		t.Fatalf("Allow() error = %v", err)
	}
	if allowed {
		t.Error("4th request should be denied")
	}

	// Different user should be allowed
	allowed, err = limiter.Allow(ctx, "user2")
	if err != nil {
		t.Fatalf("Allow() error = %v", err)
	}
	if !allowed {
		t.Error("different user should be allowed")
	}
}

func TestRateLimiter_Remaining_Integration(t *testing.T) {
	client := setupRedis(t)
	ctx := context.Background()

	limiter := NewRateLimiter(client, "remaining-test", 5, 60)

	// Initially all remaining
	remaining, err := limiter.Remaining(ctx, "user")
	if err != nil {
		t.Fatalf("Remaining() error = %v", err)
	}
	if remaining != 5 {
		t.Errorf("Remaining() = %d, want 5", remaining)
	}

	// Use 2 requests
	_, _ = limiter.Allow(ctx, "user")
	_, _ = limiter.Allow(ctx, "user")

	remaining, err = limiter.Remaining(ctx, "user")
	if err != nil {
		t.Fatalf("Remaining() error = %v", err)
	}
	if remaining != 3 {
		t.Errorf("Remaining() = %d, want 3", remaining)
	}
}

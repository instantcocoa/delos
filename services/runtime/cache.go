package runtime

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// Response caching: exact-match only. A request is cacheable when it is
// explicitly deterministic (temperature set to 0 - an absent temperature
// means the API default of 1.0) and non-streaming; the key is a
// SHA-256 over a canonical serialization of the request parameters. Semantic
// caching is deliberately out of scope (see ROADMAP.md).

// ResponseCache stores completion results keyed by request fingerprint.
// Implementations must be safe for concurrent use.
type ResponseCache interface {
	// Get returns the cached result for key, if present and unexpired.
	Get(ctx context.Context, key string) (*CompletionResult, bool)
	// Set stores result under key for the given TTL. A non-positive TTL
	// means "no expiry".
	Set(ctx context.Context, key string, result *CompletionResult, ttl time.Duration)
}

// ---- cache key ----

// canonicalRequest is the exact shape hashed into a cache key. Field order is
// the struct's declaration order, so json.Marshal is deterministic. Pointer
// and slice fields stay nil when unset so that "absent" and "zero" hash
// differently.
type canonicalRequest struct {
	Model          string             `json:"model"`
	Messages       []canonicalMessage `json:"messages"`
	Temperature    *float64           `json:"temperature,omitempty"`
	TopP           *float64           `json:"top_p,omitempty"`
	MaxTokens      int                `json:"max_tokens"`
	Stop           []string           `json:"stop,omitempty"`
	Tools          []canonicalTool    `json:"tools,omitempty"`
	ToolChoice     *ToolChoice        `json:"tool_choice,omitempty"`
	ResponseFormat *canonicalFormat   `json:"response_format,omitempty"`
}

type canonicalMessage struct {
	Role       string          `json:"role"`
	Name       string          `json:"name,omitempty"`
	Parts      []canonicalPart `json:"parts,omitempty"`
	ToolCalls  []ToolCall      `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
}

type canonicalPart struct {
	Type      string `json:"type"`
	Text      string `json:"text,omitempty"`
	ImageURL  string `json:"image_url,omitempty"`
	ImageData string `json:"image_data,omitempty"`
	MediaType string `json:"media_type,omitempty"`
}

type canonicalTool struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parameters  string `json:"parameters,omitempty"`
}

type canonicalFormat struct {
	Type   string `json:"type"`
	Name   string `json:"name,omitempty"`
	Schema string `json:"schema,omitempty"`
	Strict bool   `json:"strict"`
}

// CacheKey returns the exact-match cache key for a completion request: the
// SHA-256 (hex) of a canonical serialization of the model, normalized
// messages, and sampling/tool parameters. Provider is deliberately excluded -
// a fallback chain must share one key - as is anything not affecting the
// model's output.
func CacheKey(params CompletionParams) string {
	req := canonicalRequest{
		Model:       params.Model,
		Temperature: params.Temperature,
		TopP:        params.TopP,
		MaxTokens:   params.MaxTokens,
		Stop:        params.Stop,
		ToolChoice:  params.ToolChoice,
	}
	for _, m := range params.Messages {
		cm := canonicalMessage{
			Role:       m.Role,
			Name:       m.Name,
			ToolCalls:  m.ToolCalls,
			ToolCallID: m.ToolCallID,
		}
		for _, p := range m.Content {
			cm.Parts = append(cm.Parts, canonicalPart{
				Type:      p.Type,
				Text:      strings.TrimRight(p.Text, " \t\r\n"),
				ImageURL:  p.ImageURL,
				ImageData: p.ImageData,
				MediaType: p.MediaType,
			})
		}
		req.Messages = append(req.Messages, cm)
	}
	for _, t := range params.Tools {
		req.Tools = append(req.Tools, canonicalTool{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  string(t.Parameters),
		})
	}
	if f := params.ResponseFormat; f != nil {
		req.ResponseFormat = &canonicalFormat{
			Type:   f.Type,
			Name:   f.Name,
			Schema: string(f.Schema),
			Strict: f.Strict,
		}
	}

	// json.Marshal on a struct emits fields in declaration order, so the
	// bytes are stable across runs and processes.
	raw, err := json.Marshal(req)
	if err != nil {
		// Every field is a plain Go value; marshalling cannot fail. Fall
		// back to a key that will simply never hit rather than panicking.
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// cachedResult prepares a stored result for return to a caller.
//
// A cache hit never reached a provider: no tokens were generated and no
// invoice line was created, so the request is billed at zero. Charging the
// original request's cost again would invent revenue for the operator and
// spend a tenant's budget on a request that cost nothing - while the response
// carries X-Delos-Cache: hit, which says the opposite.
//
// The token counts are kept: they describe the payload the client actually
// received, so client-side context accounting still works. Cost is what the
// gateway meters (see recordUsage), and cost is zero.
func cachedResult(stored *CompletionResult) *CompletionResult {
	hit := *stored
	hit.Cached = true
	hit.Usage.CostUSD = 0
	return &hit
}

// billableUsage is what the gateway charges a key for a completed request. A
// cache hit is billed as nothing at all - no provider call, no tokens bought -
// while still being recorded as a request that happened (and published on the
// tail stream with cache_hit=true), so traffic stays visible without inventing
// consumption.
func billableUsage(result *CompletionResult) Usage {
	if result == nil {
		return Usage{}
	}
	if result.Cached {
		return Usage{}
	}
	return result.Usage
}

// ---- in-process LRU ----

// DefaultCacheCapacity is the entry cap of an LRUCache created with a
// non-positive capacity.
const DefaultCacheCapacity = 1024

type lruEntry struct {
	key       string
	result    *CompletionResult
	expiresAt time.Time // zero means no expiry
}

// LRUCache is an in-process, capacity-bounded response cache with per-entry
// TTL. The zero value is not usable; call NewLRUCache.
type LRUCache struct {
	mu       sync.Mutex
	capacity int
	ll       *list.List               // front = most recently used
	items    map[string]*list.Element // key -> element holding *lruEntry

	// now is swappable in tests; nil means time.Now.
	now func() time.Time
}

// NewLRUCache returns an LRU response cache holding at most capacity entries.
// A non-positive capacity uses DefaultCacheCapacity.
func NewLRUCache(capacity int) *LRUCache {
	if capacity <= 0 {
		capacity = DefaultCacheCapacity
	}
	return &LRUCache{
		capacity: capacity,
		ll:       list.New(),
		items:    make(map[string]*list.Element, capacity),
	}
}

func (c *LRUCache) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

// Get returns the cached result for key. Expired entries are evicted and
// reported as a miss.
func (c *LRUCache) Get(ctx context.Context, key string) (*CompletionResult, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		return nil, false
	}
	ent := el.Value.(*lruEntry)
	if !ent.expiresAt.IsZero() && !c.clock().Before(ent.expiresAt) {
		c.removeElement(el)
		return nil, false
	}
	c.ll.MoveToFront(el)
	return ent.result, true
}

// Set stores result under key, evicting the least recently used entry when
// the cache is full. A non-positive TTL stores the entry without expiry.
func (c *LRUCache) Set(ctx context.Context, key string, result *CompletionResult, ttl time.Duration) {
	if result == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	var expiresAt time.Time
	if ttl > 0 {
		expiresAt = c.clock().Add(ttl)
	}
	if el, ok := c.items[key]; ok {
		ent := el.Value.(*lruEntry)
		ent.result = result
		ent.expiresAt = expiresAt
		c.ll.MoveToFront(el)
		return
	}
	el := c.ll.PushFront(&lruEntry{key: key, result: result, expiresAt: expiresAt})
	c.items[key] = el
	for c.ll.Len() > c.capacity {
		if oldest := c.ll.Back(); oldest != nil {
			c.removeElement(oldest)
		}
	}
}

// Len returns the number of entries currently held, including any that have
// expired but not yet been evicted.
func (c *LRUCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ll.Len()
}

// removeElement drops el; callers hold c.mu.
func (c *LRUCache) removeElement(el *list.Element) {
	c.ll.Remove(el)
	delete(c.items, el.Value.(*lruEntry).key)
}

// ---- redis ----

// RedisCache is a shared response cache backed by Redis. Results are stored
// as JSON with a server-side TTL.
type RedisCache struct {
	client *redis.Client
	prefix string
}

// NewRedisCache connects to Redis at a redis:// (or rediss://) URL.
func NewRedisCache(url string) (*RedisCache, error) {
	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, err
	}
	return &RedisCache{client: redis.NewClient(opts), prefix: "delos:cache:"}, nil
}

// Ping verifies connectivity to the Redis server.
func (c *RedisCache) Ping(ctx context.Context) error {
	return c.client.Ping(ctx).Err()
}

// Close releases the underlying client.
func (c *RedisCache) Close() error { return c.client.Close() }

// Get returns the cached result for key. Redis errors are treated as misses:
// caching is an optimization and must never fail a request.
func (c *RedisCache) Get(ctx context.Context, key string) (*CompletionResult, bool) {
	raw, err := c.client.Get(ctx, c.prefix+key).Bytes()
	if err != nil {
		return nil, false
	}
	var result CompletionResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, false
	}
	return &result, true
}

// Set stores result under key with the given TTL. Failures are ignored.
func (c *RedisCache) Set(ctx context.Context, key string, result *CompletionResult, ttl time.Duration) {
	if result == nil {
		return
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return
	}
	if ttl < 0 {
		ttl = 0
	}
	c.client.Set(ctx, c.prefix+key, raw, ttl)
}

// Package cache is the cache shared between API instances. Without one
// (no Redis URL) each instance keeps its forecasts in its own memory only;
// with one, a forecast region or motion field computed by any instance is
// reused by all of them.
package cache

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

// Shared stores bytes under string keys for a while. Get and Set are best
// effort: an unreachable cache is a miss, never an error.
type Shared interface {
	Get(ctx context.Context, key string) ([]byte, bool)
	Set(ctx context.Context, key string, val []byte, ttl time.Duration)
}

// Backend names the cache in logs: "redis" or "memory".
func Backend(s Shared) string {
	if s == nil {
		return "memory"
	}
	return "redis"
}

// opTimeout bounds one cache call: a slow cache must not slow a forecast
// down more than computing it would.
const opTimeout = 2 * time.Second

// Open connects to the Redis server at url ("redis://" or, for Upstash,
// "rediss://"). It returns nil, meaning memory only, when url is empty or
// the server does not answer.
func Open(ctx context.Context, url string, log *slog.Logger) Shared {
	if url == "" {
		log.Info("cache", "backend", "memory")
		return nil
	}
	opt, err := redis.ParseURL(url)
	if err != nil {
		log.Warn("cache: bad REDIS_URL, using memory", "err", err)
		return nil
	}
	c := redis.NewClient(opt)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := c.Ping(ctx).Err(); err != nil {
		c.Close()
		log.Warn("cache: redis unreachable, using memory", "err", err)
		return nil
	}
	log.Info("cache", "backend", "redis", "addr", opt.Addr)
	return &Redis{c: c, log: log}
}

// Redis is a Shared cache in Redis.
type Redis struct {
	c   *redis.Client
	log *slog.Logger
}

// Get returns the value at key, false when missing or on error.
func (r *Redis) Get(ctx context.Context, key string) ([]byte, bool) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), opTimeout)
	defer cancel()
	b, err := r.c.Get(ctx, key).Bytes()
	if err != nil {
		if !errors.Is(err, redis.Nil) {
			r.log.Warn("cache get", "key", key, "err", err)
		}
		return nil, false
	}
	return b, true
}

// Set stores val at key for ttl, logging failures.
func (r *Redis) Set(ctx context.Context, key string, val []byte, ttl time.Duration) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), opTimeout)
	defer cancel()
	if err := r.c.Set(ctx, key, val, ttl).Err(); err != nil {
		r.log.Warn("cache set", "key", key, "err", err)
	}
}

package cache

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestOpenWithoutURLIsMemory(t *testing.T) {
	if c := Open(context.Background(), "", quiet); c != nil || Backend(c) != "memory" {
		t.Fatalf("Open(\"\") = %v", c)
	}
}

func TestOpenUnreachableFallsBack(t *testing.T) {
	if c := Open(context.Background(), "redis://127.0.0.1:1", quiet); c != nil {
		t.Fatal("an unreachable server should fall back to memory")
	}
	if c := Open(context.Background(), "not a url", quiet); c != nil {
		t.Fatal("a bad URL should fall back to memory")
	}
}

// Set as RAINCAST_TEST_REDIS_URL to test against a real server.
func TestRedisRoundTrip(t *testing.T) {
	url := os.Getenv("RAINCAST_TEST_REDIS_URL")
	if url == "" {
		t.Skip("RAINCAST_TEST_REDIS_URL is not set")
	}
	ctx := context.Background()
	c := Open(ctx, url, quiet)
	if Backend(c) != "redis" {
		t.Fatal("did not connect")
	}
	key := "rc:test:" + time.Now().Format(time.RFC3339Nano)
	if _, ok := c.Get(ctx, key); ok {
		t.Fatal("hit before set")
	}
	c.Set(ctx, key, []byte("v"), time.Minute)
	if v, ok := c.Get(ctx, key); !ok || string(v) != "v" {
		t.Fatalf("got %q %v", v, ok)
	}
}

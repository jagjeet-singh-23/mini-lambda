package buildlog_test

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/jagjeet-singh-23/mini-lambda/shared/buildlog"
)

func newTestRedis(t *testing.T) *redis.Client {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { client.Close() })
	return client
}

func TestStreamKeyFormat(t *testing.T) {
	if got, want := buildlog.StreamKey("job-abc"), "build_logs:job-abc"; got != want {
		t.Fatalf("StreamKey() = %q, want %q", got, want)
	}
}

func TestAppendWritesEntry(t *testing.T) {
	client := newTestRedis(t)
	ctx := context.Background()

	if err := buildlog.Append(ctx, client, "job-1", "hello world"); err != nil {
		t.Fatalf("Append: %v", err)
	}

	results, err := client.XRange(ctx, buildlog.StreamKey("job-1"), "-", "+").Result()
	if err != nil {
		t.Fatalf("XRange: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d entries, want 1", len(results))
	}
	if got := results[0].Values["log"]; got != "hello world" {
		t.Fatalf("log = %q, want %q", got, "hello world")
	}
}

func TestAppendNilClientIsNoop(t *testing.T) {
	if err := buildlog.Append(context.Background(), nil, "job-1", "msg"); err != nil {
		t.Fatalf("Append with nil client should be a no-op, got error: %v", err)
	}
}

func TestAppendCapsStreamAtMaxLen(t *testing.T) {
	client := newTestRedis(t)
	ctx := context.Background()

	for i := 0; i < 1500; i++ {
		if err := buildlog.Append(ctx, client, "job-cap", "line"); err != nil {
			t.Fatalf("Append #%d: %v", i, err)
		}
	}

	length, err := client.XLen(ctx, buildlog.StreamKey("job-cap")).Result()
	if err != nil {
		t.Fatalf("XLen: %v", err)
	}
	if length > 1000 {
		t.Fatalf("stream length = %d, want <= 1000 (MaxLen cap)", length)
	}
}

func TestExpireSetsTTL(t *testing.T) {
	client := newTestRedis(t)
	ctx := context.Background()

	if err := buildlog.Append(ctx, client, "job-2", "line"); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := buildlog.Expire(ctx, client, "job-2", time.Hour); err != nil {
		t.Fatalf("Expire: %v", err)
	}

	ttl, err := client.TTL(ctx, buildlog.StreamKey("job-2")).Result()
	if err != nil {
		t.Fatalf("TTL: %v", err)
	}
	if ttl <= 0 {
		t.Fatalf("TTL = %v, want > 0", ttl)
	}
}

func TestExpireNilClientIsNoop(t *testing.T) {
	if err := buildlog.Expire(context.Background(), nil, "job-1", time.Hour); err != nil {
		t.Fatalf("Expire with nil client should be a no-op, got error: %v", err)
	}
}

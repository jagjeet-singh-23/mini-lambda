package main

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newTestRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { client.Close() })
	return mr, client
}

func TestStreamBuildLogWritesToStream(t *testing.T) {
	_, client := newTestRedis(t)
	ctx := context.Background()

	streamBuildLog(ctx, client, "job-abc", "hello world")

	results, err := client.XRange(ctx, "build_logs:job-abc", "-", "+").Result()
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

func TestStreamBuildLogNilClientIsNoop(t *testing.T) {
	// must not panic
	streamBuildLog(context.Background(), nil, "job-abc", "msg")
}

func TestStreamBuildLogSentinelFormat(t *testing.T) {
	_, client := newTestRedis(t)
	ctx := context.Background()

	streamBuildLog(ctx, client, "job-1", "__BUILD_DONE__")
	streamBuildLog(ctx, client, "job-2", "__BUILD_FAILED__: something broke")

	done, _ := client.XRange(ctx, "build_logs:job-1", "-", "+").Result()
	if done[0].Values["log"] != "__BUILD_DONE__" {
		t.Fatalf("got %q", done[0].Values["log"])
	}

	failed, _ := client.XRange(ctx, "build_logs:job-2", "-", "+").Result()
	if failed[0].Values["log"] != "__BUILD_FAILED__: something broke" {
		t.Fatalf("got %q", failed[0].Values["log"])
	}
}

package main

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/jagjeet-singh-23/mini-lambda/services/build-service/internal/builder"
	"github.com/jagjeet-singh-23/mini-lambda/shared/buildlog"
)

func newTestRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { client.Close() })
	return mr, client
}

func TestFailBuildWritesSentinelAndExpires(t *testing.T) {
	_, client := newTestRedis(t)
	ctx := context.Background()
	deps := workerDeps{
		rc:              client,
		webhookNotifier: builder.NewWebhookNotifier(),
	}
	job := &builder.BuildJob{ID: "job-fail-1", WebhookURL: ""}

	err := failBuild(ctx, deps, job, time.Now(), "step %s failed: %v", "clone", "exit 1")
	if err == nil {
		t.Fatal("failBuild should return a non-nil error")
	}

	results, xerr := client.XRange(ctx, buildlog.StreamKey("job-fail-1"), "-", "+").Result()
	if xerr != nil {
		t.Fatalf("XRange: %v", xerr)
	}
	if len(results) != 1 {
		t.Fatalf("got %d entries, want 1", len(results))
	}
	want := "__BUILD_FAILED__: step clone failed: exit 1"
	if got := results[0].Values["log"]; got != want {
		t.Fatalf("log = %q, want %q", got, want)
	}

	ttl, ttlErr := client.TTL(ctx, buildlog.StreamKey("job-fail-1")).Result()
	if ttlErr != nil {
		t.Fatalf("TTL: %v", ttlErr)
	}
	if ttl <= 0 {
		t.Fatalf("TTL = %v, want > 0 (Expire should have been called)", ttl)
	}
}

func TestFailBuildErrorMessageMatchesFormat(t *testing.T) {
	_, client := newTestRedis(t)
	deps := workerDeps{rc: client, webhookNotifier: builder.NewWebhookNotifier()}
	job := &builder.BuildJob{ID: "job-fail-2"}

	err := failBuild(context.Background(), deps, job, time.Now(), "plain message")
	if err == nil || err.Error() != "plain message" {
		t.Fatalf("err = %v, want %q", err, "plain message")
	}
}

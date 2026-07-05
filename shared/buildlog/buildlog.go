// Package buildlog centralizes how build-service and gateway write and
// address build log lines in Redis, so both sides agree on the key format
// and neither can bypass the MaxLen cap.
package buildlog

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const maxLen = 1000

// StreamKey returns the Redis stream key for a build job's log lines.
func StreamKey(jobID string) string {
	return fmt.Sprintf("build_logs:%s", jobID)
}

// Append writes a single log line to the job's Redis stream, trimming the
// stream to maxLen entries (approximate) so a verbose build cannot grow it
// unbounded. A nil client is a no-op — callers may run without Redis
// configured.
func Append(ctx context.Context, rc *redis.Client, jobID, line string) error {
	if rc == nil {
		return nil
	}
	return rc.XAdd(ctx, &redis.XAddArgs{
		Stream: StreamKey(jobID),
		MaxLen: maxLen,
		Approx: true,
		Values: map[string]interface{}{"log": line},
	}).Err()
}

// Expire sets a TTL on the job's Redis stream so completed build logs are
// eventually reclaimed. A nil client is a no-op.
func Expire(ctx context.Context, rc *redis.Client, jobID string, ttl time.Duration) error {
	if rc == nil {
		return nil
	}
	return rc.Expire(ctx, StreamKey(jobID), ttl).Err()
}

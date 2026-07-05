package router

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jagjeet-singh-23/mini-lambda/shared/logger"
	"github.com/redis/go-redis/v9"
)

// HandleBuildLogs streams build log lines from Redis as Server-Sent Events.
// Path: GET /jobs/{job_id}/logs
func (g *Gateway) HandleBuildLogs(w http.ResponseWriter, r *http.Request) {
	jobID, ok := parseBuildLogsPath(r.URL.Path)
	if !ok {
		http.Error(w, "invalid path: expected /jobs/{job_id}/logs", http.StatusBadRequest)
		return
	}

	if g.redisClient == nil {
		http.Error(w, "log streaming unavailable", http.StatusServiceUnavailable)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ctx := r.Context()
	streamName := fmt.Sprintf("build_logs:%s", jobID)
	lastID := "0-0"
	deadline := time.Now().Add(30 * time.Minute)

	for {
		if time.Now().After(deadline) {
			return
		}
		select {
		case <-ctx.Done():
			return
		default:
		}

		streams, err := g.redisClient.XRead(ctx, &redis.XReadArgs{
			Streams: []string{streamName, lastID},
			Count:   10,
			Block:   5 * time.Second,
		}).Result()

		if err != nil {
			if err == redis.Nil {
				continue
			}
			if ctx.Err() != nil {
				return
			}
			logger.Error("SSE: Redis read error", "job_id", jobID, "error", err)
			return
		}

		if len(streams) == 0 || len(streams[0].Messages) == 0 {
			continue
		}

		for _, msg := range streams[0].Messages {
			logLine, _ := msg.Values["log"].(string)
			fmt.Fprintf(w, "data: %s\n\n", logLine)
			flusher.Flush()
			lastID = msg.ID

			if logLine == "__BUILD_DONE__" || strings.HasPrefix(logLine, "__BUILD_FAILED__:") {
				return
			}
		}
	}
}

// parseBuildLogsPath extracts job_id from /jobs/{job_id}/logs.
func parseBuildLogsPath(path string) (string, bool) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 3 || parts[0] != "jobs" || parts[2] != "logs" {
		return "", false
	}
	if parts[1] == "" {
		return "", false
	}
	return parts[1], true
}

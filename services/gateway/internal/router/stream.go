package router

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jagjeet-singh-23/mini-lambda/shared/logger"
	"github.com/redis/go-redis/v9"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true }, // Allow all origins for POC
}

// HandleStreamLogs upgrades an HTTP request to a WebSocket and streams Redis build logs.
func (g *Gateway) HandleStreamLogs(w http.ResponseWriter, r *http.Request) {
	jobID := r.URL.Query().Get("job_id")
	if jobID == "" {
		http.Error(w, "Missing job_id parameter", http.StatusBadRequest)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		logger.Error("Failed to upgrade websocket", "error", err)
		return
	}
	defer conn.Close()

	if g.redisClient == nil {
		_ = conn.WriteMessage(websocket.TextMessage, []byte("Error: Gateway Redis streaming client is not configured."))
		return
	}

	streamName := fmt.Sprintf("build_logs:%s", jobID)
	ctx := r.Context()
	lastID := "0-0"

	// Watch the Redis Stream for new logs
	for {
		select {
		case <-ctx.Done():
			return // Client disconnected
		default:
		}

		// Block for 2 seconds waiting for new log entries
		streams, err := g.redisClient.XRead(ctx, &redis.XReadArgs{
			Streams: []string{streamName, lastID},
			Count:   10,
			Block:   2 * time.Second,
		}).Result()

		if err != nil {
			if err == redis.Nil {
				continue // No new messages, keep waiting
			}
			logger.Error("Failed to read from Redis Stream", "error", err)
			return
		}

		if len(streams) > 0 && len(streams[0].Messages) > 0 {
			for _, msg := range streams[0].Messages {
				logStr := msg.Values["log"].(string)

				// Stream log payload to client
				if err := conn.WriteMessage(websocket.TextMessage, []byte(logStr)); err != nil {
					return // Client disconnected
				}
				lastID = msg.ID

				// Terminate stream if build completes or fails
				if logStr == "Build completed successfully!" || len(logStr) > 4 && logStr[:4] == "ERR:" {
					_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
					return
				}
			}
		}
	}
}

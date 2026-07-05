package build_log_streaming_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/jagjeet-singh-23/mini-lambda/tests/e2e/testsupport"
)

const (
	gatewayURL = "http://localhost:8080"
	redisAddr  = "localhost:6379"
)

func TestMain(m *testing.M) {
	flag.Parse()
	if !testing.Short() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if err := testsupport.WaitForGatewayHealthy(ctx, gatewayURL); err != nil {
			fmt.Fprintln(os.Stderr, "e2e setup failed:", err)
			os.Exit(1)
		}
	}
	os.Exit(m.Run())
}

// createFunction POSTs a create-function request to the gateway and returns
// the job_id and function_id from the 202 response.
func createFunction(t *testing.T, userID string, req map[string]interface{}) (jobID, functionID string) {
	t.Helper()

	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	httpReq, err := http.NewRequest(http.MethodPost, gatewayURL+"/functions", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-User-ID", userID) // distinct per test to avoid the gateway's 5/min build rate limit

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatalf("POST /functions: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("POST /functions: status = %d, want 202", resp.StatusCode)
	}

	var parsed map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	jobID, _ = parsed["job_id"].(string)
	functionID, _ = parsed["function_id"].(string)
	if jobID == "" || functionID == "" {
		t.Fatalf("response missing job_id/function_id: %+v", parsed)
	}
	return jobID, functionID
}

// assertBuildCompletes concurrently reads the job's SSE log stream from the
// gateway and directly XRANGEs the same Redis stream, and requires both to
// observe a terminal sentinel containing wantSentinelSubstr — so a failure
// can be localized to the write side (worker/builder) or the read side
// (gateway's SSE proxy) instead of just "no logs showed up".
func assertBuildCompletes(t *testing.T, jobID, wantSentinelSubstr string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(2)

	var sseSentinel, redisSentinel string
	var sseErr, redisErr error

	go func() {
		defer wg.Done()
		sseSentinel, sseErr = readSSEUntilSentinel(ctx, jobID)
	}()

	go func() {
		defer wg.Done()
		redisSentinel, redisErr = pollRedisUntilSentinel(ctx, jobID)
	}()

	wg.Wait()

	if sseErr != nil {
		t.Errorf("SSE side: %v", sseErr)
	} else if !strings.Contains(sseSentinel, wantSentinelSubstr) {
		t.Errorf("SSE side: sentinel = %q, want substring %q", sseSentinel, wantSentinelSubstr)
	}

	if redisErr != nil {
		t.Errorf("Redis side: %v", redisErr)
	} else if !strings.Contains(redisSentinel, wantSentinelSubstr) {
		t.Errorf("Redis side: sentinel = %q, want substring %q", redisSentinel, wantSentinelSubstr)
	}
}

func isSentinel(line string) bool {
	return line == "__BUILD_DONE__" || strings.HasPrefix(line, "__BUILD_FAILED__:")
}

// uniqueName appends a nanosecond timestamp so re-running the suite never
// collides with build-master's idempotency check, which hashes
// Name+Runtime+PackageData (or +RepoURL+Dockerfile) and returns 409 Conflict
// instead of 202 Accepted for a repeat of the same inputs.
func uniqueName(base string) string {
	return fmt.Sprintf("%s-%d", base, time.Now().UnixNano())
}

func readSSEUntilSentinel(ctx context.Context, jobID string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/jobs/%s/logs", gatewayURL, jobID), nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("GET /jobs/%s/logs: %w", jobID, err)
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if isSentinel(data) {
			return data, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("reading SSE stream: %w", err)
	}
	return "", fmt.Errorf("SSE stream closed before a terminal sentinel was seen")
}

func pollRedisUntilSentinel(ctx context.Context, jobID string) (string, error) {
	client := redis.NewClient(&redis.Options{Addr: redisAddr})
	defer client.Close()

	streamKey := "build_logs:" + jobID
	for {
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("timed out waiting for a terminal sentinel in %s: %w", streamKey, ctx.Err())
		default:
		}

		entries, err := client.XRange(ctx, streamKey, "-", "+").Result()
		if err != nil && err != redis.Nil {
			return "", fmt.Errorf("XRange %s: %w", streamKey, err)
		}

		if len(entries) > 1000 {
			return "", fmt.Errorf("stream %s has %d entries, want <= 1000 (MaxLen cap)", streamKey, len(entries))
		}

		for _, e := range entries {
			if line, ok := e.Values["log"].(string); ok && isSentinel(line) {
				return line, nil
			}
		}

		time.Sleep(1 * time.Second)
	}
}

func TestBuildLogs_Zip_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test")
	}

	packageData := testsupport.BuildZip(map[string]string{
		"index.py": "def handler(event, context):\n    return {\"ok\": True}\n",
	})

	jobID, _ := createFunction(t, "e2e-zip-success", map[string]interface{}{
		"name":         uniqueName("e2e-zip-success"),
		"runtime":      "python3.11",
		"handler":      "index.handler",
		"package_data": base64.StdEncoding.EncodeToString(packageData),
		"webhook_url":  "",
		"timeout":      30,
		"memory":       128,
	})

	assertBuildCompletes(t, jobID, "__BUILD_DONE__")
}

func TestBuildLogs_Zip_Failure(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test")
	}

	// Deliberately invalid ZIP bytes: the build pipeline never executes the
	// handler at build time (only lambda-service does that, at invoke time),
	// so the only reachable build-time failure on the zip path is package
	// validation rejecting malformed archive data.
	invalidZip := []byte("this is not a valid zip file")

	jobID, _ := createFunction(t, "e2e-zip-failure", map[string]interface{}{
		"name":         uniqueName("e2e-zip-failure"),
		"runtime":      "python3.11",
		"handler":      "index.handler",
		"package_data": base64.StdEncoding.EncodeToString(invalidZip),
		"webhook_url":  "",
		"timeout":      30,
		"memory":       128,
	})

	assertBuildCompletes(t, jobID, "__BUILD_FAILED__:")
}

func TestBuildLogs_Docker_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test")
	}

	jobID, _ := createFunction(t, "e2e-docker-success", map[string]interface{}{
		"name":        uniqueName("e2e-docker-success"),
		"runtime":     "container",
		"handler":     "",
		"repo_url":    "http://git-fixture/docker-build-repo.git",
		"dockerfile":  "",
		"webhook_url": "",
		"timeout":     30,
		"memory":      128,
	})

	assertBuildCompletes(t, jobID, "__BUILD_DONE__")
}

func TestBuildLogs_Docker_Failure(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test")
	}

	jobID, _ := createFunction(t, "e2e-docker-failure", map[string]interface{}{
		"name":        uniqueName("e2e-docker-failure"),
		"runtime":     "container",
		"handler":     "",
		"repo_url":    "http://git-fixture/docker-build-repo-broken.git",
		"dockerfile":  "",
		"webhook_url": "",
		"timeout":     30,
		"memory":      128,
	})

	assertBuildCompletes(t, jobID, "__BUILD_FAILED__:")
}

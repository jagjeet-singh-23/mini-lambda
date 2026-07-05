package router

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newGatewayWithRedis(t *testing.T, mr *miniredis.Miniredis) *Gateway {
	t.Helper()
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { client.Close() })
	return &Gateway{redisClient: client}
}

// readSSELines reads SSE data lines until the body closes or timeout.
func readSSELines(t *testing.T, resp *http.Response, timeout time.Duration) []string {
	t.Helper()
	var lines []string
	done := make(chan struct{})
	go func() {
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data: ") {
				lines = append(lines, strings.TrimPrefix(line, "data: "))
			}
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		t.Log("readSSELines: timed out waiting for stream to close")
	}
	return lines
}

func TestParseBuildLogsPath(t *testing.T) {
	cases := []struct {
		path   string
		wantID string
		wantOK bool
	}{
		{"/jobs/abc-123/logs", "abc-123", true},
		{"/jobs/abc-123/logs/", "abc-123", true}, // trailing slash is accepted
		{"/jobs//logs", "", false},
		{"/jobs/abc-123", "", false},
		{"/jobs/", "", false},
		{"/other/abc/logs", "", false},
	}
	for _, tc := range cases {
		id, ok := parseBuildLogsPath(tc.path)
		if ok != tc.wantOK || id != tc.wantID {
			t.Errorf("parseBuildLogsPath(%q) = (%q, %v), want (%q, %v)", tc.path, id, ok, tc.wantID, tc.wantOK)
		}
	}
}

func TestHandleBuildLogsInvalidPath(t *testing.T) {
	mr := miniredis.RunT(t)
	g := newGatewayWithRedis(t, mr)

	for _, path := range []string{"/jobs/", "/jobs/abc", "/jobs/abc/logs/extra"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		g.HandleBuildLogs(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("path %q: status = %d, want 400", path, w.Code)
		}
	}
}

func TestHandleBuildLogsNilRedis(t *testing.T) {
	g := &Gateway{redisClient: nil}
	req := httptest.NewRequest(http.MethodGet, "/jobs/job-1/logs", nil)
	w := httptest.NewRecorder()
	g.HandleBuildLogs(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
}

func TestHandleBuildLogsSSEHeadersSet(t *testing.T) {
	mr := miniredis.RunT(t)
	mr.XAdd("build_logs:job-h", "*", []string{"log", "__BUILD_DONE__"})
	g := newGatewayWithRedis(t, mr)

	srv := httptest.NewServer(http.HandlerFunc(g.HandleBuildLogs))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/jobs/job-h/logs")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	readSSELines(t, resp, 3*time.Second)

	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}
	if resp.Header.Get("Cache-Control") != "no-cache" {
		t.Fatalf("Cache-Control = %q, want no-cache", resp.Header.Get("Cache-Control"))
	}
}

func TestHandleBuildLogsStreamsLinesAndClosesOnDone(t *testing.T) {
	mr := miniredis.RunT(t)
	mr.XAdd("build_logs:job-1", "*", []string{"log", "starting build"})
	mr.XAdd("build_logs:job-1", "*", []string{"log", "step 1 done"})
	mr.XAdd("build_logs:job-1", "*", []string{"log", "__BUILD_DONE__"})
	g := newGatewayWithRedis(t, mr)

	srv := httptest.NewServer(http.HandlerFunc(g.HandleBuildLogs))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/jobs/job-1/logs")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	lines := readSSELines(t, resp, 5*time.Second)
	want := []string{"starting build", "step 1 done", "__BUILD_DONE__"}
	if len(lines) != len(want) {
		t.Fatalf("got lines %v, want %v", lines, want)
	}
	for i, w := range want {
		if lines[i] != w {
			t.Fatalf("line[%d] = %q, want %q", i, lines[i], w)
		}
	}
}

func TestHandleBuildLogsClosesOnFailed(t *testing.T) {
	mr := miniredis.RunT(t)
	mr.XAdd("build_logs:job-2", "*", []string{"log", "ERR: docker build failed"})
	mr.XAdd("build_logs:job-2", "*", []string{"log", "__BUILD_FAILED__: Docker build failed: exit 1"})
	g := newGatewayWithRedis(t, mr)

	srv := httptest.NewServer(http.HandlerFunc(g.HandleBuildLogs))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/jobs/job-2/logs")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	lines := readSSELines(t, resp, 5*time.Second)
	if len(lines) == 0 {
		t.Fatal("expected at least one line")
	}
	last := lines[len(lines)-1]
	if !strings.HasPrefix(last, "__BUILD_FAILED__:") {
		t.Fatalf("last line = %q, want __BUILD_FAILED__: prefix", last)
	}
}

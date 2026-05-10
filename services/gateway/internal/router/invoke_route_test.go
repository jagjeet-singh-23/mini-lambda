package router

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jagjeet-singh-23/mini-lambda/services/gateway/internal/circuitbreaker"
)

type recordingLimiter struct {
	mu      sync.Mutex
	keys    []string
	allowed bool
	err     error
}

func (r *recordingLimiter) Allow(ctx context.Context, key string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.keys = append(r.keys, key)
	if r.err != nil {
		return false, r.err
	}
	return r.allowed, nil
}

func (r *recordingLimiter) Close() error { return nil }

func (r *recordingLimiter) lastKey() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.keys) == 0 {
		return ""
	}
	return r.keys[len(r.keys)-1]
}

type downstreamRequest struct {
	method string
	path   string
	body   string
}

func newTestGateway(t *testing.T, downstream http.HandlerFunc, limiter *recordingLimiter) (*Gateway, *httptest.Server, *httptest.Server) {
	t.Helper()

	downstreamServer := httptest.NewServer(downstream)
	t.Cleanup(downstreamServer.Close)

	buildLimiter := &recordingLimiter{allowed: true}
	cbRegistry := circuitbreaker.NewRegistry()
	cbRegistry.Register("lambda-service", 10, time.Second)
	cbRegistry.Register("build-service", 10, time.Second)

	g := NewGateway(ServiceConfig{
		LambdaServiceURL: downstreamServer.URL,
		BuildServiceURL:  downstreamServer.URL,
		Timeout:          2 * time.Second,
	}, limiter, buildLimiter, cbRegistry, nil)

	gatewayServer := httptest.NewServer(g.SetupRoutes())
	t.Cleanup(gatewayServer.Close)

	return g, gatewayServer, downstreamServer
}

func TestInvokeFunctionForwardsOriginalBodyAndRateLimitsByPathID(t *testing.T) {
	limiter := &recordingLimiter{allowed: true}
	received := make(chan downstreamRequest, 1)

	_, gatewayServer, _ := newTestGateway(t, func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("downstream read body: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		received <- downstreamRequest{method: r.Method, path: r.URL.Path, body: string(body)}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}, limiter)

	payload := `{"name":"Mini-Lambda","nested":{"value":42}}`
	resp, err := http.Post(gatewayServer.URL+"/functions/fn-123/invoke", "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("post invoke: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	select {
	case req := <-received:
		if req.method != http.MethodPost {
			t.Fatalf("downstream method = %s, want POST", req.method)
		}
		if req.path != "/functions/fn-123/invoke" {
			t.Fatalf("downstream path = %s, want /functions/fn-123/invoke", req.path)
		}
		if req.body != payload {
			t.Fatalf("downstream body = %q, want %q", req.body, payload)
		}
	case <-time.After(time.Second):
		t.Fatal("downstream did not receive request")
	}

	if got := limiter.lastKey(); got != "fn-123" {
		t.Fatalf("rate-limit key = %q, want fn-123", got)
	}
}

func TestInvokeFunctionRejectsWrongMethod(t *testing.T) {
	limiter := &recordingLimiter{allowed: true}
	_, gatewayServer, _ := newTestGateway(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("downstream should not be called")
	}, limiter)

	req, err := http.NewRequest(http.MethodGet, gatewayServer.URL+"/functions/fn-123/invoke", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusMethodNotAllowed)
	}
	if got := limiter.lastKey(); got != "" {
		t.Fatalf("rate limiter was called with %q", got)
	}
}

func TestInvokeFunctionRejectsMalformedPath(t *testing.T) {
	limiter := &recordingLimiter{allowed: true}
	_, gatewayServer, _ := newTestGateway(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("downstream should not be called")
	}, limiter)

	resp, err := http.Post(gatewayServer.URL+"/functions/fn-123/invoke/extra", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("post malformed invoke: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 400 or 404", resp.StatusCode)
	}
	if got := limiter.lastKey(); got != "" {
		t.Fatalf("rate limiter was called with %q", got)
	}
}

func TestLegacyInvokePathIsNotSuccessPath(t *testing.T) {
	limiter := &recordingLimiter{allowed: true}
	_, gatewayServer, _ := newTestGateway(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("downstream should not be called for legacy path")
	}, limiter)

	resp, err := http.Post(gatewayServer.URL+"/invoke", "application/json", strings.NewReader(`{"function_id":"fn-123","payload":{}}`))
	if err != nil {
		t.Fatalf("post legacy invoke: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		t.Fatalf("legacy /invoke returned success status %d", resp.StatusCode)
	}
}

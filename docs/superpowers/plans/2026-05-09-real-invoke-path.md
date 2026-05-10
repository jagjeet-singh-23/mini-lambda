# Real Invoke Path Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the stub synchronous invoke path with a real, tested `POST /functions/{function_id}/invoke` flow.

**Architecture:** Gateway extracts the function ID from the path, rate-limits by that ID, and forwards the original body unchanged. Lambda-service owns invocation semantics through a small `internal/invoke` handler package with injectable function-service and runtime-manager interfaces.

**Tech Stack:** Go `net/http`, `httptest`, existing `ratelimit`, `circuitbreaker`, `domain.FunctionService`, `domain.RuntimeManager`, k6 load scripts.

---

## File Structure

- Modify `services/gateway/internal/router/routes.go`
  - Register `POST /functions/{id}/invoke` subtree before generic `/functions` handling.
- Modify `services/gateway/internal/router/gateway.go`
  - Add path-based invoke handler and path parser.
  - Keep forwarding body-preserving by avoiding pre-read/decode in the invoke handler.
- Create `services/gateway/internal/router/invoke_route_test.go`
  - Gateway route, body preservation, rate-limit key, method rejection, and legacy path tests.
- Create `services/gateway/internal/router/invoke_route_benchmark_test.go`
  - Gateway forwarding allocation/latency benchmark with small and medium payloads.
- Create `services/lambda-service/internal/invoke/handler.go`
  - Real lambda-service invoke handler with request size limit, execution, response mapping, and save-execution best effort.
- Create `services/lambda-service/internal/invoke/handler_test.go`
  - Handler correctness tests with fakes.
- Create `services/lambda-service/internal/invoke/handler_benchmark_test.go`
  - No-op fake runtime benchmark.
- Modify `services/lambda-service/cmd/main.go`
  - Wire `internal/invoke.Handler` into `http.HandleFunc("/functions/", ...)`.
  - Remove or stop registering the stub `/invoke` success path.
- Modify `README.md`
  - Document `POST /functions/{function_id}/invoke`.
- Modify `docs/README_MICROSERVICES.md`
  - Document the new invoke path.
- Modify `infrastructure/load-testing/k100_multi_tenant.js`
  - Call the new path.

---

### Task 1: Gateway Failing Tests For Path Invoke

**Files:**
- Create: `services/gateway/internal/router/invoke_route_test.go`
- Modify later: `services/gateway/internal/router/routes.go`
- Modify later: `services/gateway/internal/router/gateway.go`

- [ ] **Step 1: Write the failing gateway route tests**

Create `services/gateway/internal/router/invoke_route_test.go`:

```go
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
```

- [ ] **Step 2: Run the gateway tests and verify they fail**

Run:

```bash
cd services/gateway
go test ./internal/router -run 'TestInvokeFunction|TestLegacyInvokePath' -count=1 -v
```

Expected: FAIL. At least the new path test should fail because `/functions/fn-123/invoke` is not registered, and the legacy path currently still routes through `/invoke`.

- [ ] **Step 3: Commit failing tests**

```bash
git add services/gateway/internal/router/invoke_route_test.go
git commit -m "test: capture path-based gateway invoke contract"
```

---

### Task 2: Gateway Path Invoke Implementation

**Files:**
- Modify: `services/gateway/internal/router/routes.go`
- Modify: `services/gateway/internal/router/gateway.go`
- Test: `services/gateway/internal/router/invoke_route_test.go`

- [ ] **Step 1: Add path parser and path invoke handler**

In `services/gateway/internal/router/gateway.go`, add `strings` to the imports:

```go
import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jagjeet-singh-23/mini-lambda/services/gateway/internal/circuitbreaker"
	"github.com/jagjeet-singh-23/mini-lambda/services/gateway/internal/ratelimit"
	"github.com/jagjeet-singh-23/mini-lambda/shared/logger"
	"github.com/redis/go-redis/v9"
)
```

Then add these functions near `HandleInvoke`:

```go
func parseInvokeFunctionID(path string) (string, bool) {
	const prefix = "/functions/"
	const suffix = "/invoke"

	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return "", false
	}

	functionID := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
	functionID = strings.Trim(functionID, "/")
	if functionID == "" || strings.Contains(functionID, "/") {
		return "", false
	}

	return functionID, true
}

func (g *Gateway) HandleInvokeFunction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	functionID, ok := parseInvokeFunctionID(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}

	allowed, err := g.invokeLimiter.Allow(r.Context(), functionID)
	if err != nil {
		logger.Error("Rate limiter error", "error", err, "function_id", functionID)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	if !allowed {
		w.Header().Set("X-RateLimit-Retry-After", "1")
		http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
		return
	}

	targetURL := g.config.LambdaServiceURL + r.URL.Path
	g.forwardRequest(w, r, targetURL, "lambda-service")
}
```

- [ ] **Step 2: Register the new route and remove legacy `/invoke`**

In `services/gateway/internal/router/routes.go`, update `SetupRoutes`:

```go
func (g *Gateway) SetupRoutes() *http.ServeMux {
	mux := http.NewServeMux()

	// Function execution
	mux.HandleFunc("/functions/", g.HandleInvokeFunction)

	// Function management
	mux.HandleFunc("/functions", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			g.HandleCreateFunction(w, r)
		case http.MethodGet:
			if r.URL.Query().Get("id") != "" {
				g.HandleGetFunction(w, r)
			} else {
				g.HandleListFunctions(w, r)
			}
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})

	// Streaming Build Logs WebSocket
	mux.HandleFunc("/stream", g.HandleStreamLogs)

	// Health check
	mux.HandleFunc("/health", g.HandleHealth)

	// Metrics endpoint
	mux.Handle("/metrics", promhttp.Handler())

	return mux
}
```

- [ ] **Step 3: Run the gateway route tests**

Run:

```bash
cd services/gateway
go test ./internal/router -run 'TestInvokeFunction|TestLegacyInvokePath' -count=1 -v
```

Expected: PASS.

- [ ] **Step 4: Run all gateway tests**

Run:

```bash
cd services/gateway
go test ./... -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit gateway implementation**

```bash
git add services/gateway/internal/router/routes.go services/gateway/internal/router/gateway.go
git commit -m "feat: route gateway invokes by function path"
```

---

### Task 3: Lambda Invoke Handler Failing Tests

**Files:**
- Create: `services/lambda-service/internal/invoke/handler_test.go`
- Modify later: `services/lambda-service/internal/invoke/handler.go`

- [ ] **Step 1: Write the failing lambda handler tests**

Create `services/lambda-service/internal/invoke/handler_test.go`:

```go
package invoke

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jagjeet-singh-23/mini-lambda/shared/domain"
)

type fakeFunctionService struct {
	fn       *domain.Function
	getErr   error
	saveErr  error
	saved    []*domain.Execution
	lastID   string
}

func (f *fakeFunctionService) GetFunction(ctx context.Context, id string) (*domain.Function, error) {
	f.lastID = id
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.fn, nil
}

func (f *fakeFunctionService) SaveExecution(ctx context.Context, execution *domain.Execution) error {
	f.saved = append(f.saved, execution)
	return f.saveErr
}

type fakeRuntimeManager struct {
	result      *domain.ExecutionResult
	err         error
	lastFunction *domain.Function
	lastInput    []byte
}

func (f *fakeRuntimeManager) Execute(ctx context.Context, fn *domain.Function, input []byte) (*domain.ExecutionResult, error) {
	f.lastFunction = fn
	f.lastInput = append([]byte(nil), input...)
	if f.err != nil {
		return nil, f.err
	}
	return f.result, nil
}

func newFunction() *domain.Function {
	return &domain.Function{
		ID:      "fn-123",
		Name:    "hello",
		Runtime: "python3.11",
		Handler: "handler",
		Code:    []byte("def handler(event, ctx): return event"),
		Timeout: time.Second,
		Memory:  128,
	}
}

func performInvoke(handler http.HandlerFunc, path string, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}

func TestHandlerExecutesFunctionWithRawPayload(t *testing.T) {
	functions := &fakeFunctionService{fn: newFunction()}
	runtime := &fakeRuntimeManager{result: &domain.ExecutionResult{
		Output:       []byte(`{"message":"hello"}`),
		Logs:         []byte("log line"),
		ExitCode:     0,
		WasWarmStart: true,
		MemoryUsed:   1024,
	}}
	handler := NewHandler(functions, runtime, 1024)

	payload := []byte(`{"name":"World"}`)
	rec := performInvoke(handler.HandleInvoke, "/functions/fn-123/invoke", payload)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if functions.lastID != "fn-123" {
		t.Fatalf("loaded function id = %q, want fn-123", functions.lastID)
	}
	if string(runtime.lastInput) != string(payload) {
		t.Fatalf("runtime input = %q, want %q", runtime.lastInput, payload)
	}
	if len(functions.saved) != 1 {
		t.Fatalf("saved executions = %d, want 1", len(functions.saved))
	}

	var response Response
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if response.FunctionID != "fn-123" {
		t.Fatalf("response function_id = %q, want fn-123", response.FunctionID)
	}
	if response.ExitCode != 0 {
		t.Fatalf("response exit_code = %d, want 0", response.ExitCode)
	}
	if !response.WarmStart {
		t.Fatal("response warm_start = false, want true")
	}
	if string(response.Output) != `{"message":"hello"}` {
		t.Fatalf("response output = %s", response.Output)
	}
}

func TestHandlerReturnsNotFoundForMissingFunction(t *testing.T) {
	handler := NewHandler(&fakeFunctionService{getErr: domain.ErrFunctionNotFound}, &fakeRuntimeManager{}, 1024)

	rec := performInvoke(handler.HandleInvoke, "/functions/missing/invoke", []byte(`{}`))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestHandlerReturnsPayloadTooLarge(t *testing.T) {
	handler := NewHandler(&fakeFunctionService{fn: newFunction()}, &fakeRuntimeManager{}, 4)

	rec := performInvoke(handler.HandleInvoke, "/functions/fn-123/invoke", []byte(`{"too":"large"}`))

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestHandlerMapsRuntimeTimeoutToGatewayTimeout(t *testing.T) {
	handler := NewHandler(&fakeFunctionService{fn: newFunction()}, &fakeRuntimeManager{err: context.DeadlineExceeded}, 1024)

	rec := performInvoke(handler.HandleInvoke, "/functions/fn-123/invoke", []byte(`{}`))

	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusGatewayTimeout)
	}
}

func TestHandlerMapsRuntimeErrorToInternalServerError(t *testing.T) {
	handler := NewHandler(&fakeFunctionService{fn: newFunction()}, &fakeRuntimeManager{err: errors.New("runtime failed")}, 1024)

	rec := performInvoke(handler.HandleInvoke, "/functions/fn-123/invoke", []byte(`{}`))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestHandlerReturnsOKForNonZeroFunctionExit(t *testing.T) {
	handler := NewHandler(&fakeFunctionService{fn: newFunction()}, &fakeRuntimeManager{result: &domain.ExecutionResult{
		Output:   []byte(`{"error":"bad input"}`),
		Logs:     []byte("function error"),
		ExitCode: 2,
	}}, 1024)

	rec := performInvoke(handler.HandleInvoke, "/functions/fn-123/invoke", []byte(`{}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var response Response
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if response.ExitCode != 2 {
		t.Fatalf("exit_code = %d, want 2", response.ExitCode)
	}
}

func TestHandlerDoesNotFailWhenSaveExecutionFails(t *testing.T) {
	handler := NewHandler(&fakeFunctionService{
		fn:      newFunction(),
		saveErr: errors.New("db unavailable"),
	}, &fakeRuntimeManager{result: &domain.ExecutionResult{
		Output:   []byte(`{"ok":true}`),
		ExitCode: 0,
	}}, 1024)

	rec := performInvoke(handler.HandleInvoke, "/functions/fn-123/invoke", []byte(`{}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
}
```

- [ ] **Step 2: Run the lambda handler tests and verify they fail**

Run:

```bash
cd services/lambda-service
go test ./internal/invoke -count=1 -v
```

Expected: FAIL because package `internal/invoke` and `NewHandler` do not exist yet.

- [ ] **Step 3: Commit failing tests**

```bash
git add services/lambda-service/internal/invoke/handler_test.go
git commit -m "test: capture lambda invoke handler contract"
```

---

### Task 4: Lambda Invoke Handler Implementation

**Files:**
- Create: `services/lambda-service/internal/invoke/handler.go`
- Test: `services/lambda-service/internal/invoke/handler_test.go`

- [ ] **Step 1: Implement the invoke handler**

Create `services/lambda-service/internal/invoke/handler.go`:

```go
package invoke

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jagjeet-singh-23/mini-lambda/shared/domain"
	"github.com/jagjeet-singh-23/mini-lambda/shared/logger"
)

type FunctionService interface {
	GetFunction(ctx context.Context, id string) (*domain.Function, error)
	SaveExecution(ctx context.Context, execution *domain.Execution) error
}

type RuntimeManager interface {
	Execute(ctx context.Context, function *domain.Function, input []byte) (*domain.ExecutionResult, error)
}

type Handler struct {
	functions    FunctionService
	runtime      RuntimeManager
	maxBodyBytes int64
}

type Response struct {
	FunctionID string          `json:"function_id"`
	Output     json.RawMessage `json:"output"`
	Logs       string          `json:"logs,omitempty"`
	ExitCode   int             `json:"exit_code"`
	WarmStart  bool            `json:"warm_start"`
	DurationMS int64           `json:"duration_ms"`
}

func NewHandler(functions FunctionService, runtime RuntimeManager, maxBodyBytes int64) *Handler {
	if maxBodyBytes <= 0 {
		maxBodyBytes = 1024 * 1024
	}
	return &Handler{
		functions:    functions,
		runtime:      runtime,
		maxBodyBytes: maxBodyBytes,
	}
}

func (h *Handler) HandleInvoke(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	functionID, ok := parseFunctionID(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}

	payload, err := readLimitedBody(r.Body, h.maxBodyBytes)
	if err != nil {
		http.Error(w, "Payload too large", http.StatusRequestEntityTooLarge)
		return
	}

	function, err := h.functions.GetFunction(r.Context(), functionID)
	if err != nil {
		if errors.Is(err, domain.ErrFunctionNotFound) {
			http.Error(w, "Function not found", http.StatusNotFound)
			return
		}
		logger.Error("Failed to load function", "function_id", functionID, "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	start := time.Now()
	result, err := h.runtime.Execute(r.Context(), function, payload)
	duration := time.Since(start)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			http.Error(w, "Execution timeout", http.StatusGatewayTimeout)
			return
		}
		logger.Error("Runtime execution failed", "function_id", functionID, "error", err)
		http.Error(w, "Runtime execution failed", http.StatusInternalServerError)
		return
	}
	if result == nil {
		logger.Error("Runtime returned nil result", "function_id", functionID)
		http.Error(w, "Runtime execution failed", http.StatusInternalServerError)
		return
	}

	execution := domain.NewExecution(function.ID, payload)
	execution.StartedAt = start
	execution.MarkSuccess(result.Output)
	execution.MemoryUsed = result.MemoryUsed
	execution.IsWarmStart = result.WasWarmStart
	if err := h.functions.SaveExecution(r.Context(), execution); err != nil {
		logger.Error("Failed to save execution", "function_id", functionID, "error", err)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(Response{
		FunctionID: functionID,
		Output:     normalizeOutput(result.Output),
		Logs:       string(result.Logs),
		ExitCode:   result.ExitCode,
		WarmStart:  result.WasWarmStart,
		DurationMS: duration.Milliseconds(),
	})
}

func parseFunctionID(path string) (string, bool) {
	const prefix = "/functions/"
	const suffix = "/invoke"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return "", false
	}
	id := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
	id = strings.Trim(id, "/")
	if id == "" || strings.Contains(id, "/") {
		return "", false
	}
	return id, true
}

func readLimitedBody(body io.Reader, maxBytes int64) ([]byte, error) {
	limited := io.LimitReader(body, maxBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, http.ErrBodyReadAfterClose
	}
	return data, nil
}

func normalizeOutput(output []byte) json.RawMessage {
	trimmed := strings.TrimSpace(string(output))
	if trimmed == "" {
		return json.RawMessage(`null`)
	}
	if json.Valid([]byte(trimmed)) {
		return json.RawMessage(trimmed)
	}
	encoded, err := json.Marshal(trimmed)
	if err != nil {
		return json.RawMessage(`null`)
	}
	return encoded
}
```

- [ ] **Step 2: Run the lambda handler tests**

Run:

```bash
cd services/lambda-service
go test ./internal/invoke -count=1 -v
```

Expected: PASS.

- [ ] **Step 3: Commit lambda handler implementation**

```bash
git add services/lambda-service/internal/invoke/handler.go
git commit -m "feat: add real lambda invoke handler"
```

---

### Task 5: Wire Lambda-Service HTTP Route

**Files:**
- Modify: `services/lambda-service/cmd/main.go`
- Test: `services/lambda-service/internal/invoke/handler_test.go`

- [ ] **Step 1: Import the invoke package**

In `services/lambda-service/cmd/main.go`, add:

```go
	"github.com/jagjeet-singh-23/mini-lambda/services/lambda-service/internal/invoke"
```

- [ ] **Step 2: Replace the stub invoke route with the real path route**

In `services/lambda-service/cmd/main.go`, replace the existing `/invoke` stub handler block:

```go
	http.HandleFunc("/invoke", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Lambda Service - Invoke endpoint"))
	})
```

With:

```go
	invokeHandler := invoke.NewHandler(functionService, runtimeManager, 1024*1024)
	http.HandleFunc("/functions/", invokeHandler.HandleInvoke)
```

- [ ] **Step 3: Run lambda-service tests**

Run:

```bash
cd services/lambda-service
go test ./... -count=1
```

Expected: PASS.

- [ ] **Step 4: Build lambda-service**

Run:

```bash
cd services/lambda-service
go build ./cmd
```

Expected: command exits successfully.

- [ ] **Step 5: Commit lambda-service wiring**

```bash
git add services/lambda-service/cmd/main.go
git commit -m "feat: wire lambda invoke route"
```

---

### Task 6: Gateway And Lambda Benchmarks

**Files:**
- Create: `services/gateway/internal/router/invoke_route_benchmark_test.go`
- Create: `services/lambda-service/internal/invoke/handler_benchmark_test.go`

- [ ] **Step 1: Add gateway forwarding benchmark**

Create `services/gateway/internal/router/invoke_route_benchmark_test.go`:

```go
package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jagjeet-singh-23/mini-lambda/services/gateway/internal/circuitbreaker"
)

func BenchmarkInvokeFunctionForwarding(b *testing.B) {
	benchmarks := []struct {
		name string
		body string
	}{
		{name: "small", body: `{"name":"bench"}`},
		{name: "medium", body: strings.Repeat("x", 64*1024)},
	}

	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"ok":true}`))
			}))
			defer downstream.Close()

			limiter := &recordingLimiter{allowed: true}
			cbRegistry := circuitbreaker.NewRegistry()
			cbRegistry.Register("lambda-service", 10, time.Second)
			cbRegistry.Register("build-service", 10, time.Second)
			g := NewGateway(ServiceConfig{
				LambdaServiceURL: downstream.URL,
				BuildServiceURL:  downstream.URL,
				Timeout:          2 * time.Second,
			}, limiter, &recordingLimiter{allowed: true}, cbRegistry, nil)

			server := httptest.NewServer(g.SetupRoutes())
			defer server.Close()

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				resp, err := http.Post(server.URL+"/functions/fn-bench/invoke", "application/json", strings.NewReader(bm.body))
				if err != nil {
					b.Fatalf("post: %v", err)
				}
				_ = resp.Body.Close()
			}
		})
	}
}
```

- [ ] **Step 2: Add lambda handler benchmark**

Create `services/lambda-service/internal/invoke/handler_benchmark_test.go`:

```go
package invoke

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jagjeet-singh-23/mini-lambda/shared/domain"
)

func BenchmarkHandlerInvokeNoopRuntime(b *testing.B) {
	functions := &fakeFunctionService{fn: newFunction()}
	runtime := &fakeRuntimeManager{result: &domain.ExecutionResult{
		Output:       []byte(`{"ok":true}`),
		ExitCode:     0,
		WasWarmStart: true,
	}}
	handler := NewHandler(functions, runtime, 1024*1024)
	payload := []byte(`{"name":"bench"}`)

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodPost, "/functions/fn-123/invoke", bytes.NewReader(payload))
		rec := httptest.NewRecorder()
		handler.HandleInvoke(rec, req)
		if rec.Code != http.StatusOK {
			b.Fatalf("status = %d", rec.Code)
		}
	}
}
```

- [ ] **Step 3: Run benchmarks**

Run:

```bash
cd services/gateway
go test ./internal/router -bench BenchmarkInvokeFunctionForwarding -benchmem -run '^$'
cd ../lambda-service
go test ./internal/invoke -bench BenchmarkHandlerInvokeNoopRuntime -benchmem -run '^$'
```

Expected: both benchmark commands complete and print `ns/op`, `B/op`, and `allocs/op`.

- [ ] **Step 4: Commit benchmarks**

```bash
git add services/gateway/internal/router/invoke_route_benchmark_test.go services/lambda-service/internal/invoke/handler_benchmark_test.go
git commit -m "test: benchmark real invoke path overhead"
```

---

### Task 7: Update Load Script And Documentation

**Files:**
- Modify: `infrastructure/load-testing/k100_multi_tenant.js`
- Modify: `README.md`
- Modify: `docs/README_MICROSERVICES.md`

- [ ] **Step 1: Update k6 invoke path**

In `infrastructure/load-testing/k100_multi_tenant.js`, replace:

```js
const res = http.post(`${BASE_URL}/invoke?id=${randomFunctionId}`, payload, params);
```

With:

```js
const res = http.post(`${BASE_URL}/functions/${randomFunctionId}/invoke`, payload, params);
```

- [ ] **Step 2: Update README invoke example**

In `README.md`, replace the invoke curl block with:

```bash
curl -X POST http://localhost:8080/functions/<RETURNED_FUNCTION_ID>/invoke \
  -H "Content-Type: application/json" \
  -d '{"name": "World"}'
```

- [ ] **Step 3: Update microservices docs invoke endpoint**

In `docs/README_MICROSERVICES.md`, replace the gateway invoke API section with:

```text
POST /functions/{function_id}/invoke
{
  "key": "value"
}
```

- [ ] **Step 4: Verify no stale load-test invoke path remains**

Run:

```bash
rg -n "/invoke\\?id=|POST /invoke|localhost:8080/invoke" README.md docs infrastructure tests services
```

Expected: no matches for the legacy success-path examples. Mentions in old analysis/spec documents are acceptable only if they explicitly describe legacy behavior.

- [ ] **Step 5: Commit docs and load script**

```bash
git add infrastructure/load-testing/k100_multi_tenant.js README.md docs/README_MICROSERVICES.md
git commit -m "docs: document path-based function invoke"
```

---

### Task 8: Full Verification

**Files:**
- All files touched by previous tasks.

- [ ] **Step 1: Run gateway tests**

```bash
cd services/gateway
go test ./... -count=1
```

Expected: PASS.

- [ ] **Step 2: Run lambda-service tests**

```bash
cd services/lambda-service
go test ./... -count=1
```

Expected: PASS.

- [ ] **Step 3: Run shared tests**

```bash
cd shared
go test ./... -count=1
```

Expected: PASS.

- [ ] **Step 4: Build gateway and lambda-service**

```bash
cd services/gateway
go build ./cmd
cd ../lambda-service
go build ./cmd
```

Expected: both builds exit successfully.

- [ ] **Step 5: Run benchmark smoke checks**

```bash
cd services/gateway
go test ./internal/router -bench BenchmarkInvokeFunctionForwarding/small -benchmem -run '^$'
cd ../lambda-service
go test ./internal/invoke -bench BenchmarkHandlerInvokeNoopRuntime -benchmem -run '^$'
```

Expected: both benchmark commands complete and report allocation data.

- [ ] **Step 6: Final status check**

```bash
git status --short
```

Expected: clean working tree.

---

## Self-Review Notes

- Spec coverage: This plan covers the path invoke contract, gateway body preservation, lambda-service real execution handler, error mapping, failing-first tests, benchmarks, load-test path update, and docs update.
- Deferred backlog coverage: Pool races, eviction, admission control, circuit breaker semantics, RabbitMQ retries, artifact caching, runtime protocol redesign, Redis degraded mode, build idempotency, metrics cardinality, and deployment tuning remain intentionally out of scope and are tracked in `docs/superpowers/specs/2026-05-09-real-invoke-path-design.md`.
- Type consistency: Gateway tests use the existing `ratelimit.RateLimiter` shape. Lambda handler uses local interfaces compatible with `domain.FunctionService` and `domain.RuntimeManager.Execute`.

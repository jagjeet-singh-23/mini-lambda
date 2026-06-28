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
	fn      *domain.Function
	getErr  error
	saveErr error
	saved   []*domain.Execution
	lastID  string
}

func (f *fakeFunctionService) GetFunctionMeta(ctx context.Context, id string) (*domain.Function, error) {
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
	result       *domain.ExecutionResult
	err          error
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

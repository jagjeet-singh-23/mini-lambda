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
	go func() {
		if err := h.functions.SaveExecution(context.Background(), execution); err != nil {
			logger.Error("Failed to save execution", "function_id", functionID, "error", err)
		}
	}()

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

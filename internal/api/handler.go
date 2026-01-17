package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/jagjeet-singh-23/mini-lambda/internal/domain"
	"github.com/jagjeet-singh-23/mini-lambda/pkg/utils"
)

// Handler handles HTTP requests for Lambda API
type Handler struct {
	// runtimeManager executes functions
	runtimeManager domain.RuntimeManager

	// functionService stores the function definitions
	functionService *domain.FunctionService
}

// NewHandler creates a new API handler
func NewHandler(
	runtimeManager domain.RuntimeManager,
	functionService *domain.FunctionService,
) *Handler {
	return &Handler{
		runtimeManager:  runtimeManager,
		functionService: functionService,
	}
}

// CreateFunctionRequest represents the request body for creating a function
type CreateFunctionRequest struct {
	Name        string            `json:"name"`
	Runtime     string            `json:"runtime"`
	Handler     string            `json:"handler"`
	Code        string            `json:"code"`    // base64 encoded
	Timeout     int               `json:"timeout"` // seconds
	Memory      int64             `json:"memory"`  // MB
	Environment map[string]string `json:"environment"`
}

// CreateFunctionResponse represents the response after creating a function
type CreateFunctionResponse struct {
	FunctionID string    `json:"function_id"`
	Name       string    `json:"name"`
	Runtime    string    `json:"runtime"`
	CreatedAt  time.Time `json:"created_at"`
}

// InvokeFunctionRequest represents the request body for invoking a function
type InvokeFunctionRequest struct {
	Payload json.RawMessage `json:"payload"`
}

// InvokeFunctionResponse represents the response after invoking a function
type InvokeFunctionResponse struct {
	ExecutionID string          `json:"execution_id"`
	Status      string          `json:"status"`
	Output      json.RawMessage `json:"output,omitempty"`
	Logs        string          `json:"logs,omitempty"`
	Duration    int64           `json:"duration_ms"`
	MemoryUsed  int64           `json:"memory_used_bytes"`
	Error       string          `json:"error,omitempty"`
}

// ErrorResponse represents an error response
type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

func (h *Handler) validateAndNormalizeCreateRequest(
	w http.ResponseWriter,
	r *http.Request,
) (*CreateFunctionRequest, bool) {
	if r.Method != http.MethodPost {
		h.respondError(
			w,
			http.StatusMethodNotAllowed,
			"method_not_allowed",
			"Only POST method allowed",
		)
		return nil, false
	}

	var req CreateFunctionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.respondError(
			w,
			http.StatusBadRequest,
			"invalid_request",
			"Failed to parse request body",
		)
		return nil, false
	}

	if req.Name == "" {
		h.respondError(
			w,
			http.StatusBadRequest,
			"invalid_request",
			"Function name is required",
		)
		return nil, false
	}

	if req.Runtime == "" {
		h.respondError(
			w,
			http.StatusBadRequest,
			"invalid_request",
			"Runtime is required",
		)
		return nil, false
	}

	if req.Code == "" {
		h.respondError(
			w,
			http.StatusBadRequest,
			"invalid_request",
			"Function  code is required",
		)
		return nil, false
	}

	// Set default values
	if req.Timeout == 0 {
		req.Timeout = 30
	}

	if req.Memory == 0 {
		req.Memory = 128
	}

	if req.Handler == "" {
		req.Handler = "handler"
	}

	return &req, true
}

func (h *Handler) CreateFunction(w http.ResponseWriter, r *http.Request) {
	req, ok := h.validateAndNormalizeCreateRequest(w, r)
	if !ok {
		return
	}

	function := &domain.Function{
		ID:          utils.GenerateID(),
		Name:        req.Name,
		Runtime:     req.Runtime,
		Handler:     req.Handler,
		Code:        []byte(req.Code),
		Timeout:     time.Duration(req.Timeout) * time.Second,
		Memory:      req.Memory,
		Environment: req.Environment,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	if err := function.Validate(); err != nil {
		h.respondError(
			w,
			http.StatusBadRequest,
			"validation_failed",
			err.Error(),
		)
		return
	}

	if _, err := h.runtimeManager.GetRuntime(function.Runtime); err != nil {
		h.respondError(
			w,
			http.StatusBadRequest,
			"invalid_runtime",
			fmt.Sprintf(
				"Runtime %s not supported. Available: %v",
				function.Runtime,
				h.runtimeManager.ListRuntimes(),
			),
		)
		return
	}

	if err := h.functionService.CreateFunction(r.Context(), function); err != nil {
		if err == domain.ErrFunctionExists {
			h.respondError(
				w,
				http.StatusConflict,
				"function_exists",
				fmt.Sprintf(
					"Function with name %s already exists",
					function.Name,
				),
			)
		} else {
			h.respondError(
				w,
				http.StatusInternalServerError,
				"failed_to_create_function",
				err.Error(),
			)
		}
		return
	}

	resp := CreateFunctionResponse{
		FunctionID: function.ID,
		Name:       function.Name,
		Runtime:    function.Runtime,
		CreatedAt:  function.CreatedAt,
	}

	h.respondJSON(w, http.StatusCreated, resp)
}

// InvokeFunction handles POST /functions/{functionID}/invoke
// Executes a function with the provided payload
func (h *Handler) InvokeFunction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.respondError(
			w,
			http.StatusMethodNotAllowed,
			"method_not_allowed",
			"Only POST method",
		)
		return
	}

	// Extract function ID from path
	functionID := r.URL.Path[len("/functions/"):]
	if idx := len(functionID) - len("/invoke"); idx > 0 {
		functionID = functionID[:idx]
	}

	if functionID == "" {
		h.respondError(
			w,
			http.StatusMethodNotAllowed,
			"invalid_request",
			"Function ID is required",
		)
		return
	}

	function, err := h.functionService.GetFunction(r.Context(), functionID)
	if err != nil {
		if err == domain.ErrCodeNotFound {
			h.respondError(
				w,
				http.StatusNotFound,
				"function_not_found",
				fmt.Sprintf("Function %s not found", functionID),
			)
		} else {
			h.respondError(
				w,
				http.StatusInternalServerError,
				"failed_to_get_function",
				err.Error(),
			)
		}
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.respondError(
			w,
			http.StatusBadRequest,
			"invalid_request",
			"Failed to read request body.",
		)
		return
	}

	// Use empty JSON, if body is empty
	if len(body) == 0 {
		body = []byte("{}")
	}

	ctx, cancel := context.WithTimeout(r.Context(), function.Timeout)
	defer cancel()

	// Create execution record
	execution := domain.NewExecution(function.ID, body)
	execution.MarkStarted()

	// Execute function
	startTime := time.Now()
	result, err := h.runtimeManager.Execute(ctx, function, body)
	duration := time.Since(startTime)

	var resp InvokeFunctionResponse

	if err != nil {
		// Execution failed
		execution.MarkFailed(err)
		resp = InvokeFunctionResponse{
			ExecutionID: execution.ID,
			Status:      string(domain.StatusFailed),
			Error:       err.Error(),
			Duration:    duration.Milliseconds(),
		}
		h.respondJSON(w, http.StatusOK, resp)
		return
	}

	if ctx.Err() == context.DeadlineExceeded {
		execution.MarkTimeout()
		resp = InvokeFunctionResponse{
			ExecutionID: execution.ID,
			Status:      string(domain.StatusTimeout),
			Error:       "Function execution timed out",
			Duration:    duration.Milliseconds(),
		}
		h.respondJSON(w, http.StatusOK, resp)
		return
	}

	execution.MarkSuccess(resp.Output)
	resp = InvokeFunctionResponse{
		ExecutionID: execution.ID,
		Status:      string(domain.StatusSuccess),
		Output:      json.RawMessage(result.Output),
		Logs:        string(result.Logs),
		Duration:    duration.Milliseconds(),
		MemoryUsed:  result.MemoryUsed,
	}

	h.respondJSON(w, http.StatusOK, resp)
}

// ListFunctions handles GET /functions
func (h *Handler) ListFunctions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		h.respondError(
			w,
			http.StatusMethodNotAllowed,
			"method_not_allowed",
			"Only GET method alllowed",
		)
		return
	}

	functions, err := h.functionService.ListFunctions(r.Context(), 0, 100)
	if err != nil {
		h.respondError(
			w,
			http.StatusInternalServerError,
			"failed_to_list_functions",
			err.Error(),
		)
		return
	}

	functionList := make([]CreateFunctionResponse, 0, len(functions))
	for _, fn := range functions {
		functionList = append(functionList, CreateFunctionResponse{
			FunctionID: fn.ID,
			Name:       fn.Name,
			Runtime:    fn.Runtime,
			CreatedAt:  fn.CreatedAt,
		})
	}

	h.respondJSON(w, http.StatusOK, map[string]interface{}{
		"functions": functionList,
		"count":     len(functions),
	})
}

// GetFunction handles GET /functions/{functionID}
func (h *Handler) GetFunction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		h.respondError(
			w,
			http.StatusMethodNotAllowed,
			"method_not_allowed",
			"Only GET method alllowed",
		)
		return
	}

	functionID := r.URL.Path[len("/funtions/"):]

	function, err := h.functionService.GetFunction(r.Context(), functionID)
	if err != nil {
		if err == domain.ErrFunctionNotFound {
			h.respondError(
				w,
				http.StatusNotFound,
				"function_not_found",
				fmt.Sprintf("Function %s not found", functionID),
			)
		} else {
			h.respondError(
				w,
				http.StatusInternalServerError,
				"failed_to_get_function",
				fmt.Sprintf("Failed to retrieve function: %v", err),
			)
		}
		return
	}

	h.respondJSON(w, http.StatusOK, map[string]interface{}{
		"function_id": function.ID,
		"name":        function.Name,
		"runtime":     function.Runtime,
		"handler":     function.Handler,
		"timeout":     int(function.Timeout.Seconds()),
		"memory":      function.Memory,
		"environment": function.Environment,
		"created_at":  function.CreatedAt,
		"updated_at":  function.UpdatedAt,
	})
}

func (h *Handler) HealthCheck(w http.ResponseWriter, r *http.Request) {
	h.respondJSON(w, http.StatusOK, map[string]interface{}{
		"status":   "healthy",
		"service":  "mini-lambda",
		"version":  "1.0.0",
		"runtimes": h.runtimeManager.ListRuntimes(),
	})
}

func (h *Handler) respondError(
	w http.ResponseWriter,
	status int,
	errorType, message string,
) {
	h.respondJSON(w, status, ErrorResponse{
		Error:   errorType,
		Message: message,
	})
}

func (h *Handler) respondJSON(
	w http.ResponseWriter,
	status int,
	data interface{},
) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

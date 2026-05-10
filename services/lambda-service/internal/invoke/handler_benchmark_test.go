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

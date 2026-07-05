package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jagjeet-singh-23/mini-lambda/shared/middleware"
)

// TestResponseWriterUnwrapSupportsSetWriteDeadline is a regression test for a bug
// where *responseWriter (used internally by both MetricsMiddleware and
// LoggingMiddleware) did not implement Unwrap() http.ResponseWriter. Without
// Unwrap, http.ResponseController could not reach the underlying connection's
// write deadline, so calls like SetWriteDeadline silently failed with
// http.ErrNotSupported for every request that passed through this middleware
// chain -- including the gateway's SSE build-log streaming handler, which
// relies on SetWriteDeadline to defeat the server's 45s WriteTimeout for
// long-lived streams.
//
// This must use a real httptest.NewServer (not just httptest.NewRecorder,
// which never supports SetWriteDeadline/Flush regardless of wrapping) so the
// underlying http.ResponseWriter genuinely supports these methods, and the
// only thing under test is whether *responseWriter correctly delegates to it.
func TestResponseWriterUnwrapSupportsSetWriteDeadline(t *testing.T) {
	var setDeadlineErr, flushErr error

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rc := http.NewResponseController(w)
		setDeadlineErr = rc.SetWriteDeadline(time.Time{})
		flushErr = rc.Flush()
	})

	// Mirrors the gateway's real middleware nesting: MetricsMiddleware wraps
	// LoggingMiddleware wraps the handler, so the ResponseWriter reaching the
	// handler is a *responseWriter wrapped in another *responseWriter.
	wrapped := middleware.MetricsMiddleware("test")(middleware.LoggingMiddleware(handler))

	server := httptest.NewServer(wrapped)
	defer server.Close()

	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("GET request failed: %v", err)
	}
	defer resp.Body.Close()

	if setDeadlineErr != nil {
		t.Fatalf("SetWriteDeadline returned error, Unwrap delegation is broken: %v", setDeadlineErr)
	}
	if flushErr != nil {
		t.Fatalf("Flush returned error, Unwrap delegation is broken: %v", flushErr)
	}
}

package router

import (
	"io"
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
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
			}
		})
	}
}

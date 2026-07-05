package router

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// SetupRoutes configures all the HTTP multiplexer routes for the Gateway
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

	// Build log streaming (SSE)
	mux.HandleFunc("/jobs/", g.HandleBuildLogs)

	// Health check
	mux.HandleFunc("/health", g.HandleHealth)

	// Metrics endpoint
	mux.Handle("/metrics", promhttp.Handler())

	return mux
}

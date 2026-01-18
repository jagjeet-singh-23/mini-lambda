package api

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

type Router struct {
	handler *Handler
	mux     *http.ServeMux
}

func NewRouter(handler *Handler) *Router {
	router := &Router{
		handler: handler,
		mux:     http.NewServeMux(),
	}
	router.setupRoutes()
	return router
}

func (r *Router) setupRoutes() {
	r.mux.HandleFunc("/health", r.logRequest(r.handler.HealthCheck))
	r.mux.HandleFunc("stats/pools", r.logRequest(r.handler.PoolStats))
	r.mux.HandleFunc("/functions", r.logRequest(r.routeFunctions))
	r.mux.HandleFunc("/functions/", r.logRequest(r.routeFunctionsByID))
}

func (r *Router) routeFunctions(w http.ResponseWriter, req *http.Request) {
	if req.URL.Path != "/functions" {
		http.NotFound(w, req)
		return
	}

	switch req.Method {
	case http.MethodPost:
		r.handler.CreateFunction(w, req)
	case http.MethodGet:
		r.handler.ListFunctions(w, req)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (r *Router) routeFunctionsByID(w http.ResponseWriter, req *http.Request) {
	path := strings.TrimPrefix(req.URL.Path, "/functions/")

	segments := strings.Split(path, "/")

	if len(segments) == 0 || segments[0] == "" {
		http.NotFound(w, req)
		return
	}

	if len(segments) == 2 && segments[1] == "invoke" {
		if req.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		r.handler.InvokeFunction(w, req)
		return
	}

	if len(segments) == 1 {
		if req.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		r.handler.GetFunction(w, req)
		return
	}

	http.NotFound(w, req)
}

func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mux.ServeHTTP(w, req)
}

func (r *Router) logRequest(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		start := time.Now()
		wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		next(wrapped, req)

		duration := time.Since(start)
		log.Printf(
			"[%s] %s %s - %d (%s)",
			req.Method,
			req.URL.Path,
			req.RemoteAddr,
			wrapped.statusCode,
			duration,
		)
	}
}

type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

type Server struct {
	httpServer *http.Server
	router     *Router
}

func NewServer(port int, handler *Handler) *Server {
	router := NewRouter(handler)

	return &Server{
		httpServer: &http.Server{
			Addr:         fmt.Sprintf(":%d", port),
			Handler:      router,
			ReadTimeout:  15 * time.Second,
			WriteTimeout: 15 * time.Second,
			IdleTimeout:  60 * time.Second,
		},
		router: router,
	}
}

func (s *Server) Start() error {
	log.Printf("Starting mini-lambda server on %s", s.httpServer.Addr)
	return s.httpServer.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	log.Println("Shutting down server...")
	return s.httpServer.Shutdown(ctx)
}

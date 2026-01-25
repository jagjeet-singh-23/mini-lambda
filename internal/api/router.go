package api

import (
	"net/http"

	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// SetupRouter configures all application routes
func SetupRouter(handler *Handler, eventHandler *EventHandler) *mux.Router {
	router := mux.NewRouter()

	// Health check
	router.HandleFunc("/health", HealthCheck).Methods("GET")

	// Prometheus metrics
	router.Handle("/metrics", promhttp.Handler())

	// API v1
	api := router.PathPrefix("/api/v1").Subrouter()

	// Function management
	api.HandleFunc("/functions", handler.CreateFunction).Methods("POST")
	api.HandleFunc("/functions", handler.ListFunctions).Methods("GET")
	api.HandleFunc("/functions/{id}", handler.GetFunction).Methods("GET")
	api.HandleFunc("/functions/{id}", handler.UpdateFunction).Methods("PUT")
	api.HandleFunc("/functions/{id}", handler.DeleteFunction).Methods("DELETE")

	// Function execution
	api.HandleFunc("/functions/{id}/invoke", handler.InvokeFunction).Methods("POST")
	api.HandleFunc("/functions/{id}/executions", handler.GetExecutions).Methods("GET")

	// Cron triggers
	api.HandleFunc("/functions/{id}/triggers/cron", eventHandler.CreateCronTrigger).Methods("POST")
	api.HandleFunc("/functions/{id}/triggers/cron", eventHandler.ListCronTriggers).Methods("GET")
	api.HandleFunc("/triggers/cron/{id}", eventHandler.DeleteCronTrigger).Methods("DELETE")

	// Webhooks
	api.HandleFunc("/functions/{id}/webhooks", eventHandler.CreateWebhook).Methods("POST")
	api.HandleFunc("/webhooks/{id}", eventHandler.DeleteWebhook).Methods("DELETE")

	// Webhook receiver (public endpoint, no /api/v1 prefix)
	router.HandleFunc("/webhooks/{path:.*}", eventHandler.HandleWebhook).Methods("POST", "GET")

	// Dead Letter Queue
	api.HandleFunc("/dlq", eventHandler.ListDeadLetterQueue).Methods("GET")
	api.HandleFunc("/dlq/{id}/retry", eventHandler.RetryDeadLetterItem).Methods("POST")
	api.HandleFunc("/dlq/{id}", eventHandler.DeleteDeadLetterItem).Methods("DELETE")

	// Event audit logs
	api.HandleFunc("/functions/{id}/events", eventHandler.GetEventAuditLog).Methods("GET")

	return router
}

// HealthCheck returns service health status
func HealthCheck(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status": "healthy"}`))
}

package api

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strconv"

	"github.com/gofrs/uuid/v5"
	"github.com/gorilla/mux"
	"github.com/jagjeet-singh-23/mini-lambda/internal/domain"
	"github.com/jagjeet-singh-23/mini-lambda/internal/events"
)

type EventHandler struct {
	cronScheduler  *events.CronScheduler
	cronRepo       events.CronRepository
	webhookHandler *events.WebhookHandler
	webhookRepo    events.WebhookRepository
	dlq            events.DeadLetterQueue
	auditRepo      events.EventAuditRepository
}

func NewEventHandler(
	cronScheduler *events.CronScheduler,
	cronRepo events.CronRepository,
	webhookHandler *events.WebhookHandler,
	webhookRepo events.WebhookRepository,
	dlq events.DeadLetterQueue,
	auditRepo events.EventAuditRepository,
) *EventHandler {
	return &EventHandler{
		cronScheduler:  cronScheduler,
		cronRepo:       cronRepo,
		webhookHandler: webhookHandler,
		webhookRepo:    webhookRepo,
		dlq:            dlq,
		auditRepo:      auditRepo,
	}
}

func (h *EventHandler) CreateCronTrigger(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	functionID := vars["id"]

	var req struct {
		Name           string `json:"name"`
		CronExpression string `json:"cron_expression"`
		Timezone       string `json:"timezone"`
		Enabled        bool   `json:"enabled"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	trigger := &domain.CronTrigger{
		ID:             uuid.Must(uuid.NewV4()).String(),
		FunctionID:     functionID,
		Name:           req.Name,
		CronExpression: req.CronExpression,
		Timezone:       req.Timezone,
		Enabled:        req.Enabled,
	}

	if trigger.Timezone == "" {
		trigger.Timezone = "UTC"
	}

	if err := h.cronScheduler.AddTrigger(r.Context(), trigger); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondJSON(w, http.StatusAccepted, map[string]string{
		"status": "webhook received",
	})
}

// DeleteWebhook deletes a webhook trigger
func (h *EventHandler) DeleteWebhook(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	webhookID := vars["id"]

	if err := h.webhookRepo.DeleteWebhook(r.Context(), webhookID); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ListDeadLetterQueue lists failed events
// GET /dlq
func (h *EventHandler) ListDeadLetterQueue(w http.ResponseWriter, r *http.Request) {
	limit := 50
	offset := 0

	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if ilimit, err := strconv.Atoi(limitStr); err == nil {
			limit = ilimit
		}
	}

	if offsetStr := r.URL.Query().Get("offset"); offsetStr != "" {
		if ioffset, err := strconv.Atoi(offsetStr); err == nil {
			offset = ioffset
		}
	}

	items, err := h.dlq.ListDeadLetterItems(r.Context(), limit, offset)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, items)
}

// RetryDeadLetterItem retries a dead letter item
// POST /dlq/{id}/retry
func (h *EventHandler) RetryDeadLetterItem(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	itemID := vars["id"]

	if err := h.dlq.Retry(r.Context(), itemID); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{
		"status": "retry initiated",
	})
}

// DeleteDeadLetterItem deletes a dead letter item
// DELETE /dlq/{id}
func (h *EventHandler) DeleteDeadLetterItem(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	itemID := vars["id"]

	if err := h.dlq.DeleteDeadLetterItem(r.Context(), itemID); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// GetEventAuditLog gets event audit logs for a function
// GET /functions/{id}/events
func (h *EventHandler) GetEventAuditLog(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	functionID := vars["id"]

	limit := 100
	offset := 0

	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if ilimit, err := strconv.Atoi(limitStr); err == nil {
			limit = ilimit
		}
	}

	if offsetStr := r.URL.Query().Get("offset"); offsetStr != "" {
		if ioffset, err := strconv.Atoi(offsetStr); err == nil {
			offset = ioffset
		}
	}

	logs, err := h.auditRepo.Get(r.Context(), functionID, limit, offset)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, logs)
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{
		"error": message,
	})
}

func (h *EventHandler) ListCronTriggers(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	functionID := vars["id"]

	triggers, err := h.cronRepo.ListTriggers(r.Context(), functionID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, triggers)
}

func (h *EventHandler) DeleteCronTrigger(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	triggerID := vars["id"]

	if err := h.cronScheduler.RemoveTrigger(r.Context(), triggerID); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *EventHandler) CreateWebhook(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	functionID := vars["id"]

	var req struct {
		Name            string            `json:"name"`
		Path            string            `json:"path"`
		Secret          string            `json:"secret"`
		SignatureHeader string            `json:"signature_header"`
		Headers         map[string]string `json:"headers"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	webhook := &domain.Webhook{
		FunctionID:      functionID,
		Name:            req.Name,
		Path:            req.Path,
		Secret:          req.Secret,
		SignatureHeader: req.SignatureHeader,
		Headers:         req.Headers,
		Enabled:         true,
	}

	if err := h.webhookHandler.RegisterWebhook(r.Context(), webhook); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondJSON(w, http.StatusCreated, webhook)
}

func (h *EventHandler) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	path := vars["path"]

	body, err := io.ReadAll(r.Body)
	if err != nil {
		respondError(w, http.StatusBadRequest, "failed to read body")
		return
	}

	headers := make(map[string]string)
	for k, v := range r.Header {
		if len(v) > 0 {
			headers[k] = v[0]
		}
	}

	if err := h.webhookHandler.HandleRequest(r.Context(), path, body, headers); err != nil {
		log.Printf("Webhook error: %v", err)
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{"status": "received"})
}

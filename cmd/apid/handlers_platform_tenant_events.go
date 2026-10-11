package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
	pkgtrace "github.com/onebox-faas/faas/pkg/trace"
)

const platformTenantEventReceiptCursorPrefix = "terc1."

type platformTenantEventReceiptCursor struct {
	Version  int    `json:"v"`
	AppSlug  string `json:"app_slug"`
	Source   string `json:"source"`
	EventID  string `json:"id"`
	OutboxID int64  `json:"outbox_id"`
	Position int64  `json:"position"`
}

func tenantEventReceiptURL(slug, source, id string) string {
	return "/v1/platform-tenant-self/apps/" + url.PathEscape(slug) + "/events/receipts/" + url.PathEscape(id) + "?" + url.Values{"source": {source}}.Encode()
}

func decodePlatformTenantEventReceiptCursor(raw, slug, source, id string) (state.EventReceiptCursor, error) {
	if raw == "" {
		return state.EventReceiptCursor{}, nil
	}
	invalid := errors.New("invalid or mismatched event receipt cursor")
	if len(raw) > 8192 || !strings.HasPrefix(raw, platformTenantEventReceiptCursorPrefix) {
		return state.EventReceiptCursor{}, invalid
	}
	data, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(raw, platformTenantEventReceiptCursorPrefix))
	if err != nil {
		return state.EventReceiptCursor{}, invalid
	}
	var cursor platformTenantEventReceiptCursor
	if json.Unmarshal(data, &cursor) != nil || cursor.Version != 1 || cursor.AppSlug != slug || cursor.Source != source || cursor.EventID != id || cursor.OutboxID <= 0 || cursor.Position <= 0 {
		return state.EventReceiptCursor{}, invalid
	}
	return state.EventReceiptCursor{OutboxID: cursor.OutboxID, Position: cursor.Position}, nil
}

func encodePlatformTenantEventReceiptCursor(slug string, receipt state.EventReceipt) string {
	if receipt.NextPosition == 0 {
		return ""
	}
	data, _ := json.Marshal(platformTenantEventReceiptCursor{Version: 1, AppSlug: slug, Source: receipt.EventSource,
		EventID: receipt.EventID, OutboxID: receipt.OutboxID, Position: receipt.NextPosition})
	return platformTenantEventReceiptCursorPrefix + base64.RawURLEncoding.EncodeToString(data)
}

func (s *server) platformTenantEventApp(w http.ResponseWriter, r *http.Request, acct state.Account) (string, state.App, bool) {
	tenantID, ok := platformTenantSelfID(w, r)
	if !ok {
		return "", state.App{}, false
	}
	app, err := s.store.AppBySlug(r.Context(), r.PathValue("slug"))
	if errors.Is(err, state.ErrNotFound) || err == nil && (!app.PlatformTenantRequired || app.AccountID != acct.ID) {
		s.notFound(w, "app not found")
		return "", state.App{}, false
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not resolve tenant app"))
		return "", state.App{}, false
	}
	tenants, ok := s.store.(state.PlatformTenantStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("platform tenant store unavailable"))
		return "", state.App{}, false
	}
	if err := state.ValidatePlatformTenantAppBinding(r.Context(), tenants, acct.ID, tenantID, app.ID); err != nil {
		if errors.Is(err, state.ErrNotFound) || errors.Is(err, state.ErrPlatformTenantSuspended) {
			s.notFound(w, "app not found")
		} else {
			api.WriteProblem(w, api.ErrInternal("could not verify tenant app access"))
		}
		return "", state.App{}, false
	}
	return tenantID, app, true
}

// publishPlatformTenantSelfEvent accepts an event from the authenticated
// downstream tenant and scopes its durable fanout to that tenant's linked app.
func (s *server) publishPlatformTenantSelfEvent(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenantID, app, ok := s.platformTenantEventApp(w, r, acct)
	if !ok {
		return
	}
	if !acct.Plan.WorkflowsAllowed() {
		api.WriteProblem(w, api.ErrPlanWorkflowsNotAllowed(acct.Plan))
		return
	}
	var req api.PublishEventRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid JSON body"))
		return
	}
	envelope, err := normalizePublishRequest(req, acct.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	if strings.HasPrefix(envelope.Source, "gregale.") {
		api.WriteProblem(w, api.ErrValidation("gregale.* sources are reserved for platform events"))
		return
	}
	if !s.validateEventSchemaForIngress(w, r, acct.ID, envelope) {
		return
	}
	clientEventID := envelope.ID
	envelope.ID = uuid.NewSHA1(uuid.NameSpaceURL, []byte("gregale.tenant-event.v1\x00"+acct.ID+"\x00"+tenantID+"\x00"+app.ID+"\x00"+envelope.Source+"\x00"+clientEventID)).String()
	envelope.AppID, envelope.PlatformTenantID, envelope.TenantEventID = app.ID, tenantID, clientEventID
	traceHeaders := pkgtrace.InjectHeaders(r.Context())
	envelope.Traceparent, envelope.Tracestate, envelope.Baggage = traceHeaders["traceparent"], traceHeaders["tracestate"], traceHeaders["baggage"]
	payload, err := json.Marshal(envelope)
	if err != nil {
		s.log.ErrorContext(r.Context(), "marshal tenant event failed", "err", err)
		api.WriteProblem(w, api.ErrCapacity("failed to record event"))
		return
	}
	store, ok := s.store.(state.TenantPublishedEventStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("tenant event store unavailable"))
		return
	}
	if err := store.AppendTenantPublishedEvent(r.Context(), "apid", acct.ID, tenantID, app.ID, payload, nil); err != nil {
		var problem *api.Problem
		if errors.As(err, &problem) {
			api.WriteProblem(w, problem)
			return
		}
		if errors.Is(err, state.ErrNotFound) {
			s.notFound(w, "app not found")
			return
		}
		if writeEventStorageCapacity(w, err) {
			return
		}
		var pgErr *pgconn.PgError
		if errors.Is(err, state.ErrConflict) || errors.As(err, &pgErr) && pgErr.ConstraintName == "event_fanout_identity_uniq" {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict,
				"Event identity conflict", "this event id was already accepted with different type, schema version, or data"))
			return
		}
		s.log.ErrorContext(r.Context(), "record tenant event failed", "err", err)
		api.WriteProblem(w, api.ErrCapacity("failed to record event"))
		return
	}
	_ = s.notif.Notify(r.Context(), db.NotifyEventPublished, "1")
	acceptance, ok := s.store.(state.EventReceiptAcceptanceStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("event receipt acceptance store"))
		return
	}
	acceptedAt, err := acceptance.EventReceiptAcceptedAt(r.Context(), acct.ID, envelope.Source, envelope.ID)
	if err != nil {
		s.log.ErrorContext(r.Context(), "read tenant event acceptance", "err", err)
		api.WriteProblem(w, api.ErrCapacity("failed to read event acceptance; retry the same event id"))
		return
	}
	location := tenantEventReceiptURL(app.Slug, envelope.Source, envelope.ID)
	w.Header().Set("Location", location)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusAccepted, api.PlatformTenantPublishEventResponse{ReceiptURL: location, ID: envelope.ID,
		ClientEventID: clientEventID, AcceptedAt: acceptedAt})
}

func (s *server) getPlatformTenantSelfEventReceipt(w http.ResponseWriter, r *http.Request, acct state.Account) {
	_, app, ok := s.platformTenantEventApp(w, r, acct)
	if !ok {
		return
	}
	source, id := strings.TrimSpace(r.URL.Query().Get("source")), strings.TrimSpace(r.PathValue("event_id"))
	if source == "" || id == "" || len(source) > events.EnvelopeStringMax || len(id) > events.EnvelopeStringMax {
		api.WriteProblem(w, api.ErrValidation("source and event id are required and must be at most 256 bytes"))
		return
	}
	problem, limit := api.ParseLimit(r.URL.Query().Get("limit"), 100, api.EventReceiptPageMax, "recipients")
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	cursor, err := decodePlatformTenantEventReceiptCursor(r.URL.Query().Get("after"), app.Slug, source, id)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	store, ok := s.store.(state.EventReceiptStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("event receipt store"))
		return
	}
	receipt, err := store.EventReceipt(r.Context(), acct.ID, source, id, cursor, limit)
	if err != nil {
		s.writeEventReceiptError(w, r, err)
		return
	}
	tenantID, ok := platformTenantSelfID(w, r)
	if !ok {
		return
	}
	if receipt.AppID != app.ID || receipt.PlatformTenantID != tenantID {
		s.notFound(w, "event receipt not found")
		return
	}
	response := eventReceiptResponse(acct.ID, receipt)
	response.NextAfter = encodePlatformTenantEventReceiptCursor(app.Slug, receipt)
	for i := range response.Recipients {
		// Tenant event fanout contains only workflow starts. Keep the receipt
		// useful without exposing account-operator recovery routes.
		response.Recipients[i].RecoveryActions = []api.EventReceiptRecoveryAction{}
		response.Recipients[i].FanoutHistoryURL = ""
		response.Recipients[i].AttemptHistoryURL = ""
		if response.Recipients[i].WorkflowRunID != "" {
			response.Recipients[i].ExecutionUnavailable = ""
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, response)
}

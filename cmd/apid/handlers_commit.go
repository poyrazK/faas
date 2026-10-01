package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	commitwork "github.com/onebox-faas/faas/pkg/commit"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
)

func commitAPIEnabled(w http.ResponseWriter) bool {
	if os.Getenv("FAAS_COMMIT_API_ENABLED") != "true" {
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, "commit_not_qualified", "Commit unavailable", "Gregale Commit is restricted to operator qualification"))
		return false
	}
	return true
}

func (s *server) createCommitSource(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !commitAPIEnabled(w) {
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if !decodeJSONLimit(w, r, &req, 4096) {
		return
	}
	if strings.TrimSpace(req.Name) == "" || len(req.Name) > 128 {
		api.WriteProblem(w, api.ErrValidation("source name must contain 1-128 bytes"))
		return
	}
	store, ok := s.store.(state.CommitStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("commit store unavailable"))
		return
	}
	src, err := store.CreateCommitSource(r.Context(), state.CommitSource{AccountID: acct.ID, AppID: app.ID, Name: req.Name})
	if errors.Is(err, state.ErrConflict) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "commit_source_name_conflict", "Source name already used", "This source name belongs to another application"))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("create commit source"))
		return
	}
	writeJSON(w, http.StatusCreated, src)
}

func (s *server) acceptCommitEvent(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !commitAPIEnabled(w) {
		return
	}
	store, ok := s.store.(state.CommitStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("commit store unavailable"))
		return
	}
	source := r.PathValue("source")
	if _, err := uuid.Parse(source); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid source ID"))
		return
	}
	src, err := store.CommitSourceByID(r.Context(), acct.ID, source)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(404, "commit_source_not_found", "Source not found", "commit source is unavailable"))
		return
	}
	limits := api.MustLimitsFor(acct.Plan)
	if limits.MaxQueueDepth == 0 {
		api.WriteProblem(w, api.ErrPlanFeatureGated("queues", acct.Plan))
		return
	}
	var event commitwork.Event
	if !decodeJSONLimit(w, r, &event, int64(limits.MaxSourceBytesPerInvocation)) {
		return
	}
	if err := event.Validate(); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	// Replay must survive release expiry, destination changes, and a full queue.
	if _, lookupErr := store.CommitReceiptByEvent(r.Context(), acct.ID, source, event.ID); lookupErr == nil {
		receipt, replayErr := store.AcceptCommitEvent(r.Context(), acct.ID, source, event.ID, event.Type, event.Data, state.Invocation{}, 0)
		if errors.Is(replayErr, state.ErrConflict) {
			api.WriteProblem(w, api.NewProblem(409, "commit_identity_conflict", "Event identity conflict", "event ID was already accepted with different content"))
		} else if replayErr != nil {
			api.WriteProblem(w, api.ErrCapacity("recover commit receipt"))
		} else {
			writeJSON(w, http.StatusAccepted, receipt)
		}
		return
	} else if !errors.Is(lookupErr, state.ErrNotFound) {
		api.WriteProblem(w, api.ErrCapacity("lookup commit receipt"))
		return
	}
	// The event source is assigned by Gregale, not supplied by the producer.
	envelope, err := (events.Envelope{ID: event.ID, Source: "gregale.commit." + source, Type: event.Type, Data: event.Data}).Normalize(acct.ID, time.Now().UTC())
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid commit envelope"))
		return
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("encode commit event"))
		return
	}
	app, err := s.store.AppByID(r.Context(), src.AppID)
	if err != nil || app.AccountID != acct.ID {
		api.WriteProblem(w, api.ErrValidation("commit destination unavailable"))
		return
	}
	if !app.AcceptsRequestInvocations() {
		api.WriteProblem(w, api.ErrValidation("commit destination requires a request workload"))
		return
	}
	if app.PlatformTenantRequired {
		api.WriteProblem(w, api.ErrValidation("commit tenant targeting is not yet supported"))
		return
	}
	inv := state.Invocation{AccountID: acct.ID, AppID: src.AppID, Source: state.InvocationAsyncInvoke, Method: "POST", Path: "/", Payload: payload, DueAt: time.Now().UTC(), RetryPolicyJSON: effectiveInvocationRetryPolicy(app, nil, limits.MaxQueueAttempts)}
	inv, _, err = state.ResolveInvocationVersion(r.Context(), s.store, inv)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("resolve commit destination version"))
		return
	}
	receipt, err := store.AcceptCommitEvent(r.Context(), acct.ID, source, event.ID, event.Type, event.Data, inv, limits.MaxQueueDepth)
	switch {
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(409, "commit_identity_conflict", "Event identity conflict", "event ID was already accepted with different content"))
	case errors.Is(err, state.ErrCommitQueueFull):
		api.WriteProblem(w, api.ErrCapacity("commit destination queue full"))
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.NewProblem(409, "commit_source_paused", "Source unavailable", "commit source is not accepting new events"))
	case err != nil:
		api.WriteProblem(w, api.ErrCapacity("accept commit event"))
	default:
		writeJSON(w, http.StatusAccepted, receipt)
	}
}

func (s *server) getCommitReceipt(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.store.(state.CommitStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("commit store unavailable"))
		return
	}
	for _, id := range []string{r.PathValue("source"), r.PathValue("event")} {
		if _, err := uuid.Parse(id); err != nil {
			api.WriteProblem(w, api.ErrValidation("invalid commit identity"))
			return
		}
	}
	receipt, err := store.CommitReceiptByEvent(r.Context(), acct.ID, r.PathValue("source"), r.PathValue("event"))
	if err != nil {
		api.WriteProblem(w, api.NewProblem(404, "commit_event_not_found", "Event not found", "event has not been accepted"))
		return
	}
	writeJSON(w, http.StatusOK, receipt)
}

func (s *server) setCommitSourceEnabled(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !commitAPIEnabled(w) {
		return
	}
	store, ok := s.store.(state.CommitStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("commit store unavailable"))
		return
	}
	id := r.PathValue("source")
	if _, err := uuid.Parse(id); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid source ID"))
		return
	}
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if !decodeJSONLimit(w, r, &req, 4096) {
		return
	}
	if req.Enabled == nil {
		api.WriteProblem(w, api.ErrValidation("enabled is required"))
		return
	}
	src, err := store.SetCommitSourceEnabled(r.Context(), acct.ID, id, *req.Enabled)
	if errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.NewProblem(404, "commit_source_not_found", "Source not found", "commit source is unavailable"))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("update commit source"))
		return
	}
	writeJSON(w, http.StatusOK, src)
}

func (s *server) putCommitSourceConnection(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !commitAPIEnabled(w) {
		return
	}
	store, ok := s.store.(state.CommitStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("commit store unavailable"))
		return
	}
	source := r.PathValue("source")
	if _, err := uuid.Parse(source); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid source ID"))
		return
	}
	if _, err := store.CommitSourceByID(r.Context(), acct.ID, source); err != nil {
		api.WriteProblem(w, api.NewProblem(404, "commit_source_not_found", "Source not found", "commit source is unavailable"))
		return
	}
	var req struct {
		Connection string `json:"connection_url"`
	}
	if !decodeJSONLimit(w, r, &req, 10<<10) {
		return
	}
	if err := commitwork.ValidateConnection(req.Connection); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	if outboundCredentialRecipient == nil || outboundCredentialRecipient() == nil {
		api.WriteProblem(w, api.ErrCapacity("credential encryption unavailable"))
		return
	}
	blob, err := commitwork.SealConnection(outboundCredentialRecipient(), source, req.Connection)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("seal database credential"))
		return
	}
	if err := store.SetCommitSourceConnection(r.Context(), acct.ID, source, blob); err != nil {
		api.WriteProblem(w, api.ErrCapacity("store database credential"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) getCommitSource(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.store.(state.CommitStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("commit store unavailable"))
		return
	}
	source := r.PathValue("source")
	if _, err := uuid.Parse(source); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid source ID"))
		return
	}
	src, err := store.CommitSourceByID(r.Context(), acct.ID, source)
	if errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.NewProblem(404, "commit_source_not_found", "Source not found", "commit source is unavailable"))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("read commit source"))
		return
	}
	writeJSON(w, http.StatusOK, src)
}

func (s *server) listCommitBlockedEvents(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, source, ok := s.loadCommitRecoverySource(w, r, acct)
	if !ok {
		return
	}
	items, err := store.ListCommitBlockedEvents(r.Context(), acct.ID, source)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("read blocked commit events"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "limit": 32, "observation": "latest relay snapshot; source info reports the full blocked count"})
}
func (s *server) replayCommitBlockedEvent(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !commitAPIEnabled(w) {
		return
	}
	store, source, ok := s.loadCommitRecoverySource(w, r, acct)
	if !ok {
		return
	}
	event := r.PathValue("event")
	if _, err := uuid.Parse(event); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid event ID"))
		return
	}
	err := store.RequestCommitReplay(r.Context(), acct.ID, source, event)
	if errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.NewProblem(404, "commit_blocked_event_not_found", "Blocked event not found", "event is absent from the latest blocked snapshot"))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("request commit replay"))
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"event_id": event, "status": "replay_requested"})
}
func (s *server) loadCommitRecoverySource(w http.ResponseWriter, r *http.Request, acct state.Account) (state.CommitStore, string, bool) {
	store, ok := s.store.(state.CommitStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("commit store unavailable"))
		return nil, "", false
	}
	source := r.PathValue("source")
	if _, err := uuid.Parse(source); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid source ID"))
		return nil, "", false
	}
	_, err := store.CommitSourceByID(r.Context(), acct.ID, source)
	if errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.NewProblem(404, "commit_source_not_found", "Source not found", "commit source is unavailable"))
		return nil, "", false
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("read commit source"))
		return nil, "", false
	}
	return store, source, true
}

// Operations reuse invocation identities and dispatch. This view preserves
// minimal acceptance/completion facts without creating another execution loop.
func (s *server) getCommitOperation(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.store.(state.CommitStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("commit store unavailable"))
		return
	}
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid operation ID"))
		return
	}
	operation, err := store.CommitOperationByID(r.Context(), acct.ID, id)
	if errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.NewProblem(404, "operation_not_found", "Operation not found", "operation is unavailable"))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("read operation"))
		return
	}
	writeJSON(w, http.StatusOK, operation)
}

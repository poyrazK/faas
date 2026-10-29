package apidserver

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/issuetrackers"
	"github.com/onebox-faas/faas/pkg/issuetracker"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) linearIssueTrackerCallback(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), api.IssueTrackerCallbackRequestTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	appID, err := uuid.Parse(r.PathValue("app_id"))
	if err != nil || appID == uuid.Nil {
		api.WriteProblem(w, api.ErrValidation("invalid callback app"))
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, api.IssueTrackerCallbackBodyMaxBytes))
	if err != nil {
		api.WriteProblem(w, api.NewProblem(413, "issue_tracker_callback_too_large", "Callback too large", "callback payload exceeds the supported bound"))
		return
	}
	record, secret, ok := s.linearIssueTrackerCallbackCredential(w, r, appID.String())
	if !ok {
		return
	}
	event, err := issuetracker.DecodeLinearIssueCallback(body, r.Header.Get("Linear-Signature"), r.Header.Get("Linear-Delivery"), secret, linearBoundConnection(record).Scope.Identity, record.AppID, record.Connection.Revision)
	if errors.Is(err, issuetracker.ErrIgnoredCallback) {
		writeJSON(w, 200, map[string]string{"status": "ignored"})
		return
	}
	if err != nil {
		s.recordLinearCallbackDiagnostic(r, record, issuetracker.CallbackRejectionReason(err))
		api.WriteProblem(w, api.NewProblem(401, "issue_tracker_callback_invalid", "Callback not verified", "signature or callback scope did not match"))
		return
	}
	s.acceptLinearIssueTrackerCallback(w, r, event, record)
}
func (s *server) acceptLinearIssueTrackerCallback(w http.ResponseWriter, r *http.Request, event issuetracker.CallbackEvent, record state.IssueTrackerLinearRecord) {
	ctx := r.Context()
	store, ok := s.store.(state.IssueTrackerCallbackStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("callback storage unavailable"))
		return
	}
	known, err := store.KnownIssueTrackerCallback(ctx, event)
	if err != nil {
		if errors.Is(err, state.ErrInvalidArgument) {
			s.recordLinearCallbackDiagnostic(r, record, "delivery_conflict")
		}
		writeNativeIssueTrackerError(w, err)
		return
	}
	if known {
		if event.Fresh(time.Now()) && !s.recordLinearCallbackDiagnostic(r, record, "verified") {
			api.WriteProblem(w, api.ErrCapacity("callback diagnostics unavailable"))
			return
		}
		writeJSON(w, 200, map[string]string{"status": "duplicate"})
		return
	}
	if !event.Fresh(time.Now()) {
		s.recordLinearCallbackDiagnostic(r, record, "stale_delivery")
		api.WriteProblem(w, api.NewProblem(401, "issue_tracker_callback_expired", "Callback expired", "sending timestamp is outside the supported window"))
		return
	}
	if !s.recordLinearCallbackDiagnostic(r, record, "verified") {
		api.WriteProblem(w, api.ErrCapacity("callback diagnostics unavailable"))
		return
	}
	status, err := store.EnqueueIssueTrackerCallback(ctx, event)
	if err != nil {
		if errors.Is(err, state.ErrInvalidArgument) {
			s.recordLinearCallbackDiagnostic(r, record, "delivery_conflict")
		}
		writeNativeIssueTrackerError(w, err)
		return
	}
	// Linear retries non-200 responses; acknowledgment follows durable recording.
	writeJSON(w, http.StatusOK, map[string]string{"status": status})
}
func (s *server) linearIssueTrackerCallbackCredential(w http.ResponseWriter, r *http.Request, appID string) (state.IssueTrackerLinearRecord, string, bool) {
	app, err := s.store.AppByID(r.Context(), appID)
	if err != nil {
		writeNativeIssueTrackerError(w, err)
		return state.IssueTrackerLinearRecord{}, "", false
	}
	store, ok := s.store.(state.IssueTrackerLinearStore)
	if !ok || len(accountIdentities()) == 0 {
		api.WriteProblem(w, api.ErrCapacity("callback credentials unavailable"))
		return state.IssueTrackerLinearRecord{}, "", false
	}
	record, err := store.GetIssueTrackerLinearConnection(r.Context(), appID, app.AccountID)
	if err != nil {
		writeNativeIssueTrackerError(w, err)
		return record, "", false
	}
	if !record.Connection.Connected || !record.Connection.WebhookEnabled {
		api.WriteProblem(w, api.NewProblem(404, api.CodeNotFound, "Callback not configured", "this app has no active Linear callback"))
		return record, "", false
	}
	secret, err := issuetrackers.OpenLinearWebhookSecret(accountIdentities(), linearBoundConnection(record).Scope, record.SealedCredential)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("callback credentials unavailable"))
		return record, "", false
	}
	return record, secret, true
}

// Scope comes from the configured credential record, never from rejected payloads.
func (s *server) recordLinearCallbackDiagnostic(r *http.Request, record state.IssueTrackerLinearRecord, reason string) bool {
	store, ok := s.store.(state.IssueTrackerCallbackDiagnosticStore)
	if !ok {
		return false
	}
	if err := store.RecordIssueTrackerCallbackDiagnostic(r.Context(), record.AppID, record.AccountID, record.Connection.Revision, reason); err != nil {
		s.log.Warn("tracker callback diagnostic could not be recorded", "app_id", record.AppID, "reason", reason)
		return false
	}
	return true
}

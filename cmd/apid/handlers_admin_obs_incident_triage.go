package main

import (
	"net/http"
	"regexp"
	"strings"
	"unicode"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

var obsIncidentTriageDedupeShape = regexp.MustCompile(`^[a-z0-9][a-z0-9:_-]{0,254}$`)

// putObsIncidentTriage handles the small operator workflow state attached to
// one inbox dedupe key. requireAdminMutation at the route provides strict
// session step-up, same-origin protection, and idempotency; this handler adds
// the allowlist and input/audit boundaries.
func (s *server) putObsIncidentTriage(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if allowed, prob := s.adminAllows(acct); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	key := strings.TrimSpace(r.PathValue("dedupe_key"))
	if !obsIncidentTriageDedupeShape.MatchString(key) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"invalid dedupe key", "dedupe_key must match [a-z0-9][a-z0-9:_-]{0,254}"))
		return
	}
	var req api.ObsIncidentTriageRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"invalid triage request", err.Error()))
		return
	}
	req.Status = strings.TrimSpace(req.Status)
	req.Owner = strings.TrimSpace(req.Owner)
	req.Note = strings.TrimSpace(req.Note)
	req.Reason = strings.TrimSpace(req.Reason)
	if !validObsIncidentTriageStatus(req.Status) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"invalid triage status", "status must be open, acknowledged, in_progress, or resolved"))
		return
	}
	if len(req.Owner) > 128 || len(req.Note) > 1024 || strings.IndexFunc(req.Owner, unicode.IsControl) >= 0 {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"triage metadata too long", "owner is limited to 128 characters and note to 1024 characters"))
		return
	}
	if len(req.Reason) == 0 || len(req.Reason) > obsOpsReasonMaxLen || !obsOpsReasonShape.MatchString(req.Reason) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"invalid reason", "reason must match [a-z0-9_]{1,64}"))
		return
	}
	if req.Owner == "" {
		req.Owner = strings.TrimSpace(acct.Email)
	}
	if len(req.Owner) > 128 || strings.IndexFunc(req.Owner, unicode.IsControl) >= 0 {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"invalid owner", "owner is limited to 128 characters and cannot contain control characters"))
		return
	}
	row, err := s.store.UpsertOperatorIncidentTriage(r.Context(), key, req.Status, req.Owner, req.Note, acct.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not persist incident triage state"))
		return
	}
	if s.audit != nil {
		s.audit.Emit(r.Context(), "operator.action.incident_triage", nil, map[string]any{
			"actor":      acct.ID,
			"dedupe_key": key,
			"status":     req.Status,
			"owner":      req.Owner,
			"reason":     req.Reason,
			"note_len":   len(req.Note),
		})
	}
	updatedAt := row.UpdatedAt
	writeJSON(w, http.StatusOK, api.ObsIncidentTriageResponse{Triage: api.ObsIncidentTriage{
		DedupeKey: row.DedupeKey,
		Status:    row.Status,
		Owner:     row.Owner,
		Note:      row.Note,
		UpdatedAt: &updatedAt,
		UpdatedBy: row.UpdatedBy,
	}})
}

func validObsIncidentTriageStatus(status string) bool {
	switch status {
	case state.OperatorIncidentTriageOpen, state.OperatorIncidentTriageAcknowledged,
		state.OperatorIncidentTriageInProgress, state.OperatorIncidentTriageResolved:
		return true
	default:
		return false
	}
}

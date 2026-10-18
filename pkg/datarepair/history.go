package datarepair

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// HistoryQuery lists live navigation metadata, not a frozen repair selection.
// It shares discovery's page, cursor and response budgets.
type HistoryQuery struct {
	Limit  int    `json:"limit"`
	Cursor string `json:"cursor,omitempty"`
}

func (q HistoryQuery) Normalize() (HistoryQuery, error) {
	normalized, err := (SourceQuery{Limit: q.Limit, Cursor: q.Cursor}).Normalize()
	q.Limit = normalized.Limit
	return q, err
}

// HistoryExecution is optional hosted context. Its absence at the source and
// explicit unavailability in hosted history never imply a rolled-back apply.
type HistoryExecution struct {
	Availability string             `json:"availability"`
	ID           string             `json:"id,omitempty"`
	State        api.OperationState `json:"state,omitempty"`
	Phase        string             `json:"phase,omitempty"`
}

type HistoryItem struct {
	ID                string            `json:"id"`
	Digest            string            `json:"digest"`
	CreatedAt         time.Time         `json:"created_at"`
	ExpiresAt         time.Time         `json:"expires_at"`
	Candidates        int               `json:"candidates"`
	Conflicts         int               `json:"conflicts"`
	Unavailable       int               `json:"unavailable"`
	RehearsedAt       *time.Time        `json:"rehearsed_at,omitempty"`
	ApprovedAt        *time.Time        `json:"approved_at,omitempty"`
	ApprovedBy        *string           `json:"approved_by,omitempty"`
	AppliedAt         *time.Time        `json:"applied_at,omitempty"`
	RepairOperationID *string           `json:"repair_operation_id,omitempty"`
	PayloadPurgedAt   *time.Time        `json:"payload_purged_at,omitempty"`
	Replacement       *Replacement      `json:"replacement,omitempty"`
	ReplacementPlanID string            `json:"replacement_plan_id,omitempty"`
	ApplyResolution   *ApplyResolution  `json:"apply_resolution,omitempty"`
	Execution         *HistoryExecution `json:"execution,omitempty"`
}

type HistoryPage struct {
	Scope             Scope         `json:"scope"`
	SourceOperationID string        `json:"source_operation_id"`
	CheckedAt         time.Time     `json:"checked_at"`
	Items             []HistoryItem `json:"items"`
	NextCursor        string        `json:"next_cursor,omitempty"`
}

// Valid checks the metadata contract at executor and CLI trust boundaries.
// No receipt is represented by nil, never by a fabricated not-committed result.
func (p HistoryPage) Valid(limit int) bool {
	if p.Scope.Validate() != nil || !validID(p.SourceOperationID) || p.CheckedAt.IsZero() ||
		limit < 1 || limit > api.DataRepairSourceMaxPageSize || p.Items == nil || len(p.Items) > limit ||
		len(p.NextCursor) > api.DataRepairSourceMaxCursorBytes || (p.NextCursor != "" && len(p.Items) != limit) {
		return false
	}
	seen := map[string]bool{}
	for i, item := range p.Items {
		if !item.valid(p.SourceOperationID) || seen[item.ID] {
			return false
		}
		seen[item.ID] = true
		if i > 0 {
			previous := p.Items[i-1]
			if item.CreatedAt.After(previous.CreatedAt) || item.CreatedAt.Equal(previous.CreatedAt) && item.ID >= previous.ID {
				return false
			}
		}
	}
	return true
}

func (h HistoryItem) valid(source string) bool {
	if !validID(h.ID) || !digestPattern.MatchString(h.Digest) || h.CreatedAt.IsZero() || h.ExpiresAt.IsZero() ||
		h.Candidates < 0 || h.Conflicts < 0 || h.Unavailable < 0 || h.Candidates > api.DataRepairMaxRows ||
		h.Conflicts > api.DataRepairMaxRows-h.Candidates || h.Unavailable > api.DataRepairMaxRows-h.Candidates-h.Conflicts ||
		(h.ApprovedAt == nil) != (h.ApprovedBy == nil) || (h.AppliedAt == nil) != (h.RepairOperationID == nil) {
		return false
	}
	for _, t := range []*time.Time{h.RehearsedAt, h.ApprovedAt, h.AppliedAt, h.PayloadPurgedAt} {
		if t != nil && t.IsZero() {
			return false
		}
	}
	if h.ApprovedBy != nil && (*h.ApprovedBy == "" || len(*h.ApprovedBy) > api.DataRepairActorMaxBytes) {
		return false
	}
	if h.RepairOperationID != nil && (!validID(*h.RepairOperationID) || *h.RepairOperationID == source || h.ApprovedAt == nil || h.Candidates < 1 || h.ReplacementPlanID != "" || h.ApplyResolution != nil) {
		return false
	}
	if h.ReplacementPlanID != "" && (!validID(h.ReplacementPlanID) || h.ReplacementPlanID == h.ID || h.ReplacementPlanID == source) {
		return false
	}
	plan := Plan{ID: h.ID, Digest: h.Digest, SourceOperationID: source, AppliedAt: h.AppliedAt, RepairOperationID: h.RepairOperationID}
	if h.ApplyResolution != nil && !h.ApplyResolution.Valid(plan) {
		return false
	}
	if r := h.Replacement; r != nil && (!validID(r.ParentPlanID) || r.ParentPlanID == h.ID || !digestPattern.MatchString(r.ParentPlanDigest) ||
		r.PreviousCandidates < 1 || r.PreviousCandidates > api.DataRepairMaxRows || r.PreviousExcluded < 0 || r.PreviousExcluded > api.DataRepairMaxRows-r.PreviousCandidates ||
		r.RetainedCandidates != h.Candidates || r.RetainedCandidates > r.PreviousCandidates || r.RemovedCandidates < 0 || r.RemovedCandidates != r.PreviousCandidates-r.RetainedCandidates ||
		h.Conflicts+h.Unavailable != r.PreviousExcluded+r.RemovedCandidates) {
		return false
	}
	return h.Execution == nil || h.Execution.Valid(h.ID)
}

func (e HistoryExecution) Valid(id string) bool {
	if e.Availability == "not_retained" || e.Availability == "unavailable" {
		return e.ID == "" && e.State == "" && e.Phase == ""
	}
	if e.Availability != "retained" || e.ID != id {
		return false
	}
	switch e.State {
	case api.OperationAccepted, api.OperationRunning, api.OperationSucceeded, api.OperationFailed, api.OperationCancelled, api.OperationRequiresReconciliation, api.OperationWaiting:
	default:
		return false
	}
	switch e.Phase {
	case api.DataRepairPrepared, api.DataRepairRehearsing, api.DataRepairAwaitingApproval, api.DataRepairApproved,
		api.DataRepairApplying, api.DataRepairUnknown, api.DataRepairSucceeded, api.DataRepairFailed, api.DataRepairRejected,
		api.DataRepairReplacing, api.DataRepairReplaced, api.DataRepairResolving, api.DataRepairClosed:
		return true
	default:
		return false
	}
}

func historyFingerprint(scope Scope, source string, q HistoryQuery) string {
	q.Cursor = ""
	b, _ := json.Marshal(struct {
		Scope  Scope
		Source string
		Query  HistoryQuery
	}{scope, source, q})
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

func (e *Engine) History(ctx context.Context, scope Scope, source string, q HistoryQuery) (HistoryPage, error) {
	if err := e.ready(scope); err != nil {
		return HistoryPage{}, err
	}
	q, err := q.Normalize()
	if err != nil || !validID(source) {
		return HistoryPage{}, ErrInvalid
	}
	fingerprint := historyFingerprint(scope, source, q)
	var afterTime, afterID any
	if q.Cursor != "" {
		var cursor sourceCursor
		if DecodeSourceCursor(q.Cursor, &cursor) != nil || cursor.Version != 1 || cursor.Fingerprint != fingerprint || !validID(cursor.ID) || cursor.CreatedAt.IsZero() {
			return HistoryPage{}, ErrInvalid
		}
		afterTime, afterID = cursor.CreatedAt, cursor.ID
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(api.DataRepairTimeoutSeconds)*time.Second)
	defer cancel()
	var raw []byte
	if err := e.Pool.QueryRow(ctx, `SELECT gregale_repair.list_history($1::uuid,$2::jsonb,$3::timestamptz,$4::uuid,$5::integer)`,
		source, scopeJSON(scope), afterTime, afterID, q.Limit).Scan(&raw); err != nil {
		return HistoryPage{}, classify(err)
	}
	var p HistoryPage
	if len(raw) > api.DataRepairSourceMaxResponseBytes || json.Unmarshal(raw, &p) != nil || len(p.Items) > q.Limit+1 {
		return HistoryPage{}, ErrUnavailable
	}
	if len(p.Items) > q.Limit {
		p.Items = p.Items[:q.Limit]
		last := p.Items[len(p.Items)-1]
		p.NextCursor = EncodeSourceCursor(sourceCursor{1, fingerprint, last.CreatedAt, last.ID})
	}
	if !p.Valid(q.Limit) || p.Scope != scope || p.SourceOperationID != source {
		return HistoryPage{}, ErrUnavailable
	}
	return p, nil
}

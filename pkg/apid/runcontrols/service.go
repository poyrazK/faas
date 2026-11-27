// Package runcontrols owns the customer-facing Run policy and history contract.
package runcontrols

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type Access struct {
	AccountID, PrincipalID string
	Wide                   bool
}
type Service struct {
	Store   state.RunControlStore
	Enabled bool
}

func requireWide(a Access) error {
	if !a.Wide {
		return api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Account authority required", "Run policy management requires unrestricted account authority")
	}
	return nil
}
func (s Service) PutBudget(ctx context.Context, a Access, kind, ref string, req api.SetRunBudgetRequest) (api.RunBudgetPolicy, error) {
	if err := requireWide(a); err != nil {
		return api.RunBudgetPolicy{}, err
	}
	if !s.Enabled && (req.ExpectedRevision == 0 || req.Enabled) {
		return api.RunBudgetPolicy{}, api.ErrCapacity("new Run controls are disabled")
	}
	if s.Store == nil {
		return api.RunBudgetPolicy{}, api.ErrCapacity("Run controls storage unavailable")
	}
	return s.Store.PutRunBudget(ctx, a.AccountID, kind, ref, req)
}
func (s Service) GetBudget(ctx context.Context, a Access, kind, ref string) (api.RunBudgetPolicy, error) {
	if err := requireWide(a); err != nil {
		return api.RunBudgetPolicy{}, err
	}
	if s.Store == nil {
		return api.RunBudgetPolicy{}, api.ErrCapacity("Run controls storage unavailable")
	}
	return s.Store.GetRunBudget(ctx, a.AccountID, kind, ref)
}
func (s Service) PutBinding(ctx context.Context, a Access, kind, id string, req api.SetRunAttributionBindingRequest) (api.RunAttributionBinding, error) {
	if err := requireWide(a); err != nil {
		return api.RunAttributionBinding{}, err
	}
	if !s.Enabled {
		return api.RunAttributionBinding{}, api.ErrCapacity("new Run bindings are disabled")
	}
	if s.Store == nil {
		return api.RunAttributionBinding{}, api.ErrCapacity("Run controls storage unavailable")
	}
	return s.Store.PutRunBinding(ctx, a.AccountID, kind, id, req)
}
func (s Service) GetBinding(ctx context.Context, a Access, kind, id string) (api.RunAttributionBinding, error) {
	if err := requireWide(a); err != nil {
		return api.RunAttributionBinding{}, err
	}
	if s.Store == nil {
		return api.RunAttributionBinding{}, api.ErrCapacity("Run controls storage unavailable")
	}
	return s.Store.GetRunBinding(ctx, a.AccountID, kind, id)
}
func (s Service) DeleteBinding(ctx context.Context, a Access, kind, id string, revision int64) error {
	if err := requireWide(a); err != nil {
		return err
	}
	if s.Store == nil {
		return api.ErrCapacity("Run controls storage unavailable")
	}
	return s.Store.DeleteRunBinding(ctx, a.AccountID, kind, id, revision)
}

type historyCursor struct {
	At time.Time `json:"at"`
	ID string    `json:"id"`
}

func (s Service) History(ctx context.Context, a Access, f api.RunHistoryFilter) (api.RunHistoryResponse, error) {
	out := api.RunHistoryResponse{Runs: []api.RunHistoryEntry{}}
	if !a.Wide && a.PrincipalID == "" {
		return out, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Run history authority required", "use an unrestricted account reader or a Runs key family")
	}
	for _, ref := range []string{f.TaskName, f.CustomerRef, f.AgentRef} {
		if ref != "" && !api.ValidRunControlRef(ref) {
			return out, api.ErrValidation("invalid bounded history reference")
		}
	}
	if f.TaskVersion < 0 || f.TaskVersion > api.ExecutionTaskVersionMax || f.TaskVersion > 0 && f.TaskName == "" || !f.Since.IsZero() && !f.Until.IsZero() && !f.Since.Before(f.Until) {
		return out, api.ErrValidation("invalid history version or time range")
	}
	if f.Status != "" && !f.Status.Valid() {
		return out, api.ErrValidation("invalid Run status")
	}
	q := state.RunHistoryQuery{AccountID: a.AccountID, PrincipalID: a.PrincipalID, Wide: a.Wide, Filter: f, Limit: api.RunHistoryPageMax + 1}
	if f.Cursor != "" {
		if len(f.Cursor) > api.RunControlBodyMaxBytes {
			return out, api.ErrValidation("invalid history cursor")
		}
		raw, err := base64.RawURLEncoding.DecodeString(f.Cursor)
		var c historyCursor
		if err != nil || json.Unmarshal(raw, &c) != nil || c.At.IsZero() {
			return out, api.ErrValidation("invalid history cursor")
		}
		if _, err := uuid.Parse(c.ID); err != nil {
			return out, api.ErrValidation("invalid history cursor")
		}
		q.Before = c.At
		q.BeforeID = c.ID
	}
	if s.Store == nil {
		return out, api.ErrCapacity("Run history storage unavailable")
	}
	rows, err := s.Store.ListRunHistory(ctx, q)
	if err != nil {
		return out, err
	}
	if len(rows) > api.RunHistoryPageMax {
		rows = rows[:api.RunHistoryPageMax]
		last := rows[len(rows)-1]
		at, err := time.Parse(time.RFC3339Nano, last.CreatedAt)
		if err != nil {
			return out, err
		}
		raw, err := json.Marshal(historyCursor{at, last.ID})
		if err != nil {
			return out, err
		}
		out.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	out.Runs = rows
	return out, nil
}
func Problem(err error) *api.Problem {
	var p *api.Problem
	if errors.As(err, &p) {
		return p
	}
	var budget *state.RunBudgetError
	if errors.As(err, &budget) {
		p := api.NewProblem(http.StatusTooManyRequests, "run_budget_exceeded", "Run budget refused admission", budget.Error()).WithLimit(budget.Maximum, budget.Observed).WithDocs("https://gregale.dev/docs/run-controls")
		p.RunBudget = &api.RunBudgetRefusal{Kind: budget.Kind, Ref: budget.Ref, Metric: budget.Metric}
		return p
	}

	var quota *state.ExecutionTaskQuotaError
	if errors.As(err, &quota) {
		return api.NewProblem(http.StatusTooManyRequests, "run_controls_quota", "Run controls quota exceeded", quota.Error()).WithLimit(quota.Maximum, quota.Observed).WithDocs("https://gregale.dev/docs/run-controls")
	}
	switch {
	case errors.Is(err, state.ErrRunControlConflict):
		return api.NewProblem(http.StatusConflict, "run_control_revision_conflict", "Run control changed", "read the current revision before retrying")
	case errors.Is(err, state.ErrRunAttributionDenied):
		return api.NewProblem(http.StatusForbidden, "run_attribution_denied", "Run attribution refused", "customer_ref must match the authenticated binding; selecting an unbound customer requires unrestricted account authority")
	case errors.Is(err, state.ErrNotFound):
		return api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Run control unavailable", "no accessible control, key, endpoint or customer")
	case errors.Is(err, state.ErrExecutionInvalid):
		return api.ErrValidation("invalid Run control, limit, binding or revision")
	default:
		return api.ErrCapacity("Run controls are unavailable")
	}
}

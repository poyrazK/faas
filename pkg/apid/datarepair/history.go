package datarepair

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	core "github.com/onebox-faas/faas/pkg/datarepair"
	"github.com/onebox-faas/faas/pkg/state"
)

type HistoryReader interface {
	History(context.Context, core.Scope, string, core.HistoryQuery) (core.HistoryPage, error)
}

func historyQuery(raw string, hosted bool) (core.HistoryQuery, url.Values, error) {
	q, values, err := sourceQuery(raw, hosted)
	if err != nil {
		return core.HistoryQuery{}, nil, err
	}
	for key := range values {
		if key != "limit" && key != "cursor" && key != "environment" && key != "customer" {
			return core.HistoryQuery{}, nil, core.ErrInvalid
		}
	}
	return core.HistoryQuery{Limit: q.Limit, Cursor: q.Cursor}, values, nil
}

func HistoryQueryValues(q core.HistoryQuery) url.Values {
	return SourceQueryValues(core.SourceQuery{Limit: q.Limit, Cursor: q.Cursor})
}

func (h *Handler) history(w http.ResponseWriter, r *http.Request) {
	p, ok := h.principal(w, r)
	if !ok {
		return
	}
	q, _, err := historyQuery(r.URL.RawQuery, false)
	if err != nil {
		problem(w, err)
		return
	}
	page, err := h.Engine.History(r.Context(), p.Scope, r.PathValue("id"), q)
	if err != nil {
		problem(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, page)
}

func (c *Remote) History(ctx context.Context, scope core.Scope, source string, q core.HistoryQuery) (core.HistoryPage, error) {
	q, err := q.Normalize()
	if err != nil || scope.Validate() != nil || !validUUID(source) {
		return core.HistoryPage{}, core.ErrInvalid
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.origin+"/v1/data-repair/sources/"+url.PathEscape(source)+"/history?"+HistoryQueryValues(q).Encode(), nil)
	if err != nil {
		return core.HistoryPage{}, core.ErrInvalid
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.client.Do(req)
	if err != nil {
		return core.HistoryPage{}, core.ErrUnavailable
	}
	defer func() { _ = resp.Body.Close() }()
	if err := validationResponseStatus(resp.StatusCode); err != nil {
		return core.HistoryPage{}, err
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, int64(api.DataRepairSourceMaxResponseBytes+1)))
	var p core.HistoryPage
	if err != nil || len(raw) > api.DataRepairSourceMaxResponseBytes || json.Unmarshal(raw, &p) != nil || !p.Valid(q.Limit) || p.Scope != scope || p.SourceOperationID != source {
		return core.HistoryPage{}, core.ErrUnavailable
	}
	for _, item := range p.Items {
		if item.Execution != nil {
			return core.HistoryPage{}, core.ErrUnavailable
		}
	}
	return p, nil
}

func (s *Managed) historyBinding(w http.ResponseWriter, r *http.Request, acct state.Account) (Binding, core.HistoryQuery, bool) {
	w.Header().Set("Cache-Control", "no-store")
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return Binding{}, core.HistoryQuery{}, false
	}
	q, values, err := historyQuery(r.URL.RawQuery, true)
	if err != nil || !validUUID(r.PathValue("id")) {
		problem(w, core.ErrInvalid)
		return Binding{}, q, false
	}
	b, err := s.sourceBinding(acct.ID, app.ID, values)
	if err != nil {
		problem(w, err)
		return Binding{}, q, false
	}
	return b, q, true
}

func (s *Managed) History(w http.ResponseWriter, r *http.Request, acct state.Account) {
	b, q, ok := s.historyBinding(w, r, acct)
	if !ok {
		return
	}
	p, err := s.historyPage(r.Context(), b, r.PathValue("id"), q)
	if err != nil {
		problem(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, p)
}

func (s *Managed) historyPage(ctx context.Context, b Binding, source string, q core.HistoryQuery) (core.HistoryPage, error) {
	reader, ok := b.Executor.(HistoryReader)
	if !ok {
		return core.HistoryPage{}, core.ErrUnavailable
	}
	q, err := q.Normalize()
	if err != nil {
		return core.HistoryPage{}, err
	}
	if q.Cursor != "" {
		var cursor boundSourceCursor
		if core.DecodeSourceCursor(q.Cursor, &cursor) != nil || cursor.Binding != b.Identity || cursor.Cursor == "" {
			return core.HistoryPage{}, core.ErrInvalid
		}
		q.Cursor = cursor.Cursor
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(api.DataRepairTimeoutSeconds)*time.Second)
	defer cancel()
	p, err := reader.History(ctx, b.Scope, source, q)
	if err != nil {
		return core.HistoryPage{}, err
	}
	if !p.Valid(q.Limit) || p.Scope != b.Scope || p.SourceOperationID != source {
		return core.HistoryPage{}, core.ErrUnavailable
	}
	for i := range p.Items {
		item := &p.Items[i]
		if item.Execution != nil {
			return core.HistoryPage{}, core.ErrUnavailable
		}
		item.Execution, err = s.historyExecution(ctx, b, source, item.ID, item.Digest)
		if err != nil {
			return core.HistoryPage{}, err
		}
	}
	if p.NextCursor != "" {
		p.NextCursor = core.EncodeSourceCursor(boundSourceCursor{b.Identity, p.NextCursor})
	}
	raw, _ := json.Marshal(p)
	if !p.Valid(q.Limit) || len(raw) > api.DataRepairSourceMaxResponseBytes {
		return core.HistoryPage{}, core.ErrUnavailable
	}
	return p, nil
}

func (s *Managed) historyExecution(ctx context.Context, b Binding, source, id, digest string) (*core.HistoryExecution, error) {
	store, _, ok := s.store()
	if !ok {
		return nil, core.ErrUnavailable
	}
	op, err := store.OperationByID(ctx, b.Scope.AccountID, "", id)
	if errors.Is(err, state.ErrNotFound) || errors.Is(err, state.ErrOperationExpired) {
		return &core.HistoryExecution{Availability: "not_retained"}, nil
	}
	if err != nil {
		return nil, core.ErrUnavailable
	}
	if repairScope(op) != b.Scope || op.DataRepairBinding != b.Identity || op.DataRepair == nil ||
		op.DataRepair.PlanID != id || op.DataRepair.PlanDigest != digest || op.DataRepair.SourceOperationID != source {
		return &core.HistoryExecution{Availability: "unavailable"}, nil
	}
	execution := &core.HistoryExecution{Availability: "retained", ID: op.ID, State: op.State, Phase: op.DataRepair.Phase}
	if !execution.Valid(id) {
		return nil, core.ErrUnavailable
	}
	return execution, nil
}

// HistoricalPlanResponse provides read-only recovery independently of the
// hosted execution ledger. It never reconstructs a writable hosted operation.
type HistoricalPlanResponse struct {
	Plan      core.Plan              `json:"plan"`
	Execution *core.HistoryExecution `json:"execution"`
}

func retainedPlan(ctx context.Context, b Binding, source, id string) (core.Plan, error) {
	if !validUUID(id) {
		return core.Plan{}, core.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(api.DataRepairTimeoutSeconds)*time.Second)
	defer cancel()
	p, err := b.Executor.Get(ctx, b.Scope, id)
	if err != nil {
		return core.Plan{}, err
	}
	if p.ID != id || p.Scope != b.Scope || p.SourceOperationID != source {
		return core.Plan{}, core.ErrNotFound
	}
	hash, err := hex.DecodeString(p.Digest)
	if err != nil || len(hash) != 32 || hex.EncodeToString(hash) != p.Digest {
		return core.Plan{}, core.ErrUnavailable
	}
	return p, nil
}

func (s *Managed) HistoryPlan(w http.ResponseWriter, r *http.Request, acct state.Account) {
	b, q, ok := s.historyBinding(w, r, acct)
	if !ok {
		return
	}
	if q.Cursor != "" || r.URL.Query().Has("limit") {
		problem(w, core.ErrInvalid)
		return
	}
	p, err := retainedPlan(r.Context(), b, r.PathValue("id"), r.PathValue("plan"))
	if err != nil {
		problem(w, err)
		return
	}
	execution, err := s.historyExecution(r.Context(), b, p.SourceOperationID, p.ID, p.Digest)
	if err != nil {
		problem(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, HistoricalPlanResponse{Plan: p, Execution: execution})
}

func (s *Managed) HistoryVerify(w http.ResponseWriter, r *http.Request, acct state.Account) {
	b, q, ok := s.historyBinding(w, r, acct)
	if !ok {
		return
	}
	if q.Cursor != "" || r.URL.Query().Has("limit") {
		problem(w, core.ErrInvalid)
		return
	}
	digest, ok := decodeValidation(w, r)
	if !ok {
		return
	}
	p, err := retainedPlan(r.Context(), b, r.PathValue("id"), r.PathValue("plan"))
	if err != nil {
		problem(w, err)
		return
	}
	if p.Digest != digest {
		problem(w, core.ErrConflict)
		return
	}
	x, ok := b.Executor.(VerificationExecutor)
	if !ok {
		problem(w, core.ErrUnavailable)
		return
	}
	report, err := x.Verify(r.Context(), b.Scope, p.ID, digest)
	if err != nil {
		problem(w, err)
		return
	}
	if !report.MatchesPlan(p) {
		problem(w, core.ErrUnavailable)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, report)
}

package datarepair

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	core "github.com/onebox-faas/faas/pkg/datarepair"
	"github.com/onebox-faas/faas/pkg/state"
)

// VerificationExecutor is additive: old executors cannot claim support for
// current observations by merely returning their historical apply receipt.
type VerificationExecutor interface {
	Verify(context.Context, core.Scope, string, string) (core.VerificationReport, error)
}

func (h *Handler) verify(w http.ResponseWriter, r *http.Request) {
	p, ok := h.principal(w, r)
	if !ok {
		return
	}
	digest, ok := decodeValidation(w, r)
	if !ok {
		return
	}
	report, err := h.Engine.Verify(r.Context(), p.Scope, r.PathValue("id"), digest)
	if err != nil {
		problem(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, report)
}

func (s *Managed) Verify(w http.ResponseWriter, r *http.Request, acct state.Account) {
	op, b, ok := s.owned(w, r, acct)
	if !ok {
		return
	}
	digest, ok := decodeValidation(w, r)
	if !ok {
		return
	}
	if digest != op.DataRepair.PlanDigest {
		problem(w, core.ErrConflict)
		return
	}
	x, ok := b.Executor.(VerificationExecutor)
	if !ok {
		problem(w, core.ErrUnavailable)
		return
	}
	report, err := x.Verify(r.Context(), b.Scope, op.DataRepair.PlanID, digest)
	if err != nil {
		problem(w, err)
		return
	}
	if !report.Valid() || report.PlanID != op.DataRepair.PlanID || report.PlanDigest != digest || report.Scope != b.Scope ||
		report.SourceOperationID != op.DataRepair.SourceOperationID || report.RepairOperationID != op.ID ||
		report.Restored != op.DataRepair.Candidates || report.Excluded != op.DataRepair.Conflicts+op.DataRepair.Unavailable ||
		(op.DataRepair.AppliedAt != nil && !report.AppliedAt.Equal(*op.DataRepair.AppliedAt)) {
		problem(w, core.ErrUnavailable)
		return
	}
	// Observation never settles an unknown outcome or changes terminal history.
	httpkit.WriteJSON(w, http.StatusOK, report)
}

func (c *Remote) Verify(ctx context.Context, scope core.Scope, id, digest string) (core.VerificationReport, error) {
	if scope.Validate() != nil || !validUUID(id) || len(digest) != 64 {
		return core.VerificationReport{}, core.ErrInvalid
	}
	body, _ := json.Marshal(map[string]string{"digest": digest})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.origin+"/v1/data-repair/plans/"+url.PathEscape(id)+"/verify", bytes.NewReader(body))
	if err != nil {
		return core.VerificationReport{}, core.ErrInvalid
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return core.VerificationReport{}, core.ErrUnavailable
	}
	defer func() { _ = resp.Body.Close() }()
	if err := validationResponseStatus(resp.StatusCode); err != nil {
		return core.VerificationReport{}, err
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, int64(api.DataRepairMaxPlanBytes+1)))
	var report core.VerificationReport
	if err != nil || len(raw) > api.DataRepairMaxPlanBytes || json.Unmarshal(raw, &report) != nil || !report.Valid() ||
		report.Scope != scope || report.PlanID != id || report.PlanDigest != digest {
		return core.VerificationReport{}, core.ErrUnavailable
	}
	return report, nil
}

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

// ValidationExecutor is additive. An older executor can still serve existing
// plans without claiming support for the advisory drift-report contract.
type ValidationExecutor interface {
	Validate(context.Context, core.Scope, string, string) (core.ValidationReport, error)
}

func decodeValidation(w http.ResponseWriter, r *http.Request) (string, bool) {
	var in struct {
		Digest string `json:"digest"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, int64(api.DataRepairMaxRequestBytes)))
	d.DisallowUnknownFields()
	if d.Decode(&in) != nil || d.Decode(new(any)) != io.EOF || in.Digest == "" {
		problem(w, core.ErrInvalid)
		return "", false
	}
	return in.Digest, true
}

func (h *Handler) validate(w http.ResponseWriter, r *http.Request) {
	p, ok := h.principal(w, r)
	if !ok {
		return
	}
	digest, ok := decodeValidation(w, r)
	if !ok {
		return
	}
	report, err := h.Engine.Validate(r.Context(), p.Scope, r.PathValue("id"), digest)
	if err != nil {
		problem(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, report)
}

func (s *Managed) Validate(w http.ResponseWriter, r *http.Request, acct state.Account) {
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
	x, ok := b.Executor.(ValidationExecutor)
	if !ok {
		problem(w, core.ErrUnavailable)
		return
	}
	report, err := x.Validate(r.Context(), b.Scope, op.DataRepair.PlanID, digest)
	if err != nil {
		problem(w, err)
		return
	}
	if !report.Valid() || report.PlanID != op.DataRepair.PlanID || report.PlanDigest != digest || report.Scope != b.Scope {
		problem(w, core.ErrUnavailable)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, report)
}

func (c *Remote) Validate(ctx context.Context, scope core.Scope, id, digest string) (core.ValidationReport, error) {
	if scope.Validate() != nil || !validUUID(id) || len(digest) != 64 {
		return core.ValidationReport{}, core.ErrInvalid
	}
	body, _ := json.Marshal(map[string]string{"digest": digest})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.origin+"/v1/data-repair/plans/"+url.PathEscape(id)+"/validate", bytes.NewReader(body))
	if err != nil {
		return core.ValidationReport{}, core.ErrInvalid
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return core.ValidationReport{}, core.ErrUnavailable
	}
	defer func() { _ = resp.Body.Close() }()
	if err := validationResponseStatus(resp.StatusCode); err != nil {
		return core.ValidationReport{}, err
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, int64(api.DataRepairMaxPlanBytes+1)))
	var report core.ValidationReport
	if err != nil || len(raw) > api.DataRepairMaxPlanBytes || json.Unmarshal(raw, &report) != nil || !report.Valid() ||
		report.Scope != scope || report.PlanID != id || report.PlanDigest != digest {
		return core.ValidationReport{}, core.ErrUnavailable
	}
	return report, nil
}

func validationResponseStatus(status int) error {
	switch status {
	case http.StatusOK:
		return nil
	case http.StatusBadRequest:
		return core.ErrInvalid
	case http.StatusNotFound:
		return core.ErrNotFound
	case http.StatusConflict:
		return core.ErrConflict
	default:
		return core.ErrUnavailable
	}
}

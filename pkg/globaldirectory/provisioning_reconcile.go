package globaldirectory

// adr: 797
import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
)

func (v Verifier) VerifyAccount(ctx context.Context, cell Cell, key, account string) error {
	if cell.Validate() != nil || !cell.Enabled || cell.Generation < 1 || !ValidID(account) || !api.ValidAPIKeyFormat(key) {
		return ErrVerification
	}
	var proof api.CellAccountBindingResponse
	status, err := v.provisionJSON(ctx, http.MethodGet, cell.APIBase+"/v1/cell/account/binding", key, nil, &proof)
	if err != nil || status != http.StatusOK || proof.CellID != cell.ID || proof.Region != cell.Region || proof.AccountID != account {
		return ErrVerification
	}
	return nil
}

// Response bodies and transport errors never enter persisted diagnostics.
func (v Verifier) provisionJSON(ctx context.Context, method, path, key string, body []byte, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, method, path, bytes.NewReader(body))
	if err != nil {
		return 0, ErrVerification
	}
	req.Header.Set("Authorization", "Bearer "+key)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := v.probeClient().Do(req)
	if err != nil {
		return 0, ErrVerification
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return resp.StatusCode, ErrVerification
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, api.GlobalAppProvisionRequestMaxBytes+1))
	if err != nil || len(raw) > api.GlobalAppProvisionRequestMaxBytes {
		return resp.StatusCode, ErrVerification
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil || d.Decode(&struct{}{}) != io.EOF {
		return resp.StatusCode, ErrVerification
	}
	return resp.StatusCode, nil
}

type ProvisionResult struct {
	Plan       AppProvision
	Target     *Target
	Diagnostic string
}

func ReconcileAppProvision(ctx context.Context, directory Store, plans AppProvisionStore, v Verifier, owner, slug string, p AppProvision, key string) ProvisionResult {
	out := ProvisionResult{Plan: p}
	if p.Status == ProvisionCancelled {
		return out
	}
	cell, err := directory.CellByRegion(ctx, p.Region)
	if err != nil || !cell.Enabled || cell.ID != p.Cell.ID || cell.Generation != p.Cell.Generation || cell.APIBase != p.Cell.APIBase {
		out.Diagnostic = DiagnosticIdentity
		return out
	}
	if !api.ValidAPIKeyFormat(key) {
		out.Diagnostic = DiagnosticRejected
		return out
	}
	path := p.Cell.APIBase + "/v1/cells/" + p.Cell.ID + "/app-provisions/" + p.ID
	var receipt api.CellAppProvisionResponse
	status, err := v.provisionJSON(ctx, http.MethodGet, path, key, nil, &receipt)
	if status == http.StatusNotFound {
		var req api.CreateAppRequest
		if json.Unmarshal(p.Request, &req) != nil {
			out.Diagnostic = DiagnosticReceipt
			return out
		}
		body, _ := json.Marshal(api.CellAppProvisionRequest{GlobalAppID: p.GlobalAppID, App: req})
		status, err = v.provisionJSON(ctx, http.MethodPut, path, key, body, &receipt)
	}
	if err != nil {
		out.Diagnostic = DiagnosticUnavailable
		if status >= http.StatusBadRequest && status < http.StatusInternalServerError {
			out.Diagnostic = DiagnosticRejected
		}
		if status == http.StatusGone {
			out.Diagnostic = ReleaseExpired
		}
		return out
	}
	raw, _ := json.Marshal(receipt.Request)
	b := Binding{CellID: receipt.Binding.CellID, Region: receipt.Binding.Region, CellGeneration: p.Cell.Generation, RegionalAccountID: receipt.Binding.AccountID, RegionalOrgID: receipt.Binding.OrgID, RegionalAppID: receipt.Binding.AppID, RegionalAppSlug: receipt.Binding.AppSlug}
	if receipt.ProvisionID != p.ID || receipt.GlobalAppID != p.GlobalAppID || receipt.CreatedAt.IsZero() || !bytes.Equal(raw, p.Request) || b.Validate() != nil || !p.bindingMatches(b) {
		out.Diagnostic = DiagnosticReceipt
		return out
	}
	proof, err := v.Verify(ctx, cell, b.RegionalAppSlug, key)
	if err != nil || proof != b {
		out.Diagnostic = DiagnosticRejected
		return out
	}
	target, err := plans.AttachProvisionTarget(ctx, owner, slug, p.ID, b)
	if err != nil {
		out.Diagnostic = DiagnosticIdentity
		if !errors.Is(err, ErrStaleCell) && !errors.Is(err, ErrConflict) {
			out.Diagnostic = DiagnosticUnavailable
		}
		return out
	}
	out.Plan.Status = ProvisionAttached
	out.Target = &target
	return out
}

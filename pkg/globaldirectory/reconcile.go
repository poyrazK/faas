package globaldirectory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"

	"github.com/onebox-faas/faas/pkg/api"
)

// Reconciler uses request-only regional keys. Durable state contains no key.
// It never calls regional VM services or publishes readiness/ingress.
type Reconciler struct {
	Store    Store
	Verifier Verifier
}

func (c Reconciler) Resume(ctx context.Context, owner, slug, releaseID string, keys map[string]string) (Release, error) {
	ctx, cancel := context.WithTimeout(ctx, api.GlobalImageReleaseRequestTimeout)
	defer cancel()
	r, err := c.Store.GetRelease(ctx, owner, slug, releaseID)
	if err != nil {
		return Release{}, err
	}
	if r.Status() == ReleaseSuperseded {
		return r, nil
	}
	work := make(chan ReleaseTarget, len(r.Targets))
	failures := make(chan error, len(r.Targets))
	for _, target := range r.Targets {
		if target.Outcome == ReleasePending {
			work <- target
		}
	}
	close(work)
	var wg sync.WaitGroup
	for range api.GlobalImageReleaseConcurrentTargets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for target := range work {
				if err := c.resumeTarget(ctx, owner, slug, r, target, keys[target.Region]); err != nil {
					failures <- err
				}
			}
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		if !errors.Is(err, ErrSuperseded) {
			return Release{}, err
		}
	}
	return c.Store.GetRelease(ctx, owner, slug, releaseID)
}

func (c Reconciler) resumeTarget(ctx context.Context, owner, slug string, r Release, target ReleaseTarget, key string) error {
	if err := c.Store.CheckReleaseTarget(ctx, owner, slug, r.ReleaseID, target.Region); err != nil {
		if errors.Is(err, ErrStaleCell) {
			return c.Store.RecordReleaseResult(ctx, owner, slug, r.ReleaseID, target.Region, ReleaseResult{Outcome: ReleaseBlocked, Diagnostic: DiagnosticIdentity})
		}
		return err
	}
	if key == "" {
		return nil
	}
	cell := Cell{ID: target.CellID, Region: target.Region, APIBase: target.APIBase, Generation: target.CellGeneration, Enabled: true}
	proof, err := c.Verifier.Verify(ctx, cell, target.RegionalAppSlug, key)
	result := ReleaseResult{Outcome: ReleasePending, Diagnostic: DiagnosticRejected}
	if err == nil && proof == target.Binding {
		// A directory change during the proof must not redirect dispatch.
		if err := c.Store.CheckReleaseTarget(ctx, owner, slug, r.ReleaseID, target.Region); err != nil {
			return err
		}
		result = c.regionalReceipt(ctx, owner, slug, r, target, key)
	}
	err = c.Store.RecordReleaseResult(ctx, owner, slug, r.ReleaseID, target.Region, result)
	if errors.Is(err, ErrStaleCell) {
		return c.Store.RecordReleaseResult(ctx, owner, slug, r.ReleaseID, target.Region, ReleaseResult{Outcome: ReleaseBlocked, Diagnostic: DiagnosticIdentity})
	}
	return err
}

func (c Reconciler) regionalReceipt(ctx context.Context, owner, slug string, r Release, target ReleaseTarget, key string) ReleaseResult {
	path := target.APIBase + "/v1/cells/" + target.CellID + "/apps/" + target.RegionalAppID + "/image-releases/" + r.ReleaseID
	receipt, status, diagnostic := c.fetchReceipt(ctx, http.MethodGet, path, key, nil)
	if status == http.StatusNotFound {
		if err := c.Store.CheckReleaseTarget(ctx, owner, slug, r.ReleaseID, target.Region); err != nil {
			return ReleaseResult{Outcome: ReleasePending, Diagnostic: DiagnosticRejected}
		}
		// The same release identity and ordering are used after every uncertain try.
		request := api.PutCellImageReleaseRequest{Image: r.Image, Ordering: &api.CellImageReleaseOrdering{GlobalAppID: r.GlobalAppID, Generation: r.Generation}}
		body, err := json.Marshal(request)
		if err != nil {
			return ReleaseResult{Outcome: ReleasePending, Diagnostic: DiagnosticReceipt}
		}
		receipt, _, diagnostic = c.fetchReceipt(ctx, http.MethodPut, path, key, body)
	}
	if diagnostic != "" {
		return ReleaseResult{Outcome: ReleasePending, Diagnostic: diagnostic}
	}
	if receipt.CellID != target.CellID || receipt.Region != target.Region || receipt.RegionalAccountID != target.RegionalAccountID || receipt.RegionalAppID != target.RegionalAppID ||
		receipt.ReleaseID != r.ReleaseID || receipt.Image != r.Image || receipt.Ordering == nil || receipt.Ordering.GlobalAppID != r.GlobalAppID || receipt.Ordering.Generation != r.Generation || receipt.CreatedAt.IsZero() {
		return ReleaseResult{Outcome: ReleasePending, Diagnostic: DiagnosticReceipt}
	}
	if receipt.DeploymentID == nil {
		if receipt.DeploymentStatus == ReleaseExpired {
			return ReleaseResult{Outcome: ReleaseExpired}
		}
		return ReleaseResult{Outcome: ReleasePending, Diagnostic: DiagnosticReceipt}
	}
	if !ValidID(*receipt.DeploymentID) || !validRegionalDeploymentStatus(receipt.DeploymentStatus) {
		return ReleaseResult{Outcome: ReleasePending, Diagnostic: DiagnosticReceipt}
	}
	return ReleaseResult{Outcome: ReleaseAccepted, DeploymentID: *receipt.DeploymentID}
}

func validRegionalDeploymentStatus(status string) bool {
	switch status {
	case "pending", "building", "imaging", "snapshotting", "live", "failed", "superseded", "cancelled":
		return true
	default:
		return false
	}
}

func (c Reconciler) fetchReceipt(ctx context.Context, method, path, key string, body []byte) (api.CellImageReleaseResponse, int, string) {
	ctx, cancel := context.WithTimeout(ctx, api.GlobalImageReleaseRegionalTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, path, bytes.NewReader(body))
	if err != nil {
		return api.CellImageReleaseResponse{}, 0, DiagnosticUnavailable
	}
	req.Header.Set("Authorization", "Bearer "+key)
	if method == http.MethodPut {
		req.Header.Set("Content-Type", "application/json")
	}
	client := c.Verifier.probeClient()
	resp, err := client.Do(req)
	if err != nil {
		return api.CellImageReleaseResponse{}, 0, DiagnosticUnavailable
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return api.CellImageReleaseResponse{}, resp.StatusCode, DiagnosticRejected
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, api.GlobalImageReleaseReceiptMaxBytes+1))
	if err != nil || len(data) > api.GlobalImageReleaseReceiptMaxBytes {
		return api.CellImageReleaseResponse{}, resp.StatusCode, DiagnosticReceipt
	}
	var out api.CellImageReleaseResponse
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&out) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return out, resp.StatusCode, DiagnosticReceipt
	}
	return out, resp.StatusCode, ""
}

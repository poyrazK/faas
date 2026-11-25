package neon

import (
	"context"
	"net/http"
	"net/url"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

var _ managedpostgres.SuspendTimeoutConverger = (*Provider)(nil)

// ConvergeSuspendTimeout moves a scale-to-zero database's primary endpoint
// onto target seconds. It writes only a suspended (idle),
// enabled endpoint with nothing pending, so a compute serving queries or held
// by a cap is never touched; anything else is deferred to a later sweep.
func (p *Provider) ConvergeSuspendTimeout(ctx context.Context, providerResourceID string, target int64) (managedpostgres.SuspendTimeoutResult, error) {
	if target < managedpostgres.MinSuspendTimeoutSeconds || target > managedpostgres.MaxSuspendTimeoutSeconds {
		return managedpostgres.SuspendTimeoutDeferred, managedpostgres.ErrInvalid
	}
	ref, err := parseResourceRef(providerResourceID)
	if err != nil {
		return managedpostgres.SuspendTimeoutDeferred, err
	}
	metadata, err := p.readDatabaseMetadata(ctx, ref, false)
	if err != nil {
		return managedpostgres.SuspendTimeoutDeferred, err
	}
	_, primary, ready := selectBranch(metadata.branches.Branches, metadata.endpoints.Endpoints, ref.branchID)
	if !ready || primary.ID == "" || !validProviderID.MatchString(primary.ID) {
		return managedpostgres.SuspendTimeoutDeferred, nil
	}
	switch {
	case primary.SuspendTimeoutSecond == -1 || primary.SuspendTimeoutSecond == target:
		return managedpostgres.SuspendTimeoutConverged, nil
	case primary.CurrentState != "idle" || primary.PendingState != "" || endpointDisabled(primary):
		return managedpostgres.SuspendTimeoutDeferred, nil
	}
	var payload updateComputeRequest
	payload.Endpoint.SuspendTimeoutSecond = &target
	path := "/projects/" + url.PathEscape(ref.projectID) + "/endpoints/" + url.PathEscape(primary.ID)
	if err := p.doJSON(ctx, http.MethodPatch, path, nil, payload, nil, http.StatusOK); err != nil {
		return managedpostgres.SuspendTimeoutDeferred, err
	}
	return managedpostgres.SuspendTimeoutChanged, nil
}

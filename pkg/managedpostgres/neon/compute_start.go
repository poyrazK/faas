package neon

import (
	"context"
	"net/http"
	"net/url"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

var _ managedpostgres.ComputeStarter = (*Provider)(nil)

// StartCompute starts the database's suspended read-write compute
// (ADR-20261125105038): one endpoint read and, when the compute is idle, one
// start. It is not paced as background work, because a waking app's request
// waits on it. A running, held (disabled) or ambiguous compute is left alone.
func (p *Provider) StartCompute(ctx context.Context, providerResourceID string) error {
	ref, err := parseResourceRef(providerResourceID)
	if err != nil {
		return managedpostgres.ErrInvalid
	}
	var endpoints endpointsResponse
	if err := p.doJSON(ctx, http.MethodGet, "/projects/"+url.PathEscape(ref.projectID)+"/endpoints", nil, nil, &endpoints, http.StatusOK); err != nil {
		return err
	}
	var compute endpoint
	for _, candidate := range endpoints.Endpoints {
		if candidate.Type != "read_write" || ref.branchID != "" && candidate.BranchID != ref.branchID {
			continue
		}
		if compute.ID != "" {
			// A restore's second compute: which one the app uses is not
			// known here.
			return managedpostgres.ErrUnsupported
		}
		compute = candidate
	}
	if !validProviderID.MatchString(compute.ID) {
		return managedpostgres.ErrUnavailable
	}
	if endpointDisabled(compute) || compute.CurrentState != "idle" {
		return nil
	}
	path := "/projects/" + url.PathEscape(ref.projectID) + "/endpoints/" + url.PathEscape(compute.ID) + "/start"
	return p.doJSON(ctx, http.MethodPost, path, nil, nil, nil, http.StatusOK)
}

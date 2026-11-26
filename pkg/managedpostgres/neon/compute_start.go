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
func (p *Provider) StartCompute(ctx context.Context, providerResourceID string) (bool, error) {
	ref, err := parseResourceRef(providerResourceID)
	if err != nil {
		return false, managedpostgres.ErrInvalid
	}
	compute, err := p.readWriteEndpoint(ctx, ref)
	if err != nil {
		return false, err
	}
	if endpointDisabled(compute) || compute.CurrentState != "idle" {
		return false, nil
	}
	path := "/projects/" + url.PathEscape(ref.projectID) + "/endpoints/" + url.PathEscape(compute.ID) + "/start"
	if err := p.doJSON(ctx, http.MethodPost, path, nil, nil, nil, http.StatusOK); err != nil {
		return false, err
	}
	return true, nil
}

// readWriteEndpoint reads the database's single read-write endpoint.
func (p *Provider) readWriteEndpoint(ctx context.Context, ref resourceRef) (endpoint, error) {
	var endpoints endpointsResponse
	if err := p.doJSON(ctx, http.MethodGet, "/projects/"+url.PathEscape(ref.projectID)+"/endpoints", nil, nil, &endpoints, http.StatusOK); err != nil {
		return endpoint{}, err
	}
	var compute endpoint
	for _, candidate := range endpoints.Endpoints {
		if candidate.Type != "read_write" || ref.branchID != "" && candidate.BranchID != ref.branchID {
			continue
		}
		if compute.ID != "" {
			// A restore's second compute: which one the app uses is not
			// known here.
			return endpoint{}, managedpostgres.ErrUnsupported
		}
		compute = candidate
	}
	if !validProviderID.MatchString(compute.ID) {
		return endpoint{}, managedpostgres.ErrUnavailable
	}
	return compute, nil
}

package neon

import (
	"context"
	"net/http"
	"net/url"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

type updateComputeRequest struct {
	Endpoint struct {
		MinimumCU float64 `json:"autoscaling_limit_min_cu"`
		MaximumCU float64 `json:"autoscaling_limit_max_cu"`
	} `json:"endpoint"`
}

// Update converges only the pinned primary's class. An uncertain PATCH is
// recovered by reading the configuration; it is never blindly replayed.
func (p *Provider) Update(ctx context.Context, request managedpostgres.UpdateRequest) (managedpostgres.ObservedDatabase, error) {
	if request.Generation < 2 || request.IdempotencyKey == "" || len(request.IdempotencyKey) > 255 {
		return managedpostgres.ObservedDatabase{}, managedpostgres.ErrInvalid
	}
	if err := p.Capabilities().Supports(request.Spec); err != nil {
		return managedpostgres.ObservedDatabase{}, err
	}
	if err := p.Capabilities().Supports(request.PreviousSpec); err != nil {
		return managedpostgres.ObservedDatabase{}, err
	}
	previous := request.PreviousSpec
	previous.Class = request.Spec.Class
	if previous != request.Spec {
		return managedpostgres.ObservedDatabase{}, managedpostgres.ErrUnsupported
	}
	observed, primary, err := p.resizeObservation(ctx, request)
	if err != nil {
		return managedpostgres.ObservedDatabase{}, err
	}
	if observed.Status != managedpostgres.ProviderStatusReady || observed.Spec == request.Spec {
		return observed, nil
	}
	payload := updateComputeRequest{}
	profile := profiles[request.Spec.Class]
	payload.Endpoint.MinimumCU, payload.Endpoint.MaximumCU = profile.minimumCU, profile.maximumCU
	ref, _ := parseResourceRef(request.ResourceID)
	path := "/projects/" + url.PathEscape(ref.projectID) + "/endpoints/" + url.PathEscape(primary.ID)
	// The mutation response is not readiness evidence. A worker also takes this
	// observation path after a crash or a lost response from the same request.
	err = p.doJSON(ctx, http.MethodPatch, path, nil, payload, nil, http.StatusOK)
	if err != nil {
		return managedpostgres.ObservedDatabase{}, err
	}
	observed, _, err = p.resizeObservation(ctx, request)
	if err == nil && observed.Status == managedpostgres.ProviderStatusReady && observed.Spec != request.Spec {
		observed.Status = managedpostgres.ProviderStatusPending
	}
	return observed, err
}

func (p *Provider) resizeObservation(ctx context.Context, request managedpostgres.UpdateRequest) (managedpostgres.ObservedDatabase, endpoint, error) {
	lifecycle, err := parseResourceRef(request.ResourceID)
	if err != nil {
		return managedpostgres.ObservedDatabase{}, endpoint{}, err
	}
	data, err := parseResourceRef(request.DataResourceID)
	if err != nil || data.projectID != lifecycle.projectID || data.branchID == "" || lifecycle.branchID != "" && lifecycle.branchID != data.branchID {
		return managedpostgres.ObservedDatabase{}, endpoint{}, managedpostgres.ErrInvalid
	}
	metadata, err := p.readDatabaseMetadata(ctx, data, true)
	if err != nil {
		return managedpostgres.ObservedDatabase{}, endpoint{}, err
	}
	if metadata.operations.Pagination.Cursor != "" {
		return managedpostgres.ObservedDatabase{}, endpoint{}, managedpostgres.ErrUnavailable
	}
	selected, primary, ready := selectBranch(metadata.branches.Branches, metadata.endpoints.Endpoints, data.branchID)
	if selected.ID != data.branchID || !validProviderID.MatchString(primary.ID) ||
		selected.ProjectID != "" && selected.ProjectID != data.projectID || primary.ProjectID != "" && primary.ProjectID != data.projectID ||
		primary.RegionID != "" && primary.RegionID != p.regionID || primary.Disabled != nil && *primary.Disabled {
		return managedpostgres.ObservedDatabase{}, endpoint{}, managedpostgres.ErrConflict
	}
	if lifecycle.branchID == "" {
		currentDefault, _, _ := selectBranch(metadata.branches.Branches, metadata.endpoints.Endpoints, "")
		if currentDefault.ID != data.branchID {
			return managedpostgres.ObservedDatabase{}, endpoint{}, managedpostgres.ErrConflict
		}
	}
	actual := p.observedSpec(metadata.project.Project, primary)
	if actual != request.Spec && actual != request.PreviousSpec {
		return managedpostgres.ObservedDatabase{}, endpoint{}, managedpostgres.ErrConflict
	}
	relevant := make([]operation, 0, len(metadata.operations.Operations))
	for _, op := range metadata.operations.Operations {
		if op.ProjectID != "" && op.ProjectID != data.projectID {
			return managedpostgres.ObservedDatabase{}, endpoint{}, managedpostgres.ErrUnavailable
		}
		if op.EndpointID != "" && op.EndpointID != primary.ID || op.BranchID != "" && op.BranchID != data.branchID {
			continue
		}
		relevant = append(relevant, op)
	}
	ready = ready && primary.PendingState == "" && selected.PendingState == ""
	return managedpostgres.ObservedDatabase{ProviderResourceID: request.ResourceID, DataResourceID: request.DataResourceID,
		Spec: actual, Status: operationStatus(relevant, ready), ComputeState: computeState(primary.CurrentState)}, primary, nil
}

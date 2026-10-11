package neon

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"golang.org/x/sync/errgroup"
)

type databaseMetadata struct {
	project    projectResponse
	branches   branchesResponse
	endpoints  endpointsResponse
	operations operationsResponse
}

// readDatabaseMetadata never opens SQL connections or retrieves credentials.
// Lifecycle inspection includes operations; health observes current resources
// without letting historical operation failures/pagination distort health.
func (p *Provider) readDatabaseMetadata(ctx context.Context, ref resourceRef, includeOperations bool) (databaseMetadata, error) {
	var metadata databaseMetadata
	path := "/projects/" + url.PathEscape(ref.projectID)
	group, groupCtx := errgroup.WithContext(ctx)
	group.Go(func() error {
		return p.doJSON(groupCtx, http.MethodGet, path, nil, nil, &metadata.project, http.StatusOK)
	})
	group.Go(func() error {
		var err error
		metadata.branches.Branches, err = p.listProjectBranches(groupCtx, ref.projectID, "")
		if errors.Is(err, managedpostgres.ErrConflict) {
			// Contradictory provider inventory is an availability incident,
			// not a customer intent conflict or evidence of missing data.
			return managedpostgres.ErrUnavailable
		}
		return err
	})
	group.Go(func() error {
		return p.doJSON(groupCtx, http.MethodGet, path+"/endpoints", nil, nil, &metadata.endpoints, http.StatusOK)
	})
	if includeOperations {
		group.Go(func() error {
			var err error
			metadata.operations.Operations, err = p.listProjectOperations(groupCtx, ref.projectID)
			return err
		})
	}
	if err := group.Wait(); err != nil {
		return databaseMetadata{}, err
	}
	if metadata.project.Project.ID != ref.projectID {
		return databaseMetadata{}, managedpostgres.ErrUnavailable
	}
	return metadata, nil
}

var _ managedpostgres.DatabaseObserver = (*Provider)(nil)

// Observe is the read-only monitoring boundary, including restored branches.
// It deliberately does not call Inspect's inherited-login security repair.
func (p *Provider) Observe(ctx context.Context, providerResourceID string) (managedpostgres.ObservedDatabase, error) {
	ref, err := parseResourceRef(providerResourceID)
	if err != nil {
		return managedpostgres.ObservedDatabase{}, err
	}
	metadata, err := p.readDatabaseMetadata(ctx, ref, false)
	if err != nil {
		return managedpostgres.ObservedDatabase{}, err
	}
	selected, primary, ready := selectBranch(metadata.branches.Branches, metadata.endpoints.Endpoints, ref.branchID)
	if selected.ID == "" {
		for _, candidate := range metadata.branches.Branches {
			if (ref.branchID == "" && candidate.Default) || (ref.branchID != "" && candidate.ID == ref.branchID) {
				return managedpostgres.ObservedDatabase{}, managedpostgres.ErrUnavailable
			}
		}
		return managedpostgres.ObservedDatabase{}, managedpostgres.ErrNotFound
	}
	status := managedpostgres.ProviderStatusPending
	if ready {
		status = managedpostgres.ProviderStatusReady
	}
	if primary.ID == "" {
		for _, candidate := range metadata.endpoints.Endpoints {
			if candidate.BranchID == selected.ID && candidate.Type == "read_write" {
				return managedpostgres.ObservedDatabase{}, managedpostgres.ErrUnavailable
			}
		}
		status = managedpostgres.ProviderStatusFailed
	}
	return managedpostgres.ObservedDatabase{ProviderResourceID: providerResourceID, Status: status,
		ComputeState: computeState(primary.CurrentState), Spec: p.observedSpec(metadata.project.Project, primary)}, nil
}

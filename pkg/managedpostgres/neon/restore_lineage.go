package neon

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

const restoreLineageTimeout = 20 * time.Second

// A successful POST may precede Neon's asynchronous parent metadata. Read
// only the acknowledged identity; never repeat a non-idempotent create or
// replace the provider's proof with the requested lineage.
func (p *Provider) awaitRestoredBranch(ctx context.Context, projectID, parentID, name string, target branch, request managedpostgres.RestoreRequest) (managedpostgres.ObservedDatabase, error) {
	ctx, cancel := context.WithTimeout(ctx, restoreLineageTimeout)
	defer cancel()
	id := target.ID
	for {
		if target.ID != id {
			return managedpostgres.ObservedDatabase{}, managedpostgres.ErrConflict
		}
		observed, err := restoredBranchObservation(projectID, parentID, name, target, request)
		if err == nil || !errors.Is(err, managedpostgres.ErrUnavailable) || !validProviderID.MatchString(id) || target.Name != name ||
			(target.ParentID != "" && target.ParentID != parentID) ||
			(target.ParentID != "" && target.ParentTimestamp != "" && target.ProjectID != "" && target.InitSource != "") {
			return observed, err
		}
		var response struct {
			Branch branch `json:"branch"`
		}
		path := "/projects/" + url.PathEscape(projectID) + "/branches/" + url.PathEscape(id)
		if err := p.doJSON(ctx, http.MethodGet, path, nil, nil, &response, http.StatusOK); err != nil {
			if ctx.Err() != nil || errors.Is(err, managedpostgres.ErrNotFound) {
				err = managedpostgres.ErrUnavailable
			}
			return managedpostgres.ObservedDatabase{}, err
		}
		target = response.Branch
		if target.ParentID != "" && target.ParentTimestamp != "" && target.ProjectID != "" && target.InitSource != "" {
			continue
		}
		timer := time.NewTimer(p.credentialPollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return managedpostgres.ObservedDatabase{}, managedpostgres.ErrUnavailable
		case <-timer.C:
		}
	}
}

// Both a creation acknowledgement and a deterministic-name recovery must
// describe the requested data fork. Names alone do not establish ownership.
func restoredBranchObservation(projectID, parentID, name string, target branch, request managedpostgres.RestoreRequest) (managedpostgres.ObservedDatabase, error) {
	if !validProviderID.MatchString(target.ID) || target.Name == "" {
		return managedpostgres.ObservedDatabase{}, managedpostgres.ErrUnavailable
	}
	if target.ID == parentID || target.Name != name {
		return managedpostgres.ObservedDatabase{}, managedpostgres.ErrConflict
	}
	if target.InitSource != "" && target.InitSource != "parent-data" {
		return managedpostgres.ObservedDatabase{}, managedpostgres.ErrConflict
	}
	lineage, err := observedBranchLineage(projectID, target)
	if err != nil {
		return managedpostgres.ObservedDatabase{}, err
	}
	if lineage == nil {
		return managedpostgres.ObservedDatabase{}, managedpostgres.ErrUnavailable
	}
	if lineage.SourceResourceID != (resourceRef{projectID: projectID, branchID: parentID}).String() || !lineage.PointInTime.Equal(request.PointInTime) {
		return managedpostgres.ObservedDatabase{}, managedpostgres.ErrConflict
	}
	return managedpostgres.ObservedDatabase{
		ProviderResourceID: (resourceRef{projectID: projectID, branchID: target.ID}).String(),
		DataResourceID:     (resourceRef{projectID: projectID, branchID: target.ID}).String(),
		Status:             managedpostgres.ProviderStatusPending, Spec: request.Spec, RestoreLineage: lineage,
	}, nil
}

// Neon can return parent_lsn without parent_timestamp. An LSN cannot be
// compared to a captured timestamp without a separately verified mapping, so
// such responses deliberately carry no timestamp lineage proof.
func observedBranchLineage(projectID string, target branch) (*managedpostgres.RestoreLineage, error) {
	if target.ProjectID != "" && target.ProjectID != projectID {
		return nil, managedpostgres.ErrConflict
	}
	if target.InitSource != "" && target.InitSource != "parent-data" {
		return nil, nil
	}
	if target.ParentID == "" || target.ParentTimestamp == "" || target.ProjectID == "" || target.InitSource == "" {
		return nil, nil
	}
	if !validProviderID.MatchString(target.ID) || !validProviderID.MatchString(target.ParentID) || target.ParentID == target.ID {
		return nil, managedpostgres.ErrConflict
	}
	point, err := time.Parse(time.RFC3339Nano, target.ParentTimestamp)
	if err != nil || point.IsZero() {
		return nil, managedpostgres.ErrUnavailable
	}
	return &managedpostgres.RestoreLineage{SourceResourceID: (resourceRef{projectID: projectID, branchID: target.ParentID}).String(), PointInTime: point.UTC()}, nil
}

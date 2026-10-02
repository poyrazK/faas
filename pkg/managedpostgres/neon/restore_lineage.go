package neon

import (
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

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

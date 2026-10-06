package neon

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

var _ managedpostgres.RestoreSourceObserver = (*Provider)(nil)

// The pointer distinguishes disabled retention from missing/null evidence.
type recoveryProject struct {
	ID               string `json:"id"`
	OrganizationID   string `json:"org_id"`
	RegionID         string `json:"region_id"`
	PostgresMajor    int    `json:"pg_version"`
	CreatedAt        string `json:"created_at"`
	RetentionSeconds *int64 `json:"history_retention_seconds"`
}

func (p *Provider) ObserveRestoreSource(ctx context.Context, definition managedpostgres.RestoreSourceDefinition) (managedpostgres.RestoreSourceObservation, error) {
	resource, err := parseResourceRef(definition.ProviderResourceID)
	if err != nil {
		return managedpostgres.RestoreSourceObservation{}, err
	}
	data, err := parseResourceRef(definition.DataResourceID)
	if err != nil || data.branchID == "" || data.projectID != resource.projectID || resource.branchID != "" && resource.branchID != data.branchID {
		return managedpostgres.RestoreSourceObservation{}, managedpostgres.ErrUnsupported
	}
	var project struct {
		Project recoveryProject `json:"project"`
	}
	var source struct {
		Branch branch `json:"branch"`
	}
	path := "/projects/" + url.PathEscape(data.projectID)
	if err := p.doJSON(ctx, http.MethodGet, path, nil, nil, &project, http.StatusOK); err != nil {
		return managedpostgres.RestoreSourceObservation{}, err
	}
	if err := p.doJSON(ctx, http.MethodGet, path+"/branches/"+url.PathEscape(data.branchID), nil, nil, &source, http.StatusOK); err != nil {
		return managedpostgres.RestoreSourceObservation{}, err
	}
	return p.restoreSourceObservation(definition, data, project.Project, source.Branch)
}

func (p *Provider) restoreSourceObservation(definition managedpostgres.RestoreSourceDefinition, data resourceRef, project recoveryProject, source branch) (managedpostgres.RestoreSourceObservation, error) {
	if project.ID != data.projectID || project.OrganizationID != p.organizationID || project.RegionID != p.regionID || project.PostgresMajor != definition.Spec.PostgresMajor ||
		definition.Spec.Region != p.logicalRegion || project.RetentionSeconds == nil || *project.RetentionSeconds < 0 || source.ID != data.branchID || source.ProjectID != data.projectID {
		return managedpostgres.RestoreSourceObservation{}, managedpostgres.ErrConflict
	}
	created, err := time.Parse(time.RFC3339Nano, project.CreatedAt)
	if err != nil || created.IsZero() {
		return managedpostgres.RestoreSourceObservation{}, managedpostgres.ErrUnavailable
	}
	branchCreated, err := time.Parse(time.RFC3339Nano, source.CreatedAt)
	if err != nil || branchCreated.IsZero() || branchCreated.Before(created) || branchCreated.After(p.now()) {
		return managedpostgres.RestoreSourceObservation{}, managedpostgres.ErrUnavailable
	}
	lineage, err := observedBranchLineage(data.projectID, source)
	if err != nil {
		return managedpostgres.RestoreSourceObservation{}, err
	}
	status := managedpostgres.ProviderStatusPending
	if source.CurrentState == "ready" && source.PendingState == "" {
		status = managedpostgres.ProviderStatusReady
	} else if source.CurrentState != "init" && source.CurrentState != "resetting" && source.CurrentState != "ready" && source.CurrentState != "archived" {
		return managedpostgres.RestoreSourceObservation{}, managedpostgres.ErrUnavailable
	}
	// Project creation and retention are necessary limits. The documented API
	// does not expose the earliest retained WAL timestamp. Branch creation or
	// reset times do not establish inherited history, so no full bounds are claimed.
	return managedpostgres.RestoreSourceObservation{ProviderResourceID: definition.ProviderResourceID, DataResourceID: definition.DataResourceID,
		Status: status, RetentionSeconds: *project.RetentionSeconds, HistoryNotBefore: created.UTC(), Lineage: lineage}, nil
}

package neon

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

var _ managedpostgres.SnapshotRestoreProvider = (*Provider)(nil)

// This private seam consumes an owned native snapshot and target dispatch
// receipt. The coordinator must retain those receipts after unknown outcomes.
// Storage restoration does not publish a stage or enable customer credentials.
func (p *Provider) RestoreSnapshot(ctx context.Context, definition managedpostgres.RestoreSourceDefinition, request managedpostgres.SnapshotRestoreRequest) (managedpostgres.SnapshotRestoreObservation, error) {
	return p.restoreSnapshot(ctx, definition, request, true)
}

func (p *Provider) FindSnapshotRestore(ctx context.Context, definition managedpostgres.RestoreSourceDefinition, request managedpostgres.SnapshotRestoreRequest) (managedpostgres.SnapshotRestoreObservation, error) {
	return p.restoreSnapshot(ctx, definition, request, false)
}

func (p *Provider) restoreSnapshot(ctx context.Context, definition managedpostgres.RestoreSourceDefinition, request managedpostgres.SnapshotRestoreRequest, create bool) (managedpostgres.SnapshotRestoreObservation, error) {
	if p == nil {
		return managedpostgres.SnapshotRestoreObservation{}, managedpostgres.ErrUnavailable
	}
	if err := request.Validate(); err != nil {
		return managedpostgres.SnapshotRestoreObservation{}, err
	}
	source, snapshotID, targetID, err := p.snapshotRestoreSelectors(definition, request)
	if err != nil {
		return managedpostgres.SnapshotRestoreObservation{}, err
	}
	if err := p.snapshotRestorePlacement(ctx, source.projectID, definition.Spec); err != nil {
		return managedpostgres.SnapshotRestoreObservation{}, err
	}
	actualSnapshot, err := p.findSnapshot(ctx, source.projectID, snapshotID, "")
	if err != nil {
		return managedpostgres.SnapshotRestoreObservation{}, err
	}
	retained, err := validateOwnedSnapshot(source, p.snapshotName(request.Snapshot.ResourceID), request.Snapshot.PointInTime, actualSnapshot)
	if err != nil || retained.ExpiresAt != nil {
		if err == nil {
			err = managedpostgres.ErrUnavailable
		}
		return managedpostgres.SnapshotRestoreObservation{}, err
	}
	name := p.restoreBranchName(request.ResourceID)
	actual, err := p.findSnapshotRestoreBranch(ctx, source.projectID, targetID, name)
	if errors.Is(err, managedpostgres.ErrNotFound) && create && targetID == "" {
		// Explicitly preview the new fork. Production computes and identity
		// move only during finalize, which this path never requests.
		payload := struct {
			Name            string `json:"name"`
			TargetBranchID  string `json:"target_branch_id"`
			FinalizeRestore bool   `json:"finalize_restore"`
		}{Name: name, TargetBranchID: source.branchID, FinalizeRestore: false}
		var accepted createdBranchResponse
		path := "/projects/" + url.PathEscape(source.projectID) + "/snapshots/" + url.PathEscape(snapshotID) + "/restore"
		postErr := p.doJSON(ctx, http.MethodPost, path, nil, payload, &accepted, http.StatusOK)
		if postErr == nil {
			if _, err := snapshotRestoreObservation(source, snapshotID, name, retained, accepted.Branch); err != nil {
				return managedpostgres.SnapshotRestoreObservation{}, err
			}
			actual, err = p.findSnapshotRestoreBranch(ctx, source.projectID, accepted.Branch.ID, name)
		} else if errors.Is(postErr, managedpostgres.ErrUnavailable) && ctx.Err() == nil {
			// One discovery recovers a lost response. Never repeat POST here.
			actual, err = p.findSnapshotRestoreBranch(ctx, source.projectID, "", name)
			if errors.Is(err, managedpostgres.ErrNotFound) {
				err = managedpostgres.ErrUnavailable
			}
		} else {
			err = postErr
		}
	}
	if err != nil {
		return managedpostgres.SnapshotRestoreObservation{}, err
	}
	result, err := snapshotRestoreObservation(source, snapshotID, name, retained, actual)
	if err != nil {
		return managedpostgres.SnapshotRestoreObservation{}, err
	}
	if err := p.snapshotRestorePlacement(ctx, source.projectID, definition.Spec); err != nil {
		return managedpostgres.SnapshotRestoreObservation{}, err
	}
	return result, nil
}

func (p *Provider) snapshotRestoreSelectors(definition managedpostgres.RestoreSourceDefinition, r managedpostgres.SnapshotRestoreRequest) (resourceRef, string, string, error) {
	source, err := snapshotCaptureSource(r.Snapshot)
	lifecycle, lifecycleErr := parseResourceRef(definition.ProviderResourceID)
	projectID, snapshotID, snapshotErr := parseSnapshotRef(r.ProviderSnapshotID)
	if err != nil || lifecycleErr != nil || snapshotErr != nil || definition.DataResourceID != r.Snapshot.SourceResourceID ||
		definition.Spec.Region != p.logicalRegion {
		return source, "", "", managedpostgres.ErrInvalid
	}
	if lifecycle.projectID != source.projectID || projectID != source.projectID || lifecycle.branchID != "" && lifecycle.branchID != source.branchID {
		return source, "", "", managedpostgres.ErrConflict
	}
	if err := p.Capabilities().Supports(definition.Spec); err != nil {
		return source, "", "", err
	}
	targetID := ""
	if r.ExpectedTargetResourceID != "" {
		target, err := parseResourceRef(r.ExpectedTargetResourceID)
		if err != nil || target.projectID != source.projectID || target.branchID == "" || target.branchID == source.branchID {
			return source, "", "", managedpostgres.ErrInvalid
		}
		targetID = target.branchID
	}
	return source, snapshotID, targetID, nil
}

func (p *Provider) snapshotRestorePlacement(ctx context.Context, projectID string, spec managedpostgres.Spec) error {
	var response projectResponse
	if err := p.doJSON(ctx, http.MethodGet, "/projects/"+url.PathEscape(projectID), nil, nil, &response, http.StatusOK); err != nil {
		return err
	}
	actual := response.Project
	if actual.ID != projectID || actual.OrganizationID != p.organizationID || actual.RegionID != p.regionID || actual.PostgresMajor != spec.PostgresMajor {
		return managedpostgres.ErrConflict
	}
	return nil
}

func (p *Provider) findSnapshotRestoreBranch(ctx context.Context, projectID, id, name string) (branch, error) {
	path := "/projects/" + url.PathEscape(projectID) + "/branches"
	if id != "" {
		var response struct {
			Branch branch `json:"branch"`
		}
		if err := p.doJSON(ctx, http.MethodGet, path+"/"+url.PathEscape(id), nil, nil, &response, http.StatusOK); err != nil {
			return branch{}, err
		}
		if response.Branch.ID != id || response.Branch.Name != name {
			return branch{}, managedpostgres.ErrConflict
		}
		return response.Branch, nil
	}
	seen := map[string]bool{}
	cursor := ""
	var found branch
	for {
		var response struct {
			Branches   []branch `json:"branches"`
			Pagination struct {
				Next string `json:"next"`
			} `json:"pagination"`
		}
		query := url.Values{"search": {name}, "limit": {"10000"}}
		if cursor != "" {
			query.Set("cursor", cursor)
		}
		if err := p.doJSON(ctx, http.MethodGet, path, query, nil, &response, http.StatusOK); err != nil {
			return branch{}, err
		}
		if response.Branches == nil {
			return branch{}, managedpostgres.ErrUnavailable
		}
		for _, candidate := range response.Branches {
			if candidate.Name != name {
				continue
			}
			if found.ID != "" || !validProviderID.MatchString(candidate.ID) {
				return branch{}, managedpostgres.ErrConflict
			}
			found = candidate
		}
		cursor = response.Pagination.Next
		if cursor == "" {
			break
		}
		if seen[cursor] {
			return branch{}, managedpostgres.ErrUnavailable
		}
		seen[cursor] = true
	}
	if found.ID == "" {
		return branch{}, managedpostgres.ErrNotFound
	}
	return found, nil
}

func snapshotRestoreObservation(source resourceRef, snapshotID, name string, retained managedpostgres.DatabaseSnapshot, actual branch) (managedpostgres.SnapshotRestoreObservation, error) {
	if !validProviderID.MatchString(actual.ID) || actual.ID == source.branchID || actual.ProjectID != source.projectID || actual.Name != name ||
		actual.RestoredFrom != "" && actual.RestoredFrom != snapshotID || actual.RestoredAs != "" && actual.RestoredAs != source.branchID || actual.Default ||
		actual.RestoreStatus == "finalized" {
		return managedpostgres.SnapshotRestoreObservation{}, managedpostgres.ErrConflict
	}
	created, err := time.Parse(time.RFC3339Nano, actual.CreatedAt)
	if err != nil || created.IsZero() || created.Before(retained.CreatedAt) || created.After(time.Now()) {
		return managedpostgres.SnapshotRestoreObservation{}, managedpostgres.ErrUnavailable
	}
	if actual.RestoredFrom == "" || actual.CurrentState == "" || actual.RestoreStatus == "" {
		return managedpostgres.SnapshotRestoreObservation{}, managedpostgres.ErrUnavailable
	}
	return managedpostgres.SnapshotRestoreObservation{ProviderResourceID: (resourceRef{projectID: source.projectID, branchID: actual.ID}).String(),
		ProviderSnapshotID: retained.ProviderSnapshotID, SourceDataResourceID: source.String(), PointInTime: retained.PointInTime,
		SnapshotCreatedAt: retained.CreatedAt, TargetCreatedAt: created.UTC(),
		Restored: actual.CurrentState == "ready" && actual.PendingState == "" && actual.RestoreStatus == "restored"}, nil
}

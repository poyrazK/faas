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
	actual, err := p.findOwnedBranch(ctx, source.projectID, targetID, name)
	readRequired := false
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
			actual, err, readRequired = accepted.Branch, nil, true
		} else if errors.Is(postErr, managedpostgres.ErrUnavailable) && ctx.Err() == nil {
			// One discovery recovers a lost response. Never repeat POST here.
			actual, err = p.findOwnedBranch(ctx, source.projectID, "", name)
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
	result, err := p.awaitSnapshotRestoreObservation(ctx, source, snapshotID, name, retained, actual, readRequired)
	if err != nil {
		return managedpostgres.SnapshotRestoreObservation{}, err
	}
	if err := p.snapshotRestorePlacement(ctx, source.projectID, definition.Spec); err != nil {
		return managedpostgres.SnapshotRestoreObservation{}, err
	}
	return result, nil
}

// Recover incomplete restore metadata on the original identity. Readiness is
// still observed independently: an authenticated pending fork is returned as
// pending, while a name/source/snapshot change stops recovery immediately.
func (p *Provider) awaitSnapshotRestoreObservation(ctx context.Context, source resourceRef, snapshotID, name string, retained managedpostgres.DatabaseSnapshot, actual branch, readRequired bool) (managedpostgres.SnapshotRestoreObservation, error) {
	ctx, cancel := context.WithTimeout(ctx, restoreLineageTimeout)
	defer cancel()
	id := actual.ID
	for {
		observed, err := snapshotRestoreObservation(source, snapshotID, name, retained, actual)
		if err != nil && !errors.Is(err, managedpostgres.ErrUnavailable) || err == nil && !readRequired {
			return observed, err
		}
		if !validProviderID.MatchString(id) {
			return managedpostgres.SnapshotRestoreObservation{}, managedpostgres.ErrUnavailable
		}
		target, err := p.findOwnedBranch(ctx, source.projectID, id, name)
		if err != nil && !errors.Is(err, managedpostgres.ErrNotFound) {
			if ctx.Err() != nil {
				err = managedpostgres.ErrUnavailable
			}
			return managedpostgres.SnapshotRestoreObservation{}, err
		}
		if err == nil {
			actual, readRequired = target, false
			if _, err := snapshotRestoreObservation(source, snapshotID, name, retained, actual); !errors.Is(err, managedpostgres.ErrUnavailable) {
				continue
			}
		}
		timer := time.NewTimer(p.credentialPollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return managedpostgres.SnapshotRestoreObservation{}, managedpostgres.ErrUnavailable
		case <-timer.C:
		}
	}
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

func snapshotRestoreObservation(source resourceRef, snapshotID, name string, retained managedpostgres.DatabaseSnapshot, actual branch) (managedpostgres.SnapshotRestoreObservation, error) {
	if actual.ID != "" && !validProviderID.MatchString(actual.ID) || actual.ID == source.branchID || actual.ProjectID != "" && actual.ProjectID != source.projectID || actual.Name != "" && actual.Name != name ||
		actual.RestoredFrom != "" && actual.RestoredFrom != snapshotID || actual.RestoredAs != "" && actual.RestoredAs != source.branchID || actual.Default ||
		actual.RestoreStatus == "finalized" {
		return managedpostgres.SnapshotRestoreObservation{}, managedpostgres.ErrConflict
	}
	if actual.ID == "" || actual.ProjectID == "" || actual.Name == "" {
		return managedpostgres.SnapshotRestoreObservation{}, managedpostgres.ErrUnavailable
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

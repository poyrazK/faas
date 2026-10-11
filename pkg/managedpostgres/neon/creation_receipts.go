package neon

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

var _ managedpostgres.RestoreCreationProvider = (*Provider)(nil)
var _ managedpostgres.SnapshotCreationProvider = (*Provider)(nil)

func restoreCreationAcknowledgement(projectID, parentID, name string, target branch) (managedpostgres.CreationAcknowledgement, error) {
	if target.ID == parentID || target.ProjectID != "" && target.ProjectID != projectID || target.ParentID != "" && target.ParentID != parentID ||
		target.Name != "" && target.Name != name || target.InitSource != "" && target.InitSource != "parent-data" {
		return managedpostgres.CreationAcknowledgement{}, managedpostgres.ErrConflict
	}
	if !validProviderID.MatchString(target.ID) || target.ProjectID == "" || target.ParentID == "" || target.Name == "" || target.InitSource == "" {
		return managedpostgres.CreationAcknowledgement{}, managedpostgres.ErrUnavailable
	}
	created, err := time.Parse(time.RFC3339Nano, target.CreatedAt)
	if err != nil {
		return managedpostgres.CreationAcknowledgement{}, managedpostgres.ErrUnavailable
	}
	a := managedpostgres.CreationAcknowledgement{ProviderResourceID: (resourceRef{projectID: projectID, branchID: target.ID}).String(),
		SourceResourceID: (resourceRef{projectID: projectID, branchID: parentID}).String(), CreatedAt: created.UTC()}
	if a.Validate() != nil {
		return managedpostgres.CreationAcknowledgement{}, managedpostgres.ErrUnavailable
	}
	return a, nil
}

func (p *Provider) readRestoreCreation(ctx context.Context, request managedpostgres.RestoreRequest, expected managedpostgres.CreationAcknowledgement) (branch, error) {
	source, err := parseResourceRef(request.SourceResourceID)
	target, targetErr := parseResourceRef(expected.ProviderResourceID)
	if p == nil || err != nil || targetErr != nil || source.branchID == "" || target.branchID == "" || target.projectID != source.projectID ||
		request.ResourceID == "" || len(request.ResourceID) > 255 || expected.Validate() != nil || expected.SourceResourceID != source.String() ||
		request.PointInTime.IsZero() || expected.CreatedAt.Before(request.PointInTime) {
		return branch{}, managedpostgres.ErrInvalid
	}
	if target.branchID == source.branchID {
		return branch{}, managedpostgres.ErrConflict
	}
	var response createdBranchResponse
	path := "/projects/" + url.PathEscape(target.projectID) + "/branches/" + url.PathEscape(target.branchID)
	if err := p.doJSON(ctx, http.MethodGet, path, nil, nil, &response, http.StatusOK); err != nil {
		return branch{}, err
	}
	actual, err := restoreCreationAcknowledgement(source.projectID, source.branchID, p.restoreBranchName(request.ResourceID), response.Branch)
	if err != nil {
		return branch{}, err
	}
	if actual.ProviderResourceID != expected.ProviderResourceID || !actual.CreatedAt.Equal(expected.CreatedAt) {
		return branch{}, managedpostgres.ErrConflict
	}
	return response.Branch, nil
}

// Custody permits deleting a failed owned target. It never permits publishing a
// target with wrong or missing parent_timestamp. Re-read the exact ID, source,
// owner, init mode and immutable creation time before issuing any mutation.
func (p *Provider) DeleteRestoreCreation(ctx context.Context, request managedpostgres.RestoreRequest, accepted managedpostgres.CreationAcknowledgement, cleanup managedpostgres.CreationCleanup) (managedpostgres.DeleteResult, error) {
	target, err := p.readRestoreCreation(ctx, request, accepted)
	if errors.Is(err, managedpostgres.ErrNotFound) {
		if !cleanup.Started {
			return managedpostgres.DeleteResult{}, managedpostgres.ErrUnavailable
		}
		return managedpostgres.DeleteResult{Done: true}, nil
	}
	if err != nil {
		return managedpostgres.DeleteResult{}, err
	}
	// A target still initializing can disappear temporarily. Only a settled
	// independently visible branch can authorize absence-based cleanup replay.
	if target.CurrentState != "ready" || target.PendingState != "" {
		return managedpostgres.DeleteResult{}, managedpostgres.ErrUnavailable
	}
	if !cleanup.Started {
		if cleanup.RecordStarted == nil {
			return managedpostgres.DeleteResult{}, managedpostgres.ErrUnsupported
		}
		if err := cleanup.RecordStarted(ctx); err != nil {
			return managedpostgres.DeleteResult{}, err
		}
	}
	result, deleteErr := p.Delete(ctx, managedpostgres.DeleteRequest{ResourceID: request.ResourceID, ProviderResourceID: accepted.ProviderResourceID, IdempotencyKey: request.IdempotencyKey})
	if deleteErr != nil && !errors.Is(deleteErr, managedpostgres.ErrUnavailable) {
		return managedpostgres.DeleteResult{}, deleteErr
	}
	// Returned operation statuses and a successful DELETE alone do not retire
	// custody. Observe physical absence independently, including lost replies.
	_, readErr := p.readRestoreCreation(ctx, request, accepted)
	if errors.Is(readErr, managedpostgres.ErrNotFound) {
		return managedpostgres.DeleteResult{Done: true}, nil
	}
	if readErr != nil {
		return managedpostgres.DeleteResult{}, readErr
	}
	if deleteErr != nil {
		return managedpostgres.DeleteResult{}, deleteErr
	}
	result.Done = false
	return result, nil
}

func snapshotCreationAcknowledgement(source resourceRef, name string, actual snapshot) (managedpostgres.CreationAcknowledgement, error) {
	if actual.Name != "" && actual.Name != name || actual.SourceBranchID != "" && actual.SourceBranchID != source.branchID {
		return managedpostgres.CreationAcknowledgement{}, managedpostgres.ErrConflict
	}
	if !validProviderID.MatchString(actual.ID) || actual.Name == "" || actual.SourceBranchID == "" || !actual.Manual {
		return managedpostgres.CreationAcknowledgement{}, managedpostgres.ErrUnavailable
	}
	created, err := time.Parse(time.RFC3339Nano, actual.CreatedAt)
	if err != nil {
		return managedpostgres.CreationAcknowledgement{}, managedpostgres.ErrUnavailable
	}
	a := managedpostgres.CreationAcknowledgement{ProviderResourceID: source.projectID + "/snapshots/" + actual.ID, SourceResourceID: source.String(), CreatedAt: created.UTC()}
	if a.Validate() != nil {
		return managedpostgres.CreationAcknowledgement{}, managedpostgres.ErrUnavailable
	}
	return a, nil
}

func (p *Provider) readSnapshotCreation(ctx context.Context, request managedpostgres.SnapshotCaptureRequest, expected managedpostgres.CreationAcknowledgement) (snapshot, resourceRef, error) {
	source, err := snapshotCaptureSource(request)
	project, id, idErr := parseSnapshotRef(expected.ProviderResourceID)
	if p == nil || err != nil || idErr != nil || expected.Validate() != nil || project != source.projectID ||
		expected.SourceResourceID != source.String() || expected.CreatedAt.Before(request.PointInTime) {
		return snapshot{}, resourceRef{}, managedpostgres.ErrInvalid
	}
	actual, err := p.findSnapshot(ctx, project, id, "")
	if err != nil {
		return snapshot{}, source, err
	}
	observed, err := snapshotCreationAcknowledgement(source, p.snapshotName(request.ResourceID), actual)
	if err != nil {
		return snapshot{}, source, err
	}
	if observed.ProviderResourceID != expected.ProviderResourceID || !observed.CreatedAt.Equal(expected.CreatedAt) {
		return snapshot{}, source, managedpostgres.ErrConflict
	}
	return actual, source, nil
}

func (p *Provider) ObserveSnapshotCreation(ctx context.Context, request managedpostgres.SnapshotCaptureRequest, accepted managedpostgres.CreationAcknowledgement) (managedpostgres.DatabaseSnapshot, error) {
	actual, source, err := p.readSnapshotCreation(ctx, request, accepted)
	if errors.Is(err, managedpostgres.ErrNotFound) {
		err = managedpostgres.ErrUnavailable
	}
	if err != nil {
		return managedpostgres.DatabaseSnapshot{}, err
	}
	actual, err = p.awaitSnapshotMetadata(ctx, source, p.snapshotName(request.ResourceID), request.PointInTime, actual)
	if err != nil {
		return managedpostgres.DatabaseSnapshot{}, err
	}
	return p.retainOwnedSnapshot(ctx, source, p.snapshotName(request.ResourceID), request.PointInTime, actual)
}

func (p *Provider) DeleteSnapshotCreation(ctx context.Context, request managedpostgres.SnapshotCaptureRequest, accepted managedpostgres.CreationAcknowledgement, cleanup managedpostgres.CreationCleanup) (managedpostgres.DeleteResult, error) {
	_, source, err := p.readSnapshotCreation(ctx, request, accepted)
	if errors.Is(err, managedpostgres.ErrNotFound) {
		if !cleanup.Started {
			return managedpostgres.DeleteResult{}, managedpostgres.ErrUnavailable
		}
		return managedpostgres.DeleteResult{Done: true}, nil
	}
	if err != nil {
		return managedpostgres.DeleteResult{}, err
	}
	if !cleanup.Started {
		if cleanup.RecordStarted == nil {
			return managedpostgres.DeleteResult{}, managedpostgres.ErrUnsupported
		}
		if err := cleanup.RecordStarted(ctx); err != nil {
			return managedpostgres.DeleteResult{}, err
		}
	}
	_, id, _ := parseSnapshotRef(accepted.ProviderResourceID)
	path := "/projects/" + url.PathEscape(source.projectID) + "/snapshots/" + url.PathEscape(id)
	err = p.doJSON(ctx, http.MethodDelete, path, nil, nil, nil, http.StatusAccepted)
	if err != nil && !errors.Is(err, managedpostgres.ErrUnavailable) && !errors.Is(err, managedpostgres.ErrNotFound) {
		return managedpostgres.DeleteResult{}, err
	}
	_, _, readErr := p.readSnapshotCreation(ctx, request, accepted)
	if errors.Is(readErr, managedpostgres.ErrNotFound) {
		return managedpostgres.DeleteResult{Done: true}, nil
	}
	if readErr != nil {
		return managedpostgres.DeleteResult{}, readErr
	}
	if err != nil {
		return managedpostgres.DeleteResult{}, err
	}
	return managedpostgres.DeleteResult{}, nil
}

package neon

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

var _ managedpostgres.SnapshotRestoreDeletionProvider = (*Provider)(nil)

func (p *Provider) DeleteSnapshotRestore(ctx context.Context, d managedpostgres.RestoreSourceDefinition, r managedpostgres.SnapshotRestoreDeletionRequest) (managedpostgres.SnapshotRestoreDeletionObservation, error) {
	return p.snapshotRestoreDeletion(ctx, d, r, true)
}

func (p *Provider) ObserveSnapshotRestoreDeletion(ctx context.Context, d managedpostgres.RestoreSourceDefinition, r managedpostgres.SnapshotRestoreDeletionRequest) (managedpostgres.SnapshotRestoreDeletionObservation, error) {
	return p.snapshotRestoreDeletion(ctx, d, r, false)
}

func (p *Provider) snapshotRestoreDeletion(ctx context.Context, d managedpostgres.RestoreSourceDefinition, r managedpostgres.SnapshotRestoreDeletionRequest, mutate bool) (managedpostgres.SnapshotRestoreDeletionObservation, error) {
	if p == nil {
		return managedpostgres.SnapshotRestoreDeletionObservation{}, managedpostgres.ErrUnavailable
	}
	if err := r.Validate(); err != nil {
		return managedpostgres.SnapshotRestoreDeletionObservation{}, err
	}
	for _, value := range r.OperationIDs {
		if id, err := uuid.Parse(value); err != nil || id == uuid.Nil || id.String() != value {
			return managedpostgres.SnapshotRestoreDeletionObservation{}, managedpostgres.ErrInvalid
		}
	}
	source, snapshotID, targetID, err := p.snapshotRestoreSelectors(d, r.Restore)
	if err != nil {
		return managedpostgres.SnapshotRestoreDeletionObservation{}, err
	}
	if err := p.snapshotRestorePlacement(ctx, source.projectID, d.Spec); err != nil {
		return managedpostgres.SnapshotRestoreDeletionObservation{}, err
	}
	actual, readErr := p.findOwnedBranch(ctx, source.projectID, targetID, p.restoreBranchName(r.Restore.ResourceID))
	absent := errors.Is(readErr, managedpostgres.ErrNotFound)
	if readErr != nil && !absent {
		return managedpostgres.SnapshotRestoreDeletionObservation{}, readErr
	}
	if !absent {
		// Authenticate against previously retained, durable snapshot evidence;
		// cleanup does not require recreating a discarded source snapshot.
		retained := managedpostgres.DatabaseSnapshot{ProviderSnapshotID: r.Restore.ProviderSnapshotID, SourceResourceID: source.String(), PointInTime: r.Restore.Snapshot.PointInTime, CreatedAt: r.SnapshotCreatedAt}
		observed, err := snapshotRestoreObservation(source, snapshotID, p.restoreBranchName(r.Restore.ResourceID), retained, actual)
		if err != nil {
			return managedpostgres.SnapshotRestoreDeletionObservation{}, err
		}
		if !observed.TargetCreatedAt.Equal(r.TargetCreatedAt) {
			return managedpostgres.SnapshotRestoreDeletionObservation{}, managedpostgres.ErrConflict
		}
	}
	ops, err := p.snapshotRestoreDeletionOperations(ctx, source.projectID, targetID, r)
	if err != nil {
		return managedpostgres.SnapshotRestoreDeletionObservation{}, err
	}
	ids, hasDeletion, finished, err := snapshotRestoreDeletionOperationProof(source.projectID, targetID, r.TargetCreatedAt, ops)
	if err != nil {
		return managedpostgres.SnapshotRestoreDeletionObservation{}, err
	}
	if mutate && !absent && !hasDeletion && len(r.OperationIDs) == 0 {
		var accepted createdBranchResponse
		path := "/projects/" + url.PathEscape(source.projectID) + "/branches/" + url.PathEscape(targetID)
		if err := p.doJSON(ctx, http.MethodDelete, path, nil, nil, &accepted, http.StatusOK, http.StatusNoContent); err != nil {
			return managedpostgres.SnapshotRestoreDeletionObservation{}, err
		}
		// Neither a 204 acknowledgement nor returned statuses establish
		// terminal deletion. Recover independently through operation reads.
		if accepted.Branch.ID != "" && (accepted.Branch.ID != targetID || accepted.Branch.ProjectID != source.projectID) {
			return managedpostgres.SnapshotRestoreDeletionObservation{}, managedpostgres.ErrConflict
		}
		if len(accepted.Operations) > 0 {
			ids, hasDeletion, _, err = snapshotRestoreDeletionOperationProof(source.projectID, targetID, r.TargetCreatedAt, accepted.Operations)
			if err != nil {
				return managedpostgres.SnapshotRestoreDeletionObservation{}, err
			}
			if !hasDeletion {
				return managedpostgres.SnapshotRestoreDeletionObservation{}, managedpostgres.ErrUnavailable
			}
		} else {
			ids = nil
		}
		return managedpostgres.SnapshotRestoreDeletionObservation{ProviderResourceID: r.Restore.ExpectedTargetResourceID, OperationIDs: ids}, nil
	}
	if absent && !hasDeletion {
		return managedpostgres.SnapshotRestoreDeletionObservation{}, managedpostgres.ErrUnavailable
	}
	if !hasDeletion {
		ids = nil
	}
	if err := p.snapshotRestorePlacement(ctx, source.projectID, d.Spec); err != nil {
		return managedpostgres.SnapshotRestoreDeletionObservation{}, err
	}
	// Re-read the exact identity after operation observation. This cannot
	// adopt a replacement with the same mutable display name.
	if absent && hasDeletion && finished {
		if _, err := p.findOwnedBranch(ctx, source.projectID, targetID, p.restoreBranchName(r.Restore.ResourceID)); !errors.Is(err, managedpostgres.ErrNotFound) {
			if err == nil {
				err = managedpostgres.ErrUnavailable
			}
			return managedpostgres.SnapshotRestoreDeletionObservation{}, err
		}
	}
	return managedpostgres.SnapshotRestoreDeletionObservation{ProviderResourceID: r.Restore.ExpectedTargetResourceID, OperationIDs: ids, Done: absent && hasDeletion && finished}, nil
}

func (p *Provider) snapshotRestoreDeletionOperations(ctx context.Context, projectID, targetID string, r managedpostgres.SnapshotRestoreDeletionRequest) ([]operation, error) {
	path := "/projects/" + url.PathEscape(projectID) + "/operations"
	if len(r.OperationIDs) > 0 {
		ops := make([]operation, 0, len(r.OperationIDs))
		for _, id := range r.OperationIDs {
			if parsed, err := uuid.Parse(id); err != nil || parsed.String() != id {
				return nil, managedpostgres.ErrInvalid
			}
			var response struct {
				Operation operation `json:"operation"`
			}
			if err := p.doJSON(ctx, http.MethodGet, path+"/"+url.PathEscape(id), nil, nil, &response, http.StatusOK); err != nil {
				return nil, err
			}
			if response.Operation.ID != id {
				return nil, managedpostgres.ErrConflict
			}
			ops = append(ops, response.Operation)
		}
		return ops, nil
	}
	all, err := p.listProjectOperations(ctx, projectID)
	if err != nil {
		return nil, err
	}
	var ops []operation
	for _, op := range all {
		if op.ProjectID != projectID {
			return nil, managedpostgres.ErrConflict
		}
		if op.BranchID == targetID {
			ops = append(ops, op)
		}
	}
	return ops, nil
}

func snapshotRestoreDeletionOperationProof(projectID, targetID string, createdAt time.Time, ops []operation) ([]string, bool, bool, error) {
	ids := make([]string, 0, len(ops))
	hasDeletion, finished := false, true
	for _, op := range ops {
		id, err := uuid.Parse(op.ID)
		at, timeErr := time.Parse(time.RFC3339Nano, op.CreatedAt)
		if err != nil || id == uuid.Nil || id.String() != op.ID || op.ProjectID != projectID || op.BranchID != targetID || op.Action == "" || op.Status == "" || timeErr != nil || at.IsZero() || at.After(time.Now()) ||
			op.Action == "delete_timeline" && at.Before(createdAt) {
			return nil, false, false, managedpostgres.ErrConflict
		}
		ids = append(ids, op.ID)
		hasDeletion = hasDeletion || op.Action == "delete_timeline"
		finished = finished && op.Status == "finished"
	}
	slices.Sort(ids)
	for i := 1; i < len(ids); i++ {
		if ids[i] == ids[i-1] {
			return nil, false, false, managedpostgres.ErrConflict
		}
	}
	return ids, hasDeletion, finished, nil
}

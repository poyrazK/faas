package neon

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

var _ managedpostgres.SnapshotCopyReaderDeletionProvider = (*Provider)(nil)

func (p *Provider) DeleteSnapshotCopyReader(ctx context.Context, d managedpostgres.RestoreSourceDefinition, r managedpostgres.SnapshotCopyReaderDeletionRequest) (managedpostgres.SnapshotCopyReaderDeletionObservation, error) {
	return p.snapshotCopyReaderDeletion(ctx, d, r, true)
}

func (p *Provider) ObserveSnapshotCopyReaderDeletion(ctx context.Context, d managedpostgres.RestoreSourceDefinition, r managedpostgres.SnapshotCopyReaderDeletionRequest) (managedpostgres.SnapshotCopyReaderDeletionObservation, error) {
	return p.snapshotCopyReaderDeletion(ctx, d, r, false)
}

func (p *Provider) snapshotCopyReaderDeletion(ctx context.Context, d managedpostgres.RestoreSourceDefinition, r managedpostgres.SnapshotCopyReaderDeletionRequest, mutate bool) (managedpostgres.SnapshotCopyReaderDeletionObservation, error) {
	if p == nil {
		return managedpostgres.SnapshotCopyReaderDeletionObservation{}, managedpostgres.ErrUnavailable
	}
	if err := r.Validate(); err != nil {
		return managedpostgres.SnapshotCopyReaderDeletionObservation{}, err
	}
	if !validCopyReaderEndpointID(r.Reader.ExpectedEndpointID) || !validCopyReaderOperationIDs(r.OperationIDs) || !validCopyReaderOperationIDs(r.CaptureOperationIDs) {
		return managedpostgres.SnapshotCopyReaderDeletionObservation{}, managedpostgres.ErrInvalid
	}
	source, snapshotID, branchID, err := p.snapshotRestoreSelectors(d, r.Reader.Capture)
	if err != nil {
		return managedpostgres.SnapshotCopyReaderDeletionObservation{}, err
	}
	if err := p.snapshotRestorePlacement(ctx, source.projectID, d.Spec); err != nil {
		return managedpostgres.SnapshotCopyReaderDeletionObservation{}, err
	}
	capture := resourceRef{projectID: source.projectID, branchID: branchID}
	result := managedpostgres.SnapshotCopyReaderDeletionObservation{EndpointID: r.Reader.ExpectedEndpointID, CreatedAt: r.Reader.ExpectedCreatedAt}
	actualBranch, branchErr := p.findSnapshotRestoreBranch(ctx, source.projectID, branchID, p.restoreBranchName(r.Reader.Capture.ResourceID))
	if errors.Is(branchErr, managedpostgres.ErrNotFound) || len(r.CaptureOperationIDs) > 0 {
		proof, err := p.ObserveSnapshotRestoreDeletion(ctx, d, copyReaderCaptureDeletionRequest(r))
		if err != nil {
			return result, err
		}
		if !proof.Done {
			return result, managedpostgres.ErrUnavailable
		}
		if len(proof.OperationIDs) > api.PostgresCopyReaderMaxOperations {
			return result, managedpostgres.ErrQuotaExceeded
		}
		result.CaptureOperationIDs = proof.OperationIDs
	} else if branchErr != nil {
		return result, branchErr
	} else {
		// Cleanup can authenticate a still-present capture from retained
		// receipt pins even after the source snapshot has been disposed.
		retained := managedpostgres.DatabaseSnapshot{ProviderSnapshotID: r.Reader.Capture.ProviderSnapshotID, SourceResourceID: source.String(),
			PointInTime: r.Reader.Capture.Snapshot.PointInTime, CreatedAt: r.Reader.SnapshotCreatedAt}
		observed, err := snapshotRestoreObservation(source, snapshotID, p.restoreBranchName(r.Reader.Capture.ResourceID), retained, actualBranch)
		if err != nil || !observed.TargetCreatedAt.Equal(r.Reader.CaptureCreatedAt) {
			if err == nil {
				err = managedpostgres.ErrConflict
			}
			return result, err
		}
	}
	actual, readErr := p.findSnapshotCopyReaderEndpoint(ctx, source.projectID, result.EndpointID, p.snapshotCopyReaderName(r.Reader.ResourceID))
	absent := errors.Is(readErr, managedpostgres.ErrNotFound)
	if readErr != nil && !absent {
		return result, readErr
	}
	if !absent {
		if _, err := p.snapshotCopyReaderObservation(d.Spec, r.Reader, capture, actual); err != nil {
			return result, err
		}
	}
	if len(r.OperationIDs) == 0 && mutate && !absent {
		var accepted struct {
			Endpoint   endpoint    `json:"endpoint"`
			Operations []operation `json:"operations"`
		}
		path := "/projects/" + url.PathEscape(source.projectID) + "/endpoints/" + url.PathEscape(result.EndpointID)
		if err := p.doJSON(ctx, http.MethodDelete, path, nil, nil, &accepted, http.StatusOK, http.StatusNoContent); err != nil {
			return result, err
		}
		if accepted.Endpoint.ID != "" {
			if _, err := p.snapshotCopyReaderObservation(d.Spec, r.Reader, capture, accepted.Endpoint); err != nil {
				return result, err
			}
		}
		if len(accepted.Operations) == 0 {
			return result, managedpostgres.ErrUnavailable
		}
		ids, _, err := copyReaderDeletionOperationProof(source.projectID, branchID, r, accepted.Operations)
		result.OperationIDs = ids
		// Returned statuses and a DELETE acknowledgement never retire a
		// reader. Persist IDs and independently read every exact operation.
		return result, err
	}
	finished := false
	if len(r.OperationIDs) > 0 {
		ops, err := p.copyReaderDeletionOperations(ctx, source.projectID, r.OperationIDs)
		if err != nil {
			return result, err
		}
		result.OperationIDs, finished, err = copyReaderDeletionOperationProof(source.projectID, branchID, r, ops)
		if err != nil {
			return result, err
		}
	} else if len(result.CaptureOperationIDs) == 0 {
		// Neon reports suspend_compute for endpoint DELETE too. An
		// unrelated autosuspend cannot recover a lost DELETE chain.
		return result, managedpostgres.ErrUnavailable
	}
	if err := p.snapshotRestorePlacement(ctx, source.projectID, d.Spec); err != nil {
		return result, err
	}
	if absent && (finished || len(result.CaptureOperationIDs) > 0) {
		if _, err := p.findSnapshotCopyReaderEndpoint(ctx, source.projectID, result.EndpointID, p.snapshotCopyReaderName(r.Reader.ResourceID)); !errors.Is(err, managedpostgres.ErrNotFound) {
			if err == nil {
				err = managedpostgres.ErrUnavailable
			}
			return result, err
		}
		result.Done = true
	}
	return result, nil
}

func copyReaderCaptureDeletionRequest(r managedpostgres.SnapshotCopyReaderDeletionRequest) managedpostgres.SnapshotRestoreDeletionRequest {
	return managedpostgres.SnapshotRestoreDeletionRequest{Restore: r.Reader.Capture, SnapshotCreatedAt: r.Reader.SnapshotCreatedAt,
		TargetCreatedAt: r.Reader.CaptureCreatedAt, OperationIDs: r.CaptureOperationIDs}
}

func validCopyReaderOperationIDs(ids []string) bool {
	for _, value := range ids {
		id, err := uuid.Parse(value)
		if err != nil || id == uuid.Nil || id.String() != value {
			return false
		}
	}
	return true
}

func (p *Provider) copyReaderDeletionOperations(ctx context.Context, projectID string, ids []string) ([]operation, error) {
	ops := make([]operation, 0, len(ids))
	for _, id := range ids {
		var response struct {
			Operation operation `json:"operation"`
		}
		path := "/projects/" + url.PathEscape(projectID) + "/operations/" + url.PathEscape(id)
		if err := p.doJSON(ctx, http.MethodGet, path, nil, nil, &response, http.StatusOK); err != nil {
			return nil, err
		}
		if response.Operation.ID != id {
			return nil, managedpostgres.ErrConflict
		}
		ops = append(ops, response.Operation)
	}
	return ops, nil
}

func copyReaderDeletionOperationProof(projectID, branchID string, r managedpostgres.SnapshotCopyReaderDeletionRequest, ops []operation) ([]string, bool, error) {
	if len(ops) == 0 {
		return nil, false, managedpostgres.ErrUnavailable
	}
	if len(ops) > api.PostgresCopyReaderMaxOperations {
		return nil, false, managedpostgres.ErrQuotaExceeded
	}
	ids, finished := make([]string, 0, len(ops)), true
	for _, op := range ops {
		at, err := time.Parse(time.RFC3339Nano, op.CreatedAt)
		if !validCopyReaderOperationIDs([]string{op.ID}) || op.ProjectID != projectID || op.BranchID != branchID ||
			op.EndpointID != r.Reader.ExpectedEndpointID || op.Action != "suspend_compute" || op.Status == "" ||
			err != nil || at.IsZero() || at.Before(r.RequestedAt) || at.After(time.Now()) || at.Nanosecond()%1000 != 0 {
			return nil, false, managedpostgres.ErrConflict
		}
		ids = append(ids, op.ID)
		finished = finished && op.Status == "finished"
	}
	slices.Sort(ids)
	for i := 1; i < len(ids); i++ {
		if ids[i] == ids[i-1] {
			return nil, false, managedpostgres.ErrConflict
		}
	}
	return ids, finished, nil
}

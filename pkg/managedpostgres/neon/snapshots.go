package neon

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

type snapshot struct {
	ID             string          `json:"id"`
	Name           string          `json:"name"`
	SourceBranchID string          `json:"source_branch_id"`
	Timestamp      string          `json:"timestamp"`
	CreatedAt      string          `json:"created_at"`
	ExpiresAt      json.RawMessage `json:"expires_at"`
	Manual         bool            `json:"manual"`
}

type snapshotResponse struct {
	Snapshot snapshot `json:"snapshot"`
}

func (p *Provider) CaptureSnapshot(ctx context.Context, request managedpostgres.SnapshotCaptureRequest) (managedpostgres.DatabaseSnapshot, error) {
	if p == nil {
		return managedpostgres.DatabaseSnapshot{}, managedpostgres.ErrUnavailable
	}
	source, err := snapshotCaptureSource(request)
	if err != nil {
		return managedpostgres.DatabaseSnapshot{}, err
	}
	name := p.snapshotName(request.ResourceID)
	actual, err := p.findSnapshot(ctx, source.projectID, "", name)
	if errors.Is(err, managedpostgres.ErrNotFound) {
		query := url.Values{"name": {name}, "timestamp": {request.PointInTime.UTC().Format(time.RFC3339Nano)}}
		path := "/projects/" + url.PathEscape(source.projectID) + "/branches/" + url.PathEscape(source.branchID) + "/snapshot"
		var accepted snapshotResponse
		err = p.doJSON(ctx, http.MethodPost, path, query, nil, &accepted, http.StatusOK)
		if err == nil {
			actual = accepted.Snapshot
		} else if errors.Is(err, managedpostgres.ErrUnavailable) && ctx.Err() == nil {
			// One discovery recovers a lost POST acknowledgement. Never repeat
			// the creation request in this call or adopt a different point.
			creationErr := err
			actual, err = p.findSnapshot(ctx, source.projectID, "", name)
			if errors.Is(err, managedpostgres.ErrNotFound) {
				return managedpostgres.DatabaseSnapshot{}, creationErr
			}
		}
	}
	if err != nil {
		return managedpostgres.DatabaseSnapshot{}, err
	}
	// Creation, replay and lost-response discovery share the same bounded
	// observation path. A discovered identity can still be initializing.
	actual, err = p.awaitSnapshotMetadata(ctx, source, name, request.PointInTime, actual)
	if err != nil {
		return managedpostgres.DatabaseSnapshot{}, err
	}
	return p.retainOwnedSnapshot(ctx, source, name, request.PointInTime, actual)
}

// Snapshot creation is asynchronous too: its acknowledgement can omit the
// capture timestamp and expiry. Hydrate only that accepted identity, retaining
// exact ownership and time checks before any retention mutation.
func (p *Provider) awaitSnapshotMetadata(ctx context.Context, source resourceRef, name string, point time.Time, accepted snapshot) (snapshot, error) {
	if !validProviderID.MatchString(accepted.ID) {
		return snapshot{}, managedpostgres.ErrUnavailable
	}
	if (accepted.Name != "" && accepted.Name != name) || (accepted.SourceBranchID != "" && accepted.SourceBranchID != source.branchID) {
		return snapshot{}, managedpostgres.ErrConflict
	}
	ctx, cancel := context.WithTimeout(ctx, restoreLineageTimeout)
	defer cancel()
	actual := accepted
	for {
		_, proofErr := validateOwnedSnapshot(source, name, point, actual)
		if proofErr != nil && !errors.Is(proofErr, managedpostgres.ErrUnavailable) {
			return snapshot{}, proofErr
		}
		// Independently observe even an apparently complete acknowledgement.
		observed, err := p.findSnapshot(ctx, source.projectID, accepted.ID, "")
		if err != nil && !errors.Is(err, managedpostgres.ErrNotFound) {
			if ctx.Err() != nil {
				err = managedpostgres.ErrUnavailable
			}
			return snapshot{}, err
		}
		if err == nil {
			if _, err := validateOwnedSnapshot(source, name, point, observed); err == nil {
				return observed, nil
			} else if !errors.Is(err, managedpostgres.ErrUnavailable) {
				return snapshot{}, err
			}
			actual = observed
		}
		timer := time.NewTimer(p.credentialPollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return snapshot{}, managedpostgres.ErrUnavailable
		case <-timer.C:
		}
	}
}

func (p *Provider) RetainSnapshot(ctx context.Context, request managedpostgres.SnapshotCaptureRequest, id string) (managedpostgres.DatabaseSnapshot, error) {
	if p == nil {
		return managedpostgres.DatabaseSnapshot{}, managedpostgres.ErrUnavailable
	}
	source, err := snapshotCaptureSource(request)
	if err != nil {
		return managedpostgres.DatabaseSnapshot{}, err
	}
	projectID, snapshotID, err := parseSnapshotRef(id)
	if err != nil || projectID != source.projectID {
		return managedpostgres.DatabaseSnapshot{}, managedpostgres.ErrInvalid
	}
	actual, err := p.findSnapshot(ctx, projectID, snapshotID, "")
	if err != nil {
		return managedpostgres.DatabaseSnapshot{}, err
	}
	return p.retainOwnedSnapshot(ctx, source, p.snapshotName(request.ResourceID), request.PointInTime, actual)
}

func (p *Provider) retainOwnedSnapshot(ctx context.Context, source resourceRef, name string, point time.Time, actual snapshot) (managedpostgres.DatabaseSnapshot, error) {
	observed, err := validateOwnedSnapshot(source, name, point, actual)
	if err != nil || observed.ExpiresAt == nil {
		return observed, err
	}
	// Clear expiry on this operation-owned copy. The source branch and its
	// compute endpoints are untouched. Explicit null differs from omission.
	payload := map[string]any{"snapshot": map[string]any{"expires_at": nil}}
	path := "/projects/" + url.PathEscape(source.projectID) + "/snapshots/" + url.PathEscape(actual.ID)
	err = p.doJSON(ctx, http.MethodPatch, path, nil, payload, nil, http.StatusOK)
	if err != nil && (!errors.Is(err, managedpostgres.ErrUnavailable) || ctx.Err() != nil) {
		return managedpostgres.DatabaseSnapshot{}, err
	}
	// Even a successful PATCH is only an acknowledgement. Require the same
	// observed snapshot to report no expiry; this also recovers a lost reply.
	actual, readErr := p.findSnapshot(ctx, source.projectID, actual.ID, "")
	if readErr != nil {
		return managedpostgres.DatabaseSnapshot{}, readErr
	}
	observed, readErr = validateOwnedSnapshot(source, name, point, actual)
	if readErr != nil {
		return managedpostgres.DatabaseSnapshot{}, readErr
	}
	if observed.ExpiresAt != nil {
		return managedpostgres.DatabaseSnapshot{}, managedpostgres.ErrUnavailable
	}
	return observed, nil
}

func (p *Provider) FindSnapshot(ctx context.Context, request managedpostgres.SnapshotCaptureRequest) (managedpostgres.DatabaseSnapshot, error) {
	if p == nil {
		return managedpostgres.DatabaseSnapshot{}, managedpostgres.ErrUnavailable
	}
	source, err := snapshotCaptureSource(request)
	if err != nil {
		return managedpostgres.DatabaseSnapshot{}, err
	}
	actual, err := p.findSnapshot(ctx, source.projectID, "", p.snapshotName(request.ResourceID))
	if err != nil {
		return managedpostgres.DatabaseSnapshot{}, err
	}
	return validateOwnedSnapshot(source, p.snapshotName(request.ResourceID), request.PointInTime, actual)
}

func (p *Provider) InspectSnapshot(ctx context.Context, id string) (managedpostgres.DatabaseSnapshot, error) {
	if p == nil {
		return managedpostgres.DatabaseSnapshot{}, managedpostgres.ErrUnavailable
	}
	projectID, snapshotID, err := parseSnapshotRef(id)
	if err != nil {
		return managedpostgres.DatabaseSnapshot{}, err
	}
	actual, err := p.findSnapshot(ctx, projectID, snapshotID, "")
	if err != nil {
		return managedpostgres.DatabaseSnapshot{}, err
	}
	return observedSnapshot(projectID, actual)
}

func (p *Provider) DeleteSnapshot(ctx context.Context, request managedpostgres.SnapshotDeleteRequest) (managedpostgres.DeleteResult, error) {
	if p == nil {
		return managedpostgres.DeleteResult{}, managedpostgres.ErrUnavailable
	}
	source, err := snapshotCaptureSource(managedpostgres.SnapshotCaptureRequest{ResourceID: request.ResourceID, SourceResourceID: request.SourceResourceID,
		PointInTime: request.PointInTime, IdempotencyKey: "cleanup"})
	if err != nil {
		return managedpostgres.DeleteResult{}, err
	}
	projectID, snapshotID, err := parseSnapshotRef(request.ProviderSnapshotID)
	if err != nil || projectID != source.projectID {
		return managedpostgres.DeleteResult{}, managedpostgres.ErrInvalid
	}
	actual, err := p.findSnapshot(ctx, projectID, snapshotID, "")
	if errors.Is(err, managedpostgres.ErrNotFound) {
		return managedpostgres.DeleteResult{Done: true}, nil
	}
	if err != nil {
		return managedpostgres.DeleteResult{}, err
	}
	if _, err := validateOwnedSnapshot(source, p.snapshotName(request.ResourceID), request.PointInTime, actual); err != nil {
		return managedpostgres.DeleteResult{}, err
	}
	path := "/projects/" + url.PathEscape(projectID) + "/snapshots/" + url.PathEscape(snapshotID)
	err = p.doJSON(ctx, http.MethodDelete, path, nil, nil, nil, http.StatusAccepted)
	if err != nil && !errors.Is(err, managedpostgres.ErrUnavailable) && !errors.Is(err, managedpostgres.ErrNotFound) {
		return managedpostgres.DeleteResult{}, err
	}
	_, readErr := p.findSnapshot(ctx, projectID, snapshotID, "")
	if errors.Is(readErr, managedpostgres.ErrNotFound) {
		return managedpostgres.DeleteResult{Done: true}, nil
	}
	return managedpostgres.DeleteResult{}, readErr
}

func snapshotCaptureSource(request managedpostgres.SnapshotCaptureRequest) (resourceRef, error) {
	if request.ResourceID == "" || len(request.ResourceID) > 255 || request.IdempotencyKey == "" || len(request.IdempotencyKey) > 255 ||
		request.PointInTime.IsZero() || request.PointInTime.Nanosecond()%1000 != 0 || !request.PointInTime.Before(time.Now()) {
		return resourceRef{}, managedpostgres.ErrInvalid
	}
	source, err := parseResourceRef(request.SourceResourceID)
	if err != nil || source.branchID == "" {
		return resourceRef{}, managedpostgres.ErrInvalid
	}
	return source, nil
}

func (p *Provider) snapshotName(resourceID string) string {
	sum := sha256.Sum256([]byte(p.organizationID + "\x00snapshot\x00" + resourceID))
	return "gregale-snapshot-" + hex.EncodeToString(sum[:16])
}

func parseSnapshotRef(value string) (string, string, error) {
	parts := strings.Split(value, "/")
	if len(parts) != 3 || parts[1] != "snapshots" || !validProviderID.MatchString(parts[0]) || !validProviderID.MatchString(parts[2]) {
		return "", "", managedpostgres.ErrInvalid
	}
	return parts[0], parts[2], nil
}

func (p *Provider) findSnapshot(ctx context.Context, projectID, id, name string) (snapshot, error) {
	var response struct {
		Snapshots []snapshot `json:"snapshots"`
	}
	path := "/projects/" + url.PathEscape(projectID) + "/snapshots"
	if err := p.doJSON(ctx, http.MethodGet, path, nil, nil, &response, http.StatusOK); err != nil {
		return snapshot{}, err
	}
	if response.Snapshots == nil {
		// An absent/null list cannot establish absence and authorize creation.
		return snapshot{}, managedpostgres.ErrUnavailable
	}
	var found snapshot
	for _, candidate := range response.Snapshots {
		if (id != "" && candidate.ID != id) || (name != "" && candidate.Name != name) {
			continue
		}
		if found.ID != "" || !validProviderID.MatchString(candidate.ID) {
			return snapshot{}, managedpostgres.ErrConflict
		}
		found = candidate
	}
	if found.ID == "" {
		return snapshot{}, managedpostgres.ErrNotFound
	}
	return found, nil
}

func observedSnapshot(projectID string, actual snapshot) (managedpostgres.DatabaseSnapshot, error) {
	if !validProviderID.MatchString(actual.ID) || !validProviderID.MatchString(actual.SourceBranchID) || actual.Name == "" || !actual.Manual || len(actual.ExpiresAt) == 0 {
		return managedpostgres.DatabaseSnapshot{}, managedpostgres.ErrUnavailable
	}
	point, err := time.Parse(time.RFC3339Nano, actual.Timestamp)
	if err != nil || point.IsZero() || point.Nanosecond()%1000 != 0 {
		return managedpostgres.DatabaseSnapshot{}, managedpostgres.ErrUnavailable
	}
	created, err := time.Parse(time.RFC3339Nano, actual.CreatedAt)
	if err != nil || created.IsZero() || created.Before(point) {
		return managedpostgres.DatabaseSnapshot{}, managedpostgres.ErrUnavailable
	}
	observed := managedpostgres.DatabaseSnapshot{ProviderSnapshotID: projectID + "/snapshots/" + actual.ID,
		SourceResourceID: (resourceRef{projectID: projectID, branchID: actual.SourceBranchID}).String(), PointInTime: point.UTC(), CreatedAt: created.UTC()}
	if !bytes.Equal(bytes.TrimSpace(actual.ExpiresAt), []byte("null")) {
		var value string
		if json.Unmarshal(actual.ExpiresAt, &value) != nil {
			return managedpostgres.DatabaseSnapshot{}, managedpostgres.ErrUnavailable
		}
		expiry, err := time.Parse(time.RFC3339Nano, value)
		if err != nil || expiry.IsZero() {
			return managedpostgres.DatabaseSnapshot{}, managedpostgres.ErrUnavailable
		}
		observed.ExpiresAt = &expiry
	}
	return observed, nil
}

func validateOwnedSnapshot(source resourceRef, name string, point time.Time, actual snapshot) (managedpostgres.DatabaseSnapshot, error) {
	// Do not let an omitted expiry or timestamp hide contradictory ownership
	// already reported by the provider and then adopt a later substitution.
	if actual.Name != "" && actual.Name != name || actual.SourceBranchID != "" && actual.SourceBranchID != source.branchID {
		return managedpostgres.DatabaseSnapshot{}, managedpostgres.ErrConflict
	}
	if actual.Timestamp != "" {
		at, err := time.Parse(time.RFC3339Nano, actual.Timestamp)
		if err == nil && !at.Equal(point) {
			return managedpostgres.DatabaseSnapshot{}, managedpostgres.ErrConflict
		}
	}
	observed, err := observedSnapshot(source.projectID, actual)
	if err != nil {
		return observed, err
	}
	if actual.Name != name || observed.SourceResourceID != source.String() || !observed.PointInTime.Equal(point) {
		return managedpostgres.DatabaseSnapshot{}, managedpostgres.ErrConflict
	}
	return observed, nil
}

var _ managedpostgres.SnapshotProvider = (*Provider)(nil)

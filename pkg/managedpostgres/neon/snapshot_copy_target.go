package neon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

var _ managedpostgres.SnapshotCopyTargetProvider = (*Provider)(nil)

func (p *Provider) PrepareSnapshotCopyTarget(ctx context.Context, d managedpostgres.RestoreSourceDefinition, r managedpostgres.SnapshotCopyTargetRequest) (managedpostgres.SnapshotCopyTargetObservation, error) {
	return p.snapshotCopyTarget(ctx, d, r, true)
}

func (p *Provider) FindSnapshotCopyTarget(ctx context.Context, d managedpostgres.RestoreSourceDefinition, r managedpostgres.SnapshotCopyTargetRequest) (managedpostgres.SnapshotCopyTargetObservation, error) {
	return p.snapshotCopyTarget(ctx, d, r, false)
}

func (p *Provider) snapshotCopyTarget(ctx context.Context, d managedpostgres.RestoreSourceDefinition, r managedpostgres.SnapshotCopyTargetRequest, create bool) (managedpostgres.SnapshotCopyTargetObservation, error) {
	if p == nil {
		return managedpostgres.SnapshotCopyTargetObservation{}, managedpostgres.ErrUnavailable
	}
	if r.Validate() != nil {
		return managedpostgres.SnapshotCopyTargetObservation{}, managedpostgres.ErrInvalid
	}
	source, _, _, err := p.snapshotRestoreSelectors(d, r.Capture)
	if err != nil {
		return managedpostgres.SnapshotCopyTargetObservation{}, err
	}
	id := r.ExpectedProviderResourceID
	if id != "" && (!validProviderID.MatchString(id) || id == source.projectID) {
		return managedpostgres.SnapshotCopyTargetObservation{}, managedpostgres.ErrInvalid
	}
	// Authenticate the exact immutable input, without source retention/spec
	// reselection. This is a GET-only operation; finalize is never used.
	capture, err := p.FindSnapshotRestore(ctx, d, r.Capture)
	if err != nil {
		return managedpostgres.SnapshotCopyTargetObservation{}, err
	}
	if !capture.Restored || !capture.SnapshotCreatedAt.Equal(r.SnapshotCreatedAt) || !capture.TargetCreatedAt.Equal(r.CaptureCreatedAt) {
		return managedpostgres.SnapshotCopyTargetObservation{}, managedpostgres.ErrConflict
	}
	name := p.snapshotCopyProjectName(r.ResourceID)
	if id == "" {
		id, err = p.findSnapshotCopyProject(ctx, name)
		if errors.Is(err, managedpostgres.ErrNotFound) && create {
			var accepted createdProjectResponse
			err = p.doJSON(ctx, http.MethodPost, "/projects", nil, p.projectPayload(name, d.Spec), &accepted, http.StatusCreated)
			if err == nil {
				if !validProviderID.MatchString(accepted.Project.ID) || accepted.Project.ID == source.projectID || accepted.Project.Name != name {
					return managedpostgres.SnapshotCopyTargetObservation{}, managedpostgres.ErrConflict
				}
				id = accepted.Project.ID
			} else if errors.Is(err, managedpostgres.ErrUnavailable) && ctx.Err() == nil {
				// The persisted first-dispatch marker prevents another POST,
				// even when this discovery sees an eventually invisible project.
				id, err = p.findSnapshotCopyProject(ctx, name)
				if errors.Is(err, managedpostgres.ErrNotFound) {
					err = managedpostgres.ErrUnavailable
				}
			}
		}
		if err != nil {
			return managedpostgres.SnapshotCopyTargetObservation{}, err
		}
	}
	if id == source.projectID {
		return managedpostgres.SnapshotCopyTargetObservation{}, managedpostgres.ErrConflict
	}
	return p.observeSnapshotCopyProject(ctx, d.Spec, r, id, name)
}

func (p *Provider) snapshotCopyProjectName(owner string) string {
	sum := sha256.Sum256([]byte(p.organizationID + "\x00snapshot-copy\x00" + owner))
	return "gregale-copy-" + hex.EncodeToString(sum[:20])
}

func (p *Provider) findSnapshotCopyProject(ctx context.Context, name string) (string, error) {
	seen := map[string]bool{}
	cursor, found := "", ""
	for page := 0; page < maximumProjectSearchPages; page++ {
		q := url.Values{"limit": {"400"}, "search": {name}, "org_id": {p.organizationID}}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		var response struct {
			projectsResponse
			Unavailable json.RawMessage `json:"unavailable"`
		}
		if err := p.doJSON(ctx, http.MethodGet, "/projects", q, nil, &response, http.StatusOK); err != nil {
			return "", err
		}
		if response.Projects == nil || len(response.Unavailable) > 0 && string(response.Unavailable) != "null" && string(response.Unavailable) != "[]" && string(response.Unavailable) != "{}" {
			return "", managedpostgres.ErrUnavailable
		}
		for _, candidate := range response.Projects {
			if candidate.Name != name {
				continue
			}
			if found != "" || !validProviderID.MatchString(candidate.ID) || candidate.OrganizationID != p.organizationID {
				return "", managedpostgres.ErrConflict
			}
			found = candidate.ID
		}
		cursor = response.Pagination.Cursor
		if cursor == "" {
			if found == "" {
				return "", managedpostgres.ErrNotFound
			}
			return found, nil
		}
		if seen[cursor] {
			return "", managedpostgres.ErrUnavailable
		}
		seen[cursor] = true
	}
	return "", managedpostgres.ErrUnavailable
}

func (p *Provider) observeSnapshotCopyProject(ctx context.Context, spec managedpostgres.Spec, r managedpostgres.SnapshotCopyTargetRequest, id, name string) (managedpostgres.SnapshotCopyTargetObservation, error) {
	var result projectResponse
	path := "/projects/" + url.PathEscape(id)
	if err := p.doJSON(ctx, http.MethodGet, path, nil, nil, &result, http.StatusOK); err != nil {
		return managedpostgres.SnapshotCopyTargetObservation{}, err
	}
	actual := result.Project
	created, err := time.Parse(time.RFC3339Nano, actual.CreatedAt)
	if actual.ID != id || actual.Name != name || actual.OrganizationID != p.organizationID || actual.RegionID != p.regionID ||
		actual.PostgresMajor != spec.PostgresMajor || err != nil || created.Before(r.CaptureCreatedAt) || created.After(time.Now()) ||
		created.Nanosecond()%1000 != 0 || !r.ExpectedCreatedAt.IsZero() && !created.Equal(r.ExpectedCreatedAt) {
		return managedpostgres.SnapshotCopyTargetObservation{}, managedpostgres.ErrConflict
	}
	var branches branchesResponse
	var endpoints endpointsResponse
	var ops operationsResponse
	if err := p.doJSON(ctx, http.MethodGet, path+"/branches", nil, nil, &branches, http.StatusOK); err != nil {
		return managedpostgres.SnapshotCopyTargetObservation{}, err
	}
	if err := p.doJSON(ctx, http.MethodGet, path+"/endpoints", nil, nil, &endpoints, http.StatusOK); err != nil {
		return managedpostgres.SnapshotCopyTargetObservation{}, err
	}
	if err := p.doJSON(ctx, http.MethodGet, path+"/operations", url.Values{"limit": {"1000"}}, nil, &ops, http.StatusOK); err != nil {
		return managedpostgres.SnapshotCopyTargetObservation{}, err
	}
	if branches.Branches == nil || endpoints.Endpoints == nil || ops.Operations == nil || ops.Pagination.Cursor != "" ||
		len(branches.Branches) != 1 || len(endpoints.Endpoints) != 1 {
		return managedpostgres.SnapshotCopyTargetObservation{}, managedpostgres.ErrUnavailable
	}
	b, endpoint, ready := selectBranch(branches.Branches, endpoints.Endpoints, "")
	if !validProviderID.MatchString(b.ID) || b.ProjectID != id || b.Name != "production" || b.ParentID != "" || b.ParentTimestamp != "" ||
		b.RestoredFrom != "" || b.RestoredAs != "" || b.RestoreStatus != "" || !validProviderID.MatchString(endpoint.ID) || endpoint.ProjectID != id ||
		endpoint.RegionID != p.regionID || endpoint.Disabled == nil || *endpoint.Disabled {
		return managedpostgres.SnapshotCopyTargetObservation{}, managedpostgres.ErrConflict
	}
	for _, op := range ops.Operations {
		if !validProviderID.MatchString(op.ID) || op.ProjectID != id || op.BranchID != "" && op.BranchID != b.ID || op.Status == "error" || op.Status == "cancelled" {
			return managedpostgres.SnapshotCopyTargetObservation{}, managedpostgres.ErrConflict
		}
	}
	if p.observedSpec(actual, endpoint) != spec || actual.DefaultEndpointSettings != endpointSettingsForSpec(spec) ||
		endpoint.SuspendTimeoutSecond != endpointSettingsForSpec(spec).SuspendTimeoutSecond {
		return managedpostgres.SnapshotCopyTargetObservation{}, managedpostgres.ErrConflict
	}
	return managedpostgres.SnapshotCopyTargetObservation{ProviderResourceID: id, CreatedAt: created.UTC(), Spec: spec,
		Prepared: ready && b.PendingState == "" && operationStatus(ops.Operations, ready) == managedpostgres.ProviderStatusReady}, nil
}

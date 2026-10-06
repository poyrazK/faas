package neon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

var _ managedpostgres.SnapshotCopyTargetCleanupProvider = (*Provider)(nil)

func (p *Provider) DiscoverSnapshotCopyTargetForCleanup(ctx context.Context, d managedpostgres.RestoreSourceDefinition, r managedpostgres.SnapshotCopyTargetRequest) (managedpostgres.SnapshotCopyTargetIdentity, error) {
	return p.snapshotCopyTargetCleanup(ctx, d, r, "discover")
}

func (p *Provider) DeleteSnapshotCopyTarget(ctx context.Context, d managedpostgres.RestoreSourceDefinition, r managedpostgres.SnapshotCopyTargetRequest) (managedpostgres.SnapshotCopyTargetIdentity, error) {
	return p.snapshotCopyTargetCleanup(ctx, d, r, "delete")
}

func (p *Provider) ObserveSnapshotCopyTargetDeletion(ctx context.Context, d managedpostgres.RestoreSourceDefinition, r managedpostgres.SnapshotCopyTargetRequest) (managedpostgres.SnapshotCopyTargetIdentity, error) {
	return p.snapshotCopyTargetCleanup(ctx, d, r, "observe")
}

func (p *Provider) snapshotCopyTargetCleanup(ctx context.Context, d managedpostgres.RestoreSourceDefinition, r managedpostgres.SnapshotCopyTargetRequest, action string) (managedpostgres.SnapshotCopyTargetIdentity, error) {
	if p == nil {
		return managedpostgres.SnapshotCopyTargetIdentity{}, managedpostgres.ErrUnavailable
	}
	if r.Validate() != nil || action != "discover" && r.ExpectedProviderResourceID == "" {
		return managedpostgres.SnapshotCopyTargetIdentity{}, managedpostgres.ErrInvalid
	}
	source, _, _, err := p.snapshotRestoreSelectors(d, r.Capture)
	if err != nil {
		return managedpostgres.SnapshotCopyTargetIdentity{}, err
	}
	id := r.ExpectedProviderResourceID
	if id != "" && (!validProviderID.MatchString(id) || id == source.projectID) {
		return managedpostgres.SnapshotCopyTargetIdentity{}, managedpostgres.ErrInvalid
	}
	// Provider reads of the live source/capture are intentionally absent.
	// Durable ownership authenticates cleanup even after source resources retire.
	name := p.snapshotCopyProjectName(r.ResourceID)
	if id == "" {
		active, err := p.findSnapshotCopyOwnedProject(ctx, name, false)
		if err != nil && !errors.Is(err, managedpostgres.ErrNotFound) {
			return managedpostgres.SnapshotCopyTargetIdentity{}, err
		}
		deleted, deletedErr := p.findSnapshotCopyOwnedProject(ctx, name, true)
		if deletedErr != nil && !errors.Is(deletedErr, managedpostgres.ErrNotFound) {
			return managedpostgres.SnapshotCopyTargetIdentity{}, deletedErr
		}
		if active.ID != "" && deleted.ID != "" {
			return managedpostgres.SnapshotCopyTargetIdentity{}, managedpostgres.ErrConflict
		}
		candidate := active
		if candidate.ID == "" {
			candidate = deleted
		}
		if candidate.ID == "" {
			return managedpostgres.SnapshotCopyTargetIdentity{}, managedpostgres.ErrNotFound
		}
		if candidate.ID == source.projectID {
			return managedpostgres.SnapshotCopyTargetIdentity{}, managedpostgres.ErrConflict
		}
		identity, err := p.validateSnapshotCopyCleanupProject(d, r, candidate.ID, candidate)
		if err != nil {
			return managedpostgres.SnapshotCopyTargetIdentity{}, err
		}
		id = candidate.ID
		r.ExpectedProviderResourceID, r.ExpectedCreatedAt = identity.ProviderResourceID, identity.CreatedAt
	}
	path := "/projects/" + url.PathEscape(id)
	var result projectResponse
	readErr := p.doJSON(ctx, http.MethodGet, path, nil, nil, &result, http.StatusOK)
	if readErr == nil {
		actual, err := p.validateSnapshotCopyCleanupProject(d, r, id, result.Project)
		if err != nil {
			return managedpostgres.SnapshotCopyTargetIdentity{}, err
		}
		// A recovery race cannot authorize another DELETE from an ambiguous
		// observation that simultaneously calls this identity deleted.
		deleted, err := p.findSnapshotCopyOwnedProject(ctx, name, true)
		if err != nil && !errors.Is(err, managedpostgres.ErrNotFound) {
			return managedpostgres.SnapshotCopyTargetIdentity{}, err
		}
		if deleted.ID != "" {
			return managedpostgres.SnapshotCopyTargetIdentity{}, managedpostgres.ErrUnavailable
		}
		if action == "delete" {
			var accepted projectResponse
			if err := p.doJSON(ctx, http.MethodDelete, path, nil, nil, &accepted, http.StatusOK); err != nil {
				return managedpostgres.SnapshotCopyTargetIdentity{}, err
			}
			// An ACK is only an ACK. The worker separately observes the
			// recoverable-deleted list and absence of this exact active ID.
			if accepted.Project.ID != "" {
				if _, err := p.validateSnapshotCopyCleanupProject(d, r, id, accepted.Project); err != nil {
					return managedpostgres.SnapshotCopyTargetIdentity{}, err
				}
			}
		}
		return actual, nil
	}
	if !errors.Is(readErr, managedpostgres.ErrNotFound) {
		return managedpostgres.SnapshotCopyTargetIdentity{}, readErr
	}
	deleted, err := p.findSnapshotCopyOwnedProject(ctx, name, true)
	if errors.Is(err, managedpostgres.ErrNotFound) {
		err = managedpostgres.ErrUnavailable
	}
	if err != nil {
		return managedpostgres.SnapshotCopyTargetIdentity{}, err
	}
	actual, err := p.validateSnapshotCopyCleanupProject(d, r, id, deleted)
	if err != nil {
		return managedpostgres.SnapshotCopyTargetIdentity{}, err
	}
	// Recheck the exact active ID after the independently filtered tombstone.
	if err := p.doJSON(ctx, http.MethodGet, path, nil, nil, &result, http.StatusOK); !errors.Is(err, managedpostgres.ErrNotFound) {
		if err == nil {
			err = managedpostgres.ErrUnavailable
		}
		return managedpostgres.SnapshotCopyTargetIdentity{}, err
	}
	actual.Deleted = true
	return actual, nil
}

func (p *Provider) validateSnapshotCopyCleanupProject(d managedpostgres.RestoreSourceDefinition, r managedpostgres.SnapshotCopyTargetRequest, id string, actual project) (managedpostgres.SnapshotCopyTargetIdentity, error) {
	created, err := time.Parse(time.RFC3339Nano, actual.CreatedAt)
	if !validProviderID.MatchString(id) || actual.ID != id || actual.Name != p.snapshotCopyProjectName(r.ResourceID) || actual.OrganizationID != p.organizationID || actual.RegionID != p.regionID || actual.PostgresMajor != d.Spec.PostgresMajor ||
		err != nil || created.Before(r.CaptureCreatedAt) || created.After(time.Now()) || created.Nanosecond()%1000 != 0 || !r.ExpectedCreatedAt.IsZero() && !created.Equal(r.ExpectedCreatedAt) {
		return managedpostgres.SnapshotCopyTargetIdentity{}, managedpostgres.ErrConflict
	}
	return managedpostgres.SnapshotCopyTargetIdentity{ProviderResourceID: id, CreatedAt: created.UTC()}, nil
}

func (p *Provider) findSnapshotCopyOwnedProject(ctx context.Context, name string, recoverable bool) (project, error) {
	seen := map[string]bool{}
	cursor := ""
	var found project
	for page := 0; page < maximumProjectSearchPages; page++ {
		q := url.Values{"limit": {"400"}, "search": {name}, "org_id": {p.organizationID}, "recoverable": {"false"}}
		if recoverable {
			q.Set("recoverable", "true")
		}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		var response struct {
			projectsResponse
			Unavailable json.RawMessage `json:"unavailable"`
		}
		if err := p.doJSON(ctx, http.MethodGet, "/projects", q, nil, &response, http.StatusOK); err != nil {
			return project{}, err
		}
		if response.Projects == nil || len(response.UnavailableProjectIDs) > 0 || len(response.Unavailable) > 0 && string(response.Unavailable) != "null" && string(response.Unavailable) != "[]" && string(response.Unavailable) != "{}" {
			return project{}, managedpostgres.ErrUnavailable
		}
		for _, candidate := range response.Projects {
			if candidate.Name != name {
				continue
			}
			if found.ID != "" || !validProviderID.MatchString(candidate.ID) || candidate.OrganizationID != p.organizationID {
				return project{}, managedpostgres.ErrConflict
			}
			found = candidate
		}
		cursor = response.Pagination.Cursor
		if cursor == "" {
			if found.ID == "" {
				return project{}, managedpostgres.ErrNotFound
			}
			return found, nil
		}
		if seen[cursor] {
			return project{}, managedpostgres.ErrUnavailable
		}
		seen[cursor] = true
	}
	return project{}, managedpostgres.ErrUnavailable
}

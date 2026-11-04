package neon

import (
	"context"
	"net/http"
	"net/url"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

// The delete grace period (ADR-20261029174950). A database scheduled for
// deletion keeps its project, branch and restore history, but its primary
// endpoint is disabled: Neon schedules a suspend, restricts connections, and
// no connection or console action can enable it again. Undo enables it.

var _ managedpostgres.DeletionHoldProvider = (*Provider)(nil)

func (p *Provider) HoldForDeletion(ctx context.Context, providerResourceID string) error {
	return p.setPrimaryEndpointDisabled(ctx, providerResourceID, true)
}

func (p *Provider) ReleaseDeletionHold(ctx context.Context, providerResourceID string) error {
	return p.setPrimaryEndpointDisabled(ctx, providerResourceID, false)
}

// setPrimaryEndpointDisabled sets the database branch's read-write endpoint's
// disabled flag and reads it back, because an accepted PATCH alone is not
// evidence. It writes nothing when the flag is already set.
func (p *Provider) setPrimaryEndpointDisabled(ctx context.Context, providerResourceID string, disabled bool) error {
	ref, err := parseResourceRef(providerResourceID)
	if err != nil {
		return err
	}
	metadata, err := p.readDatabaseMetadata(ctx, ref, false)
	if err != nil {
		return err
	}
	_, primary, _ := selectBranch(metadata.branches.Branches, metadata.endpoints.Endpoints, ref.branchID)
	if !validProviderID.MatchString(primary.ID) {
		return managedpostgres.ErrUnavailable
	}
	if endpointDisabled(primary) == disabled {
		return nil
	}
	path := "/projects/" + url.PathEscape(ref.projectID) + "/endpoints/" + url.PathEscape(primary.ID)
	var patch struct {
		Endpoint struct {
			Disabled bool `json:"disabled"`
		} `json:"endpoint"`
	}
	patch.Endpoint.Disabled = disabled
	if err := p.doJSON(ctx, http.MethodPatch, path, nil, patch, nil, http.StatusOK); err != nil {
		return err
	}
	var confirmed struct {
		Endpoint endpoint `json:"endpoint"`
	}
	if err := p.doJSON(ctx, http.MethodGet, path, nil, nil, &confirmed, http.StatusOK); err != nil {
		return err
	}
	if confirmed.Endpoint.ID != primary.ID || endpointDisabled(confirmed.Endpoint) != disabled {
		return managedpostgres.ErrUnavailable
	}
	return nil
}

func endpointDisabled(e endpoint) bool {
	return e.Disabled != nil && *e.Disabled
}

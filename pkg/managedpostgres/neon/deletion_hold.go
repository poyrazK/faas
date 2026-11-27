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

// The compute cap sweep holds a database the same way when its account has
// nothing left (ADR-20261025094915 amendment 2026-11-05).
var _ managedpostgres.ComputeHoldProvider = (*Provider)(nil)

func (p *Provider) HoldForDeletion(ctx context.Context, providerResourceID string) error {
	return p.setPrimaryEndpointDisabled(ctx, providerResourceID, true)
}

func (p *Provider) ReleaseDeletionHold(ctx context.Context, providerResourceID string) error {
	return p.setPrimaryEndpointDisabled(ctx, providerResourceID, false)
}

// ComputeHeld reports whether the database branch's read-write endpoint is
// disabled.
func (p *Provider) ComputeHeld(ctx context.Context, providerResourceID string) (bool, error) {
	ref, err := parseResourceRef(providerResourceID)
	if err != nil {
		return false, err
	}
	metadata, err := p.readDatabaseMetadata(ctx, ref, false)
	if err != nil {
		return false, err
	}
	_, primary, _ := selectBranch(metadata.branches.Branches, metadata.endpoints.Endpoints, ref.branchID)
	if !validProviderID.MatchString(primary.ID) {
		return false, managedpostgres.ErrUnavailable
	}
	return endpointDisabled(primary), nil
}

// setPrimaryEndpointDisabled sets the database branch's read-write endpoint's
// disabled flag and reads it back, because an accepted PATCH alone is not
// evidence. It writes nothing when the flag is already set.
func (p *Provider) setPrimaryEndpointDisabled(ctx context.Context, providerResourceID string, disabled bool) error {
	ref, err := parseResourceRef(providerResourceID)
	if err != nil {
		return err
	}
	// A project-only selector follows its mutable default branch. Only a
	// persisted dataset identity authorizes changing an endpoint's hold.
	if ref.branchID == "" {
		return managedpostgres.ErrConflict
	}
	metadata, err := p.readDatabaseMetadata(ctx, ref, false)
	if err != nil {
		return err
	}
	selected, primary, _ := selectBranch(metadata.branches.Branches, metadata.endpoints.Endpoints, ref.branchID)
	if selected.ID != ref.branchID || primary.BranchID != ref.branchID || !validProviderID.MatchString(primary.ID) ||
		selected.ProjectID != "" && selected.ProjectID != ref.projectID || primary.ProjectID != "" && primary.ProjectID != ref.projectID {
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
	if err := p.patchWhileProjectBusy(ctx, path, patch); err != nil {
		return err
	}
	var confirmed struct {
		Endpoint endpoint `json:"endpoint"`
	}
	if err := p.doJSON(ctx, http.MethodGet, path, nil, nil, &confirmed, http.StatusOK); err != nil {
		return err
	}
	if confirmed.Endpoint.ID != primary.ID || confirmed.Endpoint.BranchID != ref.branchID ||
		confirmed.Endpoint.ProjectID != "" && confirmed.Endpoint.ProjectID != ref.projectID || endpointDisabled(confirmed.Endpoint) != disabled {
		return managedpostgres.ErrUnavailable
	}
	return nil
}

func endpointDisabled(e endpoint) bool {
	return e.Disabled != nil && *e.Disabled
}

// endpointHoldBusyAttempts bounds how often a hold change is retried while
// Neon answers 423 because the project runs another operation, as just after
// a compute starts or suspends. The cap qualification of 2026-11-22 failed
// its deletion hold probe on that refusal right after the scale-to-zero
// probe woke the compute; a cap sweep holding a database that just woke met
// the same refusal. With the default poll interval the retries span about
// two minutes.
const endpointHoldBusyAttempts = 60

// patchWhileProjectBusy sends an idempotent endpoint PATCH, waiting out
// Neon's 423 refusals. Any other outcome returns at once.
func (p *Provider) patchWhileProjectBusy(ctx context.Context, path string, patch any) error {
	return p.sendWhileProjectBusy(ctx, http.MethodPatch, path, patch)
}

// sendWhileProjectBusy sends an idempotent request, waiting out Neon's 423
// refusals. Any other outcome returns at once.
func (p *Provider) sendWhileProjectBusy(ctx context.Context, method, path string, input any) error {
	wait := 8 * p.credentialPollInterval
	for attempt := 1; ; attempt++ {
		err := p.doJSON(ctx, method, path, nil, input, nil, http.StatusOK)
		if err == nil || !projectBusy(err) || attempt >= endpointHoldBusyAttempts {
			return err
		}
		if err := sleepContext(ctx, wait); err != nil {
			return err
		}
	}
}

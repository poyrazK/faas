package neon

import (
	"context"
	"net/http"
	"net/url"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

// The storage pool (ADR-20261025094915 decision 9). A Neon project's
// settings.quota.logical_size_bytes limits each of its branches, not their
// total: a branch past it keeps its computes, but writes that would grow it
// fail, so data can still be read and deleted. A restore is a branch inside
// its source's project, so both share one limit.

var _ managedpostgres.StorageCapProvider = (*Provider)(nil)

func (p *Provider) ObserveStorageCap(ctx context.Context, providerResourceID string) (managedpostgres.StorageCapObservation, error) {
	ref, err := parseResourceRef(providerResourceID)
	if err != nil {
		return managedpostgres.StorageCapObservation{}, err
	}
	project, err := p.readComputeCapProject(ctx, ref.projectID)
	if err != nil {
		return managedpostgres.StorageCapObservation{}, err
	}
	databaseBranch, err := p.storageCapBranch(ctx, ref)
	if err != nil {
		return managedpostgres.StorageCapObservation{}, err
	}
	if databaseBranch.LogicalSize == nil || *databaseBranch.LogicalSize < 0 {
		return managedpostgres.StorageCapObservation{}, managedpostgres.ErrUnavailable
	}
	observed := managedpostgres.StorageCapObservation{Scope: ref.projectID, SizeBytes: *databaseBranch.LogicalSize}
	if limit := project.Settings.Quota.LogicalSizeBytes; limit != nil {
		if *limit < 0 {
			return managedpostgres.StorageCapObservation{}, managedpostgres.ErrUnavailable
		}
		observed.LimitBytes = *limit
	}
	return observed, nil
}

// storageCapBranch reads the database's branch: the one its resource names,
// or its project's only default branch.
func (p *Provider) storageCapBranch(ctx context.Context, ref resourceRef) (branch, error) {
	if ref.branchID != "" {
		var response struct {
			Branch branch `json:"branch"`
		}
		path := "/projects/" + url.PathEscape(ref.projectID) + "/branches/" + url.PathEscape(ref.branchID)
		if err := p.doJSON(ctx, http.MethodGet, path, nil, nil, &response, http.StatusOK); err != nil {
			return branch{}, err
		}
		if response.Branch.ID != ref.branchID {
			return branch{}, managedpostgres.ErrUnavailable
		}
		return response.Branch, nil
	}
	branches, err := p.listProjectBranches(ctx, ref.projectID, "")
	if err != nil {
		return branch{}, err
	}
	var found branch
	for _, candidate := range branches {
		if !candidate.Default {
			continue
		}
		if found.ID != "" {
			return branch{}, managedpostgres.ErrConflict
		}
		found = candidate
	}
	if found.ID == "" {
		return branch{}, managedpostgres.ErrUnavailable
	}
	return found, nil
}

// SetStorageCap writes the per-branch logical size limit and, read back
// first, the project's current compute quota, so the write never changes the
// compute cap whatever Neon's merge semantics for settings.quota. It reads the
// project again to confirm, because an accepted PATCH alone is not evidence.
func (p *Provider) SetStorageCap(ctx context.Context, providerResourceID string, limitBytes int64) error {
	if limitBytes <= 0 {
		// Zero is no limit to Neon; the pool never removes one this way.
		return managedpostgres.ErrInvalid
	}
	ref, err := parseResourceRef(providerResourceID)
	if err != nil {
		return err
	}
	current, err := p.readComputeCapProject(ctx, ref.projectID)
	if err != nil {
		return err
	}
	var patch computeCapPatch
	patch.Project.Settings.Quota = computeCapQuota{ComputeTimeSeconds: current.Settings.Quota.ComputeTimeSeconds, LogicalSizeBytes: &limitBytes}
	if err := p.doJSON(ctx, http.MethodPatch, "/projects/"+url.PathEscape(ref.projectID), nil, patch, nil, http.StatusOK); err != nil {
		return err
	}
	confirmed, err := p.readComputeCapProject(ctx, ref.projectID)
	if err != nil {
		return err
	}
	if !sameQuota(confirmed.Settings.Quota.LogicalSizeBytes, &limitBytes) ||
		!sameQuota(confirmed.Settings.Quota.ComputeTimeSeconds, current.Settings.Quota.ComputeTimeSeconds) {
		return managedpostgres.ErrUnavailable
	}
	return nil
}

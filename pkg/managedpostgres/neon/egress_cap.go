package neon

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

// Egress quota backstop (ADR-20261110160000 slice 4). A Neon project holds a
// monthly settings.quota.data_transfer_bytes; when its data_transfer_bytes
// counter for the consumption period reaches it, Neon suspends every compute
// in the project until the quota is raised or the period ends. The cap sweep
// holds a database itself once it sent its egress allowance; the quota stops
// it even when the sweep does not run. A restore is a branch inside its
// source's project, so both share one counter and one quota.

var _ managedpostgres.EgressCapProvider = (*Provider)(nil)

func (p *Provider) ObserveEgressCap(ctx context.Context, providerResourceID string) (managedpostgres.EgressCapObservation, error) {
	ref, err := parseResourceRef(providerResourceID)
	if err != nil {
		return managedpostgres.EgressCapObservation{}, err
	}
	project, err := p.readComputeCapProject(ctx, ref.projectID)
	if err != nil {
		return managedpostgres.EgressCapObservation{}, err
	}
	periodStart, err := time.Parse(time.RFC3339, project.ConsumptionPeriodStart)
	if err != nil || project.DataTransferBytes < 0 {
		return managedpostgres.EgressCapObservation{}, managedpostgres.ErrUnavailable
	}
	observed := managedpostgres.EgressCapObservation{Scope: ref.projectID, PeriodStart: periodStart.UTC(), UsedBytes: project.DataTransferBytes}
	if quota := project.Settings.Quota.DataTransferBytes; quota != nil {
		if *quota < 0 {
			return managedpostgres.EgressCapObservation{}, managedpostgres.ErrUnavailable
		}
		observed.QuotaBytes = *quota
	}
	return observed, nil
}

// SetEgressCap writes the transfer quota and keeps the project's other
// quotas as read, then reads the project again to confirm.
func (p *Provider) SetEgressCap(ctx context.Context, providerResourceID string, quotaBytes int64) error {
	if quotaBytes <= 0 {
		// Zero is unlimited to Neon; a quota is never removed this way.
		return managedpostgres.ErrInvalid
	}
	return p.writeQuota(ctx, providerResourceID, func(q *computeCapQuota) { q.DataTransferBytes = &quotaBytes })
}

// writeQuota sends the project's quotas as read with set applied, so a write
// of one never changes another whatever Neon's merge semantics for
// settings.quota, and confirms all of them by reading the project again: an
// accepted PATCH alone is not evidence.
func (p *Provider) writeQuota(ctx context.Context, providerResourceID string, set func(*computeCapQuota)) error {
	ref, err := parseResourceRef(providerResourceID)
	if err != nil {
		return err
	}
	current, err := p.readComputeCapProject(ctx, ref.projectID)
	if err != nil {
		return err
	}
	want := current.Settings.Quota
	set(&want)
	var patch computeCapPatch
	patch.Project.Settings.Quota = want
	if err := p.doJSON(ctx, http.MethodPatch, "/projects/"+url.PathEscape(ref.projectID), nil, patch, nil, http.StatusOK); err != nil {
		return err
	}
	confirmed, err := p.readComputeCapProject(ctx, ref.projectID)
	if err != nil {
		return err
	}
	got := confirmed.Settings.Quota
	if !sameQuota(got.ComputeTimeSeconds, want.ComputeTimeSeconds) || !sameQuota(got.LogicalSizeBytes, want.LogicalSizeBytes) ||
		!sameQuota(got.DataTransferBytes, want.DataTransferBytes) {
		return managedpostgres.ErrUnavailable
	}
	return nil
}

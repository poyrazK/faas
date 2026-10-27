package neon

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

// Compute caps (ADR-20261025094915). A Neon project holds a monthly
// settings.quota.compute_time_seconds; when its compute_time_seconds counter
// reaches it, Neon suspends every compute in the project until the quota is
// raised or the counters reset with the next consumption period. A restore is
// a branch inside its source's project, so both share one counter and quota.
// Neon counts CPU seconds, and one compute unit is one vCPU, so the quota is
// in the compute-unit seconds the ledger uses; staging qualification checks
// that the two agree before enforcement is turned on.

var _ managedpostgres.ComputeCapProvider = (*Provider)(nil)

type computeCapProject struct {
	ID                     string `json:"id"`
	ComputeTimeSeconds     int64  `json:"compute_time_seconds"`
	ActiveTimeSeconds      int64  `json:"active_time_seconds"`
	ConsumptionPeriodStart string `json:"consumption_period_start"`
	Settings               struct {
		Quota computeCapQuota `json:"quota"`
	} `json:"settings"`
}

type computeCapQuota struct {
	ComputeTimeSeconds *int64 `json:"compute_time_seconds,omitempty"`
	LogicalSizeBytes   *int64 `json:"logical_size_bytes,omitempty"`
}

type computeCapPatch struct {
	Project struct {
		Settings struct {
			Quota computeCapQuota `json:"quota"`
		} `json:"settings"`
	} `json:"project"`
}

func (p *Provider) ObserveComputeCap(ctx context.Context, providerResourceID string) (managedpostgres.ComputeCapObservation, error) {
	ref, err := parseResourceRef(providerResourceID)
	if err != nil {
		return managedpostgres.ComputeCapObservation{}, err
	}
	project, err := p.readComputeCapProject(ctx, ref.projectID)
	if err != nil {
		return managedpostgres.ComputeCapObservation{}, err
	}
	periodStart, err := time.Parse(time.RFC3339, project.ConsumptionPeriodStart)
	if err != nil || project.ComputeTimeSeconds < 0 {
		return managedpostgres.ComputeCapObservation{}, managedpostgres.ErrUnavailable
	}
	observed := managedpostgres.ComputeCapObservation{
		Scope:           ref.projectID,
		PeriodStart:     periodStart.UTC(),
		UsedUnitSeconds: project.ComputeTimeSeconds,
	}
	if quota := project.Settings.Quota.ComputeTimeSeconds; quota != nil {
		if *quota < 0 {
			return managedpostgres.ComputeCapObservation{}, managedpostgres.ErrUnavailable
		}
		observed.QuotaUnitSeconds = *quota
	}
	return observed, nil
}

// SetComputeCap writes the compute quota and, read back first, the project's
// current logical size quota, so the write never changes the storage limit
// whatever Neon's merge semantics for settings.quota. It reads the project
// again to confirm, because an accepted PATCH alone is not evidence.
func (p *Provider) SetComputeCap(ctx context.Context, providerResourceID string, quotaUnitSeconds int64) error {
	if quotaUnitSeconds <= 0 {
		// Zero is unlimited to Neon; a cap is never removed this way.
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
	patch.Project.Settings.Quota = computeCapQuota{ComputeTimeSeconds: &quotaUnitSeconds, LogicalSizeBytes: current.Settings.Quota.LogicalSizeBytes}
	path := "/projects/" + url.PathEscape(ref.projectID)
	if err := p.doJSON(ctx, http.MethodPatch, path, nil, patch, nil, http.StatusOK); err != nil {
		return err
	}
	confirmed, err := p.readComputeCapProject(ctx, ref.projectID)
	if err != nil {
		return err
	}
	if !sameQuota(confirmed.Settings.Quota.ComputeTimeSeconds, &quotaUnitSeconds) ||
		!sameQuota(confirmed.Settings.Quota.LogicalSizeBytes, current.Settings.Quota.LogicalSizeBytes) {
		return managedpostgres.ErrUnavailable
	}
	return nil
}

func (p *Provider) readComputeCapProject(ctx context.Context, projectID string) (computeCapProject, error) {
	var response struct {
		Project computeCapProject `json:"project"`
	}
	if err := p.doJSON(ctx, http.MethodGet, "/projects/"+url.PathEscape(projectID), nil, nil, &response, http.StatusOK); err != nil {
		return computeCapProject{}, err
	}
	if response.Project.ID != projectID {
		return computeCapProject{}, managedpostgres.ErrUnavailable
	}
	return response.Project, nil
}

func sameQuota(left, right *int64) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}

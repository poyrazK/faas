package neon

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

var _ managedpostgres.ComputeUseReporter = (*Provider)(nil)

// computeUseReadSpan is the longest window one hourly consumption read may
// cover.
const computeUseReadSpan = 168 * time.Hour

// ComputeUnitSecondsSince sums the project's compute_unit_seconds in Neon's
// hourly consumption report from from, an hour boundary, until now. Neon
// keeps the report current to about a minute, the open hour included, while
// the project's compute_time_seconds counter can stay put for over an hour
// (ADR-20261025094915 amendment 2026-11-05). Just after an hour opens the
// report can lack it, which reads as nothing used in it yet. A restore is a
// branch of its source's project, so it reports the project's use.
func (p *Provider) ComputeUnitSecondsSince(ctx context.Context, providerResourceID string, from time.Time) (int64, error) {
	ref, err := parseResourceRef(providerResourceID)
	if err != nil {
		return 0, err
	}
	from = from.UTC()
	open := p.now().UTC().Truncate(time.Hour)
	if from.IsZero() || !from.Equal(from.Truncate(time.Hour)) || from.After(open) {
		return 0, managedpostgres.ErrInvalid
	}
	until := open.Add(time.Hour)
	var total int64
	for start := from; start.Before(until); {
		end := start.Add(computeUseReadSpan)
		if end.After(until) {
			end = until
		}
		used, err := p.computeUnitSecondsBetween(ctx, ref.projectID, start, end, end.Equal(until))
		if err != nil {
			return 0, err
		}
		if total, err = addQuantity(total, used); err != nil {
			return 0, err
		}
		start = end
	}
	return total, nil
}

// computeUnitSecondsBetween reads one hourly window. With lastOpen, the
// window's last hour is still open and may be missing from the report.
func (p *Provider) computeUnitSecondsBetween(ctx context.Context, projectID string, from, to time.Time, lastOpen bool) (int64, error) {
	query := url.Values{
		"project_ids": {projectID},
		"from":        {from.Format(time.RFC3339)},
		"to":          {to.Format(time.RFC3339)},
		"granularity": {"hourly"},
		"org_id":      {p.organizationID},
		"metrics":     {"compute_unit_seconds"},
		"limit":       {"1"},
	}
	var response consumptionResponse
	if err := p.doJSON(ctx, http.MethodGet, "/consumption_history/v2/projects", query, nil, &response, http.StatusOK); err != nil {
		return 0, err
	}
	if len(response.Projects) == 0 {
		return 0, managedpostgres.ErrNotFound
	}
	project := response.Projects[0]
	if len(response.Projects) != 1 || project.ProjectID != projectID {
		return 0, managedpostgres.ErrUnavailable
	}
	if err := validateConsumptionCoverage(project, managedpostgres.UsageWindow{From: from, To: to}); err != nil {
		closed := to.Add(-time.Hour)
		switch {
		case !lastOpen:
			return 0, err
		case closed.Equal(from):
			if consumptionFrames(project) != 0 {
				return 0, err
			}
		case validateConsumptionCoverage(project, managedpostgres.UsageWindow{From: from, To: closed}) != nil:
			return 0, err
		}
	}
	compute, _, _, _, err := sumConsumption(project)
	return compute, err
}

func consumptionFrames(project projectConsumption) int {
	frames := 0
	for _, period := range project.Periods {
		frames += len(period.Consumption)
	}
	return frames
}

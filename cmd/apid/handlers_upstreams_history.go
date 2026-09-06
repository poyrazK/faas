package main

import (
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	dataUpstreamHistoryDefaultWindow = 24 * time.Hour
	dataUpstreamHistoryMaxWindow     = 30 * 24 * time.Hour
	dataUpstreamHistoryDefaultBucket = 5 * time.Minute
	dataUpstreamHistoryMinBucket     = time.Minute
	dataUpstreamHistoryMaxBucket     = 24 * time.Hour
	dataUpstreamHistoryMaxBuckets    = 1000
)

// getUpstreamHistory returns bucketed probe history for every upstream on an
// app. The raw probe stream is aggregated in Postgres so the response stays
// bounded even when a Scale app has the full 30-day retention window.
func (s *server) getUpstreamHistory(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.runtimeBool(runtimeConfigDataPlacement, s.dataPlacementEnabled) {
		api.WriteProblem(w, api.ErrPlanFeatureGated("data_upstreams", acct.Plan))
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	query, prob := parseDataUpstreamHistoryQuery(r, time.Now().UTC())
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	rows, err := s.store.ListDataUpstreamProbeHistory(
		r.Context(), acct.ID, app.ID, query.DeploymentScope, query.Region,
		query.From, query.To, query.Bucket,
	)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list upstream history"))
		return
	}
	out := make([]api.DataUpstreamHistoryResponse, 0, len(rows))
	for _, row := range rows {
		buckets := make([]api.DataUpstreamHistoryBucket, 0, len(row.Buckets))
		for _, bucket := range row.Buckets {
			buckets = append(buckets, api.DataUpstreamHistoryBucket{
				SampledAt:   bucket.SampledAt.UTC().Format(time.RFC3339Nano),
				P50Ms:       bucket.P50Ms,
				P95Ms:       bucket.P95Ms,
				SampleCount: bucket.SampleCount,
			})
		}
		out = append(out, api.DataUpstreamHistoryResponse{
			HostRedactedHash: row.HostRedactedHash,
			Kind:             api.DataUpstreamKind(row.Kind),
			Port:             row.Port,
			Scope:            row.Scope,
			DeploymentScope:  row.DeploymentScope,
			Region:           row.Region,
			Buckets:          buckets,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

type dataUpstreamHistoryQuery struct {
	From            time.Time
	To              time.Time
	Bucket          time.Duration
	Region          string
	DeploymentScope string
}

func parseDataUpstreamHistoryQuery(r *http.Request, now time.Time) (dataUpstreamHistoryQuery, *api.Problem) {
	q := r.URL.Query()
	to := now.UTC()
	if raw := q.Get("to"); raw != "" {
		parsed, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return dataUpstreamHistoryQuery{}, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "invalid to", "to must be RFC3339")
		}
		to = parsed.UTC()
	}
	from := to.Add(-dataUpstreamHistoryDefaultWindow)
	if raw := q.Get("from"); raw != "" {
		parsed, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return dataUpstreamHistoryQuery{}, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "invalid from", "from must be RFC3339")
		}
		from = parsed.UTC()
	}
	if !from.Before(to) {
		return dataUpstreamHistoryQuery{}, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "invalid window", "from must be earlier than to")
	}
	if to.Sub(from) > dataUpstreamHistoryMaxWindow {
		return dataUpstreamHistoryQuery{}, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "invalid window", "history window cannot exceed 30 days")
	}

	bucket := dataUpstreamHistoryDefaultBucket
	if raw := q.Get("bucket"); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return dataUpstreamHistoryQuery{}, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "invalid bucket", "bucket must be a duration such as 5m")
		}
		bucket = parsed
	}
	if bucket < dataUpstreamHistoryMinBucket || bucket > dataUpstreamHistoryMaxBucket || bucket%time.Second != 0 {
		return dataUpstreamHistoryQuery{}, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "invalid bucket", "bucket must be between 1m and 24h in whole seconds")
	}
	if int64(to.Sub(from)/bucket) > dataUpstreamHistoryMaxBuckets {
		return dataUpstreamHistoryQuery{}, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "invalid bucket", "bucket count cannot exceed 1000")
	}

	deploymentScope, prob := deploymentScopeFromQuery(r)
	if prob != nil {
		return dataUpstreamHistoryQuery{}, prob
	}
	region := strings.TrimSpace(q.Get("region"))
	if region != "" && !validDataUpstreamHistoryRegion(region) {
		return dataUpstreamHistoryQuery{}, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "invalid region", "region must contain 1-32 lowercase letters, digits, hyphens, or underscores")
	}
	return dataUpstreamHistoryQuery{
		From:            from,
		To:              to,
		Bucket:          bucket,
		Region:          region,
		DeploymentScope: deploymentScope,
	}, nil
}

func validDataUpstreamHistoryRegion(region string) bool {
	if len(region) == 0 || len(region) > 32 {
		return false
	}
	for _, r := range region {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			continue
		}
		return false
	}
	return true
}

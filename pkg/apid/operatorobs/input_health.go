package operatorobs

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
	"github.com/onebox-faas/faas/pkg/state"
)

const InputHealthPath = "/v1/admin/obs/input-health"

type inputJobSpec struct {
	job, metric, daemon string
	discovery           bool
}

func InputHealthRoles() []string { return []string{"single-box-v1", "control-plane-v1"} }

func inputJobSpecs(role string) []inputJobSpec {
	base := []inputJobSpec{{"apid", "apid_daemon_ready", "apid", false}, {"gatewayd", "gatewayd_public_daemon_ready", "gatewayd-public", false}, {"schedd", "schedd_daemon_ready", "schedd", false}}
	switch role {
	case "single-box-v1":
		return append(base, inputJobSpec{"vmmd", "vmmd_daemon_ready", "vmmd", false})
	case "control-plane-v1":
		return append(base, inputJobSpec{"gatewayd-internal", "gatewayd_daemon_ready", "gatewayd-internal", true}, inputJobSpec{"schedd-compute", "schedd_daemon_ready", "schedd", true}, inputJobSpec{"vmmd", "vmmd_daemon_ready", "vmmd", true})
	default:
		return nil
	}
}

func InputHealthSelection(raw string, forceQuery bool) (string, error) {
	q, err := url.ParseQuery(raw)
	if err != nil || forceQuery || len(q) > 1 {
		return "", state.ErrInvalidArgument
	}
	if len(q) == 0 {
		if raw != "" {
			return "", state.ErrInvalidArgument
		}
		return "", nil
	}
	role := q.Get("expected_role")
	if len(q["expected_role"]) != 1 || len(inputJobSpecs(role)) == 0 {
		return "", state.ErrInvalidArgument
	}
	return role, nil
}

func InputHealthLimitations() []string {
	return []string{"role_selected_per_request_not_deployment_evidence", "static_expectations_are_minimum_one_not_full_inventory", "discovery_counts_not_target_identity_membership", "default_scrape_job_collision_uses_exported_job", "readiness_sample_presence_not_readiness_value", "metric_checks_cover_fresh_up_targets_only", "one_vetted_idle_safe_gauge_per_selected_job_not_all_rule_inputs", "historical_samples_bounded_by_lookback", "raw_labels_queries_urls_and_errors_excluded"}
}

// InputHealth reads only. Each fixed job gets an independent assessment under
// one deadline; a broken job cannot erase completed evidence for another.
func InputHealth(ctx context.Context, client *promql.Client, role string, now time.Time) api.ObsInputHealth {
	now = now.UTC()
	out := api.ObsInputHealth{GeneratedAt: now, Scope: "selected_critical_telemetry_inputs", ExpectedRole: role, AvailableRoles: InputHealthRoles(), State: "unknown", Reason: "expected_role_not_selected", MaxAgeSeconds: int(api.ObsInputHealthMaxAge.Seconds()), LookbackSeconds: int(api.ObsInputHealthLookback.Seconds()), Jobs: []api.ObsInputJob{}, Limitations: InputHealthLimitations()}
	if role == "" {
		return out
	}
	specs := inputJobSpecs(role)
	if len(specs) == 0 {
		out.Reason = "expected_role_unavailable"
		return out
	}
	ctx, cancel := context.WithTimeout(ctx, api.ObsInputHealthBudget)
	defer cancel()
	out.Jobs = make([]api.ObsInputJob, len(specs))
	var wg sync.WaitGroup
	for i, spec := range specs {
		wg.Go(func() { out.Jobs[i] = readInputJob(ctx, client, spec, now) })
	}
	wg.Wait()
	states := make([]string, 0, len(out.Jobs))
	for _, job := range out.Jobs {
		states = append(states, job.State)
	}
	out.State, out.Reason = inputOverallState(states...), ""
	return out
}

func readInputJob(ctx context.Context, client *promql.Client, spec inputJobSpec, now time.Time) api.ObsInputJob {
	expect := api.ObsInputExpectation{Source: "explicit_minimum", State: "healthy", MinimumTargets: inputInt(1)}
	if spec.discovery {
		expect = readInputExpectation(ctx, client, spec.job, now)
	}
	targets, eligible := readInputTargets(ctx, client, spec.job, expect, now)
	metric := api.ObsInputMetric{Metric: spec.metric, State: "unknown", Reason: "target_evidence_unavailable"}
	if targets.Counts != nil {
		metric = readInputMetric(ctx, client, spec, eligible, expect, now)
		if len(eligible) == 0 && targets.Counts.Observed != 0 {
			metric = api.ObsInputMetric{Metric: spec.metric, State: "unknown", Reason: "no_fresh_up_targets"}
		}
	}
	return api.ObsInputJob{Job: spec.job, State: inputOverallState(expect.State, targets.State, metric.State), Expectation: expect, Targets: targets, Metrics: []api.ObsInputMetric{metric}}
}

func inputVector(ctx context.Context, client *promql.Client, expr string, now time.Time) ([]promql.VectorSample, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if client == nil || client.BaseURL() == "" {
		return nil, errNotificationNotConfigured
	}
	rows, err := client.QueryVectorStrictAt(ctx, expr, now)
	if err == nil {
		err = ctx.Err()
	}
	return rows, err
}

func inputInt(n int) *int { return &n }

func inputOverallState(states ...string) string {
	known, unknown := false, false
	for _, s := range states {
		switch s {
		case "degraded":
			return "degraded"
		case "partial":
			known, unknown = true, true
		case "unknown":
			unknown = true
		case "healthy", "not_applicable":
			known = true
		}
	}
	if unknown && known {
		return "partial"
	}
	if unknown || !known {
		return "unknown"
	}
	return "healthy"
}

func inputMetricQuery(spec inputJobSpec) string {
	selector := fmt.Sprintf(`%s{job=%q,daemon=%q}`, spec.metric, spec.job, spec.daemon)
	// Keep original sample times. The outer query's timestamp is evaluation time.
	return fmt.Sprintf(`max_over_time(timestamp(%s)[%ds:%ds])`, selector, int(api.ObsInputHealthLookback.Seconds()), int(api.ObsInputHealthStep.Seconds()))
}

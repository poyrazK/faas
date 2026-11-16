package tasktoolrelease

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func observeRecoveryDeployment(ctx context.Context, c *api.Client, record api.TaskToolReleaseIntentRecord, role string) RecoveryDeployment {
	out := RecoveryDeployment{Role: role, Lookup: "not_submitted", Status: "unknown"}
	if record.Execution == nil || record.Execution.Receipt == nil {
		return out
	}
	r := record.Execution.Receipt
	pinRole := role
	if role == "baseline" {
		pinRole = "web"
	}
	pin, ok := recoveryAppPin(record, pinRole)
	if !ok {
		return out
	}
	switch role {
	case "worker":
		out.DeploymentID = r.WorkerDeploymentID
	case "web":
		out.DeploymentID = r.WebDeploymentID
	case "baseline":
		out.DeploymentID = r.ServingDeploymentID
	}
	if out.DeploymentID != "" {
		out.Lookup = "recorded"
		out.Matches = 1
	} else if r.PendingSubmission == role {
		out = observeRecoveryHistory(ctx, c, pin, r.ReleaseID, role)
	} else {
		return out
	}
	if out.DeploymentID == "" {
		return out
	}
	dep, err := c.GetDeployment(ctx, out.DeploymentID)
	if err != nil {
		out.Lookup = "native_unavailable"
		return out
	}
	if dep.ID != out.DeploymentID || dep.AppID != pin.Review.AppID || (dep.Scope != "" && dep.Scope != api.DefaultEnvScope) || role != "baseline" && dep.Reason != SubmissionReason(r.ReleaseID, role) {
		out.Lookup = "identity_mismatch"
		return out
	}
	switch dep.Status {
	case "live":
		out.Status = "live"
	case "failed", "cancelled", "superseded":
		out.Status = "failed"
	case "queued", "building", "uploading", "scanning":
		out.Status = "pending"
	}
	return out
}

func observeRecoveryHistory(ctx context.Context, c *api.Client, pin api.TaskToolReleaseApp, releaseID, role string) RecoveryDeployment {
	out := RecoveryDeployment{Role: role, Lookup: "history_incomplete", Status: "unknown"}
	cursor := ""
	cursors := map[string]bool{}
	matches := map[string]bool{}
	for page := 0; page < api.TaskToolRecoveryHistoryPagesMax; page++ {
		if cursors[cursor] {
			return out
		}
		cursors[cursor] = true
		rows, err := c.ListAppDeployments(ctx, pin.App, cursor, 200)
		if err != nil {
			return out
		}
		if len(rows.Items) > 200 {
			return out
		}
		for _, dep := range rows.Items {
			if dep.Reason != SubmissionReason(releaseID, role) {
				continue
			}
			if dep.AppID != pin.Review.AppID || uuid.Validate(dep.ID) != nil || (dep.Scope != "" && dep.Scope != api.DefaultEnvScope) {
				out.Lookup = "identity_mismatch"
				return out
			}
			matches[dep.ID] = true
		}
		out.Matches = len(matches)
		if out.Matches > 1 {
			out.Lookup = "ambiguous_history"
			return out
		}
		if rows.NextBefore == "" {
			out.HistoryComplete = true
			if len(matches) == 0 {
				out.Lookup = "missing_history_match"
				return out
			}
			for id := range matches {
				out.DeploymentID = id
			}
			out.Lookup = "unique_history_match"
			return out
		}
		cursor = rows.NextBefore
	}
	return out
}

func observeRecoveryApp(ctx context.Context, c *api.Client, record api.TaskToolReleaseIntentRecord, pin api.TaskToolReleaseApp, out *RecoveryReport) {
	review, err := c.GetAppReleaseReview(ctx, pin.App)
	if err != nil {
		for _, name := range []string{"identity", "configuration", "generation", "instances"} {
			recoveryFact(out, roleFact(pin, name), "unknown", "native_app_unavailable")
		}
		return
	}
	if review.AppID != pin.Review.AppID || review.Scope != api.DefaultEnvScope || review.EnvironmentID != "" || review.ProjectID != "" {
		recoveryFact(out, roleFact(pin, "identity"), "failed", "app_identity_changed")
		return
	}
	recoveryFact(out, roleFact(pin, "identity"), "passed", "native_app_identity_matches")
	state, code := "passed", "configuration_matches"
	if review.ConfigurationHash != pin.Review.ConfigurationHash {
		state, code = "failed", "configuration_changed"
	}
	recoveryFact(out, roleFact(pin, "configuration"), state, code)
	expected := pin.Review.LatestDeploymentID
	if pin.Role == "worker" {
		expected = recoveryDeploymentID(*out, "worker")
	}
	if record.Execution.Receipt != nil && pin.Role == "previous_worker" {
		expected = record.Execution.Receipt.PreviousDeployments[pin.App]
	}
	state, code = "unknown", "recorded_generation_unavailable"
	if expected != "" {
		state, code = "passed", "generation_matches"
		if review.LatestDeploymentID != expected {
			state, code = "failed", "generation_changed"
		}
	}
	recoveryFact(out, roleFact(pin, "generation"), state, code)
	if pin.Role == "web" {
		observeRecoveryTraffic(review, out)
	}
	instances, err := c.ListInstances(ctx, pin.App)
	if err != nil || len(instances) > api.TaskToolReleaseReceiptWorkerIDsMax {
		recoveryFact(out, roleFact(pin, "instances"), "unknown", "instance_inventory_unavailable")
		recoveryFact(out, roleFact(pin, "zero_live"), "unknown", "instance_inventory_unavailable")
		return
	}
	running, foreign, transitioning, unknown := 0, 0, 0, false
	for _, instance := range instances {
		if instance.AppID != pin.Review.AppID {
			unknown = true
			continue
		}
		switch instance.State {
		case "parked", "stopped", "failed", "exited":
			continue
		case "running":
			if expected == "" || instance.DeploymentID != expected {
				foreign++
			} else {
				running++
			}
		case "waking", "cold_booting", "parking", "stopping", "draining":
			transitioning++
		default:
			unknown = true
		}
	}
	state, code = "passed", "recorded_generation_running"
	if unknown {
		state, code = "unknown", "instance_inventory_inconsistent"
	} else if foreign > 0 {
		state, code = "failed", "other_generation_running"
	} else if transitioning > 0 {
		state, code = "unknown", "instances_transitioning"
	} else if running == 0 {
		state, code = "failed", "no_recorded_generation_running"
	}
	recoveryFact(out, roleFact(pin, "instances"), state, code)
	state, code = "failed", "live_instances_remain"
	if unknown || transitioning > 0 {
		state, code = "unknown", "instance_boundary_unknown"
	} else if running == 0 && foreign == 0 {
		state, code = "passed", "zero_live_observed"
	}
	recoveryFact(out, roleFact(pin, "zero_live"), state, code)
}

func observeRecoveryTraffic(review api.AppReleaseReview, out *RecoveryReport) {
	if out.Receipt == nil || !out.Receipt.ServingCaptured || review.ServingDeploymentIDs == nil || len(review.ServingDeploymentIDs) > 1 {
		recoveryFact(out, "web_traffic_expected", "unknown", "serving_evidence_unavailable")
		return
	}
	serving := ""
	if len(review.ServingDeploymentIDs) == 1 {
		serving = review.ServingDeploymentIDs[0]
	}
	baseline, candidate := out.Receipt.ServingDeploymentID, recoveryDeploymentID(*out, "web")
	state, code := "failed", "serving_generation_changed"
	if serving == baseline || serving != "" && serving == candidate {
		state, code = "passed", "recorded_serving_generation"
	}
	recoveryFact(out, "web_traffic_expected", state, code)
	state, code = "failed", "baseline_not_serving"
	if serving != "" && serving == baseline {
		state, code = "passed", "baseline_serving"
	}
	recoveryFact(out, "web_baseline_serving", state, code)
	state, code = "failed", "candidate_not_serving"
	if serving != "" && serving == candidate {
		state, code = "passed", "candidate_serving"
	}
	recoveryFact(out, "web_candidate_serving", state, code)
}

func observeRecoveryObserver(ctx context.Context, c *api.Client, record api.TaskToolReleaseIntentRecord, out *RecoveryReport, now time.Time) {
	destinationVerified := false
	for _, pin := range record.Intent.Apps {
		if pin.App == record.Intent.ObserverMetricApp {
			destinationVerified = recoveryPassed(*out, roleFact(pin, "identity")) && recoveryPassed(*out, roleFact(pin, "configuration"))
		}
	}
	if !destinationVerified {
		recoveryFact(out, "observer_fresh", "unknown", "observer_metric_destination_unverified")
		return
	}
	observer, ok := recoveryAppPin(record, "observer")
	if !ok || !recoveryPassed(*out, roleFact(observer, "identity")) || !recoveryPassed(*out, roleFact(observer, "configuration")) || !recoveryPassed(*out, roleFact(observer, "instances")) {
		recoveryFact(out, "observer_fresh", "unknown", "observer_generation_unavailable")
		return
	}
	metrics, err := c.GetAppCustomMetrics(ctx, record.Intent.ObserverMetricApp)
	if err != nil {
		recoveryFact(out, "observer_fresh", "unknown", "observer_metric_unavailable")
		return
	}
	state, code := "unknown", "observer_heartbeat_missing"
	matches := 0
	for _, metric := range metrics.Metrics {
		if metric.Name != "mcp_tasks_observer_heartbeat" {
			continue
		}
		matches++
		state, code = "failed", "observer_heartbeat_stale_or_unhealthy"
		if !metric.Stale && metric.Value == 1 && !metric.ObservedAt.IsZero() && !metric.ObservedAt.After(now) && metrics.FreshnessS > 0 && metrics.FreshnessS <= api.CustomMetricFreshnessSeconds && now.Sub(metric.ObservedAt) <= time.Duration(metrics.FreshnessS)*time.Second {
			state, code = "passed", "native_observer_heartbeat_fresh"
		}
	}
	if matches > 1 {
		state, code = "unknown", "observer_metric_ambiguous"
	}
	recoveryFact(out, "observer_fresh", state, code)
}

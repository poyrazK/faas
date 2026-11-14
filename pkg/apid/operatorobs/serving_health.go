package operatorobs

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"slices"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
)

const ServingHealthPath = "/v1/admin/obs/serving-health"
const ServingHealthScope = "expected_serving_probe_coverage"

const (
	probeConfigured  = "faas_serving_probe_inventory_configured"
	probePairs       = "faas_serving_probe_expected_pairs"
	probeExpected    = "faas_serving_probe_expected"
	probeBudget      = "faas_serving_probe_expected_first_byte_seconds"
	probeAge         = "faas_serving_probe_expected_max_age_seconds"
	probeState       = "faas_serving_probe:coverage_state"
	probeEvalBudget  = "faas_serving_probe:expected_budget"
	probeEvalAge     = "faas_serving_probe:expected_max_age"
	probeRun         = "faas_serving_probe:last_run"
	probeMetricLabel = "evidence_metric"
)

var servingProbeNodePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)
var servingProbeURLPattern = regexp.MustCompile(`^https://[A-Za-z0-9.-]+(:[0-9]{1,5})?(/[A-Za-z0-9._~!&()*+,;=:@%/?-]*)?$`)

// ServingProbeStates follows the deployed coverage rule's ordinal values.
func ServingProbeStates() []string {
	return []string{"healthy", "missing", "stale", "invalid_telemetry", "content_disabled", "budget_mismatch", "failing", "unknown"}
}

func ServingHealthLimitations() []string {
	return []string{"deployment_intent_not_active_configuration", "recording_rule_semantics_trusted", "no_direct_probe_executed", "url_values_redacted_to_sha256", "no_notification_or_external_watchdog_receipt"}
}

func ServingHealthReasons() []string {
	return []string{"", "prometheus_not_configured", "query_failed", "inventory_missing", "inventory_invalid", "inventory_stale", "inventory_not_configured", "zero_expected_probes", "probe_fault", "incomplete_evidence", "coverage_missing", "coverage_stale", "coverage_invalid", "policy_not_evaluated", "run_clock_unavailable", "result_missing", "result_stale", "invalid_telemetry", "content_check_disabled", "latency_budget_mismatch", "probe_failing"}
}

// ServingProbeNodeValid accepts the same stable names as the fleet policy.
func ServingProbeNodeValid(value string) bool {
	return len(value) <= api.ObsServingProbeNodeMaxBytes && servingProbeNodePattern.MatchString(value)
}

func servingProbeURLValid(value string) bool {
	if len(value) > api.ObsServingProbeURLMaxBytes || !servingProbeURLPattern.MatchString(value) {
		return false
	}
	parsed, err := url.Parse(value)
	return err == nil && parsed.Hostname() != "" && parsed.User == nil && parsed.Fragment == ""
}

type servingProbeKey struct{ metric, source, node, url string }
type servingProbeSamples struct{ values, times map[servingProbeKey]float64 }
type servingProbePolicy struct{ budget, age float64 }
type servingProbeIntent struct {
	status api.ObsServingProbeInventory
	pairs  map[servingProbeKey]servingProbePolicy
}

type servingProbeQuery struct {
	rows []promql.VectorSample
	err  error
}

func servingHealthQueries() []string {
	inventory := `label_replace({__name__=~"up|faas_serving_probe_(inventory_configured|expected_pairs|expected|expected_first_byte_seconds|expected_max_age_seconds)",job="node"},"evidence_metric","$1","__name__","(.+)")`
	// At most 1000 pair states and three policies/run clocks per node: each
	// vector remains below the shared 4096-series Prometheus transport bound.
	recordings := `label_replace({__name__=~"faas_serving_probe:(coverage_state|expected_budget|expected_max_age|last_run)"},"evidence_metric","$1","__name__","(.+)")`
	return []string{inventory, "timestamp(" + inventory + ")", recordings, "timestamp(" + recordings + ")"}
}

// ServingHealth reads one pinned snapshot under a shared deadline. It never
// contacts probe URLs or modifies the fleet, history, alerting or credentials.
func ServingHealth(ctx context.Context, client *promql.Client, now time.Time) api.ObsServingHealth {
	ctx, cancel := context.WithTimeout(ctx, api.ObsServingProbeBudget)
	defer cancel()
	out := api.ObsServingHealth{GeneratedAt: now.UTC(), Scope: ServingHealthScope, State: "partial", MaxAgeSeconds: int(api.ObsHealthFreshnessMaxAge.Seconds()), Probes: []api.ObsServingProbe{}, Limitations: ServingHealthLimitations()}
	if client == nil || client.BaseURL() == "" {
		out.Reason = "prometheus_not_configured"
		out.Inventory = api.ObsServingProbeInventory{State: "unavailable", Reason: out.Reason}
		return out
	}
	queries := servingHealthQueries()
	results := make([]servingProbeQuery, len(queries))
	var wg sync.WaitGroup
	for i, query := range queries {
		wg.Go(func() { results[i].rows, results[i].err = client.QueryVectorStrictAt(ctx, query, now) })
	}
	wg.Wait()
	inventory, err := servingProbeSnapshot(results[0], results[1], false)
	if err != nil {
		out.Reason = "query_failed"
		out.Inventory = api.ObsServingProbeInventory{State: "unavailable", Reason: out.Reason}
		return out
	}
	intent := inventory.intent(now)
	out.Inventory = intent.status
	if intent.status.State != "healthy" || intent.status.Configured == nil || !*intent.status.Configured {
		out.Reason = intent.status.Reason
		if intent.status.State == "healthy" {
			out.Reason = "inventory_not_configured"
		}
		return out
	}
	recordings, err := servingProbeSnapshot(results[2], results[3], true)
	out.Probes = servingProbeAssess(intent, recordings, err, now)
	counts := ServingProbeCounts(out.Probes)
	out.Counts = &counts
	out.State, out.Reason = ServingProbeOverall(counts)
	return out
}

func servingProbeSnapshot(values, times servingProbeQuery, recording bool) (servingProbeSamples, error) {
	out := servingProbeSamples{}
	var err error
	if values.err != nil || times.err != nil {
		return out, errors.New("serving evidence query failed")
	}
	out.values, err = servingProbeIndex(values.rows, recording)
	if err != nil {
		return out, err
	}
	out.times, err = servingProbeIndex(times.rows, recording)
	return out, err
}

func servingProbeIndex(rows []promql.VectorSample, recording bool) (map[servingProbeKey]float64, error) {
	out := map[servingProbeKey]float64{}
	for _, row := range rows {
		key, valid := servingProbeIdentity(row.Labels, recording)
		if !valid || math.IsNaN(row.Value) || math.IsInf(row.Value, 0) {
			return nil, errors.New("invalid serving evidence")
		}
		if _, exists := out[key]; exists {
			return nil, errors.New("duplicate serving evidence")
		}
		out[key] = row.Value
	}
	return out, nil
}

func servingProbeIdentity(labels map[string]string, recording bool) (servingProbeKey, bool) {
	key := servingProbeKey{metric: labels[probeMetricLabel], node: labels["probe_node"], url: labels["url"]}
	if labels["__name__"] != "" && labels["__name__"] != key.metric {
		return key, false
	}
	metrics := []string{probeConfigured, probePairs, probeExpected, probeBudget, probeAge, "up"}
	if recording {
		metrics = []string{probeState, probeEvalBudget, probeEvalAge, probeRun}
		for name := range labels {
			if !slices.Contains([]string{"__name__", probeMetricLabel, "probe_node", "url"}, name) {
				return key, false
			}
		}
	} else {
		identity := map[string]string{}
		for name, value := range labels {
			if name != probeMetricLabel && name != "__name__" && name != "probe_node" && name != "url" {
				identity[name] = value
			}
		}
		var valid bool
		key.source, valid = inputSeriesKey(identity, false)
		if !valid || labels["job"] != "node" {
			return key, false
		}
	}
	if !slices.Contains(metrics, key.metric) {
		return key, false
	}
	nodeMetric := !slices.Contains([]string{probeConfigured, probePairs, "up"}, key.metric)
	pairMetric := key.metric == probeExpected || key.metric == probeState
	if nodeMetric != (key.node != "") || nodeMetric && !ServingProbeNodeValid(key.node) || pairMetric != (key.url != "") || pairMetric && !servingProbeURLValid(key.url) {
		return key, false
	}
	return key, true
}

func servingProbeURLID(value string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(value))) }

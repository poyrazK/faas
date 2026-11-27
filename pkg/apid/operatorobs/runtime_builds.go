package operatorobs

import (
	"context"
	"net/url"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
	"github.com/onebox-faas/faas/pkg/state"
)

const RuntimeBuildsPath = "/v1/admin/obs/runtime-builds"
const RuntimeBuildsScope = "selected_critical_daemon_builds"

var runtimeRevisionPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
var runtimeVersionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.+\-]*$`)

type RuntimeBuildSelection struct {
	diagnostics      *RuntimeDiagnostics
	role, release    string
	after, compareAt time.Time
}

func RuntimeBuildsSelection(raw string, forceQuery bool, now time.Time) (RuntimeBuildSelection, error) {
	q, err := url.ParseQuery(raw)
	if err != nil || forceQuery {
		return RuntimeBuildSelection{}, state.ErrInvalidArgument
	}
	for key, values := range q {
		if (key != "expected_role" && key != "release" && key != "after" && key != "compare_at" && key != "diagnostics") || len(values) != 1 || values[0] == "" {
			return RuntimeBuildSelection{}, state.ErrInvalidArgument
		}
	}
	after, err := time.Parse(time.RFC3339Nano, q.Get("after"))
	role := q.Get("expected_role")
	if err != nil || after.IsZero() || after.After(now.Add(api.ObsHealthClockSkewTolerance)) || !runtimeRevisionPattern.MatchString(q.Get("release")) || role != "" && !RuntimeBuildRoleValid(role) {
		return RuntimeBuildSelection{}, state.ErrInvalidArgument
	}
	var compareAt time.Time
	if q.Has("compare_at") {
		compareAt, err = time.Parse(time.RFC3339Nano, q.Get("compare_at"))
		if err != nil || compareAt.IsZero() || !compareAt.Before(now) || compareAt.Before(now.Add(-api.ObsRuntimeProcessLookback)) || compareAt.Before(after.Add(-api.ObsHealthClockSkewTolerance)) || role == "" {
			return RuntimeBuildSelection{}, state.ErrInvalidArgument
		}
	}
	var diagnostics *RuntimeDiagnostics
	if q.Has("diagnostics") {
		diagnostics, err = ParseRuntimeDiagnostics(role, q.Get("diagnostics"))
		if err != nil {
			return RuntimeBuildSelection{}, state.ErrInvalidArgument
		}
	}
	return RuntimeBuildSelection{role: role, release: q.Get("release"), after: after.UTC(), compareAt: compareAt.UTC(), diagnostics: diagnostics}, nil
}

type RuntimeBuildRegistry interface {
	ListComputeNodes(context.Context, bool) ([]state.ComputeNode, error)
}

func RuntimeBuildLimitations() []string {
	return []string{"advisory_not_deployment_qualification", "selected_critical_jobs_not_complete_fleet", "explicit_role_not_deployment_intent", "static_targets_are_scrape_inventory_with_minimum_one", "compute_membership_from_active_configured_registry", "sampled_metrics_not_binary_attestation", "target_addresses_and_raw_labels_excluded", "counts_cover_full_inspected_set_preview_bounded"}
}

// RuntimeBuilds never sends probes or changes deployment/notification state.
// All queries use original timestamps and one pinned evaluation time.
func RuntimeBuilds(ctx context.Context, client *promql.Client, registry RuntimeBuildRegistry, selected RuntimeBuildSelection, now time.Time) api.ObsRuntimeBuilds {
	ctx, cancel := context.WithTimeout(ctx, runtimeCollectionBudget)
	defer cancel()
	out, _ := collectRuntimeBuilds(ctx, client, registry, selected, now)
	return out
}

func collectRuntimeBuilds(ctx context.Context, client *promql.Client, registry RuntimeBuildRegistry, selected RuntimeBuildSelection, now time.Time) (api.ObsRuntimeBuilds, []runtimeBuildJobResult) {
	out := api.ObsRuntimeBuilds{GeneratedAt: now.UTC(), Scope: RuntimeBuildsScope, ExpectedRole: selected.role, RequestedRelease: selected.release, EvidenceAfter: selected.after, State: "unknown", Reason: "expected_role_not_selected", MaxAgeSeconds: int(api.ObsInputHealthMaxAge.Seconds()), Jobs: []api.ObsRuntimeBuildJob{}, Items: []api.ObsRuntimeBuildItem{}, Limitations: RuntimeBuildLimitations()}
	specs := runtimeBuildJobSpecs(selected.role)
	if len(specs) == 0 || len(specs) > api.ObsRuntimeBuildJobsMax {
		return out, nil
	}
	nodes, inventoryOK := runtimeBuildNodes(ctx, registry, selected.role)
	coreStarted := time.Now()
	results := make([]runtimeBuildJobResult, len(specs))
	var wg sync.WaitGroup
	for i, spec := range specs {
		wg.Go(func() { results[i] = readRuntimeBuildJob(ctx, client, spec, nodes, inventoryOK, selected, now) })
	}
	wg.Wait()
	aggregateRuntimeReadiness(&out, results)
	aggregateRuntimeProcess(&out, results, selected)
	revisions := map[string]bool{}
	incomplete := false
	for _, result := range results {
		out.Jobs = append(out.Jobs, result.job)
		out.Counts.Expected += result.job.Expected
		incomplete = incomplete || result.job.State != "available"
		for _, item := range result.items {
			if item.TargetID != "" {
				out.Counts.Observed++
			}
			if item.Unexpected {
				out.Counts.Unexpected++
			}
			switch item.State {
			case "matching":
				out.Counts.Matching++
				revisions[item.GitSHA] = true
			case "mismatched":
				out.Counts.Mismatched++
				revisions[item.GitSHA] = true
			case "missing":
				out.Counts.Missing++
			case "stale":
				out.Counts.Stale++
			default:
				out.Counts.Unavailable++
			}
			if len(out.Items) < api.ObsRuntimeBuildPreviewMax {
				out.Items = append(out.Items, item)
			} else {
				out.OmittedItems++
			}
		}
	}
	out.Counts.DistinctRevisions = len(revisions)
	out.State, out.Reason = runtimeBuildState(out.Counts, incomplete)
	runtimeObservation(ctx).observePhase(ctx, runtimePhaseCoreEvidence, coreStarted)
	return out, results
}

func runtimeBuildState(c api.ObsRuntimeBuildCounts, incomplete bool) (string, string) {
	switch {
	case c.DistinctRevisions > 1:
		return "mixed", "multiple_observed_revisions"
	case c.Mismatched > 0:
		return "mismatched", "requested_revision_mismatch"
	case incomplete || c.Missing+c.Stale+c.Unavailable+c.Unexpected > 0:
		return "incomplete", "incomplete_identity_evidence"
	case c.Matching > 0:
		return "matching", ""
	default:
		return "unknown", "identity_unavailable"
	}
}

func runtimeBuildNodes(ctx context.Context, registry RuntimeBuildRegistry, role string) ([]state.ComputeNode, bool) {
	if role != "control-plane-v1" && role != "control-plane-v2" {
		return nil, true
	}
	observation := runtimeObservation(ctx)
	started := time.Now()
	outcome := "success"
	defer func() { observation.recordInventory(ctx, started, outcome) }()
	if registry == nil {
		outcome = "not_configured"
		if ctxErr := ctx.Err(); ctxErr != nil {
			outcome = runtimeReadOutcome(ctxErr)
		}
		return nil, false
	}
	nodes, err := registry.ListComputeNodes(ctx, false)
	if ctxErr := ctx.Err(); ctxErr != nil {
		outcome = runtimeReadOutcome(ctxErr)
		return nil, false
	}
	if err != nil {
		outcome = runtimeReadOutcome(err)
		return nil, false
	}
	out := []state.ComputeNode{}
	names, ids := map[string]bool{}, map[string]bool{}
	for _, node := range nodes {
		if !node.Active || node.GatewayTargetURL == nil {
			continue
		}
		if !ServingProbeNodeValid(node.Name) || node.ID == "" || names[node.Name] || ids[node.ID] {
			outcome = "invalid_inventory"
			return nil, false
		}
		names[node.Name], ids[node.ID] = true, true
		out = append(out, node)
		if len(out) > api.ObsRuntimeBuildNodesMax {
			outcome = "invalid_inventory"
			return nil, false
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, true
}

package operatorobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type runtimeRegistry struct {
	nodes []state.ComputeNode
	err   error
}

func (r runtimeRegistry) ListComputeNodes(context.Context, bool) ([]state.ComputeNode, error) {
	return r.nodes, r.err
}

func runtimeFixture(role string, now time.Time, n int) (inputFixture, RuntimeBuildSelection, runtimeRegistry) {
	f := inputFixture{}
	selected := RuntimeBuildSelection{role: role, release: strings.Repeat("a", 40), after: now.Add(-time.Minute)}
	registry := runtimeRegistry{}
	private := "tcp://private-host:8081"
	for i := range n {
		registry.nodes = append(registry.nodes, state.ComputeNode{ID: fmt.Sprintf("id-%d", i), Name: fmt.Sprintf("node-%04d", i), Active: true, GatewayTargetURL: &private})
	}
	for _, spec := range runtimeBuildJobSpecs(role) {
		count := 1
		if spec.discovery {
			count = n
		}
		up := fmt.Sprintf(`up{job=%q}`, spec.job)
		build := fmt.Sprintf(`%s{job=%q,daemon=%q}`, spec.metric, spec.job, spec.daemon)
		for _, q := range []string{up, "timestamp(" + up + ")", build, "timestamp(" + build + ")"} {
			f[q] = []inputFixtureRow{}
		}
		for i := range count {
			for _, q := range []string{up, "timestamp(" + up + ")", build, "timestamp(" + build + ")"} {
				value := float64(1)
				if strings.HasPrefix(q, "timestamp(") {
					value = float64(now.Unix())
				}
				row := inputRow(spec.job, fmt.Sprintf("private-host-%d:9100", i), "", value)
				if spec.discovery {
					row.Metric["node"], row.Metric["node_id"] = registry.nodes[i].Name, registry.nodes[i].ID
				}
				if strings.Contains(q, "build_info") {
					row.Metric["daemon"], row.Metric["git_sha"], row.Metric["version"], row.Metric["build_time"] = spec.daemon, selected.release, "v1.2.3", "private-build-time"
				}
				f[q] = append(f[q], row)
			}
		}
	}
	addRuntimeReadinessFixture(f, role, now)
	addRuntimeProcessFixture(f, role, now)
	addRuntimeBuilderFixture(f, now)
	addRuntimeHandoffFixture(f, now)
	addRuntimePreparationFixture(f, now)
	addRuntimeSnapshotPrimeFixture(f, now)
	addRuntimePostCaptureFixture(f, now)
	addRuntimeBuildRecoveryFixture(f, now)
	return f, selected, registry
}

func runtimeBuildQuery() string { return `apid_daemon_build_info{job="apid",daemon="apid"}` }

func TestRuntimeBuildEvidenceStatesAndRedaction(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, tc := range []struct {
		name, state, reason string
		change              func(inputFixture)
	}{
		{"matching", "matching", "", func(inputFixture) {}},
		{"mixed", "mixed", "requested_revision_mismatch", func(f inputFixture) {
			for _, q := range []string{runtimeBuildQuery(), "timestamp(" + runtimeBuildQuery() + ")"} {
				f[q][0].Metric["git_sha"] = strings.Repeat("b", 40)
			}
		}},
		{"old_server", "incomplete", "build_info_missing", func(f inputFixture) { f[runtimeBuildQuery()], f["timestamp("+runtimeBuildQuery()+")"] = nil, nil }},
		{"stale", "incomplete", "build_info_stale", func(f inputFixture) {
			f["timestamp("+runtimeBuildQuery()+")"][0].Value[1] = fmt.Sprint(now.Add(-3 * time.Minute).Unix())
		}},
		{"boundary", "incomplete", "build_info_before_boundary", func(f inputFixture) {
			f["timestamp("+runtimeBuildQuery()+")"][0].Value[1] = fmt.Sprint(now.Add(-100 * time.Second).Unix())
		}},
		{"unknown_revision", "incomplete", "revision_unavailable", func(f inputFixture) {
			for _, q := range []string{runtimeBuildQuery(), "timestamp(" + runtimeBuildQuery() + ")"} {
				f[q][0].Metric["git_sha"] = "private-untrusted-value"
			}
		}},
		{"conflict", "incomplete", "conflicting_build_info", func(f inputFixture) {
			for _, q := range []string{runtimeBuildQuery(), "timestamp(" + runtimeBuildQuery() + ")"} {
				row := inputRow("apid", "private-host-0:9100", "apid", 1)
				if strings.HasPrefix(q, "timestamp(") {
					row.Value[1] = fmt.Sprint(now.Unix())
				}
				row.Metric["git_sha"] = strings.Repeat("b", 40)
				f[q] = append(f[q], row)
			}
		}},
		{"down", "incomplete", "scrape_down", func(f inputFixture) { f[`up{job="apid"}`][0].Value[1] = "0" }},
		{"future", "incomplete", "invalid_sample_time", func(f inputFixture) {
			f["timestamp("+runtimeBuildQuery()+")"][0].Value[1] = fmt.Sprint(now.Add(time.Hour).Unix())
		}},
		{"missing_target", "incomplete", "expected_target_missing", func(f inputFixture) {
			for q := range f {
				if strings.Contains(q, `job="apid"`) {
					f[q] = nil
				}
			}
		}},
		{"orphan_clock", "incomplete", "invalid_evidence", func(f inputFixture) { f["timestamp("+runtimeBuildQuery()+")"][0].Metric["other"] = "secret" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, selected, registry := runtimeFixture("single-box-v1", now, 0)
			tc.change(f)
			out := RuntimeBuilds(t.Context(), inputFixtureClient(t, f, now), registry, selected, now)
			if out.State != tc.state || len(out.Items) != 4 || out.Items[0].Reason != tc.reason || !RuntimeBuildsValid(out) {
				t.Fatalf("invalid assessment: %+v", out)
			}
			body, _ := json.Marshal(out)
			for _, secret := range []string{"private-host", "private-build-time", "private-untrusted-value", "secret"} {
				if strings.Contains(string(body), secret) {
					t.Fatalf("leaked %s", secret)
				}
			}
		})
	}
}

func TestRuntimeBuildRegistryIdentityAndFullSetCounts(t *testing.T) {
	lengthenRuntimeCollectionBudget(t)
	now := time.Now().UTC().Truncate(time.Second)
	f, selected, registry := runtimeFixture("control-plane-v1", now, 40)
	// Same target count, wrong registry member: both extra and missing survive.
	for q := range f {
		if strings.Contains(q, `job="vmmd"`) {
			f[q][39].Metric["node"], f[q][39].Metric["node_id"] = "replacement", "replacement-id"
		}
	}
	// A mismatch outside the fleet preview still changes the summary.
	for q := range f {
		if strings.Contains(q, `job="vmmd"`) && strings.Contains(q, "build_info") {
			f[q][38].Metric["git_sha"] = strings.Repeat("b", 40)
		}
	}
	out := RuntimeBuilds(t.Context(), inputFixtureClient(t, f, now), registry, selected, now)
	if !RuntimeBuildsValid(out) || out.State != "mixed" || out.Counts.Expected != 123 || out.Counts.Observed != 123 || out.Counts.Mismatched != 1 || out.Counts.Missing != 1 || out.Counts.Unexpected != 1 || out.OmittedItems != 92 || len(out.Items) != 32 {
		t.Fatalf("lost full inventory assessment: %+v", out)
	}
	body, _ := json.MarshalIndent(out, "", "  ")
	if len(body) >= api.ObsRuntimeBuildResponseMaxBytes {
		t.Fatalf("unbounded response: %d", len(body))
	}
	registry.err = errors.New("private database failure")
	out = RuntimeBuilds(t.Context(), inputFixtureClient(t, f, now), registry, selected, now)
	if !RuntimeBuildsValid(out) || out.State != "incomplete" || out.Jobs[3].Reason != "inventory_unavailable" {
		t.Fatalf("inventory failure: %+v", out)
	}
}

func TestRuntimeBuildSelectionAndUnselectedRole(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	query := "release=" + strings.Repeat("a", 40) + "&after=" + now.Format(time.RFC3339)
	for _, suffix := range []string{"&expected_role=", "&expected_role=bad", "&after=x", "&release=x", "&url=private", "&expected_role=single-box-v1&expected_role=control-plane-v1", "&limit=1"} {
		if _, err := RuntimeBuildsSelection(query+suffix, false, now); err == nil {
			t.Fatalf("accepted %s", suffix)
		}
	}
	selected, err := RuntimeBuildsSelection(query, false, now)
	if err != nil {
		t.Fatal(err)
	}
	out := RuntimeBuilds(t.Context(), nil, nil, selected, now)
	if !RuntimeBuildsValid(out) || out.Reason != "expected_role_not_selected" {
		t.Fatalf("unselected: %+v", out)
	}
	selected.role = "single-box-v1"
	out = RuntimeBuilds(t.Context(), nil, nil, selected, now)
	if !RuntimeBuildsValid(out) || out.Counts.Unavailable != 4 {
		t.Fatalf("no source: %+v", out)
	}
}

func TestRuntimeBuildRegistryZeroAndConflictingTargets(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	f, selected, registry := runtimeFixture("control-plane-v1", now, 0)
	out := RuntimeBuilds(t.Context(), inputFixtureClient(t, f, now), registry, selected, now)
	if !RuntimeBuildsValid(out) || out.State != "matching" || out.Counts.Expected != 3 || out.Counts.Observed != 3 {
		t.Fatalf("zero registry is not missing data: %+v", out)
	}
	f, selected, registry = runtimeFixture("control-plane-v1", now, 1)
	for q, rows := range f {
		if !strings.Contains(q, `job="vmmd"`) {
			continue
		}
		row := rows[0]
		labels := make(map[string]string, len(row.Metric))
		for k, v := range row.Metric {
			labels[k] = v
		}
		labels["instance"] = "another-private-target:9104"
		row.Metric = labels
		f[q] = append(rows, row)
	}
	out = RuntimeBuilds(t.Context(), inputFixtureClient(t, f, now), registry, selected, now)
	if !RuntimeBuildsValid(out) || out.State != "incomplete" || out.Counts.Unavailable != 2 || out.Counts.Matching != 5 {
		t.Fatalf("duplicate target chose a revision: %+v", out)
	}
	registry.nodes = append(registry.nodes, registry.nodes[0])
	out = RuntimeBuilds(t.Context(), inputFixtureClient(t, f, now), registry, selected, now)
	if !RuntimeBuildsValid(out) || out.State != "incomplete" || out.Jobs[3].Reason != "inventory_unavailable" {
		t.Fatal("duplicate registry was accepted")
	}
}

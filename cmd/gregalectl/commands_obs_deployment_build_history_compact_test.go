package main

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func combinedBuildHistoryCurrent(t *testing.T, at time.Time, role, state string) *obsDeploymentRuntimeBuilds {
	t.Helper()
	base, results := deploymentResultFixture(t, at, role, state)
	_, operations := deploymentOperationFixture(t, at, role, state)
	_, cronOutcomes := deploymentCronOutcomeFixture(t, at, role, state)
	_, cronTiming := deploymentCronTimingFixture(t, at, role, state)
	_, httpDelivery := deploymentHTTPDeliveryFixture(t, at, role, state)
	_, delayedDispatch := deploymentDelayedDispatchFixture(t, at, role, state)
	return &obsDeploymentRuntimeBuilds{Advisory: true, State: base.State, Reason: base.Reason, Evidence: &base,
		LoopHealth: deploymentLoopFixture(base, "no_reported_stall"), BuilderHealth: deploymentBuilderFixture(base, "waiting"),
		HandoffHealth: deploymentHandoffFixture(base, "empty"), PreparationHealth: deploymentPreparationFixture(base, "empty"),
		SnapshotPrimeHealth: deploymentSnapshotPrimeFixture(base, "empty"), PostCaptureHealth: deploymentPostCaptureFixture(base, "empty"),
		BuildRecoveryHealth: deploymentBuildRecoveryFixture(base, "empty"), AppTaskHealth: deploymentAppTaskFixture(base, "empty"),
		CommandCronHealth: deploymentCommandCronFixture(base, "empty"), CommandCronOutcomes: cronOutcomes, CommandCronTiming: cronTiming, HTTPCronDelivery: httpDelivery, DelayedTaskDispatch: delayedDispatch, AppTaskResults: results, AppTaskOperations: operations}
}

func TestBuildHistoryCombinedProfilesRetainLatestAndTransport(t *testing.T) {
	for _, role := range []string{"single-box-v2", "control-plane-v2"} {
		t.Run(role, func(t *testing.T) {
			first := time.Now().UTC().Add(-100 * time.Second).Truncate(time.Second)
			r := newObsDeploymentBuildRecorder()
			var out *obsDeploymentRuntimeBuilds
			for i := range 80 {
				at := first.Add(time.Duration(i) * time.Second)
				state := "unchanged"
				if i%2 == 0 {
					state = "errors"
				}
				current := combinedBuildHistoryCurrent(t, at, role, state)
				current.Evidence.EvidenceAfter = first.Add(-time.Minute)
				if i == 0 {
					current.State, current.Reason = "mixed", "multiple_observed_revisions"
					current.Evidence.State, current.Evidence.Reason = current.State, current.Reason
					current.Evidence.Counts.Matching--
					current.Evidence.Counts.Mismatched++
					current.Evidence.Counts.DistinctRevisions = 2
					current.Evidence.Items[0].State, current.Evidence.Items[0].Reason, current.Evidence.Items[0].GitSHA = "mismatched", "requested_revision_mismatch", strings.Repeat("b", 40)
				}
				if i == 1 {
					current = buildHistoryCurrent(t, at, "unknown")
				}
				out = r.observe(current, at)
			}
			h := out.History
			if h.RetainedEvents == 0 || h.RetainedBytes > api.ObsPostDeployBuildHistoryBytesMax || h.TotalEvents != h.RetainedEvents+h.DroppedEvents || h.Events[len(h.Events)-1].Observation != 80 {
				t.Fatalf("latest observation lost: %+v", h)
			}
			latest := h.Events[len(h.Events)-1]
			if latest.CompactDetails == nil || len(latest.OmittedSections) != 0 || h.DetailOmittedEvents != 0 {
				t.Fatal("combined summaries were not retained losslessly")
			}
			expanded, err := expandObsDeploymentBuildEvent(latest)
			if err != nil || expanded.AppTaskResults == nil || !reflect.DeepEqual(*expanded.AppTaskResults, out.AppTaskResults.RuntimeAppTaskResultsSummary) || !reflect.DeepEqual(expanded.AppTaskHealth.Classes, out.AppTaskHealth.Classes) {
				t.Fatal("compaction changed diagnostic categories", err)
			}
			if obsDeploymentBuildHistoryBytes([]obsDeploymentBuildEvent{func() obsDeploymentBuildEvent { expanded.CompactDetails = nil; return expanded }()}) <= api.ObsPostDeployBuildHistoryBytesMax {
				t.Fatal("fixture did not reproduce oversized event")
			}
			t.Logf("%s: compact event=%d bytes; retained events=%d", role, obsDeploymentBuildHistoryBytes([]obsDeploymentBuildEvent{latest}), h.RetainedEvents)
			if out.Progress.FirstMismatchAt == nil || out.Progress.LastRecoveryAt == nil || !out.Progress.ObservedSourceUnavailable || !out.Progress.AppTaskResults.ObservedFailed || !out.Progress.AppTaskOperations.ObservedErrors {
				t.Fatal("compaction lost sticky fault/recovery progress")
			}
			var human bytes.Buffer
			writeObsDeploymentBuildHistory(&human, out)
			if !strings.Contains(human.String(), "app_task_results_state=unchanged") || !strings.Contains(human.String(), "details_compacted=true") {
				t.Fatal("human output lost compacted details")
			}
			// Returned snapshots own the compact metadata, just as they own full summaries.
			h.Events[len(h.Events)-1].CompactDetails.Data = "corrupt-caller-copy"
			out = r.snapshot(out)
			if _, err := expandObsDeploymentBuildEvent(out.History.Events[len(out.History.Events)-1]); err != nil {
				t.Fatal("compact history aliased caller", err)
			}
			assertBuildHistoryPythonTransport(t, out)
			body, _ := json.MarshalIndent(obsDeploymentReport{RuntimeBuilds: out}, "", "  ")
			fitted := fitObsDeploymentRuntimeBuildsWithin(obsDeploymentReport{RuntimeBuilds: out}, len(body)-100)
			if fitted.RuntimeBuilds.History.RetainedEvents == 0 || fitted.RuntimeBuilds.History.Events[len(fitted.RuntimeBuilds.History.Events)-1].Observation != 80 || len(out.AppTaskResults.Items) == 0 {
				t.Fatal("report fitting lost latest history or mutated source")
			}
		})
	}
}

func TestBuildHistoryCompactDetailsRejectMalformedStreams(t *testing.T) {
	base := newObsDeploymentBuildRecorder().observe(combinedBuildHistoryCurrent(t, time.Now().UTC(), "single-box-v2", "errors"), time.Now().UTC()).History.Events[0]
	pack := func(body string, trailing []byte) *obsDeploymentBuildCompactDetails {
		var b bytes.Buffer
		w := zlib.NewWriter(&b)
		_, _ = w.Write([]byte(body))
		_ = w.Close()
		return &obsDeploymentBuildCompactDetails{Encoding: obsDeploymentBuildDetailEncoding, DecodedBytes: len(body), Data: base64.StdEncoding.EncodeToString(append(b.Bytes(), trailing...))}
	}
	for _, fault := range []string{"encoding", "missing_data", "length", "ceiling", "base64", "newline", "truncated", "trailing", "duplicate", "unknown", "null_section", "duplicate_inline", "bomb"} {
		t.Run(fault, func(t *testing.T) {
			e := base
			e.CompactDetails = obsDeploymentEvidencePtr(base.CompactDetails)
			switch fault {
			case "encoding":
				e.CompactDetails.Encoding = "unknown"
			case "missing_data":
				e.CompactDetails.Data = ""
			case "length":
				e.CompactDetails.DecodedBytes--
			case "ceiling":
				e.CompactDetails.DecodedBytes = api.ObsPostDeployBuildHistoryDecodedBytesMax + 1
			case "base64":
				e.CompactDetails.Data = "!"
			case "newline":
				e.CompactDetails.Data += "\n"
			case "truncated":
				e.CompactDetails.Data = e.CompactDetails.Data[:len(e.CompactDetails.Data)-4]
			case "trailing":
				e.CompactDetails = pack(`{"app_task_health":{}}`, []byte("extra"))
			case "duplicate":
				e.CompactDetails = pack(`{"app_task_health":{"state":"empty","state":"nonempty"}}`, nil)
			case "unknown":
				e.CompactDetails = pack(`{"private_error":{"message":"private-provider-error"}}`, nil)
			case "null_section":
				e.CompactDetails = pack(`{"app_task_health":null}`, nil)
			case "duplicate_inline":
				h := combinedBuildHistoryCurrent(t, time.Now().UTC(), "single-box-v2", "errors").AppTaskHealth
				e.AppTaskHealth = &h.RuntimeAppTaskSummary
			case "bomb":
				e.CompactDetails = pack(strings.Repeat("x", api.ObsPostDeployBuildHistoryDecodedBytesMax+1), nil)
				e.CompactDetails.DecodedBytes = 1
			}
			if _, err := expandObsDeploymentBuildEvent(e); err == nil {
				t.Fatal("malformed compact event accepted")
			}
		})
	}
}

func assertBuildHistoryPythonTransport(t *testing.T, out *obsDeploymentRuntimeBuilds) {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal("python3 is required for the report adapter contract")
	}
	body, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	script := `import json,sys
import post_deployment_builds as builds
import post_deployment_build_history_codec as codec
r=json.load(sys.stdin);e=r['evidence'];p=r['progress']
saved=builds.project_runtime_builds(r,e['expected_role'],e['requested_release'],e['evidence_after'],p['observations'],p['first_observed_at'])
assert 'history' in saved, saved.get('history_unavailable_reason')
assert saved['progress']==p, [k for k in saved['progress'] if k.endswith('unavailable_reason')]
h=saved['history']; assert h['retained_events']>0 and h['retained_bytes']<=builds.HISTORY_BYTES_MAX
assert h['events'][-1]['observation']==p['observations']
expanded=codec.expand(h['events'][-1]); original=codec.expand(r['history']['events'][-1])
assert expanded==original
assert h['total_events']==h['retained_events']+h['dropped_events']+h['envelope_omitted_events']
assert len(json.dumps(saved,indent=2).encode()) < (1<<20)
print('adapter: compact history and progress preserved')`
	cmd := exec.Command(python, "-c", script)
	path, err := filepath.Abs(filepath.Join("..", "..", "scripts", "ci"))
	if err != nil {
		t.Fatal(err)
	}
	cmd.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1", "PYTHONPATH="+path)
	cmd.Stdin = bytes.NewReader(body)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("report adapter: %v\n%s", err, output)
	}
}

func TestBuildHistoryExplicitDetailOmissionAndBounds(t *testing.T) {
	now := time.Now().UTC().Add(-time.Second).Truncate(time.Second)
	r := newObsDeploymentBuildRecorder()
	current := combinedBuildHistoryCurrent(t, now, "control-plane-v2", "errors")
	out := r.observe(current, now)
	full, err := expandObsDeploymentBuildEvent(out.History.Events[0])
	if err != nil {
		t.Fatal(err)
	}
	full.CompactDetails = nil
	core := full
	core.removeDetails()
	for _, name := range obsDeploymentBuildDetailSections() {
		core.OmittedSections = append(core.OmittedSections, name)
	}
	// Exercise the fallback without increasing or relying on the real budget.
	fallback := compactObsDeploymentBuildEvent(full, obsDeploymentBuildHistoryBytes([]obsDeploymentBuildEvent{core}))
	if fallback.CompactDetails != nil || len(fallback.OmittedSections) != len(obsDeploymentBuildDetailSections()) || fallback.Counts == nil || len(fallback.Jobs) == 0 {
		t.Fatal("fallback did not preserve core and explicit omissions")
	}
	out.History.Events = []obsDeploymentBuildEvent{fallback}
	out.History.account()
	if !out.History.Truncated || out.History.DetailOmittedEvents != 1 || out.History.DroppedEvents != 0 {
		t.Fatal("detail omissions confused with dropped events")
	}
	assertBuildHistoryPythonTransport(t, out)
	var human bytes.Buffer
	writeObsDeploymentBuildHistory(&human, out)
	if !strings.Contains(human.String(), "omitted_sections=[") {
		t.Fatal("human output hid omitted detail")
	}
}

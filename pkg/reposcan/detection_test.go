package reposcan

import (
	"reflect"
	"testing"
)

// TestDetectorString_ClosedVocabulary pins the wire vocabulary
// surfaced as PlanWorkload.detected_by.detector (issue #742). The
// values are mirrored in the OpenAPI enum at
// api/openapi.yaml::PlanDetectedBy.detector — renaming one here
// without renaming it there breaks the wire contract silently, so
// this table is the tripwire.
func TestDetectorString_ClosedVocabulary(t *testing.T) {
	for _, tc := range []struct {
		det  detector
		want string
	}{
		{detCompose, "compose"},
		{detProcfile, "procfile"},
		{detK8s, "k8s"},
		{detRender, "render"},
		{detFly, "fly"},
		{detServerless, "serverless"},
		{detAppYaml, "app_yaml"},
		{detOther, "other"},
	} {
		if got := tc.det.String(); got != tc.want {
			t.Errorf("detector(%d).String() = %q, want %q", tc.det, got, tc.want)
		}
	}
}

// TestDetectorString_UnknownFallsBackToOther guards the default arm.
// A future detector added to the enum without a String() case must
// not surface "" on the wire — every workload came from some
// detector, and an empty string would make the DTO's
// "zero Detection means omit the key" rule ambiguous.
func TestDetectorString_UnknownFallsBackToOther(t *testing.T) {
	if got := detector(200).String(); got != "other" {
		t.Errorf("unknown detector String() = %q, want %q", got, "other")
	}
}

// TestMergeByKey_StampsWinningDetector pins the identity half of the
// trace: the detector that wins source/tier also wins
// DetectedBy.Detector, and its priority rides along.
func TestMergeByKey_StampsWinningDetector(t *testing.T) {
	out := mergeByKey([]workloadSeed{
		{tier: TierCompose, det: detCompose, source: "compose.yaml: api", name: "api"},
	})
	if len(out) != 1 {
		t.Fatalf("got %d workloads, want 1", len(out))
	}
	got := out[0].DetectedBy
	if got.Detector != "compose" {
		t.Errorf("Detector = %q, want compose", got.Detector)
	}
	if got.Priority != detCompose.priority() {
		t.Errorf("Priority = %d, want %d", got.Priority, detCompose.priority())
	}
	if len(got.MergedFrom) != 0 {
		t.Errorf("MergedFrom = %v, want empty for a single-seed workload", got.MergedFrom)
	}
}

// TestMergeByKey_RecordsMergedFrom is the issue's canonical
// explainability case: a Procfile `web` and a compose `web` at the
// same tier collapse into ONE workload. Compose wins identity on
// priority (80 > 75); the user's question is "where did my Procfile
// entry go?" and MergedFrom is the answer.
func TestMergeByKey_RecordsMergedFrom(t *testing.T) {
	out := mergeByKey([]workloadSeed{
		{tier: TierCompose, det: detProcfile, source: "Procfile: web", name: "web", class: ClassHTTP},
		{tier: TierCompose, det: detCompose, source: "compose.yaml: web", name: "web"},
	})
	if len(out) != 1 {
		t.Fatalf("got %d workloads, want 1 (same key must merge)", len(out))
	}
	w := out[0]
	if w.DetectedBy.Detector != "compose" {
		t.Errorf("Detector = %q, want compose (priority 80 beats procfile 75)", w.DetectedBy.Detector)
	}
	if want := []string{"procfile"}; !reflect.DeepEqual(w.DetectedBy.MergedFrom, want) {
		t.Errorf("MergedFrom = %v, want %v", w.DetectedBy.MergedFrom, want)
	}
	// The merge rule itself must be unchanged: procfile still fills
	// the class field compose left empty. The trace is additive
	// observability, not a behaviour change.
	if w.Class != ClassHTTP {
		t.Errorf("Class = %q, want http (per-field fill from the procfile seed)", w.Class)
	}
}

// TestMergeByKey_MergedFromDedupesSameDetector: two seeds from the
// SAME detector landing in one bucket is not a cross-detector merge
// (e.g. two compose services that normalise to one key). The winner
// is never listed in its own MergedFrom, and a repeat does not
// duplicate.
func TestMergeByKey_MergedFromDedupesSameDetector(t *testing.T) {
	out := mergeByKey([]workloadSeed{
		{tier: TierCompose, det: detCompose, source: "compose.yaml: api", name: "api"},
		{tier: TierCompose, det: detCompose, source: "compose.yaml: api-2", name: "api"},
		{tier: TierCompose, det: detProcfile, source: "Procfile: api", name: "api"},
		{tier: TierCompose, det: detProcfile, source: "Procfile: api-2", name: "api"},
	})
	if len(out) != 1 {
		t.Fatalf("got %d workloads, want 1", len(out))
	}
	if want := []string{"procfile"}; !reflect.DeepEqual(out[0].DetectedBy.MergedFrom, want) {
		t.Errorf("MergedFrom = %v, want %v (winner excluded, repeats deduped)",
			out[0].DetectedBy.MergedFrom, want)
	}
}

// TestMergeByKey_MergedFromIsDeterministic pins the ordering
// contract. mergeByKey sorts seeds before grouping, so MergedFrom
// follows descending detector priority regardless of input order.
// A non-deterministic trace would churn `gregale scan --json`
// output between identical runs.
func TestMergeByKey_MergedFromIsDeterministic(t *testing.T) {
	seeds := []workloadSeed{
		{tier: TierCompose, det: detAppYaml, source: "app.yaml: web", name: "web"},
		{tier: TierCompose, det: detCompose, source: "compose.yaml: web", name: "web"},
		{tier: TierCompose, det: detK8s, source: "k8s: web", name: "web"},
		{tier: TierCompose, det: detProcfile, source: "Procfile: web", name: "web"},
	}
	want := []string{"procfile", "k8s", "app_yaml"} // priorities 75, 70, 50

	first := mergeByKey(seeds)[0].DetectedBy.MergedFrom
	if !reflect.DeepEqual(first, want) {
		t.Fatalf("MergedFrom = %v, want %v (descending priority)", first, want)
	}
	// Reversed input must produce the identical trace.
	rev := make([]workloadSeed, len(seeds))
	for i, s := range seeds {
		rev[len(seeds)-1-i] = s
	}
	if second := mergeByKey(rev)[0].DetectedBy.MergedFrom; !reflect.DeepEqual(second, first) {
		t.Errorf("reversed input produced %v, want %v — trace must be input-order independent", second, first)
	}
}

// TestMergeByKey_SeparateKeysDoNotCrossContaminate: two workloads
// with different keys each keep their own detector. A bug that
// shared the accumulator across buckets would show up here.
func TestMergeByKey_SeparateKeysDoNotCrossContaminate(t *testing.T) {
	out := mergeByKey([]workloadSeed{
		{tier: TierCompose, det: detCompose, source: "compose.yaml: api", name: "api"},
		{tier: TierCompose, det: detProcfile, source: "Procfile: worker", name: "worker"},
	})
	if len(out) != 2 {
		t.Fatalf("got %d workloads, want 2 (distinct keys must not merge)", len(out))
	}
	byName := map[string]Detection{}
	for _, w := range out {
		byName[w.Name] = w.DetectedBy
	}
	if d := byName["api"]; d.Detector != "compose" || len(d.MergedFrom) != 0 {
		t.Errorf("api detection = %+v, want compose with no merges", d)
	}
	if d := byName["worker"]; d.Detector != "procfile" || len(d.MergedFrom) != 0 {
		t.Errorf("worker detection = %+v, want procfile with no merges", d)
	}
}

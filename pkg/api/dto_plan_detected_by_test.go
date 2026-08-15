package api

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestPlanWorkload_DetectedByOmittedWhenNil pins the additive
// guarantee for issue #742: a PlanWorkload with no detection trace
// must serialise byte-identically to the pre-#742 shape. If the
// field were a value type (or lost its omitempty), every existing
// `gregale scan --json` consumer would start seeing a
// `"detected_by":{"detector":"","priority":0}` object appear in
// output that used to lack the key.
func TestPlanWorkload_DetectedByOmittedWhenNil(t *testing.T) {
	b, err := json.Marshal(PlanWorkload{
		Name:    "api",
		RootDir: "",
		Command: []string{"node", "server.js"},
		Ports:   []int{8080},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(b), "detected_by") {
		t.Errorf("nil DetectedBy still emitted the key: %s", b)
	}
}

// TestPlanWorkload_DetectedByRoundTrip pins the populated shape:
// the three JSON keys, and merged_from dropping out when empty
// (the single-seed case, which is the common one).
func TestPlanWorkload_DetectedByRoundTrip(t *testing.T) {
	in := PlanWorkload{
		Name:    "web",
		Command: []string{},
		Ports:   []int{},
		DetectedBy: &PlanDetectedBy{
			Detector:   "compose",
			Priority:   80,
			MergedFrom: []string{"procfile"},
		},
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, want := range []string{
		`"detector":"compose"`,
		`"priority":80`,
		`"merged_from":["procfile"]`,
	} {
		if !strings.Contains(string(b), want) {
			t.Errorf("marshalled shape missing %s: %s", want, b)
		}
	}

	var out PlanWorkload
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.DetectedBy == nil {
		t.Fatal("DetectedBy lost on round-trip")
	}
	if out.DetectedBy.Detector != "compose" || out.DetectedBy.Priority != 80 {
		t.Errorf("round-trip mismatch: %+v", out.DetectedBy)
	}
	if len(out.DetectedBy.MergedFrom) != 1 || out.DetectedBy.MergedFrom[0] != "procfile" {
		t.Errorf("MergedFrom round-trip mismatch: %v", out.DetectedBy.MergedFrom)
	}
}

// TestPlanDetectedBy_MergedFromOmittedWhenEmpty: the single-seed
// case must not emit `"merged_from":null` or `[]`. Clients render
// "merged from N detectors" off presence, so an empty array would
// read as a merge that did not happen.
func TestPlanDetectedBy_MergedFromOmittedWhenEmpty(t *testing.T) {
	b, err := json.Marshal(PlanDetectedBy{Detector: "procfile", Priority: 75})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(b), "merged_from") {
		t.Errorf("empty MergedFrom still emitted the key: %s", b)
	}
	if !strings.Contains(string(b), `"detector":"procfile"`) {
		t.Errorf("detector missing: %s", b)
	}
}

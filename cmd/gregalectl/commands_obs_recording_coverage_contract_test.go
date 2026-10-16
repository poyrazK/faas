package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/obsrulecatalog"
)

func TestCmdObsRecordingCoverageRejectsInconsistentEvidence(t *testing.T) {
	base := recordingTestResponse(t)
	if !validObsRecordingCoverage(base, obsrulecatalog.CoreSetID) {
		t.Fatal("valid server recording evidence rejected")
	}
	for _, tc := range []struct {
		name   string
		change func(*api.ObsRecordingCoverage)
	}{
		{"wrong scope", func(r *api.ObsRecordingCoverage) { r.Scope = "selected_expected_alert_rules" }},
		{"missing dependencies", func(r *api.ObsRecordingCoverage) { r.Dependencies = nil }},
		{"unknown dependency revision", func(r *api.ObsRecordingCoverage) { r.Dependencies.AlertSetRevision = "private" }},
		{"false complete mapping", func(r *api.ObsRecordingCoverage) { r.Dependencies.State = "complete" }},
		{"unknown alerts became zero", func(r *api.ObsRecordingCoverage) { r.Dependencies.TotalAlerts = 0 }},
		{"dependency truncation", func(r *api.ObsRecordingCoverage) { r.Dependencies.UncertainAlerts.Truncated = false }},
		{"unsafe reference", func(r *api.ObsRecordingCoverage) {
			r.Dependencies.UncertainAlerts.Items[0].FileName = "/private/rules.yml"
		}},
		{"duplicate references", func(r *api.ObsRecordingCoverage) {
			r.Dependencies.UncertainAlerts.Items[1] = r.Dependencies.UncertainAlerts.Items[0]
		}},
		{"reference order", func(r *api.ObsRecordingCoverage) { a := r.Dependencies.UncertainAlerts.Items; a[0], a[1] = a[1], a[0] }},
		{"missing reference array", func(r *api.ObsRecordingCoverage) { r.Items[0].DependentAlerts.Items = nil }},
		{"dependent alerts count", func(r *api.ObsRecordingCoverage) {
			r.Items[0].DependentAlerts.TotalItems = r.Dependencies.TotalAlerts + 1
		}},
		{"false covered", func(r *api.ObsRecordingCoverage) { r.State = "covered"; r.Reason = "" }},
		{"inconsistent totals", func(r *api.ObsRecordingCoverage) { r.Totals.Missing-- }},
		{"nil items", func(r *api.ObsRecordingCoverage) { r.Items = nil }},
		{"open limitations", func(r *api.ObsRecordingCoverage) { r.Limitations = []string{"private"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, _ := json.Marshal(base)
			var copy api.ObsRecordingCoverage
			if err := json.Unmarshal(raw, &copy); err != nil {
				t.Fatal(err)
			}
			tc.change(&copy)
			if validObsRecordingCoverage(copy, obsrulecatalog.CoreSetID) {
				t.Fatal("inconsistent recording evidence accepted")
			}
		})
	}
	base.Totals.Missing--
	base.Totals.Changed++
	base.Totals.ObservedRules = 1
	r := &base.Items[0].Comparison
	r.State, r.Reason, r.ObservedCandidates = "changed", "definition_changed", 1
	r.ObservedFingerprint = strings.Repeat("1", 64)
	r.ChangedFields = []string{"expression"}
	if !validObsRecordingCoverage(base, obsrulecatalog.CoreSetID) {
		t.Fatal("valid changed recording rejected")
	}
	r.ChangedFields = []string{"for"}
	if validObsRecordingCoverage(base, obsrulecatalog.CoreSetID) {
		t.Fatal("alert timer change accepted for recording")
	}
}

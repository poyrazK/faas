package gitops_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/gitops"
)

func TestReportRedactionPreservesInternalValuesAndReviewedIdentity(t *testing.T) {
	plan := api.EnvironmentGitOpsPlan{Manager: "source", Revision: "revision", Hash: strings.Repeat("a", 64),
		ObservedVersion: 7, BlockingReasons: []string{"reviewed blocker"},
		Changes: []api.EnvironmentGitOpsChange{
			{Resource: "workload/api", Path: "variables/OWNED", Action: "update", Before: json.RawMessage(`"private-old"`), After: json.RawMessage(`"private-new"`)},
			{Resource: "workload/api", Path: "variables/UNMANAGED", Action: "retain_unmanaged", Before: json.RawMessage(`"private-console"`)},
		}}
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	runs := []api.EnvironmentGitOpsRun{{ID: "run", Plan: raw, Steps: json.RawMessage(`[]`)}}
	redacted, err := gitops.RedactRuns(runs)
	if err != nil {
		t.Fatal(err)
	}
	var public api.EnvironmentGitOpsPlan
	if err := json.Unmarshal(redacted[0].Plan, &public); err != nil {
		t.Fatal(err)
	}
	if public.Hash != plan.Hash || public.ObservedVersion != 7 || len(public.Changes) != 2 ||
		public.Changes[1].Action != "retain_unmanaged" || len(public.BlockingReasons) != 1 || strings.Contains(string(redacted[0].Plan), "private-") {
		t.Fatalf("redacted report lost reviewed metadata or disclosed values: %+v", public)
	}
	preview := gitops.RedactPlan(plan)
	if preview.Hash != plan.Hash || preview.Changes[0].Before != nil || preview.Changes[0].After != nil ||
		len(plan.Changes[0].Before) == 0 || !bytes.Equal(runs[0].Plan, raw) {
		t.Fatal("response redaction changed the internal plan or stored report")
	}
}

func TestReportRedactionFailsClosedOnMalformedHistoricalPlans(t *testing.T) {
	for _, raw := range []string{`private-not-json`, `[]`, `{"changes":"private"}`} {
		if runs, err := gitops.RedactRuns([]api.EnvironmentGitOpsRun{{Plan: json.RawMessage(raw)}}); err == nil || runs != nil {
			t.Fatal("malformed report returned an unredacted fallback", raw)
		}
	}
}

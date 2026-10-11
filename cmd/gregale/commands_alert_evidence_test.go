package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestAlertEvidenceWaitRejectsChangedOrClearedAcceptedEvidence(t *testing.T) {
	now := time.Now().UTC()
	for _, change := range []string{"counts", "threshold", "cutover", "window", "cleared", "valid completion"} {
		t.Run(change, func(t *testing.T) {
			pin := alertWaitFixture()
			pin.Historical = true
			pin.Service = false
			pin.ServiceRequestID = ""
			pin.RollbackOperationID = pin.ID
			pin.DeploymentEvidence = &api.AlertRollbackDeploymentEvidence{Version: 1, DeploymentID: pin.CandidateDeploymentID, Metric: "error_rate_pct", Comparison: "gt", Threshold: 1, WindowSpec: "5m", CutoverAt: now.Add(-5 * time.Minute), WindowStart: now.Add(-4 * time.Minute), WindowEnd: now.Add(-time.Minute), Requests: 20, ServerErrors: 2, MinimumRequests: 20, ErrorRatePct: 10, Status: "breached"}
			complete := pin
			copyEvidence := *pin.DeploymentEvidence
			complete.DeploymentEvidence = &copyEvidence
			complete.Status = "complete"
			complete.CompletedAt = &now
			complete.RollbackPhase = "complete"
			complete.RollbackRoutingAuditID = "2"
			complete.AuditID = "3"
			switch change {
			case "counts":
				complete.DeploymentEvidence.Requests++
			case "threshold":
				complete.DeploymentEvidence.Threshold++
			case "cutover":
				complete.DeploymentEvidence.CutoverAt = now
			case "window":
				complete.DeploymentEvidence.WindowEnd = now
			case "cleared":
				complete.DeploymentEvidence = nil
			}
			_, err := waitAlertRollback(t.Context(), &alertWaitFixtureClient{rows: []api.AlertRollback{complete}}, "app", pin, time.Millisecond)
			if change == "valid completion" && err != nil || change != "valid completion" && err == nil {
				t.Fatalf("wait %s: %v", change, err)
			}
		})
	}
}

func TestAlertEvidenceHumanOutput(t *testing.T) {
	var out bytes.Buffer
	old := osStdout
	osStdout = &out
	t.Cleanup(func() { osStdout = old })
	r := api.AlertRollback{Status: "blocked", Historical: true, DeploymentEvidence: &api.AlertRollbackDeploymentEvidence{Status: "insufficient", Code: "alert_rollback_evidence_insufficient", Requests: 3, MinimumRequests: 20, Threshold: 1, Comparison: "gt"}}
	if code := outputAlertRollback(r); code != 0 {
		t.Fatal(code)
	}
	for _, want := range []string{"deployment evidence: insufficient", "requests: 3 (minimum 20)", "error rate:", "alert_rollback_evidence_insufficient"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q in %s", want, out.String())
		}
	}
}

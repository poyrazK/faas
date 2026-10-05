package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPreviewEventIncludesWorkflowsWithoutStartingRuns(t *testing.T) {
	e := setup(t, api.PlanPro)
	appID := mustSeedApp(t, e, "preview-workflow")
	definitions := json.RawMessage(`[
 {"name":"paid","trigger":{"type":"event","source":"billing.*","event_type":"invoice.paid"},"steps":[{"name":"main","path":"/paid"}]},
 {"name":"large","trigger":{"type":"event","source":"billing.*","event_type":"invoice.paid","filter":{"data":{"amount":{"$gt":1000}}}},"steps":[{"name":"main","path":"/large"}]},
 {"name":"disabled","trigger":{"type":"event","source":"billing.*","event_type":"invoice.paid","enabled":false},"steps":[{"name":"main","path":"/disabled"}]}]`)
	deployment, err := e.store.CreateDeployment(context.Background(), state.Deployment{AppID: appID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc", Status: state.DeployLive, Workflows: definitions})
	if err != nil {
		t.Fatal(err)
	}
	rec := e.do(t, http.MethodPost, "/v1/events:preview", api.PreviewEventRequest{Source: "billing.stripe", Type: "invoice.paid", Data: json.RawMessage(`{"amount":150}`)}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out api.PreviewEventResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.CandidateCount != 2 || out.MatchedCount != 1 || out.FilterMismatchCount != 1 || len(out.Matches) != 1 || out.Matches[0].WorkflowName != "paid" || out.Matches[0].DeploymentID != deployment.ID {
		t.Fatalf("preview=%+v", out)
	}
	if _, total, err := e.store.ListWorkflowRuns(context.Background(), appID, state.ListWorkflowRunsOpts{Limit: 10}); err != nil || total != 0 {
		t.Fatalf("preview admitted runs: %d %v", total, err)
	}
	other, err := e.store.CreateAccount(context.Background(), "foreign-workflow-preview@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if rows, err := e.store.ListMatchingEventWorkflows(context.Background(), other.ID, "billing.stripe", "invoice.paid", "", 256); err != nil || len(rows) != 0 {
		t.Fatalf("foreign candidates=%+v err=%v", rows, err)
	}
}

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestWorkflowEventReplayPreviewAPIIsReadOnlyAndPagesCapturedRecipients(t *testing.T) {
	e := setup(t, api.PlanPro)
	appID := mustSeedApp(t, e, "workflow-replay-preview")
	definitions := json.RawMessage(`[{"name":"paid","trigger":{"type":"event","source":"billing.*","event_type":"invoice.paid","filter":{"data":{"amount":{"$gt":100}}}},"steps":[{"name":"main","path":"/paid"}]}]`)
	if _, err := e.store.CreateDeployment(context.Background(), state.Deployment{AppID: appID, Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:abc", Status: state.DeployLive, Workflows: definitions}); err != nil {
		t.Fatal(err)
	}
	for _, event := range []struct {
		id, source string
		amount     int
	}{
		{"match", "billing.stripe", 150},
		{"filtered", "billing.stripe", 50},
		{"not-captured", "shipping.us", 150},
	} {
		payload, err := json.Marshal(map[string]any{
			"specversion": "1.0", "id": event.id, "source": event.source, "type": "invoice.paid",
			"accountid": e.acct.ID, "datacontenttype": "application/json", "time": time.Now().UTC(),
			"data": map[string]any{"amount": event.amount, "private": "retained-payload-secret"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := e.store.AppendEvent(context.Background(), "apid", "event.published", &e.acct.ID, payload); err != nil {
			t.Fatal(err)
		}
	}
	from, until := time.Now().Add(-time.Hour), time.Now().Add(time.Hour)
	base := "/v1/apps/workflow-replay-preview/workflow-event-replay-preview?" + url.Values{
		"workflow_name": {"paid"}, "from": {from.Format(time.RFC3339Nano)}, "until": {until.Format(time.RFC3339Nano)}, "limit": {"2"},
	}.Encode()
	firstRec := e.do(t, http.MethodGet, base, nil, nil)
	var first api.WorkflowEventReplayPreviewResponse
	if firstRec.Code != http.StatusOK || json.Unmarshal(firstRec.Body.Bytes(), &first) != nil || firstRec.Header().Get("Cache-Control") != "no-store" ||
		first.ScannedCount != 2 || first.CapturedCount != 2 || first.MatchedCount != 1 || first.FilterMismatchCount != 1 ||
		first.PotentialAdmissionCount != 1 || len(first.Matches) != 1 || first.Matches[0].EventID != "match" || first.NextAfter == "" ||
		strings.Contains(firstRec.Body.String(), "retained-payload-secret") {
		t.Fatalf("first page=%d %s", firstRec.Code, firstRec.Body)
	}
	link, err := url.Parse(first.Matches[0].ReceiptURL)
	if err != nil || link.Path != "/v1/events/receipt" || link.Query().Get("source") != "billing.stripe" || link.Query().Get("id") != "match" {
		t.Fatalf("receipt link=%v err=%v", link, err)
	}
	secondURL, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	query := secondURL.Query()
	query.Set("after", first.NextAfter)
	secondRec := e.do(t, http.MethodGet, secondURL.Path+"?"+query.Encode(), nil, nil)
	var second api.WorkflowEventReplayPreviewResponse
	if secondRec.Code != http.StatusOK || json.Unmarshal(secondRec.Body.Bytes(), &second) != nil || second.ScannedCount != 1 ||
		second.NotCapturedCount != 1 || second.MatchedCount != 0 || len(second.Matches) != 0 || second.NextAfter != "" || !second.CutoffAt.Equal(first.CutoffAt) {
		t.Fatalf("second page=%d %s", secondRec.Code, secondRec.Body)
	}
	if runs, _, err := e.store.ListWorkflowRuns(context.Background(), appID, state.ListWorkflowRunsOpts{Limit: 10}); err != nil || len(runs) != 0 {
		t.Fatalf("preview started workflow runs: %+v %v", runs, err)
	}
	bad := e.do(t, http.MethodGet, "/v1/apps/workflow-replay-preview/workflow-event-replay-preview?workflow_name=%20&from="+url.QueryEscape(from.Format(time.RFC3339Nano))+"&until="+url.QueryEscape(until.Format(time.RFC3339Nano)), nil, nil)
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("blank workflow name=%d %s", bad.Code, bad.Body)
	}
}

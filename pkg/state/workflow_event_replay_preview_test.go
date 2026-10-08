package state

// adr: 714

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestWorkflowEventReplayPreviewIsReadOnlyBoundedAndDeduplicated(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		app, firstWork, claim := seedOwnedWorkflowRouting(t, store, 150)
		var firstEvent publishedEventIdentity
		if err := json.Unmarshal(firstWork.Payload, &firstEvent); err != nil {
			t.Fatal(err)
		}
		ctx := t.Context()
		publish := func(id, source string, amount int) {
			t.Helper()
			payload, err := json.Marshal(map[string]any{
				"specversion": "1.0", "id": id, "source": source, "type": "invoice.paid",
				"accountid": canonicalMemUUID(app.AccountID), "datacontenttype": "application/json",
				"time": time.Now().UTC(), "data": map[string]any{"amount": amount},
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.AppendEvent(ctx, "apid", "event.published", &app.AccountID, payload); err != nil {
				t.Fatal(err)
			}
		}
		publish("second-match", "billing.stripe", 200)
		publish("not-captured", "shipping.us", 200)

		preview := store.(WorkflowEventReplayPreviewStore)
		q := WorkflowEventReplayPreviewQuery{AppID: app.ID, WorkflowName: "paid", WorkflowEventReplayPreviewOptions: api.WorkflowEventReplayPreviewOptions{
			From: time.Now().UTC().Add(-time.Hour), Until: time.Now().UTC().Add(time.Hour), Limit: 1,
		}}
		readAll := func() (api.WorkflowEventReplayPreviewResponse, []api.WorkflowEventReplayPreviewMatch) {
			t.Helper()
			q.After = ""
			all := api.WorkflowEventReplayPreviewResponse{}
			matches := []api.WorkflowEventReplayPreviewMatch{}
			for page := 0; page < 4; page++ {
				result, err := preview.PreviewWorkflowEventReplay(ctx, app.AccountID, q)
				if err != nil {
					t.Fatal(err)
				}
				all.ScannedCount += result.ScannedCount
				all.CapturedCount += result.CapturedCount
				all.MatchedCount += result.MatchedCount
				all.FilterMismatchCount += result.FilterMismatchCount
				all.NotCapturedCount += result.NotCapturedCount
				all.UnknownRecipientCount += result.UnknownRecipientCount
				all.AlreadyAdmittedCount += result.AlreadyAdmittedCount
				all.PotentialAdmissionCount += result.PotentialAdmissionCount
				matches = append(matches, result.Matches...)
				if result.NextAfter == "" {
					return all, matches
				}
				q.After = result.NextAfter
			}
			t.Fatal("preview did not stop within bounded pages")
			return all, matches
		}

		before, matches := readAll()
		if before.ScannedCount != 3 || before.CapturedCount != 2 || before.MatchedCount != 2 || before.NotCapturedCount != 1 ||
			before.AlreadyAdmittedCount != 0 || before.PotentialAdmissionCount != 2 || len(matches) != 2 {
			t.Fatalf("before admission=%+v matches=%+v", before, matches)
		}
		for _, match := range matches {
			if match.OriginalRecipient != "captured" || !match.FilterMatched || match.AdmissionRecorded || match.WorkflowRunID != "" {
				t.Fatalf("unexpected potential admission: %+v", match)
			}
		}
		if runs, _, err := store.ListWorkflowRuns(ctx, app.ID, ListWorkflowRunsOpts{Limit: 10}); err != nil || len(runs) != 0 {
			t.Fatalf("preview created workflow runs: %+v %v", runs, err)
		}

		admitted, err := store.(EventWorkflowRecipientAdmissionStore).AdmitEventWorkflowRecipient(ctx, claim)
		if err != nil || !admitted.RunCreated || admitted.RunID == "" {
			t.Fatalf("admission=%+v err=%v", admitted, err)
		}
		after, matches := readAll()
		if after.AlreadyAdmittedCount != 1 || after.PotentialAdmissionCount != 1 || len(matches) != 2 {
			t.Fatalf("after admission=%+v matches=%+v", after, matches)
		}
		var admittedMatch *api.WorkflowEventReplayPreviewMatch
		for i := range matches {
			if matches[i].EventID == firstEvent.ID {
				admittedMatch = &matches[i]
			}
		}
		if admittedMatch == nil || !admittedMatch.AdmissionRecorded || admittedMatch.WorkflowRunID != admitted.RunID || admittedMatch.WorkflowRunStatus != WorkflowRunStatusPending {
			t.Fatalf("admitted match=%+v all=%+v", admittedMatch, matches)
		}

		if err := store.MarkWorkflowRunStatus(ctx, admitted.RunID, WorkflowRunStatusSucceeded, nil, nil); err != nil {
			t.Fatal(err)
		}
		if n, err := store.SweepExpiredWorkflowRuns(ctx, 0); err != nil || n != 1 {
			t.Fatalf("prune run=%d err=%v", n, err)
		}
		afterPrune, matches := readAll()
		if afterPrune.AlreadyAdmittedCount != 1 || afterPrune.PotentialAdmissionCount != 1 {
			t.Fatalf("pruned receipt lost dedupe state: %+v", afterPrune)
		}
		for _, match := range matches {
			if match.AdmissionRecorded && (match.WorkflowRunID != "" || match.WorkflowRunStatus != "") {
				t.Fatalf("pruned run still exposed: %+v", match)
			}
		}
	})
}

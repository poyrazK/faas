package state

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestAutomationRevisionCheckEvidence(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		app, _ := seedWorkflowSchedule(t, store, "allow")
		author := store.(AutomationStore)
		ctx := context.Background()
		draft := mutateForTest(t, author, app.ID, "receipt", "save", 0, automationDraft("receipt", "/v1", ""), false)
		var definition api.WorkflowSpec
		_ = json.Unmarshal(draft.Draft, &definition)
		raw, _ := json.Marshal(definition)
		evidence := &api.AutomationCheckEvidence{DefinitionHash: fmt.Sprintf("%x", sha256.Sum256(raw)), CheckedVersion: draft.Version, CheckedAt: time.Now().UTC(), CoveragePassed: true, Scenarios: []api.AutomationCheckScenario{{Name: "success", Passed: true, DefinitionValid: true, Complete: true}}, Exclusions: []api.AutomationCheckExclusion{}}
		bad := *evidence
		bad.DefinitionHash = "mismatch"
		if _, err := author.MutateAutomation(ctx, app.ID, "receipt", AutomationMutation{Action: "publish", ExpectedVersion: draft.Version, CheckEvidence: &bad}); !errors.Is(err, ErrAutomationInvalid) {
			t.Fatalf("mismatched evidence: %v", err)
		}
		if rows, total, err := author.ListAutomationRevisions(ctx, app.ID, "receipt", AutomationRevisionListOptions{Limit: 10}); err != nil || total != 0 || len(rows) != 0 {
			t.Fatalf("rejected publish wrote history: %v/%d/%v", rows, total, err)
		}
		published, err := author.MutateAutomation(ctx, app.ID, "receipt", AutomationMutation{Action: "publish", ExpectedVersion: draft.Version, CheckEvidence: evidence})
		if err != nil {
			t.Fatal(err)
		}
		expected, _ := json.Marshal(evidence)
		evidence.Scenarios[0].Name = "changed-after-publish"
		revision, err := author.GetAutomationRevision(ctx, app.ID, "receipt", published.PublishedVersion)
		if err != nil {
			t.Fatal(err)
		}
		var got, want any
		_ = json.Unmarshal(revision.CheckEvidence, &got)
		_ = json.Unmarshal(expected, &want)
		gotRaw, _ := json.Marshal(got)
		wantRaw, _ := json.Marshal(want)
		if string(gotRaw) != string(wantRaw) {
			t.Fatalf("evidence changed: %s != %s", gotRaw, wantRaw)
		}
		revision.CheckEvidence[0] = 'x'
		revision, err = author.GetAutomationRevision(ctx, app.ID, "receipt", published.PublishedVersion)
		if err != nil || !json.Valid(revision.CheckEvidence) {
			t.Fatal("revision evidence alias escaped", err)
		}
		restored := mutateForTest(t, author, app.ID, "receipt", "save", published.Version, revision.Definition, false)
		plain := mutateForTest(t, author, app.ID, "receipt", "publish", restored.Version, nil, false)
		revision, err = author.GetAutomationRevision(ctx, app.ID, "receipt", plain.PublishedVersion)
		if err != nil || len(revision.CheckEvidence) != 0 {
			t.Fatalf("plain republish inherited checks: %+v/%v", revision, err)
		}
	})
}

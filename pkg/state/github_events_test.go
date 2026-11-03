// adr: 20261027090010
package state

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// A gregale.github event reaches the account's workflows whose event trigger
// names the source, the way githubd's forwarded pull requests start triage.
func TestGitHubEventStartsWorkflowTrigger(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		ctx := t.Context()
		account, err := store.CreateAccount(ctx, uuid.NewString()+"@example.com", api.PlanHobby)
		if err != nil {
			t.Fatal(err)
		}
		app, err := store.CreateApp(ctx, App{AccountID: account.ID, Slug: "triage-" + uuid.NewString()[:8], Type: AppTypeApp, RAMMB: 256})
		if err != nil {
			t.Fatal(err)
		}
		deployment, err := store.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:abc",
			Status: DeployPending, Workflows: json.RawMessage(`[{"name":"triage","trigger":{"type":"event","source":"gregale.github",` +
				`"event_type":"pull_request.*","filter":{"data":{"pull_request":{"fork":false}}}},"steps":[{"name":"main","path":"/triage"}]}]`)})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
			t.Fatal(err)
		}
		if err := store.UpsertGitHubInstall(ctx, GitHubInstall{AccountID: account.ID, InstallationID: 4242,
			SealedToken: []byte("sealed"), TokenExpiresAt: time.Now().Add(time.Hour), AuditGithubLogin: "octocat"}); err != nil {
			t.Fatal(err)
		}
		events := store.(GitHubEventStore)
		if got, err := events.AccountIDByGitHubInstallation(ctx, 4242); err != nil || !sameMemUUID(got, account.ID) {
			t.Fatalf("AccountIDByGitHubInstallation() = %q, %v", got, err)
		}
		if _, err := events.AccountIDByGitHubInstallation(ctx, 4243); !errors.Is(err, ErrNotFound) {
			t.Fatalf("unknown installation = %v, want ErrNotFound", err)
		}

		payload, _ := json.Marshal(map[string]any{"specversion": "1.0", "id": uuid.NewString(), "source": api.GitHubEventSource,
			"type": "pull_request.opened", "accountid": canonicalMemUUID(account.ID), "datacontenttype": "application/json",
			"time": time.Now().UTC(), "data": map[string]any{"action": "opened", "installation_id": 4242,
				"repository": map[string]any{"full_name": "octo/api"}, "pull_request": map[string]any{"number": 7, "fork": false,
					"head": map[string]any{"sha": strings.Repeat("a", 40)}}}})
		accepted, err := events.AcceptPublishedEvent(ctx, "githubd", account.ID, payload)
		if err != nil || accepted.Duplicate {
			t.Fatalf("AcceptPublishedEvent() = %+v, %v", accepted, err)
		}
		if again, err := events.AcceptPublishedEvent(ctx, "githubd", account.ID, payload); err != nil || !again.Duplicate {
			t.Fatalf("repeat AcceptPublishedEvent() = %+v, %v", again, err)
		}
		work, err := store.(PublishedEventWorkStore).ClaimDuePublishedEvent(ctx, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = store.(PublishedEventWorkStore).FinishPublishedEvent(t.Context(), work.ID, work.ClaimToken, nil)
		})
		if work.StorageBytes != 0 {
			t.Fatalf("platform event charged %d bytes to the account", work.StorageBytes)
		}
		var workflow api.WorkflowSpec
		if len(work.RecipientSnapshot) != 1 || json.Unmarshal(work.RecipientSnapshot[0].Workflow, &workflow) != nil || workflow.Name != "triage" ||
			work.RecipientSnapshot[0].ID != workflowEventRecipientID(app.ID, "triage") {
			t.Fatalf("recipients = %+v", work.RecipientSnapshot)
		}
	})
}

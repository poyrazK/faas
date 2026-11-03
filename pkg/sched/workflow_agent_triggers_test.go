// adr: 20261027090010 — signed webhooks and GitHub pull requests start automations
// whose agent step admits a run from the triggering event.
package sched_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/eventcontract"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// deployAgentWorkflow deploys one workflow for a new Pro account's app.
func deployAgentWorkflow(t *testing.T, store state.Store, workflow string) state.App {
	t.Helper()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "agent-trigger-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc",
		Status: state.DeployPending, Workflows: json.RawMessage(`[` + workflow + `]`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
		t.Fatal(err)
	}
	return app
}

// admitPublishedWorkflow routes the one published event to its one workflow
// recipient and returns the run.
func admitPublishedWorkflow(t *testing.T, store state.Store) string {
	t.Helper()
	ctx := context.Background()
	work, err := store.(state.PublishedEventWorkStore).ClaimDuePublishedEvent(ctx, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(work.RecipientSnapshot) != 1 {
		t.Fatalf("recipients %+v", work.RecipientSnapshot)
	}
	runID, err := store.(state.EventWorkflowStore).AdmitEventWorkflow(ctx, work.ID, work.ClaimToken, work.RecipientSnapshot[0].ID)
	if err != nil || runID == "" {
		t.Fatalf("admit %q %v", runID, err)
	}
	return runID
}

func runAgentStep(t *testing.T, store state.Store, runID string) *fakeAgents {
	t.Helper()
	ctx := context.Background()
	agents := &fakeAgents{}
	orchestrator := sched.NewWorkflowOrchestrator(store, &recordingExecutor{}, nil, nil, nil).WithAgents(agents)
	if err := orchestrator.DispatchTick(ctx); err != nil {
		t.Fatal(err)
	}
	if err := orchestrator.AdvanceWorkflowRun(ctx, runID); err != nil {
		t.Fatal(err)
	}
	if len(agents.admissions) != 1 {
		t.Fatalf("admissions %d", len(agents.admissions))
	}
	if got := stepStatus(t, store, runID, "review"); got != state.WorkflowStepStatusAwaitingEvent {
		t.Fatalf("review %s", got)
	}
	return agents
}

func TestSignedWebhookStartsAgentStep(t *testing.T) {
	sched.GuardOrchestrationStores(t, func(t *testing.T, store state.Store) {
		ctx := context.Background()
		app := deployAgentWorkflow(t, store, `{"name":"review-order","steps":[{"name":"review","agent":"order-review",`+
			`"input":{"inputs":{"order":"{{input.data.order_id}}","event":"{{input.type}}"}}}]}`)
		endpoint, err := store.(state.InboundWebhookStore).CreateInboundWebhookEndpointIfUnderQuota(ctx, state.InboundWebhookEndpoint{
			AppID: app.ID, AccountID: app.AccountID, Name: "orders", Provider: state.InboundWebhookProviderGeneric, DeliveryPath: "/",
			TokenHash: bytes.Repeat([]byte{9}, 32), SigningSecretSealed: []byte("sealed"), Enabled: true}, api.MustLimitsFor(api.PlanPro))
		if err != nil {
			t.Fatal(err)
		}
		starts := store.(state.WebhookAutomationStore)
		if _, err := starts.SaveWebhookAutomationBinding(ctx, state.WebhookAutomationBindingOptions{EndpointID: endpoint.ID,
			AppID: app.ID, AccountID: app.AccountID, WorkflowName: "review-order", EventType: "order.*"}); err != nil {
			t.Fatal(err)
		}
		receipt, handled, err := starts.AcceptVerifiedWebhookAutomation(ctx, endpoint, "evt_order_1", "order.paid",
			json.RawMessage(`{"order_id":"ord_1"}`), true)
		if err != nil || !handled || receipt.Status != "accepted" {
			t.Fatalf("accept %+v %v %v", receipt, handled, err)
		}
		a := runAgentStep(t, store, admitPublishedWorkflow(t, store)).admissions[0]
		if a.Agent != "order-review" || string(a.Inputs) != `{"event":"order.paid","order":"ord_1"}` || a.AccountID != app.AccountID {
			t.Fatalf("admission %+v inputs %s", a, a.Inputs)
		}
	})
}

func TestGitHubPullRequestStartsAgentStep(t *testing.T) {
	sched.GuardOrchestrationStores(t, func(t *testing.T, store state.Store) {
		ctx := context.Background()
		app := deployAgentWorkflow(t, store, `{"name":"triage","trigger":{"type":"event","source":"gregale.github",`+
			`"event_type":"pull_request.*","filter":{"data":{"pull_request":{"fork":false}}}},"steps":[{"name":"review","agent":"pr-triage",`+
			`"input":{"inputs":{"title":"{{input.data.pull_request.title}}"},`+
			`"github":{"repository":"{{input.data.repository.full_name}}","number":"{{input.data.pull_request.number}}"},`+
			`"checkout":{"repository":"{{input.data.repository.full_name}}","commit":"{{input.data.pull_request.head.sha}}"}}}]}`)
		head := strings.Repeat("c", 40)
		data, err := json.Marshal(api.GitHubPullRequestEvent{Action: "opened", InstallationID: 7,
			Repository: api.GitHubEventRepository{FullName: "octo/api"},
			PullRequest: api.GitHubEventPullRequest{Number: 42, Title: "Fix the thing", State: "open",
				Head: api.GitHubEventRef{Ref: "fix", SHA: head, Repository: "octo/api"}, Base: api.GitHubEventRef{Ref: "main", SHA: strings.Repeat("d", 40)}}})
		if err != nil {
			t.Fatal(err)
		}
		envelope, err := (eventcontract.Envelope{ID: uuid.NewString(), Source: api.GitHubEventSource, Type: "pull_request.opened", Data: data}).
			Normalize(app.AccountID, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		payload, _ := json.Marshal(envelope)
		if _, err := store.(state.GitHubEventStore).AcceptPublishedEvent(ctx, "githubd", app.AccountID, payload); err != nil {
			t.Fatal(err)
		}
		a := runAgentStep(t, store, admitPublishedWorkflow(t, store)).admissions[0]
		if a.Agent != "pr-triage" || string(a.Inputs) != `{"title":"Fix the thing"}` ||
			a.GitHub == nil || a.GitHub.Repository != "octo/api" || a.GitHub.Number != 42 ||
			a.Checkout == nil || a.Checkout.Repository != "octo/api" || a.Checkout.Commit != head {
			t.Fatalf("admission %+v github %+v checkout %+v", a, a.GitHub, a.Checkout)
		}
	})
}

// adr: 623 — accepted promotions resume from durable checkpoints and exact evidence.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type promotionPinnedQueueStore struct {
	*state.MemStore
	book        *state.ProjectEnvironmentQueueSettings
	readErr     error
	globalReads int
}

func (s *promotionPinnedQueueStore) ProjectEnvironmentWorkloadSpecForDeployment(_ context.Context, _, _, _ string) (state.ProjectEnvironmentWorkloadSpec, error) {
	return state.ProjectEnvironmentWorkloadSpec{Settings: state.ProjectEnvironmentWorkloadSettings{QueueBindings: s.book}}, s.readErr
}
func (s *promotionPinnedQueueStore) ListQueueBindingsForApp(_ context.Context, _, _ string) ([]state.QueueBinding, error) {
	s.globalReads++
	return nil, nil
}

// adr: 623 — pinned queues never inherit app-wide consumer health.
func TestBindingProjectPromotionPinnedQueuesAreAuthoritative(t *testing.T) {
	for _, tc := range []struct {
		name               string
		book               *state.ProjectEnvironmentQueueSettings
		readErr            error
		items, globalReads int
		issue              string
	}{
		{name: "empty", book: &state.ProjectEnvironmentQueueSettings{}},
		{name: "disabled", book: &state.ProjectEnvironmentQueueSettings{Bindings: []state.ProjectEnvironmentQueueDefinition{{Name: "jobs", QueueName: "jobs", Mode: "push"}}}, items: 1},
		{name: "enabled", book: &state.ProjectEnvironmentQueueSettings{Bindings: []state.ProjectEnvironmentQueueDefinition{{Name: "jobs", QueueName: "jobs", Mode: "push", Enabled: true}}}, items: 1, issue: "environment_queue_activation_unavailable"},
		{name: "unreadable", readErr: errors.New("storage unavailable"), issue: "deployment_configuration_unavailable"},
		{name: "legacy", readErr: state.ErrNotFound, globalReads: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &promotionPinnedQueueStore{MemStore: state.NewMemStore(), book: tc.book, readErr: tc.readErr}
			s := newServer(store, slog.Default(), "gregale.dev", noopNotifier{})
			section := s.queueDeploymentBindingInventory(context.Background(), "account", state.App{ID: "app", ProjectID: "project"}, "staging", "deployment", time.Now())
			if len(section.items) != tc.items || store.globalReads != tc.globalReads {
				t.Fatalf("pinned queue inventory: %+v, global reads %d", section, store.globalReads)
			}
			if tc.issue == "" && len(section.issues) != 0 || tc.issue != "" && (len(section.issues) != 1 || section.issues[0].Code != tc.issue) {
				t.Fatalf("wrong queue blocker: %+v", section.issues)
			}
		})
	}
}

func TestBindingProjectPromotionWorkerResumesExactCandidates(t *testing.T) {
	e := setup(t, api.PlanPro)
	enableAppTaskAPIForTest(&e)
	configureSourceRefManagedPostgres(t, sourceRefTestEnv{acctID: e.acct.ID, srv: e.s})
	ctx := context.Background()
	p, err := e.store.CreateProject(ctx, state.Project{AccountID: e.acct.ID, Slug: "promotion-shop"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: e.acct.ID, ProjectID: p.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	var sourceMembers, previousMembers []state.ProjectReleaseMember
	var apps []state.App
	for _, slug := range []string{"promotion-api", "promotion-billing"} {
		manifest := state.AppManifest{RevisionPinTTLSeconds: 3600, ServiceBindingTransport: api.ServiceBindingTransportHTTPS}
		if slug == "promotion-api" {
			manifest.ServiceBindings = api.ServiceBindingsForTargets([]string{"promotion-billing"})
		}
		app, err := e.store.CreateApp(ctx, state.App{AccountID: e.acct.ID, ProjectID: p.ID, Slug: slug, WorkloadName: slug, Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 2, Manifest: manifest})
		if err != nil {
			t.Fatal(err)
		}
		settings, err := state.WorkloadSettingsFromApp(app)
		if err != nil {
			t.Fatal(err)
		}
		settings.RAMMB = 256
		if _, err := e.store.PutProjectEnvironmentWorkloadSpec(ctx, e.acct.ID, p.ID, "staging", app.ID, 0, settings); err != nil {
			t.Fatal(err)
		}
		for _, scope := range []string{"staging", "production"} {
			d, err := e.store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: scope, Kind: state.DeploymentKindImage, ImageDigest: appTaskTestDigest, SourceSHA256: scope})
			if err != nil {
				t.Fatal(err)
			}
			if err := e.store.SetDeploymentRootfs(ctx, d.ID, "/image", "image/"+d.ID, 4096); err != nil {
				t.Fatal(err)
			}
			if err := e.store.MarkDeploymentLive(ctx, d.ID); err != nil {
				t.Fatal(err)
			}
			member := state.ProjectReleaseMember{AppID: app.ID, DeploymentID: d.ID}
			if scope == "staging" {
				sourceMembers = append(sourceMembers, member)
			} else {
				previousMembers = append(previousMembers, member)
			}
		}
		apps = append(apps, app)
	}
	source, err := e.store.PublishProjectReleaseSet(ctx, e.acct.ID, p.ID, "staging", 1800, sourceMembers)
	if err != nil {
		t.Fatal(err)
	}
	previous, err := e.store.PublishProjectReleaseSet(ctx, e.acct.ID, p.ID, "production", 1800, previousMembers)
	if err != nil {
		t.Fatal(err)
	}
	createProjectEnvironmentQualificationForTest(t, e.store, e.acct, p, "staging", source.ID, "passed", "passed")
	zero := int64(0)
	if _, err := e.store.SetBindingReleasePolicy(ctx, e.acct.ID, apps[0].ID, "production", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &zero}); err != nil {
		t.Fatal(err)
	}
	plan, problem := e.s.buildProjectEnvironmentPromotionPlan(ctx, e.acct, p.Slug, "staging", "production", true)
	if problem != nil {
		t.Fatal(problem)
	}
	token := plan.Preview.PromotionToken
	body := api.PromoteProjectEnvironmentRequest{FromEnvironment: "staging", PromotionToken: token, RequireBindings: true}
	if plan.Preview.ApprovalRequired {
		approvalToken, _, problem := e.s.issueProjectEnvironmentPromotionApproval(ctx, e.acct, p.Slug, "production", token)
		if problem != nil {
			t.Fatal(problem)
		}
		body.ApprovalToken = approvalToken
	}

	path := "/v1/projects/" + p.Slug + "/environments/production/promote-with-bindings"
	response := e.do(t, http.MethodPost, path, body, map[string]string{"Idempotency-Key": "binding-promotion"})
	if response.Code != 202 {
		t.Fatalf("admit: %d %s", response.Code, response.Body.String())
	}
	var admitted api.ProjectEnvironmentPromotionResponse
	if err := json.Unmarshal(response.Body.Bytes(), &admitted); err != nil {
		t.Fatal(err)
	}
	if !admitted.BindingsRequired || admitted.Status != "running" {
		t.Fatalf("invalid admission: %+v", admitted)
	}
	saved, rows, err := e.store.ProjectEnvironmentPromotionByID(ctx, e.acct.ID, p.Slug, "production", admitted.PromotionID)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.TargetDeploymentID != "" || row.Status != "pending" {
			t.Fatalf("POST prepared a candidate: %+v", row)
		}
	}
	statusPath := "/v1/projects/" + p.Slug + "/environments/production/promotions/" + saved.ID
	for range 2 {
		if r := e.do(t, http.MethodGet, statusPath, nil, nil); r.Code != 200 {
			t.Fatal(r.Body.String())
		}
	}
	current, err := e.store.ActiveProjectReleaseSet(ctx, e.acct.ID, p.ID, "production")
	if err != nil || current.ID != previous.ID {
		t.Fatalf("GET changed graph: %+v %v", current, err)
	}
	if err := e.s.bindingProjectPromotionSweep(ctx); err != nil {
		t.Fatal(err)
	}
	saved, rows, err = e.store.ProjectEnvironmentPromotionByID(ctx, e.acct.ID, p.Slug, "production", saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	report := projectPromotionBindingsReport(saved)
	if saved.Status != "running" || saved.TargetReleaseSetID != "" || report == nil || report.Passed || len(report.Blockers) == 0 {
		t.Fatalf("missing persisted blockers: %+v %+v", saved, report)
	}
	targets := map[string]string{}
	for _, row := range rows {
		if row.Status != "promoted" || row.TargetDeploymentID == "" {
			t.Fatalf("missing candidate: %+v", row)
		}
		targets[row.WorkloadSlug] = row.TargetDeploymentID
	}
	for _, app := range apps {
		head, err := e.store.AppByID(ctx, app.ID)
		if err != nil || head.RAMMB != 128 {
			t.Fatalf("blocked promotion changed configuration: %+v %v", head, err)
		}
	}
	// A replay reads the same receipt and cannot execute an unchecked path.
	replay := e.do(t, http.MethodPost, path, body, map[string]string{"Idempotency-Key": "binding-promotion"})
	if replay.Code != 202 {
		t.Fatalf("replay: %d %s", replay.Code, replay.Body.String())
	}
	body.RequireBindings = false
	mismatch := e.do(t, http.MethodPost, "/v1/projects/"+p.Slug+"/environments/production/promote", body, map[string]string{"Idempotency-Key": "binding-promotion"})
	if mismatch.Code != 409 {
		t.Fatalf("mode changed on replay: %d", mismatch.Code)
	}
	callerID, targetID := targets["promotion-api"], uuid.MustParse(targets["promotion-billing"]).String()
	task := createAppTaskForTest(t, e, "promotion-api", api.CreateAppTaskRequest{VerificationDeploymentID: callerID, Command: []string{api.AppTaskServiceBindingProbeCommand, "promotion-billing", targetID}})
	running, err := e.store.ClaimNextAppTask(ctx, "promotion-probe", time.Now().UTC(), time.Minute)
	if err != nil || running.ID != task.ID {
		t.Fatalf("claim: %+v %v", running, err)
	}
	running, err = e.store.MarkAppTaskRunning(ctx, running.ID, *running.LeaseToken, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	passed := api.ServiceBindingProbeCheck{Status: "passed"}
	output, _ := json.Marshal(api.ServiceBindingProbeReport{Service: "promotion-billing", TargetDeploymentID: targetID, DNS: passed, TLS: passed, Authorization: passed, Routing: passed})
	exit := 0
	if _, err := e.store.CompleteAppTask(ctx, state.CompleteAppTaskParams{ID: running.ID, LeaseToken: *running.LeaseToken, Status: state.AppTaskSucceeded, StdoutTail: string(output), ExitCode: &exit, FinishedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	// The backend restart carries no in-memory promotion plan.
	restarted := newServer(e.store, e.s.log, "gregale.dev", noopNotifier{})
	restarted.managedPostgresBindings = e.s.managedPostgresBindings
	time.Sleep(time.Until(saved.BindingCheckNextAt.Add(20 * time.Millisecond)))
	if err := restarted.bindingProjectPromotionSweep(ctx); err != nil {
		t.Fatal(err)
	}
	saved, finalRows, err := e.store.ProjectEnvironmentPromotionByID(ctx, e.acct.ID, p.Slug, "production", saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Status != "succeeded" || saved.TargetReleaseSetID == "" || saved.VerificationStatus != "verified" {
		t.Fatalf("resume failed: %+v %+v", saved, projectPromotionBindingsReport(saved))
	}
	for _, row := range finalRows {
		if row.TargetDeploymentID != targets[row.WorkloadSlug] {
			t.Fatal("restart duplicated a candidate")
		}
	}
	if _, err := e.store.ClaimNextAppTask(ctx, "unexpected-probe", time.Now().UTC(), time.Minute); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("worker submitted a probe: %v", err)
	}
	for _, app := range apps {
		head, err := e.store.AppByID(ctx, app.ID)
		if err != nil || head.RAMMB != 256 {
			t.Fatalf("checked configuration not applied: %+v %v", head, err)
		}
	}
}

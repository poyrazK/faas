package apidserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routemonitor"
	"github.com/onebox-faas/faas/pkg/routeprobe"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

func projectRollbackAPIFixture(t *testing.T, extraServices ...string) (testEnv, state.Project, []state.Deployment) {
	t.Helper()
	e := setup(t, api.PlanPro)
	ctx := t.Context()
	p, err := e.store.CreateProject(ctx, state.Project{AccountID: e.acct.ID, Slug: "rollback-shop"})
	if err != nil {
		t.Fatal(err)
	}
	oldGroup, newGroup := uuid.NewString(), uuid.NewString()
	apps := make(map[string]state.App)
	for _, name := range append([]string{"api", "web"}, extraServices...) {
		app := state.App{AccountID: e.acct.ID, ProjectID: p.ID, Slug: "rollback-shop-" + name, WorkloadName: name,
			Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30}
		if name == "web" {
			app.Manifest.ServiceBindings = []api.AppServiceBinding{{Binding: "API", Service: "api"}}
		}
		app, err = e.store.CreateApp(ctx, app)
		if err != nil {
			t.Fatal(err)
		}
		apps[name] = app
	}
	oldAPI := projectRollbackDeploy(t, e.store, apps["api"].ID, oldGroup)
	oldWeb := projectRollbackDeploy(t, e.store, apps["web"].ID, oldGroup)
	for _, name := range extraServices {
		projectRollbackDeploy(t, e.store, apps[name].ID, oldGroup)
	}
	projectRollbackDeploy(t, e.store, apps["api"].ID, newGroup)
	projectRollbackDeploy(t, e.store, apps["web"].ID, newGroup)
	for _, name := range extraServices {
		projectRollbackDeploy(t, e.store, apps[name].ID, newGroup)
	}
	return e, p, []state.Deployment{oldWeb, oldAPI}
}

func projectRollbackDeploy(t *testing.T, store state.Store, app, group string) state.Deployment {
	t.Helper()
	ctx := t.Context()
	id := uuid.NewString()
	d, err := store.CreateDeployment(ctx, state.Deployment{AppID: app, Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:" + id, ProjectDeployGroupID: group, TrafficPercent: 100})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentRootfs(ctx, d.ID, "/layers/"+id, "layers/"+id, 4096); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	return d
}

func readProjectRollbackAPI(t *testing.T, e testEnv, method, path string, req any, status int) api.ProjectRollbackOperation {
	t.Helper()
	rec := e.do(t, method, path, req, nil)
	var op api.ProjectRollbackOperation
	if err := json.Unmarshal(rec.Body.Bytes(), &op); rec.Code != status || err != nil {
		t.Fatalf("%s %s: %d %s (%v)", method, path, rec.Code, rec.Body, err)
	}
	return op
}

func TestProjectRollbackWorkerRecoversAcceptedStepAfterRestart(t *testing.T) {
	e, p, targets := projectRollbackAPIFixture(t)
	ctx := t.Context()
	path := "/v1/projects/" + p.Slug + "/rollbacks"
	preview := readProjectRollbackAPI(t, e, http.MethodGet, path+"/preview", nil, http.StatusOK)
	if preview.ID != "" || preview.Status != "planned" || len(preview.Steps) != 2 || preview.Steps[0].Slug != "rollback-shop-web" {
		t.Fatalf("unexpected preview: %+v", preview)
	}
	op := readProjectRollbackAPI(t, e, http.MethodPost, path, api.ProjectRollbackRequest{}, http.StatusAccepted)
	if rec := e.do(t, http.MethodPost, path, api.ProjectRollbackRequest{}, nil); rec.Code != http.StatusConflict {
		t.Fatalf("concurrent project operation: %d %s", rec.Code, rec.Body)
	}
	claimed, err := e.store.ClaimProjectRollback(ctx, 250*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.s.advanceProjectRollback(ctx, &claimed); err != nil {
		t.Fatal(err)
	}
	for i, target := range targets {
		child, err := e.store.GetCheckedRollback(ctx, e.acct.ID, target.AppID, op.Steps[i].AttemptID)
		if err != nil || child.ID != op.Steps[i].AttemptID || child.ProjectRollbackID != op.ID {
			t.Fatalf("admitted child: %+v, %v", child, err)
		}
	}
	saved, err := e.store.GetProjectRollback(ctx, e.acct.ID, p.ID, op.ID)
	if err != nil || saved.Steps[0].Status != "planned" || saved.Steps[0].ReceiptID != "" {
		t.Fatalf("expected the parent update to be lost: %+v, %v", saved, err)
	}

	// New apid instance and fresh API key. The parent persisted before the
	// original HTTP request ended, and the first worker never saved its step.
	e.s = newServer(e.store, e.s.log, "gregale.dev", noopNotifier{}).WithRollbackArtifactVerifier(stubRollbackArtifactVerifier{})
	e.h = e.s.handler()
	freshKey, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.CreateAPIKey(ctx, e.acct.ID, hash, "other operator", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	e.key = freshKey
	markProjectRollbackTargetsLive(t, e, targets)
	if err := e.s.checkedRollbackSweep(ctx); err != nil {
		t.Fatal(err)
	}
	assertProjectRollbackServing(t, e, op, targets)
	deadline := time.Now().Add(3 * time.Second)
	for {
		if err := e.s.projectRollbackSweep(ctx, projectRollbackLease, time.Millisecond); err != nil {
			t.Fatal(err)
		}
		if err := e.s.checkedRollbackSweep(ctx); err != nil {
			t.Fatal(err)
		}
		got := readProjectRollbackAPI(t, e, http.MethodGet, path+"/"+op.ID, nil, http.StatusOK)
		if got.Status == "complete" {
			for i, step := range got.Steps {
				if step.ReceiptID != op.Steps[i].AttemptID || step.DeploymentID != targets[i].ID || step.Status != "rolled_back" {
					t.Fatalf("receipt did not preserve the original child: %+v", step)
				}
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("rollback did not complete: %+v", got)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestProjectRollbackPausesForNewDownstreamDeployment(t *testing.T) {
	e, p, targets := projectRollbackAPIFixture(t)
	ctx := t.Context()
	path := "/v1/projects/" + p.Slug + "/rollbacks"
	op := readProjectRollbackAPI(t, e, http.MethodPost, path, api.ProjectRollbackRequest{}, http.StatusAccepted)
	if err := e.s.projectRollbackSweep(ctx, projectRollbackLease, 0); err != nil {
		t.Fatal(err)
	}
	markProjectRollbackTargetsLive(t, e, targets)
	if err := e.s.checkedRollbackSweep(ctx); err != nil {
		t.Fatal(err)
	}
	assertProjectRollbackServing(t, e, op, targets)
	if err := e.s.projectRollbackSweep(ctx, projectRollbackLease, 0); err != nil {
		t.Fatal(err)
	}
	if err := e.s.checkedRollbackSweep(ctx); err != nil {
		t.Fatal(err)
	}
	// The caller can now restore. The downstream receipt must wait for
	// the parent to record the caller's completed cutover.
	current, problem := e.s.projectRollbackCurrent(ctx, targets[0].AppID)
	if problem != nil || current.ID != targets[0].ID {
		t.Fatalf("caller did not restore: %s %v", current.ID, problem)
	}
	current, problem = e.s.projectRollbackCurrent(ctx, targets[1].AppID)
	if problem != nil || current.ID != op.Steps[1].CurrentID {
		t.Fatalf("downstream cutover was out of order: %s %v", current.ID, problem)
	}
	newer := projectRollbackDeploy(t, e.store, targets[1].AppID, "")
	if err := e.s.checkedRollbackSweep(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.s.projectRollbackSweep(ctx, projectRollbackLease, 0); err != nil {
		t.Fatal(err)
	}
	got := readProjectRollbackAPI(t, e, http.MethodGet, path+"/"+op.ID, nil, http.StatusOK)
	if got.Status != "blocked" || got.Steps[0].Status != "rolled_back" || got.Steps[1].Status != "blocked" || got.Steps[1].ReceiptID != op.Steps[1].AttemptID {
		t.Fatalf("stale plan was not paused: %+v", got)
	}
	current, problem = e.s.projectRollbackCurrent(ctx, targets[1].AppID)
	if problem != nil || current.ID != newer.ID {
		t.Fatalf("newer deployment did not stay serving: %s %v", current.ID, problem)
	}
}

func markProjectRollbackTargetsLive(t *testing.T, e testEnv, targets []state.Deployment) {
	t.Helper()
	for _, target := range targets {
		if err := e.store.MarkDeploymentLive(t.Context(), target.ID); err != nil {
			t.Fatal(err)
		}
	}
}

func assertProjectRollbackServing(t *testing.T, e testEnv, op api.ProjectRollbackOperation, targets []state.Deployment) {
	t.Helper()
	for i, target := range targets {
		current, problem := e.s.projectRollbackCurrent(t.Context(), target.AppID)
		if problem != nil || current.ID != op.Steps[i].CurrentID || current.TrafficPercent != 100 {
			t.Fatalf("target %d changed the serving deployment before cutover: %+v %v", i, current, problem)
		}
		historical, err := e.store.DeploymentByID(t.Context(), target.ID)
		if err != nil || historical.TrafficPercent != 0 {
			t.Fatalf("target %d received traffic: %+v %v", i, historical, err)
		}
	}
}

func TestProjectRollbackCapsConcurrentWarming(t *testing.T) {
	e, p, _ := projectRollbackAPIFixture(t, "search", "images", "auth")
	ctx := t.Context()
	path := "/v1/projects/" + p.Slug + "/rollbacks"
	op := readProjectRollbackAPI(t, e, http.MethodPost, path, api.ProjectRollbackRequest{}, http.StatusAccepted)
	if len(op.Steps) != projectRollbackConcurrentPreparations+1 {
		t.Fatalf("unexpected rollback plan: %+v", op.Steps)
	}
	if err := e.s.projectRollbackSweep(ctx, projectRollbackLease, 0); err != nil {
		t.Fatal(err)
	}
	held := op.Steps[projectRollbackConcurrentPreparations]
	if _, err := e.store.GetCheckedRollback(ctx, e.acct.ID, held.AppID, held.AttemptID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("queued target started before a preparation slot was available: %v", err)
	}
	if err := e.store.MarkDeploymentLive(ctx, op.Steps[0].TargetID); err != nil {
		t.Fatal(err)
	}
	if err := e.s.checkedRollbackSweep(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.s.projectRollbackSweep(ctx, projectRollbackLease, 0); err != nil {
		t.Fatal(err)
	}
	receipt, err := e.store.GetCheckedRollback(ctx, e.acct.ID, held.AppID, held.AttemptID)
	if err != nil || receipt.ProjectRollbackID != op.ID {
		t.Fatalf("queued target did not begin preparation: %+v %v", receipt, err)
	}
}

func TestProjectRollbackReleasesPreparationAfterProjectDeletion(t *testing.T) {
	e, p, targets := projectRollbackAPIFixture(t)
	ctx := t.Context()
	path := "/v1/projects/" + p.Slug + "/rollbacks"
	op := readProjectRollbackAPI(t, e, http.MethodPost, path, api.ProjectRollbackRequest{}, http.StatusAccepted)
	if err := e.s.projectRollbackSweep(ctx, projectRollbackLease, 0); err != nil {
		t.Fatal(err)
	}
	if err := e.store.DeleteProject(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.s.checkedRollbackSweep(ctx); err != nil {
		t.Fatal(err)
	}
	for _, step := range op.Steps {
		child, err := e.store.GetCheckedRollback(ctx, e.acct.ID, step.AppID, step.AttemptID)
		if err != nil || child.Status != "failed" || child.Code != api.CodeProjectRollbackPreparationReleased {
			t.Fatalf("orphaned preparation stayed active: %+v %v", child, err)
		}
	}
	assertProjectRollbackServing(t, e, op, targets)
}

func TestProjectRollbackColdDownstreamBindingFailureReleasesPreparation(t *testing.T) {
	e, p, targets := projectRollbackAPIFixture(t)
	ctx := t.Context()
	downstream, err := e.store.AppByID(ctx, targets[1].AppID)
	if err != nil {
		t.Fatal(err)
	}
	e.s.WithAppTaskAPIEnabled(true)
	readyVerificationBinding(t, e, downstream)
	zero := int64(0)
	if err := e.store.MarkDeploymentSuperseded(ctx, targets[1].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.SetBindingReleasePolicy(ctx, e.acct.ID, downstream.ID, api.DefaultEnvScope,
		api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &zero}); err != nil {
		t.Fatal(err)
	}
	path := "/v1/projects/" + p.Slug + "/rollbacks"
	op := readProjectRollbackAPI(t, e, http.MethodPost, path, api.ProjectRollbackRequest{}, http.StatusAccepted)
	if op.Phase != api.ProjectRollbackPreparing || op.PreparationDeadline == nil {
		t.Fatalf("missing preparation phase: %+v", op)
	}
	if err := e.s.projectRollbackSweep(ctx, projectRollbackLease, 0); err != nil {
		t.Fatal(err)
	}
	child, err := e.store.GetCheckedRollback(ctx, e.acct.ID, downstream.ID, op.Steps[1].AttemptID)
	if err != nil || child.Status != "preparing" {
		t.Fatalf("downstream target was not cold: %+v %v", child, err)
	}
	markProjectRollbackTargetsLive(t, e, targets)
	if err := e.s.checkedRollbackSweep(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.s.projectRollbackSweep(ctx, projectRollbackLease, 0); err != nil {
		t.Fatal(err)
	}
	getReceipt := func() api.ProjectRollbackOperation {
		return readProjectRollbackAPI(t, e, http.MethodGet, path+"/"+op.ID, nil, http.StatusOK)
	}
	held := getReceipt()
	checkBlocker := func(step api.ProjectRollbackStep) {
		t.Helper()
		if step.Problem == nil || step.Problem.Code != "bindings_check_failed" || step.TargetID != targets[1].ID ||
			!slices.ContainsFunc(step.Blockers, func(blocker api.BindingCheckFinding) bool {
				return blocker.Type == api.BindingTypePostgres && blocker.Code == "verification_unknown"
			}) {
			t.Fatalf("missing binding blocker for the exact downstream target: %+v", step)
		}
	}
	if held.Phase != api.ProjectRollbackPreparing || held.Steps[0].PreparationStatus != "ready" ||
		held.Steps[1].PreparationStatus != "blocked" {
		t.Fatalf("unexpected preparation status: %+v", held)
	}
	checkBlocker(held.Steps[1])
	assertProjectRollbackServing(t, e, op, targets)

	claim, err := e.store.ClaimProjectRollback(ctx, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	expired := time.Now().Add(-time.Second)
	claim.PreparationDeadline = &expired
	if err := e.store.SaveProjectRollback(ctx, claim, 0); err != nil {
		t.Fatal(err)
	}
	if err := e.s.projectRollbackSweep(ctx, projectRollbackLease, 0); err != nil {
		t.Fatal(err)
	}
	released := getReceipt()
	if released.Status != "blocked" || released.Steps[0].PreparationStatus != "released" ||
		released.Steps[1].PreparationStatus != "released" {
		t.Fatalf("unused preparations were not released: %+v", released)
	}
	checkBlocker(released.Steps[1])
	assertProjectRollbackServing(t, e, op, targets)
	for i, target := range targets {
		dep, err := e.store.DeploymentByID(ctx, target.ID)
		if err != nil || dep.ServiceRolloutHandoff.Phase != "" {
			t.Fatalf("target %d kept its preparation handoff: %+v, %v", i, dep.ServiceRolloutHandoff, err)
		}
	}
}

type missingProjectRollbackArtifact string

func (missing missingProjectRollbackArtifact) CheckPresent(_ context.Context, key, _ string) error {
	if key == string(missing) {
		return storage.ErrNotFound
	}
	return nil
}

func TestProjectRollbackChecksDownstreamBeforeCreatingReceipt(t *testing.T) {
	e, p, targets := projectRollbackAPIFixture(t)
	target, err := e.store.DeploymentByID(t.Context(), targets[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	e.s.rollbackArtifactVerifier = missingProjectRollbackArtifact(target.RootfsKey)
	path := "/v1/projects/" + p.Slug + "/rollbacks"
	for _, got := range []api.ProjectRollbackOperation{
		readProjectRollbackAPI(t, e, http.MethodGet, path+"/preview", nil, http.StatusOK),
		readProjectRollbackAPI(t, e, http.MethodPost, path, api.ProjectRollbackRequest{}, http.StatusOK),
	} {
		if got.ID != "" || got.Status != "blocked" || len(got.Steps) != 2 || got.Steps[0].Status != "planned" ||
			got.Steps[1].Problem == nil || got.Steps[1].Problem.Code != api.CodeRollbackTargetUnavailable {
			t.Fatalf("expected downstream blocker before creating receipt: %+v", got)
		}
	}
	if _, err := e.store.ClaimProjectRollback(t.Context(), time.Minute); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("blocked plan was queued: %v", err)
	}
}

type projectRollbackHealthTestStore struct {
	*state.MemStore // teststorecheck:allow implements state.ProjectRollbackHealthStore, which statetest.Mem would hide
	routes          map[string]api.ProjectRollbackTraffic
}

func (s *projectRollbackHealthTestStore) ProjectRollbackHealthRoute(_ context.Context, _, attempt string) (api.ProjectRollbackTraffic, error) {
	route, ok := s.routes[attempt]
	if !ok {
		return route, state.ErrNotFound
	}
	return route, nil
}

type projectRollbackHealthTestSender struct {
	t         *testing.T
	probes    map[string]api.ProjectReleaseProbe
	outcomes  map[string]routeprobe.Outcome
	probedIDs []string
}

func (s *projectRollbackHealthTestSender) Configured() bool { return true }

func (s *projectRollbackHealthTestSender) Probe(ctx context.Context, slug, id, token, method, path string) (routeprobe.Outcome, error) {
	return s.ProbeCritical(ctx, slug, id, token, method, path)
}

func (s *projectRollbackHealthTestSender) ProbeCritical(_ context.Context, slug, id, token, method, path string) (routeprobe.Outcome, error) {
	want, ok := s.probes[id]
	if !ok || token == "" || want.Slug != slug || want.Method != method || want.Path != path {
		s.t.Fatalf("wrong rollback probe for deployment %s: %s %s %s", id, slug, method, path)
	}
	s.probedIDs = append(s.probedIDs, id)
	return s.outcomes[id], nil
}

func TestProjectRollbackHealthFailureHoldsDownstreamUntilResume(t *testing.T) {
	e, p, targets := projectRollbackAPIFixture(t)
	ctx := t.Context()
	delay := routeProbeChallengeDelay
	routeProbeChallengeDelay = 0
	t.Cleanup(func() { routeProbeChallengeDelay = delay })
	for i, target := range targets {
		zero := int64(0)
		method := http.MethodGet
		if i == 1 {
			method = http.MethodHead
		}
		if _, err := e.store.SetRouteHealthGate(ctx, e.acct.ID, target.AppID, api.SetRouteHealthGateRequest{
			Mode: "report", ExpectedRevision: &zero, Routes: []api.RouteHealthRoute{
				{Method: method, Path: "/ready", Probe: &api.RouteHealthProbe{Path: "/ready"}},
			},
		}); err != nil {
			t.Fatal(err)
		}
	}
	routes := &projectRollbackHealthTestStore{MemStore: e.store, routes: make(map[string]api.ProjectRollbackTraffic)}
	sender := &projectRollbackHealthTestSender{t: t, probes: map[string]api.ProjectReleaseProbe{
		targets[0].ID: {Slug: "rollback-shop-web", Method: http.MethodGet, Path: "/ready"},
		targets[1].ID: {Slug: "rollback-shop-api", Method: http.MethodHead, Path: "/ready"},
	}, outcomes: map[string]routeprobe.Outcome{targets[0].ID: routeprobe.ServerError, targets[1].ID: routeprobe.Success}}
	newWorker := func() {
		e.s = newServer(routes, e.s.log, "gregale.dev", noopNotifier{}).
			WithRollbackArtifactVerifier(stubRollbackArtifactVerifier{}).WithRouteProbes(sender)
		e.h = e.s.handler()
	}
	newWorker()
	sweep := func() {
		t.Helper()
		if err := e.s.projectRollbackSweep(ctx, projectRollbackLease, 0); err != nil {
			t.Fatal(err)
		}
	}
	check := func() {
		t.Helper()
		if err := e.s.checkedRollbackSweep(ctx); err != nil {
			t.Fatal(err)
		}
	}
	path := "/v1/projects/" + p.Slug + "/rollbacks"
	preview := readProjectRollbackAPI(t, e, http.MethodGet, path+"/preview?verify=true", nil, http.StatusOK)
	if !preview.Verify || preview.Steps[0].Health == nil || preview.Steps[1].Health == nil {
		t.Fatalf("verify preview is missing the health gates: %+v", preview)
	}
	op := readProjectRollbackAPI(t, e, http.MethodPost, path, api.ProjectRollbackRequest{Verify: true}, http.StatusAccepted)
	get := func() api.ProjectRollbackOperation {
		return readProjectRollbackAPI(t, e, http.MethodGet, path+"/"+op.ID, nil, http.StatusOK)
	}
	expectServing := func(step int, id string) {
		t.Helper()
		d, problem := e.s.projectRollbackCurrent(ctx, targets[step].AppID)
		if problem != nil || d.ID != id || d.TrafficPercent != 100 {
			t.Fatalf("step %d serving = %+v, problem = %v, want %s", step, d, problem, id)
		}
	}
	makeRoute := func(generation int64, applied bool) api.ProjectRollbackTraffic {
		cutover := time.Now().UTC()
		r := api.ProjectRollbackTraffic{RouteGeneration: &generation, RoutingCommittedAt: &cutover}
		if applied {
			r.GatewaysAppliedAt = &cutover
		}
		return r
	}
	webAttempt, apiAttempt := op.Steps[0].AttemptID, op.Steps[1].AttemptID
	sweep()
	markProjectRollbackTargetsLive(t, e, targets)
	check()
	sweep()
	check()
	expectServing(0, targets[0].ID)
	expectServing(1, op.Steps[1].CurrentID)

	routes.routes[webAttempt] = makeRoute(7, false)
	sweep()
	if pending := get(); pending.Steps[0].Health.Status != "awaiting_gateways" || len(sender.probedIDs) != 0 {
		t.Fatalf("probed before gateway acknowledgements: %+v, probes=%v", pending, sender.probedIDs)
	}
	check()
	expectServing(1, op.Steps[1].CurrentID)

	routes.routes[webAttempt] = makeRoute(7, true)
	sweep()
	paused := get()
	if paused.Status != "blocked" || paused.Steps[0].Health.Status != "failed" ||
		paused.Steps[0].Problem == nil || paused.Steps[0].Problem.Code != api.CodeProjectRollbackHealthFailed ||
		paused.Steps[1].PreparationStatus != "released" {
		t.Fatalf("expected a saved health blocker and released downstream preparation: %+v", paused)
	}
	expectServing(1, op.Steps[1].CurrentID)

	newWorker()
	sender.outcomes[targets[0].ID] = routeprobe.Success
	resumed := readProjectRollbackAPI(t, e, http.MethodPost, path+"/"+op.ID+"/resume", nil, http.StatusOK)
	if resumed.Steps[0].AttemptID != webAttempt || resumed.Steps[1].AttemptID == apiAttempt || !resumed.Verify {
		t.Fatalf("resume changed the completed cutover or its health policy: %+v", resumed)
	}
	sweep()
	got := get()
	if got.Steps[0].Health.Status != "passed" || got.Steps[0].ReceiptID != webAttempt {
		t.Fatalf("resume did not verify the previous cutover: %+v", got)
	}
	expectServing(1, op.Steps[1].CurrentID)
	markProjectRollbackTargetsLive(t, e, targets[1:])
	check()
	sweep()
	check()
	expectServing(1, targets[1].ID)
	apiAttempt = resumed.Steps[1].AttemptID
	routes.routes[apiAttempt] = makeRoute(8, false)
	sweep()
	if got = get(); got.Status != "running" || got.Steps[1].Health.Status != "awaiting_gateways" {
		t.Fatalf("operation completed before its final verification: %+v", got)
	}
	routes.routes[apiAttempt] = makeRoute(8, true)
	sweep()
	if got = get(); got.Status != "complete" || got.Steps[1].Health.Status != "passed" {
		t.Fatalf("verified rollback did not complete: %+v", got)
	}
	if want := []string{targets[0].ID, targets[0].ID, targets[1].ID}; !slices.Equal(sender.probedIDs, want) {
		t.Fatalf("probed %v, want %v", sender.probedIDs, want)
	}
}

func TestProjectRollbackVerifiesPreparedTargetsBeforeCutover(t *testing.T) {
	for _, test := range []struct {
		name    string
		outcome routeprobe.Outcome
		code    string
	}{
		{"healthy", routeprobe.Success, ""},
		{"downstream 5xx", routeprobe.ServerError, api.CodeProjectRollbackPreCutoverHealthFailed},
		{"unattributed", routeprobe.Unattributed, api.CodeProjectRollbackPreCutoverHealthTimeout},
	} {
		t.Run(test.name, func(t *testing.T) {
			e, p, targets := projectRollbackAPIFixture(t)
			ctx := t.Context()
			delay := routeProbeChallengeDelay
			routeProbeChallengeDelay = 0
			t.Cleanup(func() { routeProbeChallengeDelay = delay })
			sender := &projectRollbackHealthTestSender{t: t, probes: map[string]api.ProjectReleaseProbe{
				targets[0].ID: {Slug: "rollback-shop-web", Method: http.MethodGet, Path: "/ready"},
				targets[1].ID: {Slug: "rollback-shop-api", Method: http.MethodHead, Path: "/ready"},
			}, outcomes: map[string]routeprobe.Outcome{targets[0].ID: routeprobe.Success, targets[1].ID: test.outcome}}
			for _, target := range targets {
				zero := int64(0)
				probe := sender.probes[target.ID]
				if _, err := e.store.SetRouteHealthGate(ctx, e.acct.ID, target.AppID, api.SetRouteHealthGateRequest{
					Mode: "report", ExpectedRevision: &zero, Routes: []api.RouteHealthRoute{
						{Method: probe.Method, Path: probe.Path, Probe: &api.RouteHealthProbe{Path: probe.Path}},
					},
				}); err != nil {
					t.Fatal(err)
				}
			}
			routes := &projectRollbackHealthTestStore{MemStore: e.store, routes: make(map[string]api.ProjectRollbackTraffic)}
			restart := func() {
				e.s = newServer(routes, e.s.log, "gregale.dev", noopNotifier{}).
					WithRollbackArtifactVerifier(stubRollbackArtifactVerifier{}).WithRouteProbes(sender)
				e.h = e.s.handler()
			}
			restart()
			path := "/v1/projects/" + p.Slug + "/rollbacks"
			plain := readProjectRollbackAPI(t, e, http.MethodGet, path+"/preview?verify=true", nil, http.StatusOK)
			reviewed := readProjectRollbackAPI(t, e, http.MethodGet, path+"/preview?verify=true&verify_before_cutover=true", nil, http.StatusOK)
			if !reviewed.VerifyBeforeCutover || reviewed.PlanHash == "" || reviewed.PlanHash == plain.PlanHash || reviewed.Steps[1].PreCutoverHealth == nil {
				t.Fatalf("preview omitted its pre-cutover policy: %+v", reviewed)
			}
			op := readProjectRollbackAPI(t, e, http.MethodPost, path, api.ProjectRollbackRequest{
				Verify: true, VerifyBeforeCutover: true, ExpectedPlanHash: reviewed.PlanHash,
			}, http.StatusAccepted)
			get := func() api.ProjectRollbackOperation {
				t.Helper()
				return readProjectRollbackAPI(t, e, http.MethodGet, path+"/"+op.ID, nil, http.StatusOK)
			}
			advance := func() {
				t.Helper()
				claim, err := e.store.ClaimProjectRollback(ctx, projectRollbackLease)
				if err != nil {
					t.Fatal(err)
				}
				if err := e.s.advanceProjectRollback(ctx, &claim); err != nil {
					t.Fatal(err)
				}
				if err := e.store.SaveProjectRollback(ctx, claim, 0); err != nil {
					t.Fatal(err)
				}
			}
			sweepProjectRollbackForTest(t, e)
			markProjectRollbackTargetsLive(t, e, targets[:1])
			checkProjectRollbackForTest(t, e)
			advance()
			if len(sender.probedIDs) != 0 {
				t.Fatalf("probed before both targets were ready: %v", sender.probedIDs)
			}
			markProjectRollbackTargetsLive(t, e, targets[1:])
			checkProjectRollbackForTest(t, e)
			advance()
			checkProjectRollbackForTest(t, e)
			if held := get(); held.Phase != api.ProjectRollbackPreparing || !held.Steps[0].PreCutoverHealthPassed() ||
				held.Steps[1].PreCutoverHealthPassed() {
				t.Fatalf("only the caller probe should have passed: %+v", held)
			}
			assertProjectRollbackServing(t, e, op, targets)

			restart()
			advance()
			if want := []string{targets[0].ID, targets[1].ID}; !slices.Equal(sender.probedIDs, want) {
				t.Fatalf("probed %v, want %v", sender.probedIDs, want)
			}
			if test.code == "" {
				advance()
				got := get()
				if got.Phase != api.ProjectRollbackCutover || !got.Steps[1].PreCutoverHealthPassed() || got.Steps[0].Health.Status != "pending" {
					t.Fatalf("pre-cutover checks did not open the barrier independently of post-cutover health: %+v", got)
				}
				checkProjectRollbackForTest(t, e)
				current, problem := e.s.projectRollbackCurrent(ctx, targets[0].AppID)
				if problem != nil || current.ID != targets[0].ID {
					t.Fatalf("healthy target did not switch: %+v %v", current, problem)
				}
				return
			}
			if test.outcome == routeprobe.Unattributed {
				if held := get(); held.Phase != api.ProjectRollbackPreparing || held.Steps[1].PreCutoverHealth.Status != "awaiting_probe" {
					t.Fatalf("unattributed response released the barrier: %+v", held)
				}
				claim, err := e.store.ClaimProjectRollback(ctx, projectRollbackLease)
				if err != nil {
					t.Fatal(err)
				}
				expired := time.Now().Add(-time.Second)
				claim.PreparationDeadline = &expired
				if err := e.store.SaveProjectRollback(ctx, claim, 0); err != nil {
					t.Fatal(err)
				}
			}
			sweepProjectRollbackForTest(t, e)
			got := get()
			if got.Status != "blocked" || got.AutoUndo != nil || got.Steps[1].PreCutoverHealth.Status != "failed" ||
				got.Steps[1].Problem == nil || got.Steps[1].Problem.Code != test.code ||
				got.Steps[0].PreparationStatus != "released" || got.Steps[1].PreparationStatus != "released" {
				t.Fatalf("pre-cutover failure did not release both preparations: %+v", got)
			}
			assertProjectRollbackServing(t, e, op, targets)
			resumed := readProjectRollbackAPI(t, e, http.MethodPost, path+"/"+op.ID+"/resume", nil, http.StatusOK)
			for i, step := range resumed.Steps {
				if step.AttemptID == op.Steps[i].AttemptID || step.PreCutoverHealth.Status != "pending" || step.PreCutoverHealthPassed() {
					t.Fatalf("resume reused pre-cutover evidence: %+v", step)
				}
			}
		})
	}
}

type projectRollbackLiveTrafficTestStore struct {
	*projectRollbackHealthTestStore
	counts api.RouteHealthCounts
	policy state.ProjectRollbackLiveTrafficPolicy
}

func (s *projectRollbackLiveTrafficTestStore) ProjectRollbackLiveTrafficReport(_ context.Context, _ string, step api.ProjectRollbackStep, policy state.ProjectRollbackLiveTrafficPolicy) (api.RouteMonitorReport, error) {
	s.policy = policy
	now := time.Now().UTC()
	anchor := now.Add(-4 * time.Minute)
	report := routemonitor.NewReport(api.RouteMonitorConfig{AppID: step.AppID, Enabled: true, Revision: policy.Revision, Routes: policy.Routes}, now)
	report.DeploymentID, report.ObservationAnchor = step.TargetID, &anchor
	for i := range report.Routes {
		for j := range report.Routes[i].Windows {
			report.Routes[i].Windows[j].Observed = s.counts
		}
	}
	routemonitor.Evaluate(&report, "")
	return report, nil
}

func TestProjectRollbackLiveTrafficHoldsDownstreamDespiteHealthyProbe(t *testing.T) {
	for _, test := range []string{"manual resume", "automatic undo after restart", "traffic recovered before undo", "route changed before undo"} {
		t.Run(test, func(t *testing.T) { testProjectRollbackLiveTraffic(t, test) })
	}
}

func testProjectRollbackLiveTraffic(t *testing.T, scenario string) {
	e, p, targets := projectRollbackAPIFixture(t)
	ctx := t.Context()
	delay := routeProbeChallengeDelay
	routeProbeChallengeDelay = 0
	t.Cleanup(func() { routeProbeChallengeDelay = delay })

	web := targets[0]
	if _, err := e.store.SetRouteHealthGate(ctx, e.acct.ID, web.AppID, api.SetRouteHealthGateRequest{
		Mode: "report", ExpectedRevision: new(int64), Routes: []api.RouteHealthRoute{
			{Method: http.MethodGet, Path: "/ready", Probe: &api.RouteHealthProbe{Path: "/ready"}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	store := &projectRollbackLiveTrafficTestStore{projectRollbackHealthTestStore: &projectRollbackHealthTestStore{
		MemStore: e.store, routes: make(map[string]api.ProjectRollbackTraffic),
	}}
	sender := &projectRollbackHealthTestSender{t: t, probes: map[string]api.ProjectReleaseProbe{
		web.ID: {Slug: "rollback-shop-web", Method: http.MethodGet, Path: "/ready"},
	}, outcomes: map[string]routeprobe.Outcome{web.ID: routeprobe.Success}}
	restart := func() {
		e.s = newServer(store, e.s.log, "gregale.dev", noopNotifier{}).
			WithRollbackArtifactVerifier(stubRollbackArtifactVerifier{}).WithRouteProbes(sender)
		e.h = e.s.handler()
	}
	restart()

	path := "/v1/projects/" + p.Slug + "/rollbacks"
	options := "?verify=true&verify_live_traffic=true"
	rec := e.do(t, http.MethodGet, path+"/preview"+options, nil, nil)
	var missingBudget api.Problem
	if rec.Code != http.StatusConflict || json.Unmarshal(rec.Body.Bytes(), &missingBudget) != nil ||
		missingBudget.Code != api.CodeProjectRollbackLiveTrafficRequired {
		t.Fatalf("expected a missing production error budget response, got %d %s", rec.Code, rec.Body)
	}
	budget := int64(500)
	config := api.SetRouteMonitorRequest{Enabled: true, ExpectedRevision: new(int64), Routes: []api.RouteMonitorRoute{
		{Method: http.MethodPost, Path: "/checkout", Max5xxRateBPS: &budget},
	}}
	monitor, err := e.store.SetRouteMonitor(ctx, e.acct.ID, web.AppID, config)
	if err != nil {
		t.Fatal(err)
	}
	plain := readProjectRollbackAPI(t, e, http.MethodGet, path+"/preview?verify=true", nil, http.StatusOK)
	preview := readProjectRollbackAPI(t, e, http.MethodGet, path+"/preview"+options, nil, http.StatusOK)
	if !preview.VerifyLiveTraffic || preview.Steps[0].LiveTraffic == nil || preview.PlanHash == "" || preview.PlanHash == plain.PlanHash {
		t.Fatalf("preview did not capture live verification: %+v", preview)
	}
	automatic := scenario != "manual resume"
	if automatic {
		withUndo := readProjectRollbackAPI(t, e, http.MethodGet, path+"/preview"+options+"&undo_on_live_traffic_failure=true", nil, http.StatusOK)
		if !withUndo.UndoOnLiveTrafficFailure || withUndo.PlanHash == "" || withUndo.PlanHash == preview.PlanHash {
			t.Fatalf("preview did not bind automatic live-traffic undo: %+v", withUndo)
		}
		preview = withUndo
	}
	op := readProjectRollbackAPI(t, e, http.MethodPost, path, api.ProjectRollbackRequest{
		Verify: true, VerifyLiveTraffic: true, UndoOnLiveTrafficFailure: automatic, ExpectedPlanHash: preview.PlanHash,
	}, http.StatusAccepted)
	root := path + "/" + op.ID
	get := func() api.ProjectRollbackOperation {
		t.Helper()
		return readProjectRollbackAPI(t, e, http.MethodGet, root, nil, http.StatusOK)
	}
	config.ExpectedRevision = &monitor.Revision
	relaxedBudget := int64(10000)
	config.Routes[0].Max5xxRateBPS = &relaxedBudget
	if _, err := e.store.SetRouteMonitor(ctx, e.acct.ID, web.AppID, config); err != nil {
		t.Fatal(err)
	}

	sweepProjectRollbackForTest(t, e)
	markProjectRollbackTargetsLive(t, e, targets)
	checkProjectRollbackForTest(t, e)
	sweepProjectRollbackForTest(t, e)
	checkProjectRollbackForTest(t, e)
	ack, generation := time.Now().UTC().Add(-4*time.Minute), int64(7)
	store.routes[op.Steps[0].AttemptID] = api.ProjectRollbackTraffic{RouteGeneration: &generation, GatewaysAppliedAt: &ack}
	sweepProjectRollbackForTest(t, e)
	checkProjectRollbackForTest(t, e)
	held := get()
	if held.Steps[0].Health.Status != "passed" || held.Steps[0].LiveTraffic.Status != "insufficient_traffic" ||
		held.Steps[1].Status == "rolled_back" || held.AutoUndo != nil || len(sender.probedIDs) != 1 {
		t.Fatalf("sparse live traffic bypassed a healthy probe: %+v, probes %v", held, sender.probedIDs)
	}

	claim, err := e.store.ClaimProjectRollback(ctx, projectRollbackLease)
	if err != nil {
		t.Fatal(err)
	}
	claim.Steps[0].LiveTraffic.CheckedAt = nil
	store.counts = api.RouteHealthCounts{Requests: 40, ServerErrors: 10}
	if err := e.s.advanceProjectRollback(ctx, &claim); err != nil {
		t.Fatal(err)
	}
	if err := e.store.SaveProjectRollback(ctx, claim, 0); err != nil {
		t.Fatal(err)
	}
	if automatic {
		pending := get()
		if pending.Phase != api.ProjectRollbackReleasing || pending.AutoUndo == nil ||
			pending.AutoUndo.Status != api.ProjectRollbackAutoUndoPending || pending.UndoOperationID != "" ||
			!pending.AutoUndo.MatchesLiveTrafficFailure(pending.Steps[0]) ||
			pending.AutoUndo.WindowStart.Before(ack) {
			t.Fatalf("production breach was not persisted before cleanup: %+v", pending)
		}
		restart()
		switch scenario {
		case "traffic recovered before undo":
			store.counts = api.RouteHealthCounts{Requests: 40}
		case "route changed before undo":
			next := generation + 1
			store.routes[op.Steps[0].AttemptID] = api.ProjectRollbackTraffic{RouteGeneration: &next, GatewaysAppliedAt: &ack}
		}
		sweepProjectRollbackForTest(t, e)
		result := get()
		current, problem := e.s.projectRollbackCurrent(ctx, targets[1].AppID)
		if problem != nil || current.ID != op.Steps[1].CurrentID ||
			result.Steps[1].PreparationStatus != "released" || result.Status != "blocked" {
			t.Fatalf("downstream moved after the production breach: %+v, current %+v, %v", result, current, problem)
		}
		if scenario != "automatic undo after restart" {
			if result.UndoOperationID != "" || result.AutoUndo == nil || result.AutoUndo.Status != api.ProjectRollbackAutoUndoBlocked {
				t.Fatalf("undo started on stale production evidence: %+v", result)
			}
			return
		}
		if result.UndoOperationID == "" || result.AutoUndo == nil || result.AutoUndo.Status != api.ProjectRollbackAutoUndoStarted ||
			!result.AutoUndo.MatchesLiveTrafficFailure(result.Steps[0]) {
			t.Fatalf("the persisted breach did not start automatic undo: %+v", result)
		}
		undoPath := path + "/" + result.UndoOperationID
		undo := readProjectRollbackAPI(t, e, http.MethodGet, undoPath, nil, http.StatusOK)
		if undo.UndoOf != op.ID || len(undo.Steps) != 1 || undo.Steps[0].AppID != web.AppID ||
			undo.Steps[0].TargetID != op.Steps[0].CurrentID {
			t.Fatalf("undo did not target the switched service: %+v", undo)
		}
		restart()
		if err := e.store.MarkDeploymentLive(ctx, op.Steps[0].CurrentID); err != nil {
			t.Fatal(err)
		}
		for range 3 {
			checkProjectRollbackForTest(t, e)
			sweepProjectRollbackForTest(t, e)
		}
		done := readProjectRollbackAPI(t, e, http.MethodGet, undoPath, nil, http.StatusOK)
		history, err := e.store.ListProjectRollbacks(ctx, e.acct.ID, p.ID)
		current, problem = e.s.projectRollbackCurrent(ctx, web.AppID)
		if err != nil || len(history) != 2 || done.Status != "complete" || problem != nil || current.ID != op.Steps[0].CurrentID {
			t.Fatalf("the switched service was not restored exactly once: %+v, %v, %+v, %v", done, err, current, problem)
		}
		return
	}
	sweepProjectRollbackForTest(t, e)
	failed := get()
	if failed.Status != "blocked" || failed.Steps[0].Health.Status != "passed" ||
		failed.Steps[0].LiveTraffic.Status != "failed" || failed.Steps[0].Problem == nil ||
		failed.Steps[0].Problem.Code != api.CodeProjectRollbackLiveTrafficFailed ||
		failed.Steps[1].PreparationStatus != "released" || failed.UndoOperationID != "" ||
		store.policy.Revision != monitor.Revision || *store.policy.Routes[0].Max5xxRateBPS != budget {
		t.Fatalf("a sustained breach did not block with the pinned budget: %+v, policy %+v", failed, store.policy)
	}
	current, problem := e.s.projectRollbackCurrent(ctx, targets[1].AppID)
	if problem != nil || current.ID != op.Steps[1].CurrentID {
		t.Fatalf("downstream changed despite the breach: %+v, %v", current, problem)
	}

	resumed := readProjectRollbackAPI(t, e, http.MethodPost, root+"/resume", nil, http.StatusOK)
	if gate := resumed.Steps[0].LiveTraffic; gate.Status != "pending" || gate.ObserveAfter == nil || gate.ObserveAfter.Before(ack) {
		t.Fatalf("resume did not start fresh observation: %+v", gate)
	}
	store.counts = api.RouteHealthCounts{Requests: 40}
	sweepProjectRollbackForTest(t, e)
	markProjectRollbackTargetsLive(t, e, targets[1:])
	checkProjectRollbackForTest(t, e)
	sweepProjectRollbackForTest(t, e)
	checkProjectRollbackForTest(t, e)
	sweepProjectRollbackForTest(t, e)
	if done := get(); done.Status != "complete" || !done.Steps[0].LiveTraffic.Passed() {
		t.Fatalf("healthy production traffic did not release the next service: %+v", done)
	}
}

func TestProjectRollbackLiveTrafficDeadlineHoldsOnSparseTraffic(t *testing.T) {
	e := setup(t, api.PlanPro)
	expired := time.Now().Add(-time.Second)
	step := api.ProjectRollbackStep{LiveTraffic: &api.ProjectRollbackLiveTraffic{Status: "insufficient_traffic", Deadline: &expired}}
	op := state.ProjectRollbackRecord{}
	if e.s.verifyProjectRollbackLiveTraffic(t.Context(), &op, &step) || op.Status != "blocked" ||
		step.Problem == nil || step.Problem.Code != api.CodeProjectRollbackLiveTrafficTimeout ||
		!strings.Contains(step.Problem.Detail, "insufficient production traffic") {
		t.Fatalf("sparse traffic was released after its deadline: %+v", step)
	}
}

func TestProjectRollbackHealthDeadlinePausesWithoutEvidence(t *testing.T) {
	e := setup(t, api.PlanPro)
	expired := time.Now().Add(-time.Second)
	step := api.ProjectRollbackStep{Status: "rolled_back", Health: &api.ProjectRollbackHealth{Status: "awaiting_gateways", Deadline: &expired}}
	op := state.ProjectRollbackRecord{ProjectRollbackOperation: api.ProjectRollbackOperation{UndoOnHealthFailure: true}}
	if e.s.verifyProjectRollbackStep(t.Context(), &op, &step) || op.Status != "blocked" ||
		step.Problem == nil || step.Problem.Code != api.CodeProjectRollbackHealthTimeout || op.AutoUndo != nil {
		t.Fatalf("unverified cutover advanced after the deadline: %+v", step)
	}
}

func TestProjectRollbackPreparationBudgetsPendingVerification(t *testing.T) {
	now := time.Now().UTC()
	stabilizeFor := time.Minute
	healthySince := now.Add(-stabilizeFor)
	generation := int64(1)
	op := state.ProjectRollbackRecord{ProjectRollbackOperation: api.ProjectRollbackOperation{
		Status:              "blocked",
		Phase:               api.ProjectRollbackReleasing,
		StabilizeForSeconds: int(stabilizeFor / time.Second),
		Steps: []api.ProjectRollbackStep{
			{Status: "skipped", Health: &api.ProjectRollbackHealth{Status: "pending"}},
			{Status: "rolled_back", Health: &api.ProjectRollbackHealth{Status: "passed", HealthySince: &healthySince, CheckedAt: &now},
				LiveTraffic: &api.ProjectRollbackLiveTraffic{Status: "passed", RouteGeneration: &generation, CheckedAt: &now}},
			{Status: "rolled_back", Health: &api.ProjectRollbackHealth{Status: "pending"},
				LiveTraffic: &api.ProjectRollbackLiveTraffic{Status: "pending"}},
		},
	}}
	for range 10 {
		op.Steps = append(op.Steps, api.ProjectRollbackStep{Status: "planned", Health: &api.ProjectRollbackHealth{Status: "pending"}})
	}

	resetProjectRollbackForResume(&op, now, true)
	want := now.Add(projectRollbackPreparationWindow + 11*(projectRollbackHealthWindow+stabilizeFor) + projectRollbackLiveTrafficWindow)
	if op.PreparationDeadline == nil || !op.PreparationDeadline.Equal(want) {
		t.Fatalf("preparation deadline = %v, want %v", op.PreparationDeadline, want)
	}
}

func TestProjectRollbackStabilizationRequiresContinuousEvidence(t *testing.T) {
	e, _, targets := projectRollbackAPIFixture(t)
	delay := routeProbeChallengeDelay
	routeProbeChallengeDelay = 0
	t.Cleanup(func() { routeProbeChallengeDelay = delay })
	target := targets[0]
	probe := api.ProjectReleaseProbe{Slug: "rollback-shop-web", Method: http.MethodGet, Path: "/ready"}
	attempt := uuid.NewString()
	now := time.Now().UTC()
	generation := int64(7)
	routes := &projectRollbackHealthTestStore{MemStore: e.store, routes: map[string]api.ProjectRollbackTraffic{
		attempt: {RouteGeneration: &generation, GatewaysAppliedAt: &now},
	}}
	sender := &projectRollbackHealthTestSender{t: t, probes: map[string]api.ProjectReleaseProbe{target.ID: probe},
		outcomes: make(map[string]routeprobe.Outcome)}
	e.s = newServer(routes, e.s.log, "gregale.dev", noopNotifier{}).WithRouteProbes(sender)

	for _, test := range []struct {
		name    string
		age     time.Duration
		outcome routeprobe.Outcome
		status  string
	}{
		{"wait for next sample", -time.Second, routeprobe.Success, "stabilizing"},
		{"late restart", projectRollbackStabilizationMaxGap + time.Second, routeprobe.Success, "stabilizing"},
		{"unattributed response", projectRollbackStabilizationInterval + time.Second, routeprobe.Unattributed, "awaiting_probe"},
		{"continuous window", projectRollbackStabilizationInterval + time.Second, routeprobe.Success, "passed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			begin, last, deadline := now.Add(-31*time.Second), now.Add(-test.age), now.Add(time.Minute)
			h := &api.ProjectRollbackHealth{Status: "stabilizing", HealthySince: &begin, CheckedAt: &last,
				Deadline: &deadline, RouteGeneration: &generation}
			op := state.ProjectRollbackRecord{ProjectRollbackOperation: api.ProjectRollbackOperation{
				Status: "running", Verify: true, StabilizeForSeconds: 30, Steps: []api.ProjectRollbackStep{
					{Status: "rolled_back", Health: h, AttemptID: attempt, AppID: target.AppID, TargetID: target.ID},
				},
			}, HealthProbes: map[string][]api.ProjectReleaseProbe{target.AppID: {probe}}}
			sender.outcomes[target.ID] = test.outcome
			probesBefore := len(sender.probedIDs)
			if e.s.verifyProjectRollbackStep(t.Context(), &op, &op.Steps[0]) || h.Status != test.status ||
				(op.Status == "complete") != (test.status == "passed") {
				t.Fatalf("health = %+v, rollback = %s, want %s", h, op.Status, test.status)
			}
			switch test.name {
			case "wait for next sample":
				if len(sender.probedIDs) != probesBefore {
					t.Fatal("probed before the next sample was due")
				}
			case "late restart":
				if !h.HealthySince.Equal(*h.CheckedAt) {
					t.Fatal("carried a stale sample across the gap")
				}
			case "unattributed response":
				if h.HealthySince != nil {
					t.Fatal("an unattributed response preserved the window")
				}
			}
		})
	}
}

func TestProjectRollbackAutomaticUndoResumesAfterWorkerRestart(t *testing.T) {
	for _, test := range []struct {
		name      string
		drift     bool
		pause     bool
		stabilize bool
	}{
		{name: "undo exactly once"},
		{name: "503 during stabilization", stabilize: true},
		{name: "newer downstream deploy", drift: true},
		{name: "operator pause", pause: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			e, p, targets := projectRollbackAPIFixture(t)
			ctx := t.Context()
			delay := routeProbeChallengeDelay
			routeProbeChallengeDelay = 0
			t.Cleanup(func() { routeProbeChallengeDelay = delay })
			zero := int64(0)
			if _, err := e.store.SetRouteHealthGate(ctx, e.acct.ID, targets[0].AppID, api.SetRouteHealthGateRequest{
				Mode: "report", ExpectedRevision: &zero, Routes: []api.RouteHealthRoute{
					{Method: http.MethodGet, Path: "/ready", Probe: &api.RouteHealthProbe{Path: "/ready"}},
				},
			}); err != nil {
				t.Fatal(err)
			}
			routes := &projectRollbackHealthTestStore{MemStore: e.store, routes: make(map[string]api.ProjectRollbackTraffic)}
			sender := &projectRollbackHealthTestSender{t: t, probes: map[string]api.ProjectReleaseProbe{
				targets[0].ID: {Slug: "rollback-shop-web", Method: http.MethodGet, Path: "/ready"},
			}, outcomes: map[string]routeprobe.Outcome{targets[0].ID: routeprobe.ServerError}}
			restart := func() {
				e.s = newServer(routes, e.s.log, "gregale.dev", noopNotifier{}).
					WithRollbackArtifactVerifier(stubRollbackArtifactVerifier{}).WithRouteProbes(sender)
				e.h = e.s.handler()
			}
			restart()
			path := "/v1/projects/" + p.Slug + "/rollbacks"
			req := api.ProjectRollbackRequest{Verify: true, UndoOnHealthFailure: true}
			plain := readProjectRollbackAPI(t, e, http.MethodGet, path+"/preview?verify=true", nil, http.StatusOK)
			reviewed := readProjectRollbackAPI(t, e, http.MethodGet, path+"/preview?verify=true&undo_on_health_failure=true", nil, http.StatusOK)
			if !reviewed.UndoOnHealthFailure || reviewed.PlanHash == "" || reviewed.PlanHash == plain.PlanHash {
				t.Fatalf("review hash did not bind automatic undo: %+v", reviewed)
			}
			if test.stabilize {
				stabilized := readProjectRollbackAPI(t, e, http.MethodGet, path+"/preview?verify=true&undo_on_health_failure=true&stabilize_for_seconds=30", nil, http.StatusOK)
				if stabilized.StabilizeForSeconds != 30 || stabilized.PlanHash == "" || stabilized.PlanHash == reviewed.PlanHash {
					t.Fatalf("review hash did not bind stabilization: %+v", stabilized)
				}
				req.StabilizeForSeconds, reviewed = 30, stabilized
			}
			req.ExpectedPlanHash = reviewed.PlanHash
			op := readProjectRollbackAPI(t, e, http.MethodPost, path, req, http.StatusAccepted)
			root := path + "/" + op.ID
			sweepProjectRollbackForTest(t, e)
			markProjectRollbackTargetsLive(t, e, targets)
			checkProjectRollbackForTest(t, e)
			sweepProjectRollbackForTest(t, e)
			checkProjectRollbackForTest(t, e)
			generation := int64(7)
			now := time.Now()
			routes.routes[op.Steps[0].AttemptID] = api.ProjectRollbackTraffic{RouteGeneration: &generation, GatewaysAppliedAt: &now}
			if test.stabilize {
				sender.outcomes[targets[0].ID] = routeprobe.Success
				sweepProjectRollbackForTest(t, e)
				checkProjectRollbackForTest(t, e)
				held := readProjectRollbackAPI(t, e, http.MethodGet, root, nil, http.StatusOK)
				if health := held.Steps[0].Health; health.Status != "stabilizing" || health.HealthySince == nil || len(sender.probedIDs) != 1 {
					t.Fatalf("the first 2xx should have started stabilization: %+v, probes %v", held, sender.probedIDs)
				}
				current, problem := e.s.projectRollbackCurrent(ctx, targets[1].AppID)
				if problem != nil || current.ID != op.Steps[1].CurrentID {
					t.Fatalf("downstream cut over after one success: %+v, %v", current, problem)
				}
				restart()
				claim, err := e.store.ClaimProjectRollback(ctx, projectRollbackLease)
				if err != nil {
					t.Fatal(err)
				}
				due := time.Now().Add(-projectRollbackStabilizationInterval - time.Second)
				claim.Steps[0].Health.CheckedAt, claim.Steps[0].Health.HealthySince = &due, &due
				if err := e.store.SaveProjectRollback(ctx, claim, 0); err != nil {
					t.Fatal(err)
				}
				sender.outcomes[targets[0].ID] = routeprobe.ServerError
			}
			// Stop after a persisted failure, before cleanup can admit the undo.
			for range 8 {
				claim, err := e.store.ClaimProjectRollback(ctx, projectRollbackLease)
				if err != nil {
					t.Fatal(err)
				}
				if err := e.s.advanceProjectRollback(ctx, &claim); err != nil {
					t.Fatal(err)
				}
				if err := e.store.SaveProjectRollback(ctx, claim, 0); err != nil {
					t.Fatal(err)
				}
				if claim.AutoUndo != nil && claim.AutoUndo.Status == api.ProjectRollbackAutoUndoPending {
					break
				}
			}
			failed := readProjectRollbackAPI(t, e, http.MethodGet, root, nil, http.StatusOK)
			if failed.Phase != api.ProjectRollbackReleasing || failed.Status != "running" || failed.UndoOperationID != "" ||
				failed.AutoUndo == nil || failed.AutoUndo.Status != api.ProjectRollbackAutoUndoPending {
				t.Fatalf("auto undo was not durably pending after confirmed failure: %+v", failed)
			}
			restart()
			if test.drift {
				projectRollbackDeploy(t, e.store, targets[1].AppID, "")
			}
			if test.pause {
				readProjectRollbackAPI(t, e, http.MethodPost, root+"/pause", nil, http.StatusAccepted)
			}
			sweepProjectRollbackForTest(t, e)
			result := readProjectRollbackAPI(t, e, http.MethodGet, root, nil, http.StatusOK)
			if result.Status != "blocked" || result.Steps[1].PreparationStatus != "released" || result.AutoUndo == nil {
				t.Fatalf("source cleanup was not settled: %+v", result)
			}
			if test.drift || test.pause {
				if result.UndoOperationID != "" || result.AutoUndo.Status != api.ProjectRollbackAutoUndoBlocked ||
					result.AutoUndo.Problem == nil {
					t.Fatalf("undo ran despite a serving change or pause: %+v", result)
				}
				return
			}
			if result.UndoOperationID == "" || result.AutoUndo.Status != api.ProjectRollbackAutoUndoStarted {
				t.Fatalf("auto undo was not linked on restart: %+v", result)
			}
			undo := readProjectRollbackAPI(t, e, http.MethodGet, path+"/"+result.UndoOperationID, nil, http.StatusOK)
			if undo.UndoOf != op.ID || len(undo.Steps) != 1 || undo.Steps[0].CurrentID != targets[0].ID ||
				undo.Steps[0].TargetID != op.Steps[0].CurrentID || undo.AcknowledgeReleases {
				t.Fatalf("automatic undo did not pin the switched service: %+v", undo)
			}
			restart()
			if err := e.store.MarkDeploymentLive(ctx, op.Steps[0].CurrentID); err != nil {
				t.Fatal(err)
			}
			for range 3 {
				checkProjectRollbackForTest(t, e)
				sweepProjectRollbackForTest(t, e)
			}
			done := readProjectRollbackAPI(t, e, http.MethodGet, path+"/"+undo.ID, nil, http.StatusOK)
			history, err := e.store.ListProjectRollbacks(ctx, e.acct.ID, p.ID)
			if err != nil || len(history) != 2 || done.Status != "complete" {
				t.Fatalf("automatic undo did not complete exactly once: %+v %+v %v", done, history, err)
			}
		})
	}
}

func projectRollbackPausedUndoFixture(t *testing.T) (testEnv, api.ProjectRollbackOperation, []state.Deployment, string) {
	t.Helper()
	e, p, targets := projectRollbackAPIFixture(t)
	e, paused, root := pauseProjectRollbackUndoFixture(t, e, p, targets, api.ProjectRollbackRequest{Verify: true})
	return e, paused, targets, root
}

func pauseProjectRollbackUndoFixture(t *testing.T, e testEnv, p state.Project, targets []state.Deployment, req api.ProjectRollbackRequest) (testEnv, api.ProjectRollbackOperation, string) {
	t.Helper()
	ctx := t.Context()
	delay := routeProbeChallengeDelay
	routeProbeChallengeDelay = 0
	t.Cleanup(func() { routeProbeChallengeDelay = delay })
	zero := int64(0)
	if _, err := e.store.SetRouteHealthGate(ctx, e.acct.ID, targets[0].AppID, api.SetRouteHealthGateRequest{
		Mode: "report", ExpectedRevision: &zero, Routes: []api.RouteHealthRoute{
			{Method: http.MethodGet, Path: "/ready", Probe: &api.RouteHealthProbe{Path: "/ready"}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	routes := &projectRollbackHealthTestStore{MemStore: e.store, routes: make(map[string]api.ProjectRollbackTraffic)}
	sender := &projectRollbackHealthTestSender{t: t, probes: map[string]api.ProjectReleaseProbe{
		targets[0].ID: {Slug: "rollback-shop-web", Method: http.MethodGet, Path: "/ready"},
	}, outcomes: map[string]routeprobe.Outcome{targets[0].ID: routeprobe.ServerError}}
	e.s.store = routes
	e.s.WithRouteProbes(sender)
	e.h = e.s.handler()
	root := "/v1/projects/" + p.Slug + "/rollbacks"
	op := readProjectRollbackAPI(t, e, http.MethodPost, root, req, http.StatusAccepted)
	sweepProjectRollbackForTest(t, e)
	markProjectRollbackTargetsLive(t, e, targets)
	checkProjectRollbackForTest(t, e)
	sweepProjectRollbackForTest(t, e)
	checkProjectRollbackForTest(t, e)
	generation := int64(7)
	now := time.Now().UTC()
	routes.routes[op.Steps[0].AttemptID] = api.ProjectRollbackTraffic{
		RouteGeneration: &generation, RoutingCommittedAt: &now, GatewaysAppliedAt: &now,
	}
	sweepProjectRollbackForTest(t, e)
	paused := readProjectRollbackAPI(t, e, http.MethodGet, root+"/"+op.ID, nil, http.StatusOK)
	if paused.Status != "blocked" || paused.Steps[0].Status != "rolled_back" || paused.Steps[0].Problem == nil ||
		paused.Steps[0].Problem.Code != api.CodeProjectRollbackHealthFailed || paused.Steps[1].Status == "rolled_back" {
		t.Fatalf("unexpected paused rollback: %+v", paused)
	}
	return e, paused, root
}

func sweepProjectRollbackForTest(t *testing.T, e testEnv) {
	t.Helper()
	if err := e.s.projectRollbackSweep(t.Context(), projectRollbackLease, 0); err != nil {
		t.Fatal(err)
	}
}

func checkProjectRollbackForTest(t *testing.T, e testEnv) {
	t.Helper()
	if err := e.s.checkedRollbackSweep(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestProjectRollbackReviewedResumeRejectsServingDrift(t *testing.T) {
	for _, duringAdmission := range []bool{false, true} {
		name := "after_preview"
		if duringAdmission {
			name = "during_admission"
		}
		t.Run(name, func(t *testing.T) {
			e, source, targets, root := projectRollbackPausedUndoFixture(t)
			path := root + "/" + source.ID
			preview := readProjectRollbackAPI(t, e, http.MethodGet, path+"/resume/preview", nil, http.StatusOK)
			if preview.ID != source.ID || preview.Status != "planned" || !api.ValidProjectRollbackPlanHash(preview.ResumePlanHash) ||
				len(preview.Steps) != 2 || preview.Steps[0].Status != "rolled_back" ||
				preview.Steps[0].Health == nil || preview.Steps[0].Health.Status != "pending" ||
				preview.Steps[1].Status != "planned" || preview.Steps[1].AttemptID != "" ||
				preview.Steps[1].CurrentID != source.Steps[1].CurrentID || preview.Steps[1].TargetID != targets[1].ID {
				t.Fatalf("resume preview = %+v", preview)
			}
			afterPreview := readProjectRollbackAPI(t, e, http.MethodGet, path, nil, http.StatusOK)
			if afterPreview.Status != "blocked" || afterPreview.ResumePlanHash != "" ||
				afterPreview.Steps[1].AttemptID != source.Steps[1].AttemptID ||
				afterPreview.Steps[0].Problem == nil || afterPreview.Steps[0].Problem.Code != api.CodeProjectRollbackHealthFailed {
				t.Fatalf("preview changed the saved receipt: %+v", afterPreview)
			}

			var newer state.Deployment
			deploy := func() { newer = projectRollbackDeploy(t, e.store, targets[1].AppID, "") }
			if duringAdmission {
				e.s.store = &projectRollbackResumeAdmissionTestStore{
					projectRollbackHealthTestStore: e.s.store.(*projectRollbackHealthTestStore), beforeResume: deploy,
				}
			} else {
				deploy()
			}
			req := api.ProjectRollbackResumeRequest{ExpectedPlanHash: preview.ResumePlanHash}
			rec := e.do(t, http.MethodPost, path+"/resume", req, nil)
			var problem api.Problem
			if rec.Code != http.StatusConflict || json.Unmarshal(rec.Body.Bytes(), &problem) != nil ||
				problem.Code != api.CodeProjectRollbackPlanChanged {
				t.Fatalf("stale resume = %d %s", rec.Code, rec.Body)
			}
			stillPaused := readProjectRollbackAPI(t, e, http.MethodGet, path, nil, http.StatusOK)
			if stillPaused.Status != "blocked" || stillPaused.ResumePlanHash != "" ||
				stillPaused.Steps[1].AttemptID != source.Steps[1].AttemptID || stillPaused.Steps[0].ReceiptID != source.Steps[0].ReceiptID {
				t.Fatalf("stale resume changed the receipt: %+v", stillPaused)
			}
			for i, want := range []string{targets[0].ID, newer.ID} {
				current, problem := e.s.projectRollbackCurrent(t.Context(), targets[i].AppID)
				if problem != nil || current.ID != want || current.TrafficPercent != 100 {
					t.Fatalf("serving deployment %d = %+v, %v", i, current, problem)
				}
			}
			blocked := readProjectRollbackAPI(t, e, http.MethodGet, path+"/resume/preview", nil, http.StatusOK)
			if blocked.Status != "blocked" || blocked.ResumePlanHash != "" || blocked.Steps[1].Problem == nil ||
				blocked.Steps[1].Problem.Code != rollbackDeploymentChanged {
				t.Fatalf("preview omitted serving drift: %+v", blocked)
			}
		})
	}
}

type projectRollbackResumeAdmissionTestStore struct {
	*projectRollbackHealthTestStore
	beforeResume func()
}

func (s *projectRollbackResumeAdmissionTestStore) ResumeProjectRollback(ctx context.Context, account string, op state.ProjectRollbackRecord) error {
	s.beforeResume()
	return s.MemStore.ResumeProjectRollback(ctx, account, op)
}

func TestProjectRollbackReviewedResumeReplaysTheSameAttempt(t *testing.T) {
	e, source, _, root := projectRollbackPausedUndoFixture(t)
	path := root + "/" + source.ID
	preview := readProjectRollbackAPI(t, e, http.MethodGet, path+"/resume/preview", nil, http.StatusOK)
	req := api.ProjectRollbackResumeRequest{ExpectedPlanHash: preview.ResumePlanHash}
	resumed := readProjectRollbackAPI(t, e, http.MethodPost, path+"/resume", req, http.StatusOK)
	replayed := readProjectRollbackAPI(t, e, http.MethodPost, path+"/resume", req, http.StatusOK)
	if resumed.ID != source.ID || resumed.Status != "running" || resumed.ResumePlanHash != preview.ResumePlanHash ||
		resumed.PlanHash != source.PlanHash || resumed.Steps[0].ReceiptID != source.Steps[0].ReceiptID ||
		resumed.Steps[1].AttemptID == "" || resumed.Steps[1].AttemptID == source.Steps[1].AttemptID ||
		replayed.Steps[1].AttemptID != resumed.Steps[1].AttemptID {
		t.Fatalf("resume receipt = %+v, replay = %+v", resumed, replayed)
	}
}

func TestProjectRollbackUndoRestoresOnlyTheSwitchedService(t *testing.T) {
	e, source, targets, root := projectRollbackPausedUndoFixture(t)
	path := root + "/" + source.ID
	undo := readProjectRollbackAPI(t, e, http.MethodPost, path+"/undo", api.ProjectRollbackUndoRequest{}, http.StatusAccepted)
	if undo.ID == "" || undo.ID == source.ID || undo.UndoOf != source.ID || len(undo.Steps) != 1 ||
		undo.Steps[0].CurrentID != targets[0].ID || undo.Steps[0].TargetID != source.Steps[0].CurrentID {
		t.Fatalf("wrong undo plan: %+v", undo)
	}
	if got := readProjectRollbackAPI(t, e, http.MethodGet, path, nil, http.StatusOK); got.UndoOperationID != undo.ID {
		t.Fatalf("source receipt did not link the undo: %+v", got)
	}
	var conflict api.Problem
	rec := e.do(t, http.MethodPost, path+"/resume", nil, nil)
	if rec.Code != http.StatusConflict || json.Unmarshal(rec.Body.Bytes(), &conflict) != nil || conflict.Code != api.CodeProjectRollbackUndoStarted {
		t.Fatalf("original rollback could resume: %d %s", rec.Code, rec.Body)
	}

	sweepProjectRollbackForTest(t, e)
	e.s = newServer(e.store, e.s.log, "gregale.dev", noopNotifier{}).
		WithRollbackArtifactVerifier(stubRollbackArtifactVerifier{})
	e.h = e.s.handler()
	if replay := readProjectRollbackAPI(t, e, http.MethodPost, path+"/undo", api.ProjectRollbackUndoRequest{}, http.StatusAccepted); replay.ID != undo.ID {
		t.Fatalf("a retry after restart created a new undo: %+v", replay)
	}
	if err := e.store.MarkDeploymentLive(t.Context(), undo.Steps[0].TargetID); err != nil {
		t.Fatal(err)
	}
	checkProjectRollbackForTest(t, e)
	sweepProjectRollbackForTest(t, e)
	checkProjectRollbackForTest(t, e)
	sweepProjectRollbackForTest(t, e)

	finished := readProjectRollbackAPI(t, e, http.MethodPost, path+"/undo", api.ProjectRollbackUndoRequest{}, http.StatusOK)
	if finished.ID != undo.ID || finished.Status != "complete" || finished.Steps[0].ReceiptID != undo.Steps[0].AttemptID {
		t.Fatalf("undo did not finish its original checked operation: %+v", finished)
	}
	assertProjectRollbackServing(t, e, source, targets)
}

func TestProjectRollbackUndoRetainsOlderServingArtifacts(t *testing.T) {
	e, project, targets := projectRollbackAPIFixture(t)
	ctx := t.Context()
	app := targets[0].AppID
	outgoing, problem := e.s.projectRollbackCurrent(ctx, app)
	if problem != nil {
		t.Fatal(problem)
	}
	var latest state.Deployment
	for range api.SnapshotRollbackRetentionDeployments {
		latest = projectRollbackDeploy(t, e.store, app, "")
	}
	if _, err := e.store.CreateCheckedRollback(ctx, e.acct.ID, app, outgoing.ID, latest.ID, ""); err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkDeploymentLive(ctx, outgoing.ID); err != nil {
		t.Fatal(err)
	}
	checkProjectRollbackForTest(t, e)

	e, source, root := pauseProjectRollbackUndoFixture(t, e, project, targets, api.ProjectRollbackRequest{
		Verify: true, ToDeploy: targets[0].ProjectDeployGroupID,
	})
	path := root + "/" + source.ID
	hold := source.Steps[0].UndoArtifacts
	if hold == nil || hold.DeploymentID != outgoing.ID || hold.ExpiresAt == nil || !hold.ExpiresAt.After(time.Now()) ||
		source.Steps[1].UndoArtifacts != nil {
		t.Fatalf("paused rollback undo holds = %+v", source.Steps)
	}
	for _, key := range []string{outgoing.RootfsKey, "sigs/" + outgoing.RootfsKey + ".sig"} {
		if _, eligible, err := e.store.ClaimLayerArtifactDeletion(ctx, key); err != nil || eligible {
			t.Fatalf("GC claimed held artifact %q: eligible %v, error %v", key, eligible, err)
		}
	}
	preview := readProjectRollbackAPI(t, e, http.MethodGet, path+"/undo/preview", nil, http.StatusOK)
	if preview.Status != "planned" || len(preview.Steps) != 1 || preview.Steps[0].UndoArtifacts == nil ||
		preview.Steps[0].UndoArtifacts.DeploymentID != outgoing.ID || preview.Steps[0].UndoArtifacts.ExpiresAt == nil ||
		!hold.ExpiresAt.Equal(*preview.Steps[0].UndoArtifacts.ExpiresAt) {
		t.Fatalf("reviewed undo preview = %+v", preview)
	}
	req := api.ProjectRollbackUndoRequest{ExpectedPlanHash: preview.PlanHash}
	undo := readProjectRollbackAPI(t, e, http.MethodPost, path+"/undo", req, http.StatusAccepted)
	if hold := undo.Steps[0].UndoArtifacts; hold == nil || hold.DeploymentID != outgoing.ID || hold.ExpiresAt != nil {
		t.Fatalf("running undo hold = %+v", hold)
	}
	sweepProjectRollbackForTest(t, e)
	if err := e.store.MarkDeploymentLive(ctx, outgoing.ID); err != nil {
		t.Fatal(err)
	}
	checkProjectRollbackForTest(t, e)
	sweepProjectRollbackForTest(t, e)
	checkProjectRollbackForTest(t, e)
	sweepProjectRollbackForTest(t, e)
	finished := readProjectRollbackAPI(t, e, http.MethodGet, root+"/"+undo.ID, nil, http.StatusOK)
	if finished.Status != "complete" || finished.Steps[0].DeploymentID != outgoing.ID {
		t.Fatalf("undo did not restore the held deployment: %+v", finished)
	}
	if holds, err := e.store.ProjectRollbackUndoArtifactHolds(ctx, undo.ID); err != nil || len(holds) != 0 {
		t.Fatalf("completed undo retained holds: %+v, %v", holds, err)
	}
}

func TestProjectRollbackReviewedUndoRejectsNewReleaseRuns(t *testing.T) {
	e, source, targets, root := projectRollbackPausedUndoFixture(t)
	path := root + "/" + source.ID
	previewPath := path + "/undo/preview"
	acknowledgedPath := previewPath + "?acknowledge_releases=true"
	ctx := t.Context()
	preview := readProjectRollbackAPI(t, e, http.MethodGet, acknowledgedPath, nil, http.StatusOK)
	if preview.Status != "planned" || preview.ID != "" || preview.UndoOf != source.ID ||
		!api.ValidProjectRollbackPlanHash(preview.PlanHash) || len(preview.Steps) != 1 {
		t.Fatalf("undo preview = %+v", preview)
	}
	step := preview.Steps[0]
	if step.CurrentID != targets[0].ID || step.TargetID != source.Steps[0].CurrentID ||
		step.CurrentServedAt == nil || len(step.CrossedReleases) != 0 {
		t.Fatalf("undo preview step = %+v", step)
	}
	assertNoUndo := func() {
		t.Helper()
		got := readProjectRollbackAPI(t, e, http.MethodGet, path, nil, http.StatusOK)
		if got.UndoOperationID != "" {
			t.Fatalf("stale plan created a linked undo: %+v", got)
		}
		rec := e.do(t, http.MethodGet, root, nil, nil)
		var history api.ProjectRollbackHistory
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &history) != nil || len(history.Operations) != 1 {
			t.Fatalf("expected only the source receipt: %d %s", rec.Code, rec.Body)
		}
		for i, want := range []string{targets[0].ID, source.Steps[1].CurrentID} {
			current, problem := e.s.projectRollbackCurrent(ctx, targets[i].AppID)
			if problem != nil || current.ID != want || current.TrafficPercent != 100 {
				t.Fatalf("serving revision for step %d = %+v, %v", i, current, problem)
			}
		}
	}
	assertProblem := func(method, path string, req any, code string) {
		t.Helper()
		rec := e.do(t, method, path, req, nil)
		var problem api.Problem
		if rec.Code != http.StatusConflict || json.Unmarshal(rec.Body.Bytes(), &problem) != nil || problem.Code != code {
			t.Fatalf("%s %s = %d %s", method, path, rec.Code, rec.Body)
		}
	}
	assertNoUndo()
	firstRun := runProjectRollbackTestRelease(t, e, step.AppID)
	req := api.ProjectRollbackUndoRequest{AcknowledgeReleases: true, ExpectedPlanHash: preview.PlanHash}
	assertProblem(http.MethodPost, path+"/undo", req, api.CodeProjectRollbackPlanChanged)
	assertNoUndo()

	blocked := readProjectRollbackAPI(t, e, http.MethodGet, previewPath, nil, http.StatusOK)
	if blocked.Status != "blocked" || blocked.PlanHash != "" || len(blocked.Steps) != 1 ||
		blocked.Steps[0].Problem == nil || blocked.Steps[0].Problem.Code != api.CodeRollbackCrossesRelease {
		t.Fatalf("unacknowledged preview = %+v", blocked)
	}
	crossed := blocked.Steps[0].CrossedReleases
	if len(crossed) != 1 || crossed[0].DeploymentID != firstRun.DeploymentID || crossed[0].Status != string(firstRun.Status) ||
		crossed[0].StartedAt != firstRun.StartedAt.UTC().Format(time.RFC3339Nano) {
		t.Fatalf("unacknowledged crossings = %+v, wanted %+v", crossed, firstRun)
	}
	fresh := readProjectRollbackAPI(t, e, http.MethodGet, acknowledgedPath, nil, http.StatusOK)
	if fresh.Status != "planned" || !api.ValidProjectRollbackPlanHash(fresh.PlanHash) || fresh.PlanHash == preview.PlanHash ||
		len(fresh.Steps) != 1 || !slices.Equal(fresh.Steps[0].CrossedReleases, crossed) {
		t.Fatalf("fresh acknowledged preview = %+v", fresh)
	}
	req.ExpectedPlanHash = fresh.PlanHash
	originalStore := e.s.store
	e.s.store = &projectRollbackUndoAdmissionTestStore{
		projectRollbackHealthTestStore: originalStore.(*projectRollbackHealthTestStore),
		beforeAdmission:                func() { runProjectRollbackTestRelease(t, e, step.AppID) },
	}
	assertProblem(http.MethodPost, path+"/undo", req, api.CodeProjectRollbackPlanChanged)
	assertNoUndo()
	e.s.store = originalStore

	latest := readProjectRollbackAPI(t, e, http.MethodGet, acknowledgedPath, nil, http.StatusOK)
	if latest.Status != "planned" || latest.PlanHash == fresh.PlanHash || len(latest.Steps) != 1 ||
		len(latest.Steps[0].CrossedReleases) != 2 {
		t.Fatalf("preview after admission race = %+v", latest)
	}
	req.ExpectedPlanHash = latest.PlanHash
	undo := readProjectRollbackAPI(t, e, http.MethodPost, path+"/undo", req, http.StatusAccepted)
	replay := readProjectRollbackAPI(t, e, http.MethodPost, path+"/undo", req, http.StatusAccepted)
	if undo.ID == "" || undo.UndoOf != source.ID || undo.PlanHash != latest.PlanHash || replay.ID != undo.ID || replay.PlanHash != undo.PlanHash {
		t.Fatalf("accepted undo = %+v, replay = %+v", undo, replay)
	}
	assertProblem(http.MethodGet, acknowledgedPath, nil, api.CodeProjectRollbackUndoStarted)
	req.AcknowledgeReleases = false
	assertProblem(http.MethodPost, path+"/undo", req, api.CodeProjectRollbackPlanChanged)
	req.AcknowledgeReleases, req.ExpectedPlanHash = true, fresh.PlanHash
	assertProblem(http.MethodPost, path+"/undo", req, api.CodeProjectRollbackPlanChanged)
}

type projectRollbackUndoAdmissionTestStore struct {
	*projectRollbackHealthTestStore
	beforeAdmission func()
}

func (s *projectRollbackUndoAdmissionTestStore) CreateProjectRollbackUndo(ctx context.Context, account string, source, undo state.ProjectRollbackRecord) (state.ProjectRollbackRecord, error) {
	s.beforeAdmission()
	return s.MemStore.CreateProjectRollbackUndo(ctx, account, source, undo)
}

func runProjectRollbackTestRelease(t *testing.T, e testEnv, appID string) state.AppTask {
	t.Helper()
	ctx := t.Context()
	d, err := e.store.CreateDeployment(ctx, state.Deployment{
		AppID: appID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + uuid.NewString(),
		Status: state.DeploySnapshotting,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.SetDeploymentRootfs(ctx, d.ID, "/layers/"+d.ID, "layers/"+d.ID, 4096); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	task, err := e.store.CreateAppTask(ctx, state.CreateAppTaskParams{
		AccountID: e.acct.ID, AppID: appID, DeploymentID: d.ID, Kind: state.AppTaskKindRelease,
		Command: []string{"bin/migrate"}, CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := e.store.ClaimNextAppTask(ctx, "schedd", now.Add(time.Second), time.Minute)
	if err != nil || claimed.ID != task.ID || claimed.LeaseToken == nil {
		t.Fatalf("claim release = %+v, %v", claimed, err)
	}
	running, err := e.store.MarkAppTaskRunning(ctx, task.ID, *claimed.LeaseToken, now.Add(2*time.Second))
	if err != nil || running.StartedAt == nil {
		t.Fatalf("start release = %+v, %v", running, err)
	}
	exit := 0
	finished, err := e.store.CompleteAppTask(ctx, state.CompleteAppTaskParams{
		ID: task.ID, LeaseToken: *claimed.LeaseToken, Status: state.AppTaskSucceeded,
		ExitCode: &exit, FinishedAt: running.StartedAt.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	return finished
}

func TestProjectRollbackUndoRejectsNewerServingIntent(t *testing.T) {
	for _, returnToRestored := range []bool{false, true} {
		name := "newer revision"
		if returnToRestored {
			name = "same revision with newer serving period"
		}
		t.Run(name, func(t *testing.T) {
			e, source, targets, root := projectRollbackPausedUndoFixture(t)
			web := projectRollbackDeploy(t, e.store, targets[0].AppID, "")
			expected := web.ID
			if returnToRestored {
				if _, err := e.store.CreateCheckedRollback(t.Context(), e.acct.ID, web.AppID, targets[0].ID, web.ID, ""); err != nil {
					t.Fatal(err)
				}
				if err := e.store.MarkDeploymentLive(t.Context(), targets[0].ID); err != nil {
					t.Fatal(err)
				}
				checkProjectRollbackForTest(t, e)
				expected = targets[0].ID
			}
			rec := e.do(t, http.MethodPost, root+"/"+source.ID+"/undo", api.ProjectRollbackUndoRequest{}, nil)
			var problem api.Problem
			if rec.Code != http.StatusConflict || json.Unmarshal(rec.Body.Bytes(), &problem) != nil || problem.Code != "rollback_deployment_changed" {
				t.Fatalf("newer serving intent did not block undo: %d %s", rec.Code, rec.Body)
			}
			for i, want := range []string{expected, source.Steps[1].CurrentID} {
				current, problem := e.s.projectRollbackCurrent(t.Context(), targets[i].AppID)
				if problem != nil || current.ID != want || current.TrafficPercent != 100 {
					t.Fatalf("wrong serving revision for step %d: %+v %v", i, current, problem)
				}
			}
			if got := readProjectRollbackAPI(t, e, http.MethodGet, root+"/"+source.ID, nil, http.StatusOK); got.UndoOperationID != "" {
				t.Fatalf("a conflicting undo was accepted: %+v", got)
			}
		})
	}
}

func TestProjectRollbackPauseFencesCutovers(t *testing.T) {
	for _, tc := range []struct {
		name        string
		cutoverWins bool
		legacy      bool
	}{
		{name: "pause first"},
		{name: "cutover first", cutoverWins: true},
		{name: "legacy checked child", legacy: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, p, targets := projectRollbackAPIFixture(t)
			ctx := t.Context()
			root := "/v1/projects/" + p.Slug + "/rollbacks"
			op := readProjectRollbackAPI(t, e, http.MethodPost, root, api.ProjectRollbackRequest{}, http.StatusAccepted)
			path := root + "/" + op.ID
			if tc.legacy {
				claim, err := e.store.ClaimProjectRollback(ctx, time.Minute)
				if err != nil {
					t.Fatal(err)
				}
				claim.Phase, claim.PreparationDeadline = "", nil
				if err := e.store.SaveProjectRollback(ctx, claim, 0); err != nil {
					t.Fatal(err)
				}
			}
			sweepProjectRollbackForTest(t, e)
			if tc.legacy {
				markProjectRollbackTargetsLive(t, e, targets[:1])
			} else {
				markProjectRollbackTargetsLive(t, e, targets)
				checkProjectRollbackForTest(t, e)
				sweepProjectRollbackForTest(t, e)
			}
			if tc.cutoverWins {
				checkProjectRollbackForTest(t, e)
			}
			requested := readProjectRollbackAPI(t, e, http.MethodPost, path+"/pause", nil, http.StatusAccepted)
			if requested.Phase != api.ProjectRollbackReleasing || requested.PauseRequestedAt == nil {
				t.Fatalf("missing pause checkpoint: %+v", requested)
			}

			// The operation's durable checkpoint must survive loss of the
			// controller that accepted it, before any cleanup is done.
			e.s = newServer(e.store, e.s.log, "gregale.dev", noopNotifier{}).
				WithRollbackArtifactVerifier(stubRollbackArtifactVerifier{})
			e.h = e.s.handler()
			checkProjectRollbackForTest(t, e)
			sweepProjectRollbackForTest(t, e)
			paused := readProjectRollbackAPI(t, e, http.MethodPost, path+"/pause", nil, http.StatusOK)
			if paused.Status != "blocked" || paused.PauseRequestedAt == nil || !paused.PauseRequestedAt.Equal(*requested.PauseRequestedAt) ||
				paused.Steps[1].PreparationStatus != "released" {
				t.Fatalf("pause did not settle or retained unused preparation: %+v", paused)
			}
			wantWeb := op.Steps[0].CurrentID
			if tc.cutoverWins {
				wantWeb = targets[0].ID
				if paused.Steps[0].Status != "rolled_back" {
					t.Fatalf("lost the committed cutover: %+v", paused)
				}
			} else if paused.Steps[0].Problem == nil || paused.Steps[0].Problem.Code != api.CodeProjectRollbackPaused {
				t.Fatalf("missing pause reason: %+v", paused)
			}
			for i, want := range []string{wantWeb, op.Steps[1].CurrentID} {
				current, problem := e.s.projectRollbackCurrent(ctx, targets[i].AppID)
				if problem != nil || current.ID != want || current.TrafficPercent != 100 {
					t.Fatalf("step %d serving = %+v, %v, want %s", i, current, problem, want)
				}
			}
			if tc.cutoverWins {
				undo := readProjectRollbackAPI(t, e, http.MethodPost, path+"/undo", api.ProjectRollbackUndoRequest{}, http.StatusAccepted)
				if len(undo.Steps) != 1 || undo.Steps[0].TargetID != op.Steps[0].CurrentID {
					t.Fatalf("wrong undo plan for a partially applied pause: %+v", undo)
				}
				undoPath := root + "/" + undo.ID
				readProjectRollbackAPI(t, e, http.MethodPost, undoPath+"/pause", nil, http.StatusAccepted)
				sweepProjectRollbackForTest(t, e)
				resumed := readProjectRollbackAPI(t, e, http.MethodPost, undoPath+"/resume", nil, http.StatusOK)
				if resumed.Status != "running" || resumed.PauseRequestedAt != nil || resumed.UndoOf != op.ID {
					t.Fatalf("resume did not clear the undo pause: %+v", resumed)
				}
			} else {
				resumed := readProjectRollbackAPI(t, e, http.MethodPost, path+"/resume", nil, http.StatusOK)
				if resumed.Status != "running" || resumed.PauseRequestedAt != nil || resumed.Phase != api.ProjectRollbackPreparing ||
					resumed.Steps[0].AttemptID == op.Steps[0].AttemptID || resumed.Steps[0].CurrentID != op.Steps[0].CurrentID {
					t.Fatalf("resume did not clear the pause and keep the serving pin: %+v", resumed)
				}
			}
		})
	}
}

func TestProjectRollbackHistoryFindsTheActiveReceipt(t *testing.T) {
	e, p, _ := projectRollbackAPIFixture(t)
	ctx := t.Context()
	path := "/v1/projects/" + p.Slug + "/rollbacks"
	read := func() api.ProjectRollbackHistory {
		t.Helper()
		res := e.do(t, http.MethodGet, path, nil, nil)
		var history api.ProjectRollbackHistory
		if res.Code != http.StatusOK || json.Unmarshal(res.Body.Bytes(), &history) != nil ||
			history.ProjectID != p.ID || history.Project != p.Slug || history.Operations == nil ||
			res.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("history: %d %s", res.Code, res.Body)
		}
		return history
	}
	if got := read(); len(got.Operations) != 0 {
		t.Fatalf("empty project history: %+v", got)
	}

	active := readProjectRollbackAPI(t, e, http.MethodPost, path, api.ProjectRollbackRequest{}, http.StatusAccepted)
	now := active.CreatedAt.Add(time.Minute)
	sourceID, undoID := uuid.NewString(), uuid.NewString()
	for _, op := range []api.ProjectRollbackOperation{
		{ID: sourceID, ProjectID: p.ID, Project: p.Slug, Status: "blocked", PauseRequestedAt: &now,
			CreatedAt: &now, UndoOperationID: undoID, Steps: []api.ProjectRollbackStep{{Status: "rolled_back"}, {Status: "blocked"}, {Status: "skipped"}}},
		{ID: undoID, ProjectID: p.ID, Project: p.Slug, Status: "complete", CreatedAt: &now,
			UndoOf: sourceID, Steps: []api.ProjectRollbackStep{{Status: "rolled_back"}}},
	} {
		if err := e.store.CreateProjectRollback(ctx, e.acct.ID, state.ProjectRollbackRecord{ProjectRollbackOperation: op}); err != nil {
			t.Fatal(err)
		}
	}
	sweepProjectRollbackForTest(t, e)
	history := read()
	if len(history.Operations) != 3 || history.Operations[0].ID != active.ID || history.Operations[0].Status != "running" ||
		history.Operations[0].CutoversCompleted != 0 || history.Operations[0].CutoversTotal != 2 {
		t.Fatalf("active receipt was not first: %+v", history)
	}
	for _, op := range history.Operations[1:] {
		switch op.ID {
		case sourceID:
			if op.UndoOperationID != undoID || op.CutoversTotal != 2 || op.CutoversCompleted != 1 || op.PauseRequestedAt == nil {
				t.Fatalf("wrong paused rollback summary: %+v", op)
			}
		case undoID:
			if op.UndoOf != sourceID || op.CutoversTotal != 1 || op.CutoversCompleted != 1 {
				t.Fatalf("wrong undo summary: %+v", op)
			}
		default:
			t.Fatalf("unexpected receipt: %+v", op)
		}
	}
	conflict := e.do(t, http.MethodPost, path, api.ProjectRollbackRequest{}, nil)
	var problem api.Problem
	wantHint := "Inspect it with gregale project rollback " + p.Slug + " --status " + active.ID
	if conflict.Code != http.StatusConflict || json.Unmarshal(conflict.Body.Bytes(), &problem) != nil ||
		problem.Code != api.CodeProjectRollbackConflict || problem.ActiveOperationID != active.ID || problem.Hint != wantHint {
		t.Fatalf("start conflict did not point to the active operation: %d %s", conflict.Code, conflict.Body)
	}
	if receipt := readProjectRollbackAPI(t, e, http.MethodGet, path+"/"+problem.ActiveOperationID, nil, http.StatusOK); receipt.ID != active.ID {
		t.Fatalf("wrong operation after history lookup: %+v", receipt)
	}
}

func TestProjectRollbackStartRequestReplaysCompletedReceiptAfterRestart(t *testing.T) {
	e, p, targets := projectRollbackAPIFixture(t)
	root := "/v1/projects/" + p.Slug + "/rollbacks"
	preview := readProjectRollbackAPI(t, e, http.MethodGet, root+"/preview", nil, http.StatusOK)
	if !api.ValidProjectRollbackPlanHash(preview.PlanHash) {
		t.Fatalf("preview did not return a plan hash: %+v", preview)
	}
	req := api.ProjectRollbackRequest{RequestID: uuid.NewString(), ExpectedPlanHash: preview.PlanHash}
	headers := map[string]string{"Idempotency-Key": req.RequestID}
	// The first response is lost to the operator. Find the durable receipt
	// through history and complete it on a replacement worker.
	lost := e.do(t, http.MethodPost, root, req, headers)
	if lost.Code != http.StatusAccepted {
		t.Fatalf("start: %d %s", lost.Code, lost.Body)
	}
	status := e.do(t, http.MethodGet, root, nil, nil)
	var history api.ProjectRollbackHistory
	if status.Code != http.StatusOK || json.Unmarshal(status.Body.Bytes(), &history) != nil || len(history.Operations) != 1 {
		t.Fatalf("history: %d %s", status.Code, status.Body)
	}
	original := history.Operations[0].ID
	e.s = newServer(e.store, e.s.log, "gregale.dev", noopNotifier{}).
		WithRollbackArtifactVerifier(stubRollbackArtifactVerifier{})
	e.h = e.s.handler()
	sweepProjectRollbackForTest(t, e)
	markProjectRollbackTargetsLive(t, e, targets)
	checkProjectRollbackForTest(t, e)
	sweepProjectRollbackForTest(t, e)
	checkProjectRollbackForTest(t, e)
	sweepProjectRollbackForTest(t, e)
	checkProjectRollbackForTest(t, e)
	sweepProjectRollbackForTest(t, e)
	done := readProjectRollbackAPI(t, e, http.MethodGet, root+"/"+original, nil, http.StatusOK)
	if done.Status != "complete" || done.RequestID != req.RequestID || done.PlanHash != preview.PlanHash {
		t.Fatalf("original receipt did not complete: %+v", done)
	}
	for _, key := range []string{req.RequestID, uuid.NewString()} {
		replay := e.do(t, http.MethodPost, root, req, map[string]string{"Idempotency-Key": key})
		var op api.ProjectRollbackOperation
		if replay.Code != http.StatusOK || json.Unmarshal(replay.Body.Bytes(), &op) != nil ||
			op.ID != original || op.Status != "complete" || op.RequestID != req.RequestID || op.PlanHash != preview.PlanHash {
			t.Fatalf("retry did not return the current receipt: %d %s", replay.Code, replay.Body)
		}
	}
	req.ExpectedPlanHash = strings.Repeat("0", 64)
	conflict := e.do(t, http.MethodPost, root, req, headers)
	var problem api.Problem
	if conflict.Code != http.StatusConflict || json.Unmarshal(conflict.Body.Bytes(), &problem) != nil ||
		problem.Code != api.CodeProjectRollbackRequestReused {
		t.Fatalf("changed options did not conflict: %d %s", conflict.Code, conflict.Body)
	}
	items, err := e.store.ListProjectRollbacks(t.Context(), e.acct.ID, p.ID)
	if err != nil || len(items) != 1 || items[0].ID != original {
		t.Fatalf("retry submitted another operation: %+v, %v", items, err)
	}
}

func TestProjectRollbackRejectsChangedReviewedPlanBeforeCreatingReceipt(t *testing.T) {
	e, p, targets := projectRollbackAPIFixture(t)
	root := "/v1/projects/" + p.Slug + "/rollbacks"
	preview := func() api.ProjectRollbackOperation {
		return readProjectRollbackAPI(t, e, http.MethodGet, root+"/preview", nil, http.StatusOK)
	}
	original := preview()
	if !api.ValidProjectRollbackPlanHash(original.PlanHash) || preview().PlanHash != original.PlanHash {
		t.Fatalf("unchanged previews should return the same plan hash: %+v", original)
	}
	newGroup := uuid.NewString()
	for _, target := range targets {
		projectRollbackDeploy(t, e.store, target.AppID, newGroup)
	}
	req := api.ProjectRollbackRequest{RequestID: uuid.NewString(), ExpectedPlanHash: original.PlanHash}
	rec := e.do(t, http.MethodPost, root, req, nil)
	var problem api.Problem
	if rec.Code != http.StatusConflict || json.Unmarshal(rec.Body.Bytes(), &problem) != nil ||
		problem.Code != api.CodeProjectRollbackPlanChanged {
		t.Fatalf("stale preview accepted: %d %s", rec.Code, rec.Body)
	}
	items, err := e.store.ListProjectRollbacks(t.Context(), e.acct.ID, p.ID)
	if err != nil || len(items) != 0 {
		t.Fatalf("stale preview left a receipt: %+v %v", items, err)
	}
	req.ExpectedPlanHash = "invalid"
	if rec := e.do(t, http.MethodPost, root, req, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid plan hash accepted: %d %s", rec.Code, rec.Body)
	}
	fresh := preview()
	if fresh.DeployGroupID != newGroup || fresh.PlanHash == original.PlanHash {
		t.Fatalf("preview missed the new deploy: %+v", fresh)
	}
	req.ExpectedPlanHash = fresh.PlanHash
	accepted := readProjectRollbackAPI(t, e, http.MethodPost, root, req, http.StatusAccepted)
	if accepted.ID == "" || accepted.PlanHash != fresh.PlanHash || accepted.DeployGroupID != newGroup {
		t.Fatalf("fresh preview did not apply the reviewed deploy: %+v", accepted)
	}
}

func TestProjectRollbackPlanHashBindsReleaseRunsAndProbes(t *testing.T) {
	plan := state.ProjectRollbackRecord{
		ProjectRollbackOperation: api.ProjectRollbackOperation{
			ProjectID: "project", Verify: true, AcknowledgeReleases: true,
			Steps: []api.ProjectRollbackStep{
				{AppID: "api", Status: "planned", CurrentID: "current", TargetID: "target"},
			},
		},
		ReleaseReviews: map[string][]state.ReleaseRun{
			"api": {{DeploymentID: "release", StartedAt: time.Unix(123, 0).UTC()}},
		},
		HealthProbes: map[string][]api.ProjectReleaseProbe{
			"api": {{AppID: "api", Method: http.MethodGet, Path: "/ready"}},
		},
	}
	hash := func(p state.ProjectRollbackRecord) string {
		t.Helper()
		value, err := projectRollbackPlanHash(p, api.ProjectRollbackRequest{All: true})
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	original := hash(plan)
	plan.Steps[0].Warnings = []api.BindingCheckFinding{{Message: "the target is still warming"}}
	if original != hash(plan) {
		t.Fatal("an observation changed the plan hash")
	}
	plan.HealthProbes["api"][0].Path = "/healthy"
	if original == hash(plan) {
		t.Fatal("a changed health probe left the plan hash unchanged")
	}
	plan.HealthProbes["api"][0].Path = "/ready"
	plan.ReleaseReviews["api"][0].StartedAt = time.Unix(124, 0).UTC()
	if original == hash(plan) {
		t.Fatal("a different release execution left the plan hash unchanged")
	}
}

func TestProjectRollbackLegacyIdempotencyKeyStillReplays(t *testing.T) {
	e, p, _ := projectRollbackAPIFixture(t)
	root := "/v1/projects/" + p.Slug + "/rollbacks"
	headers := map[string]string{"Idempotency-Key": uuid.NewString()}
	for i := 0; i < 2; i++ {
		res := e.do(t, http.MethodPost, root, api.ProjectRollbackRequest{}, headers)
		if res.Code != http.StatusAccepted {
			t.Fatalf("legacy request %d: %d %s", i, res.Code, res.Body)
		}
	}
	items, err := e.store.ListProjectRollbacks(t.Context(), e.acct.ID, p.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("legacy replay submitted another operation: %+v %v", items, err)
	}
}

func TestProjectRollbackRestoresSelectedDeployAcrossTwoNewerReleases(t *testing.T) {
	e, p, targets := projectRollbackAPIFixture(t)
	ctx := t.Context()
	group := targets[0].ProjectDeployGroupID
	newGroup := uuid.NewString()
	for _, target := range targets {
		projectRollbackDeploy(t, e.store, target.AppID, newGroup)
	}
	other, err := e.store.CreateApp(ctx, state.App{AccountID: e.acct.ID, ProjectID: p.ID, Slug: "rollback-shop-other",
		WorkloadName: "other", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30})
	if err != nil {
		t.Fatal(err)
	}
	otherDeploy := projectRollbackDeploy(t, e.store, other.ID, "")
	root := "/v1/projects/" + p.Slug + "/rollbacks"
	res := e.do(t, http.MethodGet, root+"/targets", nil, nil)
	var listed api.ProjectRollbackTargets
	if err := json.Unmarshal(res.Body.Bytes(), &listed); err != nil || res.Code != http.StatusOK ||
		len(listed.Deploys) != 3 || listed.Deploys[0].DeployGroupID != newGroup || listed.Deploys[2].DeployGroupID != group {
		t.Fatalf("list recorded deploys: %d %s (%v)", res.Code, res.Body, err)
	}
	previewURL := root + "/preview?to_deploy=" + group
	preview := readProjectRollbackAPI(t, e, http.MethodGet, previewURL, nil, http.StatusOK)
	if preview.DeployGroupID != group || preview.PlanHash == "" || len(preview.Steps) != 3 {
		t.Fatalf("unexpected reviewed target: %+v", preview)
	}
	byApp := make(map[string]api.ProjectRollbackStep)
	for _, step := range preview.Steps {
		byApp[step.AppID] = step
	}
	for _, target := range targets {
		if step := byApp[target.AppID]; step.From != 3 || step.To != 1 || step.TargetID != target.ID || step.Status != "planned" {
			t.Fatalf("expected direct v3 to v1 restoration: %+v", step)
		}
	}
	if step := byApp[other.ID]; step.Status != "skipped" || step.TargetID != "" {
		t.Fatalf("selected deploy must leave other services alone: %+v", step)
	}
	if res := e.do(t, http.MethodGet, previewURL+"&all=true", nil, nil); res.Code != http.StatusBadRequest {
		t.Fatalf("combined selectors: %d %s", res.Code, res.Body)
	}
	target, err := e.store.DeploymentByID(ctx, targets[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	e.s.rollbackArtifactVerifier = missingProjectRollbackArtifact(target.RootfsKey)
	blocked := readProjectRollbackAPI(t, e, http.MethodPost, root, api.ProjectRollbackRequest{ToDeploy: group}, http.StatusOK)
	if blocked.Status != "blocked" || blocked.ID != "" || blocked.PlanHash != "" {
		t.Fatalf("a missing artifact admitted a rollback: %+v", blocked)
	}
	e.s.rollbackArtifactVerifier = stubRollbackArtifactVerifier{}
	req := api.ProjectRollbackRequest{ToDeploy: group, ExpectedPlanHash: preview.PlanHash, RequestID: uuid.NewString()}
	op := readProjectRollbackAPI(t, e, http.MethodPost, root, req, http.StatusAccepted)
	replay := readProjectRollbackAPI(t, e, http.MethodPost, root, req, http.StatusAccepted)
	if op.ID == "" || op.ID != replay.ID || op.PlanHash != preview.PlanHash || op.DeployGroupID != group {
		t.Fatalf("wrong accepted plan or replay: %+v %+v", op, replay)
	}
	req.ToDeploy = newGroup
	changed := e.do(t, http.MethodPost, root, req, nil)
	var problem api.Problem
	if changed.Code != http.StatusConflict || json.Unmarshal(changed.Body.Bytes(), &problem) != nil ||
		problem.Code != api.CodeProjectRollbackRequestReused {
		t.Fatalf("changed request target did not conflict: %d %s", changed.Code, changed.Body)
	}
	sweepProjectRollbackForTest(t, e)
	markProjectRollbackTargetsLive(t, e, targets)
	for range 3 {
		checkProjectRollbackForTest(t, e)
		sweepProjectRollbackForTest(t, e)
	}
	done := readProjectRollbackAPI(t, e, http.MethodGet, root+"/"+op.ID, nil, http.StatusOK)
	if done.Status != "complete" {
		t.Fatalf("direct rollback did not complete: %+v", done)
	}
	for _, target := range targets {
		current, problem := e.s.projectRollbackCurrent(ctx, target.AppID)
		if problem != nil || current.ID != target.ID || current.TrafficPercent != 100 {
			t.Fatalf("selected revision did not receive traffic: %+v %v", current, problem)
		}
	}
	live, err := e.store.LiveDeployments(ctx, other.ID)
	if err != nil || len(live) != 1 || live[0].ID != otherDeploy.ID {
		t.Fatalf("outside service changed: %+v %v", live, err)
	}
}

func TestProjectRollbackBlocksARecordedDeployThatNeverServed(t *testing.T) {
	e, p, targets := projectRollbackAPIFixture(t)
	neverServed := uuid.NewString()
	_, err := e.store.CreateDeployment(t.Context(), state.Deployment{
		AppID: targets[0].AppID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + uuid.NewString(),
		ProjectDeployGroupID: neverServed, TrafficPercent: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	root := "/v1/projects/" + p.Slug + "/rollbacks"
	preview := readProjectRollbackAPI(t, e, http.MethodGet, root+"/preview?to_deploy="+neverServed, nil, http.StatusOK)
	if preview.Status != "blocked" || preview.PlanHash != "" || preview.Steps[0].Status != "blocked" {
		t.Fatalf("unserved revision can be selected: %+v", preview)
	}
	response := e.do(t, http.MethodGet, root+"/targets", nil, nil)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), neverServed) {
		t.Fatalf("unserved group in targets: %d %s", response.Code, response.Body)
	}
}

func TestProjectRollbackProtectsAllArtifactsOrNone(t *testing.T) {
	e, p, old := projectRollbackAPIFixture(t)
	group := old[0].ProjectDeployGroupID
	sidecar := "layers/" + uuid.NewString()
	if _, err := e.store.SetDeploymentSidecarLayer(t.Context(), state.DeploymentSidecarLayer{
		DeploymentID: old[1].ID, SidecarName: "cache", StorageKey: sidecar, Bytes: 2048,
	}); err != nil {
		t.Fatal(err)
	}
	root := "/v1/projects/" + p.Slug + "/rollbacks/targets"
	path := root + "/" + group + "/protection"
	req := api.ProtectProjectRollbackDeployRequest{TTLSeconds: 3600}
	e.s.rollbackArtifactVerifier = missingProjectRollbackArtifact(sidecar)
	blocked := e.do(t, http.MethodPost, path, req, nil)
	if blocked.Code != http.StatusConflict || !strings.Contains(blocked.Body.String(), "rollback-shop-api") {
		t.Fatalf("protection did not name the missing sidecar owner: %d %s", blocked.Code, blocked.Body)
	}
	list := func() *api.ProjectRollbackProtection {
		t.Helper()
		res := e.do(t, http.MethodGet, root, nil, nil)
		var out api.ProjectRollbackTargets
		if res.Code != http.StatusOK || json.Unmarshal(res.Body.Bytes(), &out) != nil {
			t.Fatalf("targets: %d %s", res.Code, res.Body)
		}
		for _, target := range out.Deploys {
			if target.DeployGroupID == group {
				return target.Protection
			}
		}
		t.Fatal("known deploy missing")
		return nil
	}
	if hold := list(); hold != nil {
		t.Fatalf("failed verification left a partial protection: %+v", hold)
	}
	e.s.rollbackArtifactVerifier = stubRollbackArtifactVerifier{}
	res := e.do(t, http.MethodPost, path, req, nil)
	var hold api.ProjectRollbackProtection
	if res.Code != http.StatusOK || json.Unmarshal(res.Body.Bytes(), &hold) != nil ||
		hold.DeployGroupID != group || hold.LayerBytes != 10240 || !hold.ExpiresAt.After(time.Now()) {
		t.Fatalf("invalid protection: %d %s", res.Code, res.Body)
	}
	if got := list(); got == nil || !got.ExpiresAt.Equal(hold.ExpiresAt) || got.LayerBytes != hold.LayerBytes {
		t.Fatalf("targets did not show the accepted protection: %+v", got)
	}
	req.TTLSeconds = 1
	res = e.do(t, http.MethodPost, path, req, nil)
	if got := list(); res.Code != http.StatusOK || got == nil || !got.ExpiresAt.Equal(hold.ExpiresAt) {
		t.Fatalf("repeat shortened the hold: %d %+v", res.Code, got)
	}
}

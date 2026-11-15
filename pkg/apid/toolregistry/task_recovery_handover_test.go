package toolregistry_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/taskexecution"
	"github.com/onebox-faas/faas/pkg/apid/toolregistry"
	"github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/tasktoolrelease"
)

func TestMCPTaskRecoveryHandoverMemory(t *testing.T) {
	s := state.NewMemStore()
	exerciseTaskRecoveryHandover(t, s, func() state.Store { return s })
}
func TestMCPTaskRecoveryHandoverPostgres(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	config := pool.Config()
	config.MaxConns = 2
	limited, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer limited.Close()
	exerciseTaskRecoveryHandover(t, state.NewPgStore(limited), func() state.Store { return state.NewPgStore(limited) })
}

func exerciseTaskRecoveryHandover(t *testing.T, s state.Store, reopen func() state.Store) {
	t.Helper()
	ctx := t.Context()
	account, err := s.CreateAccount(ctx, "task-handover@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	key := func(label string, scopes ...string) state.APIKey {
		sum := sha256.Sum256([]byte(label))
		k, err := s.CreateAPIKey(ctx, account.ID, sum[:], label, scopes)
		if err != nil {
			t.Fatal(err)
		}
		return k
	}
	publisher, owner := key("requester", api.ScopeAgent, api.ScopeDeployWrite), key("owner", api.ScopeAdmin)
	intent := api.TaskToolReleaseIntent{ReleaseFingerprint: strings.Repeat("a", 64), WorkerRuntime: "node", Protocol: api.MCPProtocolVersion, Endpoint: "/mcp", AccessMode: "open", ObserverMetricApp: "observer", ToolScopes: map[string][]string{"report": {}}, RequiredSetup: tasktoolrelease.RequiredSetup(), Reason: "Recovery handover fixture"}
	var web, worker, baseline string
	deploy := func(appID string, traffic int) state.Deployment {
		d, err := s.CreateDeployment(ctx, state.Deployment{AppID: appID, Kind: state.DeploymentKindTarball, Status: state.DeployLive, TrafficPercent: traffic, SourceSHA256: strings.Repeat("b", 64), SourcePath: "/fixture/source.tar.gz", SourceBytes: 1})
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	for _, role := range []string{"web", "worker", "observer"} {
		app, err := s.CreateApp(ctx, state.App{AccountID: account.ID, Slug: role, Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30})
		if err != nil {
			t.Fatal(err)
		}
		if role == "web" {
			web = app.ID
			baseline = deploy(web, 100).ID
		}
		if role == "worker" {
			worker = app.ID
		}
		review, err := s.(state.ReleaseReviewStore).ReadReleaseReview(ctx, app.ID, api.DefaultEnvScope)
		if err != nil {
			t.Fatal(err)
		}
		intent.Apps = append(intent.Apps, api.TaskToolReleaseApp{Role: role, App: role, Review: review})
	}
	journal := s.(state.TaskToolReleaseIntentStore)
	r, err := journal.PrepareTaskToolReleaseIntent(ctx, account.ID, publisher.ID, strings.Repeat("c", 64), intent)
	if err != nil {
		t.Fatal(err)
	}
	decision := api.TaskToolReleaseIntentDecision{IntentSHA256: r.IntentSHA256, ReleaseFingerprint: intent.ReleaseFingerprint, Decision: "approve"}
	r, err = journal.ChangeTaskToolReleaseIntent(ctx, account.ID, r.ID, "decide", decision)
	if err != nil {
		t.Fatal(err)
	}
	request := &api.TaskToolReleaseExecutionRequest{Action: "authorize", OperationID: strings.Repeat("d", 64), BindingsSHA256: strings.Repeat("e", 64)}
	decision.Decision, decision.AuthorizerKeyID, decision.Execution = "authorize", owner.ID, request
	r, err = journal.ChangeTaskToolReleaseIntent(ctx, account.ID, r.ID, "decide", decision)
	if err != nil {
		t.Fatal(err)
	}
	request.Action, request.ClaimToken, request.FenceVersion = "claim", strings.Repeat("f", 64), 1
	check := api.TaskToolReleaseIntentDecision{ReleaseFingerprint: intent.ReleaseFingerprint, Execution: request}
	r, err = journal.ChangeTaskToolReleaseIntent(ctx, account.ID, r.ID, "execution", check)
	if err != nil {
		t.Fatal(err)
	}
	candidateWeb, candidateWorker := deploy(web, 0), deploy(worker, 100)
	request.Action = "guard"
	request.Receipt = &api.TaskToolReleaseReceipt{Revision: 1, ReleaseID: tasktoolrelease.ExecutionReleaseID(r), Stage: "web_promoted", ServingCaptured: true, ServingDeploymentID: baseline, WebDeploymentID: candidateWeb.ID, WorkerDeploymentID: candidateWorker.ID, PreviousDeployments: map[string]string{}, WorkerIDs: []string{uuid.NewString()}, Promoted: true, Parked: map[string]bool{}}
	r, err = journal.ChangeTaskToolReleaseIntent(ctx, account.ID, r.ID, "execution", check)
	if err != nil {
		t.Fatal(err)
	}
	call := func(in api.TaskToolReleaseIntentDecision, k state.APIKey) *httptest.ResponseRecorder {
		body, _ := json.Marshal(in)
		req := httptest.NewRequest("PUT", "/v1/task-tool-release-intents/"+r.ID+"/decision", bytes.NewReader(body))
		req.SetPathValue("id", r.ID)
		req = req.WithContext(middleware.WithPrincipal(ctx, account, &k, nil))
		w := httptest.NewRecorder()
		toolregistry.NewPrivateExecutor(reopen(), toolregistry.PrivateExecutionOptions{}).TaskReleaseIntent()(w, req, account)
		return w
	}
	stop := api.TaskToolReleaseIntentDecision{IntentSHA256: r.IntentSHA256, ReleaseFingerprint: intent.ReleaseFingerprint, Decision: "recovery_stop", Recovery: &api.TaskToolRecoveryRequest{HandoverID: strings.Repeat("1", 64), OperationID: request.OperationID, Revision: 1}}
	if w := call(stop, publisher); w.Code == 200 {
		t.Fatal("agent stopped/authorized owner recovery")
	}
	barrier := reopen().(state.TaskToolExecutionBarrierStore)
	fence := state.TaskToolExecutionFence{IntentID: r.ID, OperationID: request.OperationID, ClaimToken: request.ClaimToken}
	release, err := barrier.AcquireTaskToolExecutionEffect(ctx, account.ID, publisher.ID, fence)
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan *httptest.ResponseRecorder, 1)
	started := make(chan struct{})
	go func() { close(started); result <- call(stop, owner) }()
	<-started
	select {
	case w := <-result:
		release()
		t.Fatal("stop crossed an admitted write", w.Code)
	case <-time.After(50 * time.Millisecond):
	}
	// Simulate a committed promotion whose response is lost while stop is queued.
	if _, err := s.UpdateDeploymentTraffic(ctx, candidateWeb.ID, 100, baseline); err != nil {
		release()
		t.Fatal(err)
	}
	release()
	var stopped api.TaskToolReleaseIntentRecord
	select {
	case w := <-result:
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &stopped) != nil {
			t.Fatalf("stop: %d %s", w.Code, w.Body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("write barrier did not drain")
	}
	if stopped.Execution.Recovery.State != "stopping" || stopped.Execution.ExecutorExitedAt != nil {
		t.Fatal("stop invented executor exit")
	}
	if release, err := barrier.AcquireTaskToolExecutionEffect(ctx, account.ID, publisher.ID, fence); err == nil {
		release()
		t.Fatal("old executor admitted after stop")
	}
	invoked := false
	req := httptest.NewRequest("PATCH", "/v1/deployments/"+candidateWeb.ID+"/traffic", nil)
	req.Header.Set(api.TaskExecutionIntentHeader, r.ID)
	req.Header.Set(api.TaskExecutionOperationHeader, request.OperationID)
	req.Header.Set(api.TaskExecutionClaimHeader, request.ClaimToken)
	req = req.WithContext(middleware.WithPrincipal(ctx, account, &publisher, nil))
	w := httptest.NewRecorder()
	taskexecution.Wrap(reopen(), func(http.ResponseWriter, *http.Request, state.Account) { invoked = true })(w, req, account)
	if w.Code != 409 || invoked || strings.Contains(w.Body.String(), request.ClaimToken) {
		t.Fatal("native middleware admitted or leaked a stopped claim", w.Code)
	}
	for _, variant := range []string{"empty", "partial", "duplicate"} {
		req := httptest.NewRequest("PATCH", "/v1/deployments/"+candidateWeb.ID+"/traffic", nil)
		req = req.WithContext(middleware.WithPrincipal(ctx, account, &publisher, nil))
		req.Header.Set(api.TaskExecutionClaimHeader, "")
		if variant == "partial" {
			req.Header.Set(api.TaskExecutionClaimHeader, request.ClaimToken)
		}
		if variant == "duplicate" {
			req.Header.Add(api.TaskExecutionClaimHeader, request.ClaimToken)
		}
		w := httptest.NewRecorder()
		taskexecution.Wrap(reopen(), func(http.ResponseWriter, *http.Request, state.Account) { t.Error("malformed fence invoked a write") })(w, req, account)
		if w.Code != 409 {
			t.Fatal("malformed fence admitted", variant, w.Code)
		}
	}
	request.Action = "executor_exited"
	request.Receipt = nil
	settled, err := reopen().(state.TaskToolReleaseIntentStore).ChangeTaskToolReleaseIntent(ctx, account.ID, r.ID, "execution", check)
	if err != nil || settled.Execution.Recovery.State != "settled" {
		t.Fatal("exit not settled", err)
	}
	again, err := journal.ChangeTaskToolReleaseIntent(ctx, account.ID, r.ID, "execution", check)
	if err != nil || !again.Execution.ExecutorExitedAt.Equal(*settled.Execution.ExecutorExitedAt) {
		t.Fatal("lost exit reply changed settlement", err)
	}
	q := api.TaskToolRecoveryQualification{HandoverID: stop.Recovery.HandoverID, OperationID: request.OperationID, Revision: 1, Action: "restore", TargetDeploymentID: baseline, ExpectedServingDeploymentID: candidateWeb.ID, BindingsSHA256: request.BindingsSHA256, EvidenceSHA256: strings.Repeat("2", 64), ObservedAt: time.Now().UTC()}
	q.ExpiresAt = q.ObservedAt.Add(api.TaskToolRecoveryApprovalTTL)
	for _, pin := range intent.Apps {
		current, err := s.(state.ReleaseReviewStore).ReadReleaseReview(ctx, pin.Review.AppID, api.DefaultEnvScope)
		if err != nil {
			t.Fatal(err)
		}
		pin.Review = current
		q.NativeApps = append(q.NativeApps, pin)
	}
	stop.Decision, stop.Recovery.Qualification = "recovery_qualify", &q
	if w := call(stop, publisher); w.Code == 200 {
		t.Fatal("agent qualified recovery")
	}
	other := q
	other.EvidenceSHA256 = strings.Repeat("4", 64)
	first, second := stop, stop
	firstRequest, secondRequest := *stop.Recovery, *stop.Recovery
	firstCandidate := q
	firstRequest.Qualification = &firstCandidate
	secondRequest.Qualification = &other
	first.Recovery, second.Recovery = &firstRequest, &secondRequest
	qualifications := make(chan *httptest.ResponseRecorder, 2)
	go func() { qualifications <- call(first, owner) }()
	go func() { qualifications <- call(second, owner) }()
	successes, conflicts := 0, 0
	for range 2 {
		w := <-qualifications
		switch w.Code {
		case 200:
			successes++
			var retained api.TaskToolReleaseIntentRecord
			if json.Unmarshal(w.Body.Bytes(), &retained) != nil {
				t.Fatal("invalid qualification receipt")
			}
			q = *retained.Execution.Recovery.Qualification
		case 409:
			conflicts++
		default:
			t.Fatalf("qualification: %d %s", w.Code, w.Body)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatal("concurrent qualifiers replaced or duplicated authority", successes, conflicts)
	}
	competing := q
	competing.EvidenceSHA256 = strings.Repeat("3", 64)
	stop.Recovery.Qualification = &competing
	if w := call(stop, owner); w.Code != 409 {
		t.Fatal("qualification replaced", w.Code)
	}
	stop.Recovery.Qualification = nil
	stop.Recovery.QualificationSHA256 = tasktoolrelease.QualificationDigest(q)
	stop.Decision = "recovery_approve"
	// Owner recovery remains separate from the original review/requester authority.
	if _, err := s.MarkAPIKeyRevoked(ctx, account.ID, publisher.ID); err != nil {
		t.Fatal(err)
	}
	if w := call(stop, owner); w.Code != 200 {
		t.Fatalf("owner approval after requester revocation: %d %s", w.Code, w.Body)
	}
	deploy(worker, 0) // a new generation invalidates approval retries
	if w := call(stop, owner); w.Code != 409 {
		t.Fatal("changed generation accepted", w.Code)
	}
	stop.Decision = "recovery_revoke"
	if w := call(stop, owner); w.Code != 200 {
		t.Fatal("drift prevented revocation", w.Code)
	}
	if _, err := s.MarkAPIKeyRevoked(ctx, account.ID, owner.ID); err != nil {
		t.Fatal(err)
	}
	if w := call(stop, owner); w.Code == 200 {
		t.Fatal("revoked cached owner retained recovery access")
	}
}

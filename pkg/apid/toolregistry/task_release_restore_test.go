package toolregistry_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/toolregistry"
	"github.com/onebox-faas/faas/pkg/apid/trafficguard"
	"github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/tasktoolrelease"
)

type taskRestoreFixture struct {
	store                      state.Store
	account                    state.Account
	caller, owner              state.APIKey
	record                     api.TaskToolReleaseIntentRecord
	request                    api.TaskToolRecoveryRestoreRequest
	apps                       map[string]state.App
	worker, previous, baseline state.Instance
}

func newTaskRestoreFixture(t *testing.T, s state.Store) *taskRestoreFixture {
	t.Helper()
	ctx := t.Context()
	f := &taskRestoreFixture{store: s, apps: map[string]state.App{}}
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	var err error
	f.account, err = s.CreateAccount(ctx, "restore-"+suffix+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	key := func(label string, scopes ...string) state.APIKey {
		sum := sha256.Sum256([]byte(label + suffix))
		k, err := s.CreateAPIKey(ctx, f.account.ID, sum[:], label, scopes)
		if err != nil {
			t.Fatal(err)
		}
		return k
	}
	f.caller, f.owner = key("restore-agent", api.ScopeAgent, api.ScopeAppsRead, api.ScopeDeployWrite), key("recovery-owner", api.ScopeAdmin)
	in := api.TaskToolReleaseIntent{ReleaseFingerprint: strings.Repeat("a", 64), WorkerRuntime: "node", Protocol: api.MCPProtocolVersion, Endpoint: "/mcp", AccessMode: "open", ToolScopes: map[string][]string{"report": {}}, RequiredSetup: tasktoolrelease.RequiredSetup(), Reason: "Restore exactly once"}
	deploy := func(role string, traffic int) state.Deployment {
		d, err := s.CreateDeployment(ctx, state.Deployment{AppID: f.apps[role].ID, Kind: state.DeploymentKindTarball, Status: state.DeployLive, TrafficPercent: traffic, TrafficPercentExplicit: true, SourceSHA256: strings.Repeat("b", 64), SourcePath: "/fixture/source.tar.gz", SourceBytes: 1})
		if err != nil {
			t.Fatal(err)
		}
		if d.Status != state.DeployLive {
			if err := s.MarkDeploymentLive(ctx, d.ID); err != nil {
				t.Fatal(err)
			}
			d, err = s.DeploymentByID(ctx, d.ID)
			if err != nil {
				t.Fatal(err)
			}
		}
		return d
	}
	base := map[string]string{}
	for _, role := range []string{"web", "worker", "observer", "previous_worker"} {
		app, err := s.CreateApp(ctx, state.App{AccountID: f.account.ID, Slug: strings.ReplaceAll(role, "_", "-") + "-" + suffix, Status: state.AppActive, Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 5, IdleTimeoutS: 30})
		if err != nil {
			t.Fatal(err)
		}
		f.apps[role] = app
		if role != "worker" {
			base[role] = deploy(role, 100).ID
		}
		review, err := s.(state.ReleaseReviewStore).ReadReleaseReview(ctx, app.ID, api.DefaultEnvScope)
		if err != nil {
			t.Fatal(err)
		}
		in.Apps = append(in.Apps, api.TaskToolReleaseApp{Role: role, App: app.Slug, Review: review})
	}
	in.ObserverMetricApp = f.apps["observer"].Slug
	j := s.(state.TaskToolReleaseIntentStore)
	r, err := j.PrepareTaskToolReleaseIntent(ctx, f.account.ID, f.caller.ID, strings.Repeat(suffix[:1], 64), in)
	// Use a guaranteed hexadecimal identity regardless of the UUID's first digit.
	if err != nil {
		t.Fatal(err)
	}
	d := api.TaskToolReleaseIntentDecision{Decision: "approve", IntentSHA256: r.IntentSHA256, ReleaseFingerprint: in.ReleaseFingerprint}
	r, err = j.ChangeTaskToolReleaseIntent(ctx, f.account.ID, r.ID, "decide", d)
	if err != nil {
		t.Fatal(err)
	}
	x := &api.TaskToolReleaseExecutionRequest{Action: "authorize", OperationID: strings.Repeat("c", 64), BindingsSHA256: strings.Repeat("d", 64)}
	d.Decision, d.AuthorizerKeyID, d.Execution = "authorize", f.owner.ID, x
	r, err = j.ChangeTaskToolReleaseIntent(ctx, f.account.ID, r.ID, "decide", d)
	if err != nil {
		t.Fatal(err)
	}
	x.Action, x.ClaimToken, x.FenceVersion = "claim", strings.Repeat("e", 64), 1
	check := api.TaskToolReleaseIntentDecision{ReleaseFingerprint: in.ReleaseFingerprint, Execution: x}
	r, err = j.ChangeTaskToolReleaseIntent(ctx, f.account.ID, r.ID, "execution", check)
	if err != nil {
		t.Fatal(err)
	}
	candidate, worker := deploy("web", 0), deploy("worker", 100)
	if _, err = s.UpdateDeploymentTraffic(ctx, candidate.ID, 100, base["web"]); err != nil {
		t.Fatal(err)
	}
	node, err := s.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"web", "worker", "observer", "previous_worker"} {
		generation := base[role]
		if role == "worker" {
			generation = worker.ID
		}
		instance, err := s.CreateInstance(ctx, f.apps[role].ID, generation, "running", 256, node.ID, "")
		if err != nil {
			t.Fatalf("create %s instance: %v", role, err)
		}
		if role == "web" {
			f.baseline = instance
		}
		if role == "worker" {
			f.worker = instance
		}
		if role == "previous_worker" {
			f.previous = instance
		}
	}
	if err := s.PutCustomMetric(ctx, f.apps["observer"].ID, "mcp_tasks_observer_heartbeat", 1, time.Now(), api.MaxCustomMetricsPerApp); err != nil {
		t.Fatal(err)
	}
	x.Action = "guard"
	x.Receipt = &api.TaskToolReleaseReceipt{Revision: 1, ReleaseID: tasktoolrelease.ExecutionReleaseID(r), Stage: "web_promoted", ServingCaptured: true, ServingDeploymentID: base["web"], WebDeploymentID: candidate.ID, WorkerDeploymentID: worker.ID, PreviousDeployments: map[string]string{f.apps["previous_worker"].Slug: base["previous_worker"]}, WorkerIDs: []string{f.worker.ID}, Promoted: true, Parked: map[string]bool{}}
	r, err = j.ChangeTaskToolReleaseIntent(ctx, f.account.ID, r.ID, "execution", check)
	if err != nil {
		t.Fatal(err)
	}
	x.Action, x.Receipt = "executor_exited", nil
	r, err = j.ChangeTaskToolReleaseIntent(ctx, f.account.ID, r.ID, "execution", check)
	if err != nil {
		t.Fatal(err)
	}
	d.Execution = nil
	d.Decision = "recovery_stop"
	d.Recovery = &api.TaskToolRecoveryRequest{OperationID: x.OperationID, HandoverID: strings.Repeat("f", 64), Revision: 1}
	r, err = j.ChangeTaskToolReleaseIntent(ctx, f.account.ID, r.ID, "decide", d)
	if err != nil {
		t.Fatal(err)
	}
	q := api.TaskToolRecoveryQualification{HandoverID: d.Recovery.HandoverID, OperationID: x.OperationID, Revision: 1, Action: "restore", TargetDeploymentID: base["web"], ExpectedServingDeploymentID: candidate.ID, BindingsSHA256: x.BindingsSHA256, EvidenceSHA256: strings.Repeat("1", 64), ObservedAt: time.Now().UTC()}
	q.ExpiresAt = q.ObservedAt.Add(api.TaskToolRecoveryApprovalTTL)
	for _, pin := range in.Apps {
		review, err := s.(state.ReleaseReviewStore).ReadReleaseReview(ctx, pin.Review.AppID, api.DefaultEnvScope)
		if err != nil {
			t.Fatal(err)
		}
		pin.Review = review
		q.NativeApps = append(q.NativeApps, pin)
	}
	d.Decision, d.Recovery.Qualification = "recovery_qualify", &q
	r, err = j.ChangeTaskToolReleaseIntent(ctx, f.account.ID, r.ID, "decide", d)
	if err != nil {
		t.Fatal(err)
	}
	d.Decision, d.Recovery.Qualification, d.Recovery.QualificationSHA256 = "recovery_approve", nil, tasktoolrelease.QualificationDigest(q)
	r, err = j.ChangeTaskToolReleaseIntent(ctx, f.account.ID, r.ID, "decide", d)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := map[string][]string{}
	for _, app := range f.apps {
		rows, err := s.ListInstancesForApp(ctx, app.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			snapshot[app.ID] = append(snapshot[app.ID], row.ID)
		}
	}
	fingerprint, err := api.MCPAccessPolicyFingerprint("https://restore.example/mcp", nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	f.record = r
	f.request = api.TaskToolRecoveryRestoreRequest{OperationID: x.OperationID, HandoverID: d.Recovery.HandoverID, QualificationSHA256: d.Recovery.QualificationSHA256, ClaimToken: x.ClaimToken, BindingsSHA256: x.BindingsSHA256, EvidenceSHA256: strings.Repeat("2", 64), CheckedAt: time.Now().UTC(), ExpectedInstanceIDs: snapshot, ExpectedMCPAccessPolicy: &api.MCPAccessPolicyExpectation{Resource: "https://restore.example/mcp", Fingerprint: fingerprint}}
	return f
}
func (f *taskRestoreFixture) call(t *testing.T, in api.TaskToolRecoveryRestoreRequest, delegate func(http.ResponseWriter, *http.Request, state.Account)) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(in)
	r := httptest.NewRequest("POST", "/v1/task-tool-release-intents/"+f.record.ID+"/recovery/restore", bytes.NewReader(body))
	r.SetPathValue("id", f.record.ID)
	r = r.WithContext(middleware.WithPrincipal(t.Context(), f.account, &f.caller, nil))
	w := httptest.NewRecorder()
	toolregistry.NewPrivateExecutor(f.store, toolregistry.PrivateExecutionOptions{}).TaskReleaseRestore(delegate)(w, r, f.account)
	return w
}
func (f *taskRestoreFixture) traffic(w http.ResponseWriter, r *http.Request, _ state.Account) {
	var in api.UpdateDeploymentTrafficRequest
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		w.WriteHeader(400)
		return
	}
	ctx, problem := trafficguard.Prepare(r.Context(), f.account.ID, f.apps["web"].ID, &in)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	_, err := f.store.UpdateDeploymentTraffic(ctx, r.PathValue("id"), in.TrafficPercent, *in.ExpectedServingDeploymentID)
	if err != nil {
		w.WriteHeader(409)
		api.WriteProblem(w, api.NewProblem(409, "task_recovery_changed", "Task recovery changed", err.Error()))
		return
	}
	w.WriteHeader(200)
}
func TestMCPTaskRestoreAtomicMemory(t *testing.T) {
	exerciseTaskRestore(t, func(st *testing.T) *taskRestoreFixture { return newTaskRestoreFixture(st, state.NewMemStore()) })
}
func TestMCPTaskRestoreAtomicPostgres(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	config := pool.Config()
	config.MaxConns = 2
	limited, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer limited.Close()
	exerciseTaskRestore(t, func(st *testing.T) *taskRestoreFixture { return newTaskRestoreFixture(st, state.NewPgStore(limited)) })
}
func exerciseTaskRestore(t *testing.T, fixture func(*testing.T) *taskRestoreFixture) {
	t.Helper()
	t.Run("commit_lost_reply_and_no_duplicate", func(t *testing.T) {
		f := fixture(t)
		calls := 0
		w := f.call(t, f.request, func(w http.ResponseWriter, r *http.Request, a state.Account) {
			calls++
			f.traffic(w, r, a)
			w.WriteHeader(503)
		})
		var out api.TaskToolReleaseIntentRecord
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || !tasktoolrelease.ValidRecoveryResult(out) {
			t.Fatalf("lost reply: %d %s", w.Code, w.Body)
		}
		target := out.Execution.Recovery.Result.TargetDeploymentID
		candidate := out.Execution.Receipt.WebDeploymentID
		current, err := f.store.DeploymentByID(t.Context(), target)
		if err != nil || current.TrafficPercent != 100 {
			t.Fatal("receipt committed without traffic", err)
		}
		if _, err := f.store.UpdateDeploymentTraffic(t.Context(), candidate, 100, target); err != nil {
			t.Fatal(err)
		}
		w = f.call(t, f.request, func(http.ResponseWriter, *http.Request, state.Account) { calls++ })
		if w.Code != 200 || calls != 1 {
			t.Fatal("retained result resubmitted traffic", w.Code, calls)
		}
		current, err = f.store.DeploymentByID(t.Context(), candidate)
		if err != nil || current.TrafficPercent != 100 {
			t.Fatal("replay changed newer traffic", err)
		}
	})
	for _, change := range []string{"owner_revoked", "review_revoked", "observer_unhealthy", "worker_replaced", "previous_stopped", "target_stopped", "generation_changed", "foreign_digest", "missing_claim", "target_changed", "policy_changed", "snapshot_changed", "false_2xx"} {
		t.Run(change, func(t *testing.T) {
			f := fixture(t)
			ctx := t.Context()
			in := f.request
			switch change {
			case "owner_revoked":
				if _, err := f.store.MarkAPIKeyRevoked(ctx, f.account.ID, f.owner.ID); err != nil {
					t.Fatal(err)
				}
			case "review_revoked":
				_, err := f.store.(state.TaskToolReleaseIntentStore).ChangeTaskToolReleaseIntent(ctx, f.account.ID, f.record.ID, "decide", api.TaskToolReleaseIntentDecision{Decision: "recovery_revoke", IntentSHA256: f.record.IntentSHA256, ReleaseFingerprint: f.record.Intent.ReleaseFingerprint, AuthorizerKeyID: f.owner.ID, Recovery: &api.TaskToolRecoveryRequest{OperationID: in.OperationID, HandoverID: in.HandoverID, Revision: 1, QualificationSHA256: in.QualificationSHA256}})
				if err != nil {
					t.Fatal(err)
				}
			case "observer_unhealthy":
				if err := f.store.PutCustomMetric(ctx, f.apps["observer"].ID, "mcp_tasks_observer_heartbeat", 0, time.Now(), api.MaxCustomMetricsPerApp); err != nil {
					t.Fatal(err)
				}
			case "worker_replaced":
				if _, err := f.store.CreateInstance(ctx, f.worker.AppID, f.worker.DeploymentID, "running", 256, f.worker.NodeID, ""); err != nil {
					t.Fatal(err)
				}
			case "target_stopped":
				if err := f.store.UpdateInstanceState(ctx, f.baseline.ID, "stopped"); err != nil {
					t.Fatal(err)
				}
			case "previous_stopped":
				if err := f.store.UpdateInstanceState(ctx, f.previous.ID, "stopped"); err != nil {
					t.Fatal(err)
				}
			case "generation_changed":
				if _, err := f.store.CreateDeployment(ctx, state.Deployment{AppID: f.apps["worker"].ID, Kind: state.DeploymentKindTarball, Status: state.DeployLive, SourceSHA256: strings.Repeat("3", 64), SourcePath: "/fixture/other", SourceBytes: 1}); err != nil {
					t.Fatal(err)
				}
			case "foreign_digest":
				in.QualificationSHA256 = strings.Repeat("4", 64)
			case "missing_claim":
				in.ClaimToken = ""
			case "policy_changed":
				policy := *in.ExpectedMCPAccessPolicy
				policy.Fingerprint = strings.Repeat("9", 64)
				in.ExpectedMCPAccessPolicy = &policy
			case "snapshot_changed":
				in.ExpectedInstanceIDs = map[string][]string{}
			case "target_changed":
				if _, err := f.store.UpdateDeploymentTraffic(ctx, f.record.Execution.Receipt.ServingDeploymentID, 100, f.record.Execution.Receipt.WebDeploymentID); err != nil {
					t.Fatal(err)
				}
			}
			delegate := f.traffic
			if change == "false_2xx" {
				delegate = func(w http.ResponseWriter, _ *http.Request, _ state.Account) { w.WriteHeader(200) }
			}
			w := f.call(t, in, delegate)
			if w.Code < 400 {
				t.Fatalf("changed authority admitted: %d %s", w.Code, w.Body)
			}
			retained, err := f.store.(state.TaskToolReleaseIntentStore).GetTaskToolReleaseIntent(ctx, f.account.ID, f.record.ID)
			if err != nil || retained.Execution.Recovery.Result != nil {
				t.Fatal("failed write consumed approval", err)
			}
		})
	}
	t.Run("concurrent_attempts", func(t *testing.T) {
		f := fixture(t)
		responses := make(chan int, 2)
		start := make(chan struct{})
		var ready sync.WaitGroup
		ready.Add(2)
		for range 2 {
			go func() { ready.Done(); <-start; w := f.call(t, f.request, f.traffic); responses <- w.Code }()
		}
		ready.Wait()
		close(start)
		first, second := <-responses, <-responses
		// A loser may receive a conflict while the winner is still committing. It
		// can inspect the durable result; neither response permits a second write.
		if first != 200 && second != 200 {
			t.Fatal("no restore committed", first, second)
		}
		retained, err := f.store.(state.TaskToolReleaseIntentStore).GetTaskToolReleaseIntent(t.Context(), f.account.ID, f.record.ID)
		if err != nil || !tasktoolrelease.ValidRecoveryResult(retained) {
			t.Fatal("concurrent restore lost its result", err)
		}
	})
}

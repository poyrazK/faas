package toolregistry_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/apid/toolregistry"
	"github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/tasktoolrelease"
)

func wakeTaskRetirementWorker(t *testing.T, f *taskRestoreFixture) error {
	t.Helper()
	ok, err := f.store.(interface {
		CompareAndSetAppStatus(context.Context, string, state.AppStatus, state.AppStatus) (bool, error)
	}).CompareAndSetAppStatus(t.Context(), f.worker.AppID, state.AppEvictedCold, state.AppActive)
	if err == nil && !ok {
		t.Fatal("fixture worker did not wake")
	}
	return err
}
func (f *taskRestoreFixture) retirementCall(t *testing.T, in api.TaskToolReleaseIntentDecision, park httpkit.AccountHandler) *httptest.ResponseRecorder {
	t.Helper()
	key, path, method := f.caller, "check", "POST"
	var value any = api.TaskToolReleaseIntentCheck{ReleaseFingerprint: f.record.Intent.ReleaseFingerprint, Retirement: in.Retirement}
	if in.Decision != "" {
		key, path, method = f.owner, "decision", "PUT"
		value = in
	}
	if park != nil {
		path = "recovery/retire"
		value = in.Retirement
	}
	body, _ := json.Marshal(value)
	r := httptest.NewRequest(method, "/v1/task-tool-release-intents/"+f.record.ID+"/"+path, bytes.NewReader(body))
	r.SetPathValue("id", f.record.ID)
	r = r.WithContext(middleware.WithPrincipal(t.Context(), f.account, &key, nil))
	w := httptest.NewRecorder()
	e := toolregistry.NewPrivateExecutor(f.store, toolregistry.PrivateExecutionOptions{})
	if park != nil {
		e.TaskReleaseRetire(park)(w, r, f.account)
	} else {
		e.TaskReleaseIntent()(w, r, f.account)
	}
	return w
}
func (f *taskRestoreFixture) park(w http.ResponseWriter, r *http.Request, _ state.Account) {
	var in struct {
		ExpectedDeploymentID string `json:"expected_deployment_id"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || r.PathValue("slug") != f.apps["worker"].Slug {
		w.WriteHeader(400)
		return
	}
	_, _, err := f.store.(state.ConditionalAppParkTransitionStore).BeginAppParkTransitionIfDeployment(r.Context(), f.apps["worker"].ID, state.AppActive, in.ExpectedDeploymentID)
	if err != nil {
		w.WriteHeader(409)
		return
	}
	w.WriteHeader(200)
}
func retirementNativeFixture(t *testing.T, s state.Store, customize ...func(string, *state.App)) (*taskRestoreFixture, api.TaskToolReleaseIntentDecision) {
	t.Helper()
	options := []func(string, *state.App){func(role string, app *state.App) {
		if role == "worker" {
			app.Manifest.ExecutionMode = api.ExecutionModeWorker
			app.Manifest.StopGracePeriodS = 60
			app.WorkloadClass = "worker"
		}
	}}
	options = append(options, customize...)
	f, claim := quarantineNativeFixture(t, s, options...)
	for _, action := range []string{"claim", "observe"} {
		claim.Quarantine.Action = action
		claim.Quarantine.CheckedAt = time.Now().UTC()
		w := f.quarantineCall(t, claim, false)
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &f.record) != nil {
			t.Fatalf("quarantine prerequisite %d %s", w.Code, w.Body)
		}
	}
	q := f.record.Execution.Recovery.Quarantine.Qualification
	q.Action = "retire"
	q.ObservedAt = time.Now().UTC()
	q.ExpiresAt = q.ObservedAt.Add(api.TaskToolRecoveryApprovalTTL)
	in := api.TaskToolReleaseIntentDecision{IntentSHA256: f.record.IntentSHA256, ReleaseFingerprint: f.record.Intent.ReleaseFingerprint, Decision: "recovery_retire_qualify", Retirement: &api.TaskToolRetirementRequest{Qualification: &q}}
	for _, decision := range []string{"recovery_retire_qualify", "recovery_retire_approve"} {
		in.Decision = decision
		w := f.retirementCall(t, in, nil)
		if w.Code != 200 {
			t.Fatalf("%s %d %s", decision, w.Code, w.Body)
		}
		in.Retirement = &api.TaskToolRetirementRequest{QualificationSHA256: tasktoolrelease.QualificationDigest(q)}
	}
	in.Decision = ""
	in.Retirement.Action = "park"
	in.Retirement.ClaimToken = f.request.ClaimToken
	in.Retirement.BindingsSHA256 = f.request.BindingsSHA256
	in.Retirement.EvidenceSHA256 = strings.Repeat("9", 64)
	in.Retirement.CheckedAt = time.Now().UTC()
	in.Retirement.ExpectedInstanceIDs = f.request.ExpectedInstanceIDs
	in.Retirement.ExpectedMCPAccessPolicy = f.request.ExpectedMCPAccessPolicy
	in.Retirement.ShutdownTimeoutMS = 30000
	return f, in
}
func TestMCPTaskRetirementNativeMemory(t *testing.T) {
	exerciseTaskRetirementNative(t, func(t *testing.T) state.Store { return state.NewMemStore() })
}
func TestMCPTaskRetirementNativePostgres(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	config := pool.Config()
	config.MaxConns = 2
	limited, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer limited.Close()
	exerciseTaskRetirementNative(t, func(t *testing.T) state.Store { return state.NewPgStore(limited) })
}
func exerciseTaskRetirementNative(t *testing.T, store func(*testing.T) state.Store) {
	t.Helper()
	t.Run("atomic_pending_lost_reply_readonly_after_revocation", func(t *testing.T) {
		f, in := retirementNativeFixture(t, store(t))
		calls := 0
		lost := func(w http.ResponseWriter, r *http.Request, a state.Account) {
			calls++
			f.park(httptest.NewRecorder(), r, a)
			w.WriteHeader(503)
		}
		w := f.retirementCall(t, in, lost)
		var out api.TaskToolReleaseIntentRecord
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || !tasktoolrelease.ValidRetirement(out) || out.Execution.Recovery.Retirement.State != "retirement_pending" || out.Execution.Recovery.Retirement.Result != nil {
			t.Fatalf("pending %d %s", w.Code, w.Body)
		}
		app, err := f.store.AppByID(t.Context(), f.worker.AppID)
		if err != nil || app.Status != state.AppEvictedCold {
			t.Fatal("pending without park", err)
		}
		if strings.Contains(w.Body.String(), in.Retirement.ClaimToken) {
			t.Fatal("private proof leaked")
		}
		transition := out.Execution.Recovery.Retirement.ParkTransitionID
		revoke := in
		revoke.Decision = "recovery_retire_revoke"
		revoke.Retirement = &api.TaskToolRetirementRequest{QualificationSHA256: in.Retirement.QualificationSHA256}
		if w = f.retirementCall(t, revoke, nil); w.Code != 200 {
			t.Fatalf("revoke %d %s", w.Code, w.Body)
		}
		in.Retirement.Action = "observe"
		in.Retirement.CheckedAt = time.Now().UTC()
		if w = f.retirementCall(t, in, nil); w.Code < 400 {
			t.Fatal("live native instance certified retired")
		}
		if err = f.store.UpdateInstanceState(t.Context(), f.worker.ID, "stopped"); err != nil {
			t.Fatal(err)
		}
		if _, err = f.store.MarkAPIKeyRevoked(t.Context(), f.account.ID, f.owner.ID); err != nil {
			t.Fatal(err)
		}
		in.Retirement.CheckedAt = time.Now().UTC()
		w = f.retirementCall(t, in, nil)
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || !tasktoolrelease.ValidRetirement(out) || out.Execution.Recovery.Retirement.Result == nil || out.Execution.Recovery.Retirement.Result.ParkTransitionID != transition {
			t.Fatalf("retired %d %s", w.Code, w.Body)
		}
		recorded := out.Execution.Recovery.Retirement.Result.RecordedAt
		if err = wakeTaskRetirementWorker(t, f); err != nil {
			t.Fatal(err)
		}
		in.Retirement.Action = "park"
		w = f.retirementCall(t, in, lost)
		if w.Code != 200 || calls != 1 || json.Unmarshal(w.Body.Bytes(), &out) != nil || !out.Execution.Recovery.Retirement.Result.RecordedAt.Equal(recorded) {
			t.Fatal("retained replay issued parking", w.Code, calls)
		}
		app, err = f.store.AppByID(t.Context(), f.worker.AppID)
		if err != nil || app.Status != state.AppActive {
			t.Fatal("replay changed later lifecycle", err)
		}
	})
	for _, change := range []string{"owner_revoked", "review_revoked", "observer", "worker_restart", "previous_stopped", "traffic", "policy", "claim_missing", "snapshot_override", "grace", "false_2xx", "generation", "not_worker"} {
		t.Run(change, func(t *testing.T) {
			var options []func(string, *state.App)
			if change == "not_worker" {
				options = append(options, func(role string, app *state.App) {
					if role == "worker" {
						app.Manifest.ExecutionMode = "request"
						app.WorkloadClass = state.WorkloadClassHTTP
					}
				})
			}
			f, in := retirementNativeFixture(t, store(t), options...)
			ctx := t.Context()
			var err error
			park := httpkit.AccountHandler(f.park)
			switch change {
			case "owner_revoked":
				_, err = f.store.MarkAPIKeyRevoked(ctx, f.account.ID, f.owner.ID)
			case "review_revoked":
				revoke := in
				revoke.Decision = "recovery_retire_revoke"
				revoke.Retirement = &api.TaskToolRetirementRequest{QualificationSHA256: in.Retirement.QualificationSHA256}
				if w := f.retirementCall(t, revoke, nil); w.Code != 200 {
					t.Fatal(w.Body)
				}
			case "observer":
				err = f.store.PutCustomMetric(ctx, f.apps["observer"].ID, "mcp_tasks_observer_heartbeat", 0, time.Now(), api.MaxCustomMetricsPerApp)
			case "worker_restart":
				_, err = f.store.CreateInstance(ctx, f.worker.AppID, f.worker.DeploymentID, "running", 256, f.worker.NodeID, "")
			case "previous_stopped":
				err = f.store.UpdateInstanceState(ctx, f.previous.ID, "stopped")
			case "traffic":
				_, err = f.store.UpdateDeploymentTraffic(ctx, f.record.Execution.Receipt.WebDeploymentID, 100, f.record.Execution.Receipt.ServingDeploymentID)
			case "policy":
				p := *in.Retirement.ExpectedMCPAccessPolicy
				p.Fingerprint = strings.Repeat("0", 64)
				in.Retirement.ExpectedMCPAccessPolicy = &p
			case "claim_missing":
				in.Retirement.ClaimToken = ""
			case "snapshot_override":
				q := f.record.Execution.Recovery.Quarantine.Qualification
				in.Retirement.Qualification = &q
			case "grace":
				in.Retirement.ShutdownTimeoutMS = 60000
			case "false_2xx":
				park = func(w http.ResponseWriter, _ *http.Request, _ state.Account) { w.WriteHeader(200) }
			case "generation":
				_, err = f.store.CreateDeployment(ctx, state.Deployment{AppID: f.worker.AppID, Kind: state.DeploymentKindTarball, Status: state.DeployLive, SourceSHA256: strings.Repeat("b", 64), SourcePath: "/fixture/new.tar.gz", SourceBytes: 1})
			}
			if err != nil {
				t.Fatal(err)
			}
			w := f.retirementCall(t, in, park)
			if w.Code < 400 {
				t.Fatalf("changed evidence admitted %d %s", w.Code, w.Body)
			}
			r, err := f.store.(state.TaskToolReleaseIntentStore).GetTaskToolReleaseIntent(ctx, f.account.ID, f.record.ID)
			if err != nil || r.Execution.Recovery.Retirement.State != "qualified" {
				t.Fatal("failed park consumed approval", err)
			}
			app, err := f.store.AppByID(ctx, f.worker.AppID)
			if err != nil || app.Status != state.AppActive {
				t.Fatal("failed park changed lifecycle", err)
			}
		})
	}
	t.Run("pending_replay_never_parks_a_new_generation", func(t *testing.T) {
		f, in := retirementNativeFixture(t, store(t))
		w := f.retirementCall(t, in, f.park)
		if w.Code != 200 {
			t.Fatal(w.Body)
		}
		if err := f.store.UpdateInstanceState(t.Context(), f.worker.ID, "stopped"); err != nil {
			t.Fatal(err)
		}
		if err := wakeTaskRetirementWorker(t, f); err != nil {
			t.Fatal(err)
		}
		calls := 0
		w = f.retirementCall(t, in, func(w http.ResponseWriter, r *http.Request, a state.Account) { calls++; f.park(w, r, a) })
		if w.Code != 200 || calls != 0 {
			t.Fatal("pending replay resubmitted park")
		}
		in.Retirement.Action = "observe"
		in.Retirement.CheckedAt = time.Now().UTC()
		if w = f.retirementCall(t, in, nil); w.Code < 400 {
			t.Fatal("changed park transition certified drained")
		}
	})
	t.Run("new_generation_after_park_blocks_observation", func(t *testing.T) {
		f, in := retirementNativeFixture(t, store(t))
		w := f.retirementCall(t, in, f.park)
		if w.Code != 200 {
			t.Fatal(w.Body)
		}
		if err := f.store.UpdateInstanceState(t.Context(), f.worker.ID, "stopped"); err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.CreateDeployment(t.Context(), state.Deployment{AppID: f.worker.AppID, Kind: state.DeploymentKindTarball, Status: state.DeployLive, SourceSHA256: strings.Repeat("b", 64), SourcePath: "/fixture/new.tar.gz", SourceBytes: 1}); err != nil {
			t.Fatal(err)
		}
		in.Retirement.Action = "observe"
		in.Retirement.CheckedAt = time.Now().UTC()
		if w = f.retirementCall(t, in, nil); w.Code < 400 {
			t.Fatal("new native generation certified retired")
		}
	})

	t.Run("malformed_internal_policy_fails_closed", func(t *testing.T) {
		f, in := retirementNativeFixture(t, store(t))
		in.Retirement.ExpectedMCPAccessPolicy = nil
		ctx := state.WithTaskToolRetirementPark(t.Context(), f.account.ID, f.caller.ID, f.record.ID, *in.Retirement)
		_, changed, err := f.store.(state.ConditionalAppParkTransitionStore).BeginAppParkTransitionIfDeployment(ctx, f.worker.AppID, state.AppActive, f.worker.DeploymentID)
		if !errors.Is(err, state.ErrConflict) || changed {
			t.Fatal("malformed native guard admitted parking", changed, err)
		}
		r, err := f.store.(state.TaskToolReleaseIntentStore).GetTaskToolReleaseIntent(t.Context(), f.account.ID, f.record.ID)
		if err != nil || r.Execution.Recovery.Retirement.State != "qualified" {
			t.Fatal("malformed guard consumed authority", err)
		}
		app, err := f.store.AppByID(t.Context(), f.worker.AppID)
		if err != nil || app.Status != state.AppActive {
			t.Fatal("malformed guard changed lifecycle", err)
		}
	})

	t.Run("concurrent_single_transition", func(t *testing.T) {
		f, in := retirementNativeFixture(t, store(t))
		var changed atomic.Int32
		park := func(w http.ResponseWriter, r *http.Request, a state.Account) {
			var body struct {
				ExpectedDeploymentID string `json:"expected_deployment_id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			_, ok, err := f.store.(state.ConditionalAppParkTransitionStore).BeginAppParkTransitionIfDeployment(r.Context(), f.worker.AppID, state.AppActive, body.ExpectedDeploymentID)
			if err != nil {
				w.WriteHeader(409)
				return
			}
			if ok {
				changed.Add(1)
			}
			w.WriteHeader(200)
		}
		var wg sync.WaitGroup
		for range 2 {
			wg.Go(func() { f.retirementCall(t, in, park) })
		}
		wg.Wait()
		if changed.Load() != 1 {
			t.Fatal("expected one native park transition", changed.Load())
		}
	})
}

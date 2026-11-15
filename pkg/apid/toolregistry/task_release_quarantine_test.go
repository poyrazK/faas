package toolregistry_test

import (
	"bytes"
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/toolregistry"
	"github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/tasktoolrelease"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func (f *taskRestoreFixture) quarantineCall(t *testing.T, in api.TaskToolReleaseIntentDecision, owner bool) *httptest.ResponseRecorder {
	t.Helper()
	key, path, method := f.caller, "check", "POST"
	var value any = api.TaskToolReleaseIntentCheck{ReleaseFingerprint: f.record.Intent.ReleaseFingerprint, Quarantine: in.Quarantine}
	if owner {
		key, path, method = f.owner, "decision", "PUT"
		value = in
	}
	body, _ := json.Marshal(value)
	r := httptest.NewRequest(method, "/v1/task-tool-release-intents/"+f.record.ID+"/"+path, bytes.NewReader(body))
	r.SetPathValue("id", f.record.ID)
	r = r.WithContext(middleware.WithPrincipal(t.Context(), f.account, &key, nil))
	w := httptest.NewRecorder()
	toolregistry.NewPrivateExecutor(f.store, toolregistry.PrivateExecutionOptions{}).TaskReleaseIntent()(w, r, f.account)
	return w
}
func quarantineNativeFixture(t *testing.T, s state.Store) (*taskRestoreFixture, api.TaskToolReleaseIntentDecision) {
	t.Helper()
	f := newTaskRestoreFixture(t, s)
	w := f.call(t, f.request, f.traffic)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &f.record) != nil {
		t.Fatalf("restore prerequisite: %d %s", w.Code, w.Body)
	}
	q := *f.record.Execution.Recovery.Qualification
	q.Action = "quarantine"
	q.TargetDeploymentID = f.record.Execution.Receipt.WorkerDeploymentID
	q.ExpectedServingDeploymentID = f.record.Execution.Receipt.ServingDeploymentID
	q.ObservedAt = time.Now().UTC()
	q.ExpiresAt = q.ObservedAt.Add(api.TaskToolRecoveryApprovalTTL)
	q.NativeApps = nil
	for _, pin := range f.record.Intent.Apps {
		review, err := s.(state.ReleaseReviewStore).ReadReleaseReview(t.Context(), pin.Review.AppID, api.DefaultEnvScope)
		if err != nil {
			t.Fatal(err)
		}
		pin.Review = review
		q.NativeApps = append(q.NativeApps, pin)
	}
	in := api.TaskToolReleaseIntentDecision{IntentSHA256: f.record.IntentSHA256, ReleaseFingerprint: f.record.Intent.ReleaseFingerprint, Decision: "recovery_quarantine_qualify", Quarantine: &api.TaskToolQuarantineRequest{Qualification: &q}}
	for _, decision := range []string{"recovery_quarantine_qualify", "recovery_quarantine_approve"} {
		in.Decision = decision
		w = f.quarantineCall(t, in, true)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", decision, w.Code, w.Body)
		}
		in.Quarantine = &api.TaskToolQuarantineRequest{QualificationSHA256: tasktoolrelease.QualificationDigest(q)}
	}
	in.Decision = ""
	in.Quarantine.Action = "claim"
	in.Quarantine.ClaimToken = f.request.ClaimToken
	in.Quarantine.AttemptToken = strings.Repeat("7", 64)
	in.Quarantine.BindingsSHA256 = f.request.BindingsSHA256
	in.Quarantine.EvidenceSHA256 = strings.Repeat("8", 64)
	in.Quarantine.CheckedAt = time.Now().UTC()
	in.Quarantine.ExpectedInstanceIDs = f.request.ExpectedInstanceIDs
	in.Quarantine.ExpectedMCPAccessPolicy = f.request.ExpectedMCPAccessPolicy
	return f, in
}
func TestMCPTaskQuarantineNativeMemory(t *testing.T) {
	exerciseQuarantineNative(t, func(t *testing.T) state.Store { return state.NewMemStore() })
}
func TestMCPTaskQuarantineNativePostgres(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	exerciseQuarantineNative(t, func(t *testing.T) state.Store { return state.NewPgStore(pool) })
}
func exerciseQuarantineNative(t *testing.T, store func(*testing.T) state.Store) {
	t.Helper()
	t.Run("one_admission_and_reconcile_after_revocation", func(t *testing.T) {
		f, in := quarantineNativeFixture(t, store(t))
		w := f.quarantineCall(t, in, false)
		if w.Code != 200 {
			t.Fatalf("claim %d %s", w.Code, w.Body)
		}
		if strings.Contains(w.Body.String(), in.Quarantine.AttemptToken) || strings.Contains(w.Body.String(), in.Quarantine.ClaimToken) {
			t.Fatal("private proof leaked")
		}
		w = f.quarantineCall(t, in, false)
		if w.Code < 400 {
			t.Fatal("duplicate claim admitted")
		}
		revoke := in
		revoke.Decision = "recovery_quarantine_revoke"
		revoke.Quarantine = &api.TaskToolQuarantineRequest{QualificationSHA256: in.Quarantine.QualificationSHA256}
		if w = f.quarantineCall(t, revoke, true); w.Code != 200 {
			t.Fatalf("revoke %d %s", w.Code, w.Body)
		}
		in.Quarantine.Action = "observe"
		in.Quarantine.CheckedAt = time.Now().UTC()
		w = f.quarantineCall(t, in, false)
		var out api.TaskToolReleaseIntentRecord
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || !tasktoolrelease.ValidQuarantine(out) || out.Execution.Recovery.Quarantine.Result == nil {
			t.Fatalf("observe %d %s", w.Code, w.Body)
		}
		before := out.Execution.Recovery.Quarantine.Result.RecordedAt
		w = f.quarantineCall(t, in, false)
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || !out.Execution.Recovery.Quarantine.Result.RecordedAt.Equal(before) {
			t.Fatal("replay replaced historical observation")
		}
	})
	for _, change := range []string{"owner_revoked", "observer", "worker_restart", "previous_stopped", "traffic", "policy", "foreign_digest", "claim_missing", "snapshot_override"} {
		t.Run(change, func(t *testing.T) {
			f, in := quarantineNativeFixture(t, store(t))
			ctx := t.Context()
			var err error
			switch change {
			case "owner_revoked":
				_, err = f.store.MarkAPIKeyRevoked(ctx, f.account.ID, f.owner.ID)
			case "observer":
				err = f.store.PutCustomMetric(ctx, f.apps["observer"].ID, "mcp_tasks_observer_heartbeat", 0, time.Now(), api.MaxCustomMetricsPerApp)
			case "worker_restart":
				_, err = f.store.CreateInstance(ctx, f.worker.AppID, f.worker.DeploymentID, "running", 256, f.worker.NodeID, "")
			case "previous_stopped":
				err = f.store.UpdateInstanceState(ctx, f.previous.ID, "stopped")
			case "traffic":
				_, err = f.store.UpdateDeploymentTraffic(ctx, f.record.Execution.Receipt.WebDeploymentID, 100, f.record.Execution.Receipt.ServingDeploymentID)
			case "policy":
				p := *in.Quarantine.ExpectedMCPAccessPolicy
				p.Fingerprint = strings.Repeat("9", 64)
				in.Quarantine.ExpectedMCPAccessPolicy = &p
			case "foreign_digest":
				in.Quarantine.QualificationSHA256 = strings.Repeat("9", 64)
			case "snapshot_override":
				_, err = f.store.UpdateDeploymentTraffic(ctx, f.record.Execution.Receipt.WebDeploymentID, 100, f.record.Execution.Receipt.ServingDeploymentID)
				var current api.TaskToolRecoveryQualification
				for _, pin := range f.record.Intent.Apps {
					review, e := f.store.(state.ReleaseReviewStore).ReadReleaseReview(ctx, pin.Review.AppID, api.DefaultEnvScope)
					if e != nil {
						t.Fatal(e)
					}
					pin.Review = review
					current.NativeApps = append(current.NativeApps, pin)
				}
				in.Quarantine.Qualification = &current
			case "claim_missing":
				in.Quarantine.ClaimToken = ""
			}
			if err != nil {
				t.Fatal(err)
			}
			w := f.quarantineCall(t, in, false)
			if w.Code < 400 {
				t.Fatalf("changed native evidence admitted %d %s", w.Code, w.Body)
			}
			r, err := f.store.(state.TaskToolReleaseIntentStore).GetTaskToolReleaseIntent(ctx, f.account.ID, f.record.ID)
			if err != nil || r.Execution.Recovery.Quarantine.State != "qualified" {
				t.Fatal("failed admission consumed authority", err)
			}
		})
	}
	t.Run("concurrent_claims", func(t *testing.T) {
		f, in := quarantineNativeFixture(t, store(t))
		var wg sync.WaitGroup
		codes := make(chan int, 2)
		for range 2 {
			wg.Add(1)
			go func() { defer wg.Done(); codes <- f.quarantineCall(t, in, false).Code }()
		}
		wg.Wait()
		close(codes)
		ok := 0
		for code := range codes {
			if code == 200 {
				ok++
			}
		}
		if ok != 1 {
			t.Fatal("expected exactly one admitted attempt", ok)
		}
	})
}

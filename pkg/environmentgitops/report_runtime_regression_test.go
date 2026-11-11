package environmentgitops_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestReviewRuntimeDriftMustNotEmitInSyncRecovery(t *testing.T) {
	store, source, desired, base, worker := setup(t, "report")
	hook, err := store.CreateAccountReleaseWebhookIfUnderQuota(t.Context(), state.AppWebhook{
		AccountID: source.AccountID, Scope: state.AppWebhookScopeAccount, TargetURL: "https://example.test/gitops",
		SecretSealed: []byte("sealed"), EventFilter: []string{"environment.gitops.changed"}, Enabled: true,
	}, api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatal(err)
	}
	runtime := &runtimeBackend{backend: base, ready: false}
	worker.Backend = runtime
	base.observation.State.Fields[1].Value = json.RawMessage(`"console-edit"`)
	if worked, err := worker.RunOnce(t.Context()); err != nil || !worked {
		t.Fatalf("first run: %v %v", worked, err)
	}
	base.observation.State.Fields = desired.Fields
	worker.Now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	if worked, err := worker.RunOnce(t.Context()); err != nil || !worked {
		t.Fatalf("second run: %v %v", worked, err)
	}
	run := lastRun(t, store, source)
	if run.Status != "drifted" || run.ErrorCode != "environment_runtime_unacknowledged" {
		t.Fatalf("fixture: %+v", run)
	}
	rows, _, err := store.ListAccountReleaseWebhookDeliveries(t.Context(), source.AccountID, hook.ID, 50, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		var p api.EnvironmentGitOpsChangedWebhookPayload
		if err := json.Unmarshal(row.Payload, &p); err != nil {
			t.Fatal(err)
		}
		t.Logf("notification: %s -> %s, error_code=%s; run=%s", p.PreviousStatus, p.Status, p.ErrorCode, run.Status)
		if p.PreviousStatus == "drifted" && p.Status == "in_sync" {
			t.Error("false recovery: runtime is still unacknowledged")
		}
	}
	runtime.ready = true
	worker.Now = func() time.Time { return time.Now().Add(4 * time.Minute) }
	if worked, err := worker.RunOnce(t.Context()); err != nil || !worked {
		t.Fatalf("recovery run: %t %v", worked, err)
	}
	rows, _, err = store.ListAccountReleaseWebhookDeliveries(t.Context(), source.AccountID, hook.ID, 50, "")
	if err != nil {
		t.Fatal(err)
	}
	recoveries := 0
	for _, row := range rows {
		var payload api.EnvironmentGitOpsChangedWebhookPayload
		if err := json.Unmarshal(row.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Status == "in_sync" {
			recoveries++
		}
	}
	if recoveries != 1 || lastRun(t, store, source).Status != "converged" {
		t.Fatalf("fresh runtime did not produce exactly one recovery: %d", recoveries)
	}
}

//go:build !no_pg

package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/bindingcheck"
	"github.com/onebox-faas/faas/pkg/state"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPgVerificationRunAdmissionAtomicReplayAndCancellation(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	acct, err := s.CreateAccount(ctx, "run@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "run", Type: state.AppTypeFunction, WorkloadClass: state.WorkloadClassHTTP, Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	env, _ := json.Marshal(map[string]string{api.QueueCanaryCapabilityEnv: `["orders"]`})
	dep, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:run", OverrideEnv: env, Scope: "default"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetDeploymentRootfs(ctx, dep.ID, "/test/rootfs", "test/rootfs", 1); err != nil {
		t.Fatal(err)
	}
	if err = s.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	_, err = s.CreateQueueBindingWithConsumer(ctx, state.QueueBinding{AccountID: acct.ID, AppID: app.ID, Name: "orders", QueueName: "orders", Mode: "push", WorkloadClass: state.WorkloadClassHTTP, Enabled: true, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := s.SetBindingReleasePolicy(ctx, acct.ID, app.ID, "default", api.SetBindingReleasePolicyRequest{ExpectedRevision: new(int64), Mode: "enforce", MaxVerificationAge: "5m", QueueRequirements: map[string]string{"orders": api.QueueEvidenceHandlerCompletion}})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.MarkAppRuntimeConfigChanged(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	stamp, _, err := s.AppRuntimeConfigChangedAt(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	inventory := api.AppBindingInventory{App: app.Slug, Complete: true, RequestedDeploymentID: dep.ID, VerificationDeploymentID: dep.ID, VerificationScope: "default", RuntimeFreshness: &api.BindingRuntimeFreshness{Source: "instance_started_at", ObservedAt: now, ConfigChangedAt: &stamp}}
	report := api.BindingVerificationRun{ID: uuid.NewString(), App: app.Slug, DeploymentID: dep.ID, Scope: "default", Status: "pending", CreatedAt: now, ExpiresAt: now.Add(time.Minute), Policy: policy, Inventory: inventory, ReviewDigest: bindingcheck.VerificationReviewDigest(inventory, policy), QueuePublish: true, Steps: []api.BindingVerificationRunStep{{Plan: api.BindingVerificationPlanStep{Type: api.BindingTypeQueue, Name: "orders", Binding: "orders", Scope: "default", Action: "queue-canary", RequiredEvidence: api.QueueEvidenceHandlerCompletion}, Status: "planned"}}}
	run, err := s.CreateBindingVerificationRun(ctx, acct.ID, app.ID, state.VerificationRunCreate{Report: report, Admissions: make([]state.VerificationRunAdmission, 1)})
	if err != nil {
		t.Fatal(err)
	}
	// Fail the journal write after normal message admission: both must roll back.
	_, err = pool.Exec(ctx, `CREATE FUNCTION reject_run_step() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'fixture step write failure'; END$$; CREATE TRIGGER reject_run_step BEFORE INSERT ON binding_verification_run_steps FOR EACH ROW EXECUTE FUNCTION reject_run_step()`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AdmitBindingVerificationRunStep(ctx, acct.ID, app.ID, run.ID, 0); err == nil {
		t.Fatal("journal failure accepted")
	}
	var messages, canaries int
	if err = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM invocations WHERE app_id=$1),(SELECT count(*) FROM queue_binding_canaries WHERE app_id=$1)`, app.ID).Scan(&messages, &canaries); err != nil || messages != 0 || canaries != 0 {
		t.Fatalf("partial admission: %d %d %v", messages, canaries, err)
	}
	if _, err = pool.Exec(ctx, `DROP TRIGGER reject_run_step ON binding_verification_run_steps; DROP FUNCTION reject_run_step()`); err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	results := make(chan api.BindingVerificationRunStep, 8)
	errs := make(chan error, 8)
	for range 8 {
		workers.Go(func() {
			step, err := s.AdmitBindingVerificationRunStep(ctx, acct.ID, app.ID, run.ID, 0)
			results <- step
			errs <- err
		})
	}
	workers.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	resource := ""
	for step := range results {
		if resource != "" && step.ResourceID != resource {
			t.Fatal("duplicate canary")
		}
		resource = step.ResourceID
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM invocations WHERE app_id=$1`, app.ID).Scan(&messages); err != nil || messages != 1 {
		t.Fatalf("messages=%d %v", messages, err)
	}
	recovered, err := s.GetBindingVerificationRun(ctx, acct.ID, app.ID, run.ID)
	if err != nil || recovered.Steps[0].ResourceID != resource || !recovered.Steps[0].Owned {
		t.Fatalf("lost-response receipt: %+v %v", recovered, err)
	}
	canary, err := s.GetQueueBindingCanary(ctx, acct.ID, app.ID, resource)
	if err != nil || canary.ExpiresAt.After(run.ExpiresAt) {
		t.Fatalf("deadline renewal: %+v %v", canary, err)
	}
	if err = s.SetBindingVerificationRunStatus(ctx, acct.ID, app.ID, run.ID, "cancelled"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AdmitBindingVerificationRunStep(ctx, acct.ID, app.ID, run.ID, 0); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("cancelled admission: %v", err)
	}
	// A task uses the same journal transaction and immutable original deadline.
	report.ID = uuid.NewString()
	report.Steps[0] = api.BindingVerificationRunStep{Plan: api.BindingVerificationPlanStep{Type: api.BindingTypeService, Name: "billing", Binding: "BILLING", Scope: "app", Action: "probe"}, Status: "planned"}
	params := &state.CreateAppTaskParams{AccountID: acct.ID, AppID: app.ID, DeploymentID: dep.ID, Kind: state.AppTaskKindManual, RequireLiveDeployment: true, BindingVerification: &state.BindingVerificationPin{Type: api.BindingTypeService, Binding: "billing", Revision: strings.Repeat("a", 64)}, Command: []string{api.AppTaskServiceBindingProbeCommand, "billing"}, TimeoutSeconds: api.BindingVerificationRunProbeTimeoutSeconds, MaxOutputBytes: api.BindingVerificationRunProbeOutputBytes, CreatedAt: now}
	taskRun, err := s.CreateBindingVerificationRun(ctx, acct.ID, app.ID, state.VerificationRunCreate{Report: report, Admissions: []state.VerificationRunAdmission{{Task: params}}})
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.AdmitBindingVerificationRunStep(ctx, acct.ID, app.ID, taskRun.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.AdmitBindingVerificationRunStep(ctx, acct.ID, app.ID, taskRun.ID, 0)
	if err != nil || first.ResourceID != second.ResourceID {
		t.Fatalf("task replay: %+v %+v %v", first, second, err)
	}
	task, err := s.AppTaskByID(ctx, acct.ID, app.ID, first.ResourceID)
	if err != nil || task.StartDeadlineAt == nil || !task.StartDeadlineAt.Equal(run.ExpiresAt) {
		t.Fatalf("task deadline: %+v %v", task, err)
	}
	// Cancellation stays possible after configuration drift and expiry.
	if _, err = pool.Exec(ctx, `UPDATE binding_verification_runs SET expires_at=clock_timestamp()-interval '59 seconds',created_at=clock_timestamp()-interval '1 minute' WHERE id=$1`, taskRun.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.CheckBindingVerificationRun(context.Background(), acct.ID, app.ID, taskRun.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("expired resume: %v", err)
	}
	if err = s.SetBindingVerificationRunStatus(ctx, acct.ID, app.ID, taskRun.ID, "cancelled"); err != nil {
		t.Fatal(err)
	}
}

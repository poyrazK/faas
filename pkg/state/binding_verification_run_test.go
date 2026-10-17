package state

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/bindingcheck"
	"strings"
	"sync"
	"testing"
	"time"
)

func verificationReceiptFixture(t *testing.T) (*MemStore, App, Invocation, VerificationRunCreate) {
	t.Helper()
	m, app, inv := dispatchCanaryFixture(t)
	// The canary fixture supplies real binding/deployment identities; remove its
	// unclaimed setup message so the run owns the next normal canary admission.
	m.mu.Lock()
	delete(m.queueBindingCanaries, inv.ID)
	delete(m.invocations, inv.ID)
	m.mu.Unlock()
	ctx := context.Background()
	policy, err := m.SetBindingReleasePolicy(ctx, app.AccountID, app.ID, inv.DeploymentScope, api.SetBindingReleasePolicyRequest{ExpectedRevision: new(int64), Mode: "enforce", QueueRequirements: map[string]string{"orders": api.QueueEvidenceHandlerCompletion}, MaxVerificationAge: "5m"})
	if err != nil {
		t.Fatal(err)
	}
	if err = m.MarkAppRuntimeConfigChanged(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	stamp, _, err := m.AppRuntimeConfigChangedAt(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	var headers map[string]string
	if err = json.Unmarshal(inv.Headers, &headers); err != nil {
		t.Fatal(err)
	}
	deployment := headers[api.RevisionHeader]
	now := time.Now().UTC().Truncate(time.Microsecond)
	inventory := api.AppBindingInventory{App: app.Slug, Complete: true, RequestedDeploymentID: deployment, VerificationDeploymentID: deployment, VerificationScope: policy.Scope, RuntimeFreshness: &api.BindingRuntimeFreshness{Source: "instance_started_at", ObservedAt: now, ConfigChangedAt: &stamp}}
	plan := api.BindingVerificationPlanStep{Type: api.BindingTypeQueue, Name: "orders", Binding: "orders", Scope: policy.Scope, Action: "queue-canary", RequiredEvidence: api.QueueEvidenceHandlerCompletion}
	report := api.BindingVerificationRun{ID: uuid.NewString(), App: app.Slug, DeploymentID: inventory.VerificationDeploymentID, Scope: policy.Scope, CreatedAt: now, ExpiresAt: now.Add(time.Minute), Status: "pending", Policy: policy, Inventory: inventory, QueuePublish: true, ReviewDigest: bindingcheck.VerificationReviewDigest(inventory, policy), Steps: []api.BindingVerificationRunStep{{Plan: plan, Status: "planned"}}}
	return m, app, inv, VerificationRunCreate{Report: report, Admissions: make([]VerificationRunAdmission, 1)}
}

func TestVerificationRunAdmissionIsIdempotentAndFenced(t *testing.T) {
	m, app, _, in := verificationReceiptFixture(t)
	ctx := context.Background()
	run, err := m.CreateBindingVerificationRun(ctx, app.AccountID, app.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	ids := make(chan string, 12)
	errs := make(chan error, 12)
	for range 12 {
		workers.Go(func() {
			step, err := m.AdmitBindingVerificationRunStep(ctx, app.AccountID, app.ID, run.ID, 0)
			ids <- step.ResourceID
			errs <- err
		})
	}
	workers.Wait()
	close(ids)
	close(errs)
	resource := ""
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	for id := range ids {
		if resource != "" && resource != id {
			t.Fatal("duplicate message admission")
		}
		resource = id
	}
	if len(m.invocations) != 1 {
		t.Fatalf("messages=%d", len(m.invocations))
	}
	// Discarding the first response does not discard the committed receipt.
	loaded, err := m.GetBindingVerificationRun(ctx, app.AccountID, app.ID, run.ID)
	if err != nil || loaded.Steps[0].ResourceID != resource || !loaded.Steps[0].Owned {
		t.Fatalf("receipt: %+v %v", loaded, err)
	}
	canary, err := m.GetQueueBindingCanary(ctx, app.AccountID, app.ID, resource)
	if err != nil || canary.ExpiresAt.After(run.ExpiresAt) {
		t.Fatalf("deadline extended: %+v %v", canary, err)
	}
	duplicate, err := m.CreateBindingVerificationRun(ctx, app.AccountID, app.ID, in)
	if err != nil || !duplicate.ExpiresAt.Equal(run.ExpiresAt) || duplicate.Steps[0].ResourceID != resource {
		t.Fatalf("create replay: %+v %v", duplicate, err)
	}
	if _, err = m.GetBindingVerificationRun(ctx, uuid.NewString(), app.ID, run.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign receipt: %v", err)
	}
	if err = m.SetBindingVerificationRunStatus(ctx, app.AccountID, app.ID, run.ID, "cancelled"); err != nil {
		t.Fatal(err)
	}
	if _, err = m.AdmitBindingVerificationRunStep(ctx, app.AccountID, app.ID, run.ID, 0); !errors.Is(err, ErrConflict) {
		t.Fatalf("cancelled admission: %v", err)
	}
}
func TestVerificationRunDriftExpiryAndPrivatePins(t *testing.T) {
	for _, change := range []string{"expiry", "policy", "configuration", "artifact", "consumer"} {
		t.Run(change, func(t *testing.T) {
			m, app, inv, in := verificationReceiptFixture(t)
			ctx := context.Background()
			run, err := m.CreateBindingVerificationRun(ctx, app.AccountID, app.ID, in)
			if err != nil {
				t.Fatal(err)
			}
			m.mu.Lock()
			r := m.bindingVerificationRuns[run.ID]
			switch change {
			case "expiry":
				r.Report.ExpiresAt = time.Now().Add(-time.Second)
				m.bindingVerificationRuns[run.ID] = r
			case "policy":
				key := bindingReleasePolicyKey{app.ID, run.Scope}
				p := m.bindingReleasePolicies[key]
				p.Revision++
				m.bindingReleasePolicies[key] = p
			case "configuration":
				m.runtimeConfigChangedAt[app.ID] = time.Now().Add(time.Second)
			case "artifact":
				d := m.deployments[run.DeploymentID]
				d.RootfsKey += "changed"
				m.deployments[d.ID] = d
			case "consumer":
				b := m.queueBindings[inv.QueueBindingID]
				b.MaxConcurrency++
				m.queueBindings[b.ID] = b
			}
			m.mu.Unlock()
			if _, err = m.AdmitBindingVerificationRunStep(ctx, app.AccountID, app.ID, run.ID, 0); !errors.Is(err, ErrConflict) {
				t.Fatalf("drift accepted: %v", err)
			}
			if len(m.invocations) != 0 {
				t.Fatal("drift admitted message")
			}
		})
	}
}
func TestVerificationRunTaskReceiptSharesAdmission(t *testing.T) {
	m, app, _, in := verificationReceiptFixture(t)
	ctx := context.Background()
	in.Report.Steps[0].Plan = api.BindingVerificationPlanStep{Type: api.BindingTypeService, Name: "billing", Binding: "BILLING", Scope: "app", Action: "probe"}
	pin := &BindingVerificationPin{Type: api.BindingTypeService, Binding: "billing", Revision: strings.Repeat("a", 64)}
	in.Admissions[0].Task = &CreateAppTaskParams{AccountID: app.AccountID, AppID: app.ID, DeploymentID: in.Report.DeploymentID, Kind: AppTaskKindManual, RequireLiveDeployment: true, BindingVerification: pin, Command: []string{api.AppTaskServiceBindingProbeCommand, "billing"}, TimeoutSeconds: api.BindingVerificationRunProbeTimeoutSeconds, MaxOutputBytes: api.BindingVerificationRunProbeOutputBytes, CreatedAt: in.Report.CreatedAt}
	run, err := m.CreateBindingVerificationRun(ctx, app.AccountID, app.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	first, err := m.AdmitBindingVerificationRunStep(ctx, app.AccountID, app.ID, run.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.AdmitBindingVerificationRunStep(ctx, app.AccountID, app.ID, run.ID, 0)
	if err != nil || first.ResourceID != second.ResourceID || len(m.appTasks) != 1 {
		t.Fatalf("duplicate task: %+v %+v %v", first, second, err)
	}
	task, err := m.AppTaskByID(ctx, app.AccountID, app.ID, first.ResourceID)
	if err != nil || task.StartDeadlineAt == nil || !task.StartDeadlineAt.Equal(run.ExpiresAt) {
		t.Fatalf("task deadline: %+v %v", task, err)
	}
	changed := cloneVerificationRecord(m.bindingVerificationRuns[run.ID]).Admissions
	changed[0].Task.BindingVerification.Revision = strings.Repeat("b", 64)
	if err = m.CheckBindingVerificationRun(ctx, app.AccountID, app.ID, run.ID, changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("private pin changed: %v", err)
	}
}

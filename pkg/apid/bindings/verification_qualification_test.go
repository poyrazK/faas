package bindings

import (
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/bindingcheck"
	"github.com/onebox-faas/faas/pkg/state"
	"strings"
	"testing"
	"time"
)

func TestPendingQualificationAcknowledgmentClassification(t *testing.T) {
	for _, mode := range []string{"missing", "stale", "candidate", "failed", "malformed", "incomplete", "future", "mixed", "unknown"} {
		t.Run(mode, func(t *testing.T) {
			now := time.Now()
			at := now.Add(-time.Second)
			item := api.AppBindingInventoryItem{Type: api.BindingTypePostgres, Name: "database", Binding: "DATABASE_URL", Scope: "production", ApplicationAdoption: &api.BindingApplicationAdoption{Source: "application_ack", Complete: true, ObservedAt: now, SecretsExpected: 1, SecretsObserved: 1, Targets: []api.BindingApplicationAckTarget{{DeploymentID: "deployment", InstanceID: "instance", Key: "DATABASE_URL", RuntimeState: "running", CurrentVersion: 2, ReloadSupport: "enabled", ReloadVersion: 2, Projection: "updated", Signal: "sent", ReloadAt: &at, ProcessGeneration: strings.Repeat("a", 32)}}}}
			code := "application_adoption_unknown"
			allowed := false
			switch mode {
			case "missing":
				allowed = true
			case "stale":
				allowed = true
				code = "application_ack_stale"
				target := &item.ApplicationAdoption.Targets[0]
				target.ApplicationAckAt = &at
				target.ApplicationAckVersion = 1
				target.ApplicationAck = "applied"
				target.ApplicationAckGeneration = target.ProcessGeneration
			case "candidate":
				allowed = true
				code = "application_ack_candidate_unobserved"
				item.ApplicationAdoption.Targets = nil
			case "failed":
				item.ApplicationAdoption.Targets[0].Projection = "failed"
			case "malformed":
				item.ApplicationAdoption.Targets[0].ProcessGeneration = "invalid"
			case "incomplete":
				item.ApplicationAdoption.Complete = false
			case "future":
				future := now.Add(time.Hour)
				item.ApplicationAdoption.Targets[0].ApplicationAckAt = &future
			case "unknown":
				code = "new_unclassified_code"
			}
			i := api.AppBindingInventory{App: "api", Complete: true, Bindings: []api.AppBindingInventoryItem{item}}
			report := bindingcheck.Report{CheckedAt: now, Blockers: []api.BindingCheckFinding{{Type: item.Type, Name: item.Name, Binding: item.Binding, Scope: item.Scope, Code: code}}}
			if mode == "mixed" {
				report.Blockers = append(report.Blockers, api.BindingCheckFinding{Code: "verification_expired"})
			}
			q := pendingQualification(i, report)
			if (q != nil) != allowed {
				t.Fatalf("classification %+v", q)
			}
		})
	}
}
func TestVerificationCompletionWaitsForConsumerWithoutAdmission(t *testing.T) {
	b, m, i, p := verificationBackendFixture(t)
	ctx := t.Context()
	next, e := m.SetBindingReleasePolicy(ctx, b.AccountID, b.AppID, p.Scope, api.SetBindingReleasePolicyRequest{ExpectedRevision: &p.Revision, Mode: "enforce", MaxVerificationAge: "5m", QueueRequirements: map[string]string{"orders": api.QueueEvidenceConsumerReadiness}})
	if e != nil {
		t.Fatal(e)
	}
	*p = next
	env := p.Scope
	i.Bindings = []api.AppBindingInventoryItem{{Type: api.BindingTypeQueue, Name: "orders", Binding: "orders", Scope: env, QueueEnvironment: &env, State: "enabled", Access: "push", ConsumerState: "active", ConsumerLiveness: "not_observed"}}
	run, e := b.Create(ctx, newReceiptRequest(*i, *p))
	if e != nil {
		t.Fatal(e)
	}
	if len(run.Steps) != 1 || run.Steps[0].Plan.Action != "observe" {
		t.Fatalf("unexpected plan %+v", run)
	}
	pending, e := b.Complete(ctx, run.ID)
	if e != nil || pending.Status != "pending" || pending.Qualification == nil || pending.Qualification.Conditions[0].Code != "queue_consumer_unobserved" || !pending.ExpiresAt.Equal(run.ExpiresAt) || pending.Steps[0].ResourceID != "" {
		t.Fatalf("pending: %+v %v", pending, e)
	}
	now := time.Now()
	i.Bindings[0].ConsumerLiveness = "healthy"
	i.Bindings[0].ObservedAt = &now
	passed, e := b.Complete(ctx, run.ID)
	if e != nil || passed.Status != "passed" || passed.Qualification != nil || passed.ID != run.ID {
		t.Fatalf("completion %+v %v", passed, e)
	}
}
func TestGraphQualificationPendingAndPermanentDrift(t *testing.T) {
	for _, drift := range []bool{false, true} {
		t.Run(map[bool]string{false: "settles", true: "drift"}[drift], func(t *testing.T) {
			b, in, inventories := graphBackendFixture(t)
			run, e := b.Create(t.Context(), in)
			if e != nil {
				t.Fatal(e)
			}
			runtime := &inventories["graph-last"].RuntimeFreshness.Deployments[0]
			runtime.Starting = 1
			runtime.Resident.Current = 2
			runtime.Status = "updating"
			pending, e := b.Complete(t.Context(), run.ID)
			if e != nil || pending.Status != "pending" || pending.Qualification == nil || pending.Qualification.Conditions[0].App != "graph-last" {
				t.Fatalf("graph pending %+v %v", pending, e)
			}
			if drift {
				inventories["graph-last"].Bindings[0].State = "changed"
				_, e = b.Complete(t.Context(), run.ID)
				if !errors.Is(e, state.ErrConflict) {
					t.Fatalf("drift waited: %v", e)
				}
				return
			}
			runtime.Starting = 0
			runtime.Resident.Current = 1
			runtime.Status = "current"
			passed, e := b.Complete(t.Context(), run.ID)
			if e != nil || passed.Status != "passed" || passed.Qualification != nil {
				t.Fatalf("graph completion %+v %v", passed, e)
			}
		})
	}
}

func TestPendingQualificationRejectsMaskedConsumerFailure(t *testing.T) {
	now := time.Now()
	stale := now.Add(-time.Hour)
	for _, liveness := range []string{"healthy", "stale", "not_observed", "degraded", "unknown", ""} {
		t.Run(liveness, func(t *testing.T) {
			item := api.AppBindingInventoryItem{Type: api.BindingTypeQueue, Name: "orders", Binding: "orders", Scope: "production", State: "enabled", Access: "push", ConsumerState: "active", ConsumerLiveness: liveness, ObservedAt: &stale}
			report := bindingcheck.Report{CheckedAt: now, Blockers: []api.BindingCheckFinding{{Code: "queue_consumer_stale", Type: item.Type, Name: item.Name, Binding: item.Binding, Scope: item.Scope}}}
			q := pendingQualification(api.AppBindingInventory{App: "api", Complete: true, Bindings: []api.AppBindingInventoryItem{item}}, report)
			want := liveness == "healthy" || liveness == "stale" || liveness == "not_observed"
			if (q != nil) != want {
				t.Fatalf("masked %q: %+v", liveness, q)
			}
		})
	}
}

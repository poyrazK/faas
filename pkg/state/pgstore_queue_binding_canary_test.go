//go:build !no_pg

// adr:1352
package state_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestPgQueueCanaryAcknowledgementFences(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	account, err := s.CreateAccount(ctx, "queue-canary@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "queue-canary", Type: state.AppTypeFunction, WorkloadClass: state.WorkloadClassHTTP, Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	env, _ := json.Marshal(map[string]string{api.QueueCanaryCapabilityEnv: `["orders"]`})
	dep, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, ImageDigest: "sha256:queue-canary", OverrideEnv: env, Kind: state.DeploymentKindImage, Scope: "default"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetDeploymentRootfs(ctx, dep.ID, "/test/rootfs", "test/rootfs", 1); err != nil {
		t.Fatal(err)
	}
	if err = s.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	created, err := s.CreateQueueBindingWithConsumer(ctx, state.QueueBinding{AccountID: account.ID, AppID: app.ID, Name: "orders", QueueName: "orders", Mode: "push", WorkloadClass: state.WorkloadClassHTTP, Enabled: true, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	req := api.CreateQueueBindingCanaryRequest{DeploymentID: dep.ID, Binding: "orders", TTLSeconds: 60, PublishConsent: true}
	r, err := s.CreateQueueBindingCanary(ctx, account.ID, app.ID, req)
	if err != nil {
		t.Fatal(err)
	}
	assertBorrowers := func(completed int64) {
		t.Helper()
		raw, err := sqlc.New().ReadCloneDeploymentBorrowers(ctx, pool, pgtype.UUID{Bytes: uuid.MustParse(dep.ID), Valid: true})
		if err != nil {
			t.Fatal(err)
		}
		var counts map[string]int64
		if err = json.Unmarshal(raw, &counts); err != nil {
			t.Fatal(err)
		}
		if counts["queue_binding_canaries_deployment_id_fkey"] != 1 || counts["queue_binding_canaries_completed_deployment_id_fkey"] != completed {
			t.Fatalf("canary deployment references omitted: %v", counts)
		}
	}
	assertBorrowers(0)
	trigger := created.Changes[0].TriggerID
	if _, err = s.CreateQueueBindingCanary(ctx, account.ID, app.ID, req); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("duplicate: %v", err)
	}
	beforeClaim, err := s.ReadBindingPromotionRevision(ctx, account.ID, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimQueueTriggerInvocation(ctx, r.ID, trigger, app.ID, "orders", 30)
	if err != nil {
		t.Fatal(err)
	}
	afterClaim, err := s.ReadBindingPromotionRevision(ctx, account.ID, app.ID)
	if err != nil || afterClaim == beforeClaim {
		t.Fatalf("claim did not invalidate the promotion revision: %s %s %v", beforeClaim, afterClaim, err)
	}
	if claimed.Attempts != 1 {
		t.Fatalf("attempt: %+v", claimed)
	}
	if _, version, err := state.ResolveInvocationVersion(ctx, s, claimed); err != nil || version.DeploymentID != dep.ID {
		t.Fatalf("canary dispatch authority: %+v %v", version, err)
	}
	ack := state.EnvironmentWorkloadQueueServingAcknowledgement{AppID: app.ID, BindingID: created.Binding.ID, TriggerID: trigger, InvocationID: r.ID, DeploymentID: dep.ID, Mode: "push"}
	ids, err := s.CompleteEnvironmentQueueDeliveryClaims(ctx, app.ID, trigger, "queue", []string{r.ID}, []int32{2}, []int64{0}, []state.EnvironmentWorkloadQueueServingAcknowledgement{ack})
	if err != nil || len(ids) != 0 {
		t.Fatalf("stale acknowledgement: %v %v", ids, err)
	}
	got, err := s.GetQueueBindingCanary(ctx, account.ID, app.ID, r.ID)
	if err != nil || got.Status != "dispatching" || got.DeliveredAt != nil {
		t.Fatalf("poll/claim false pass: %+v %v", got, err)
	}
	ids, err = s.CompleteEnvironmentQueueDeliveryClaims(ctx, app.ID, trigger, "queue", []string{r.ID}, []int32{1}, []int64{0}, []state.EnvironmentWorkloadQueueServingAcknowledgement{ack})
	if err != nil || len(ids) != 1 {
		t.Fatalf("completion: %v %v", ids, err)
	}
	got, err = s.GetQueueBindingCanary(ctx, account.ID, app.ID, r.ID)
	if err != nil || got.Status != "succeeded" || got.CompletedAt == nil {
		t.Fatalf("proof: %+v %v", got, err)
	}
	assertBorrowers(1)
	proofRevision, err := s.ReadBindingPromotionRevision(ctx, account.ID, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, "UPDATE invocations SET state='dead_letter',outcome='dead_letter' WHERE id=$1", r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, "UPDATE invocations SET state='pending',attempts=0,outcome=NULL,completed_at=NULL WHERE id=$1", r.ID); err != nil {
		t.Fatal(err)
	}
	replayRevision, err := s.ReadBindingPromotionRevision(ctx, account.ID, app.ID)
	if err != nil || replayRevision == proofRevision {
		t.Fatalf("replay did not invalidate approved evidence: %s %s %v", proofRevision, replayRevision, err)
	}
	got, err = s.GetQueueBindingCanary(ctx, account.ID, app.ID, r.ID)
	if err != nil || got.Status != "failed" {
		t.Fatalf("replayed completion passed: %+v %v", got, err)
	}
	if _, _, err := state.ResolveInvocationVersion(ctx, s, claimed); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("replayed canary bypassed dispatch authority: %v", err)
	}
	got, err = s.CancelQueueBindingCanary(ctx, account.ID, app.ID, r.ID)
	if err != nil || got.Status != "cancelled" {
		t.Fatalf("cancel proof: %+v %v", got, err)
	}
	claimed.Source = "cron"
	claimed.Payload = nil
	if _, _, err := state.ResolveInvocationVersion(ctx, s, claimed); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("cancelled canary bypassed authority with changed source: %v", err)
	}
	for i := 0; i < api.MustLimitsFor(api.PlanPro).MaxQueueDepth; i++ {
		if _, err = s.EnqueueInvocation(ctx, state.Invocation{AccountID: account.ID, AppID: app.ID, Source: state.InvocationQueue, QueueName: "orders", State: state.InvocationPending, DueAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.CreateQueueBindingCanary(ctx, account.ID, app.ID, req); !errors.Is(err, state.ErrQuotaExceeded) {
		t.Fatalf("canary bypassed queue depth: %v", err)
	}

}

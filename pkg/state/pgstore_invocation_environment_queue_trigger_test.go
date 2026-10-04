//go:build !no_pg

// adr: 566
package state_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgStageDelayedTasksIgnoreProductionQueueTrigger(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testStageDelayedTasksIgnoreProductionQueueTrigger(t, store)
}

func TestPgProductionNamedQueueClaimRejectsStageWork(t *testing.T) {
	store, ctx, pool := pgWithPool(t)
	f := seedInvocationEnvironment(t, store)
	if _, err := store.CreateQueueBinding(ctx, state.QueueBinding{AccountID: f.account.ID, AppID: f.app.ID, Name: "orders", QueueName: "orders",
		Mode: "push", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 1}); err != nil {
		t.Fatal(err)
	}
	trigger, err := store.CreateTriggerIfUnderQuota(ctx, f.app.ID, "queue", "orders", true, []byte(`{"mode":"queue"}`),
		"queue", 1, 20, 3, 8192, "commit", api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatal(err)
	}
	for _, fault := range []struct{ name, key, pin string }{
		{"registered_owner", api.RevisionHeader, f.stage.ID},
		{"missing_owner_revision", api.RevisionHeader, f.stage.ID},
		{"missing_owner_release", api.ReleaseHeader, f.stageRelease.ID},
		{"case_folded_revision", "x-gregale-revision", f.stage.ID},
		{"case_folded_release", "x-gregale-release", f.stageRelease.ID},
		{"compact_revision", "x-gregale-revision", strings.ReplaceAll(f.stage.ID, "-", "")},
		{"urn_revision", "x-gregale-revision", "urn:uuid:" + f.stage.ID},
		{"braced_release", "x-gregale-release", "{" + f.stageRelease.ID + "}"},
		{"trimmed_release", "x-gregale-release", "\t " + f.stageRelease.ID + " \n"},
	} {
		t.Run(fault.name, func(t *testing.T) {
			inv, err := store.EnqueueInvocation(ctx, state.Invocation{AccountID: f.account.ID, AppID: f.app.ID,
				Source: state.InvocationQueue, QueueName: "orders", DueAt: time.Now().Add(-time.Second)})
			if err != nil {
				t.Fatal(err)
			}
			headers, _ := json.Marshal(map[string]string{fault.key: fault.pin})
			var owner any
			if fault.name == "registered_owner" {
				env, err := store.ProjectEnvironmentBySlug(ctx, f.account.ID, f.project.ID, "staging")
				if err != nil {
					t.Fatal(err)
				}
				owner = env.ID
			}
			if _, err := pool.Exec(ctx, `UPDATE invocations SET headers=$2,environment_id=$3 WHERE id=$1`, inv.ID, headers, owner); err != nil {
				t.Fatal(err)
			}
			if _, err := store.ClaimQueueTriggerInvocation(ctx, inv.ID, trigger.ID.String(), f.app.ID, "orders", 30); !errors.Is(err, state.ErrConflict) && !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("production consumer adopted stage: %v", err)
			}
			if got, err := store.InvocationByID(ctx, inv.ID); err != nil || got.State != state.InvocationPending || got.Attempts != 0 || got.LeaseExpiresAt != nil || got.QuotaReserved {
				t.Fatalf("rejected stage claim mutated work: %+v, %v", got, err)
			}
		})
	}
	production, err := store.EnqueueInvocation(ctx, state.Invocation{AccountID: f.account.ID, AppID: f.app.ID,
		Source: state.InvocationQueue, QueueName: "orders", DueAt: time.Now().Add(-time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := store.ClaimQueueTriggerInvocation(ctx, production.ID, trigger.ID.String(), f.app.ID, "orders", 30); err != nil || got.State != state.InvocationDispatching || got.EnvironmentID != "" {
		t.Fatalf("production claim changed: %+v, %v", got, err)
	}
}

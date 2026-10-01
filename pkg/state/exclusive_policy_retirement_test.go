// adr: 427
package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/exclusivework"
)

func TestExclusivePolicyRetirement(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			ctx := t.Context()
			var base Store
			var owners ExclusiveWorkStore
			var atomic exclusiveAtomic
			if backend == "postgres" {
				store := NewPgStore(pgtest.OpenMigrated(t))
				base, owners, atomic = store, store, store.exclusiveAtomic
			} else {
				store := NewMemStore()
				base, owners, atomic = store, store, store.exclusiveAtomic
			}
			account, err := base.CreateAccount(ctx, uuid.NewString()+"@retirement.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := base.CreateApp(ctx, App{AccountID: account.ID, Slug: "retirement-" + uuid.NewString()[:8], Type: AppTypeApp, Runtime: "node22", RAMMB: 256})
			if err != nil {
				t.Fatal(err)
			}
			policy := exclusivework.Policy{Name: "retirement", Scope: "account", MemberAppIDs: []string{app.ID}, Contention: "queue", LeaseSeconds: 5, MaxAttemptSeconds: 60}
			original, err := owners.UpsertExclusiveWorkPolicy(ctx, account.ID, policy)
			if err != nil {
				t.Fatal(err)
			}
			admission := ExclusiveAdmission{AccountID: account.ID, AppID: app.ID, PolicyName: policy.Name, Key: json.RawMessage(`"lane"`), Request: json.RawMessage(`{"kind":"sync"}`)}
			op, _, err := owners.AdmitExclusiveOperation(ctx, admission)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := owners.RetireExclusiveWorkPolicy(ctx, account.ID, policy.Name); !errors.Is(err, ErrExclusivePolicyInUse) {
				t.Fatalf("pending retirement: %v", err)
			}
			if err := owners.CancelExclusiveOperation(ctx, account.ID, op.ID); err != nil {
				t.Fatal(err)
			}
			deployment, err := base.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:retirement-test", Status: DeployLive})
			if err != nil {
				t.Fatal(err)
			}
			nodeID := "retirement-test-node"
			if backend == "postgres" {
				node, err := base.(interface {
					ComputeNodeByName(context.Context, string) (ComputeNode, error)
				}).ComputeNodeByName(ctx, DefaultLocalNodeName)
				if err != nil {
					t.Fatal(err)
				}
				nodeID = node.ID
			}
			instance, err := base.CreateInstance(ctx, app.ID, deployment.ID, string(StateRunning), 256, nodeID, uuid.NewString())
			if err != nil {
				t.Fatal(err)
			}
			running, _, err := owners.AdmitExclusiveOperation(ctx, admission)
			if err != nil {
				t.Fatal(err)
			}
			claim, err := owners.ClaimExclusiveOperation(ctx, account.ID, running.ID, ExclusiveIncarnation(instance))
			if err != nil || claim.Generation == 0 {
				t.Fatalf("claim=%+v err=%v", claim, err)
			}
			if _, err := owners.RetireExclusiveWorkPolicy(ctx, account.ID, policy.Name); !errors.Is(err, ErrExclusivePolicyInUse) {
				t.Fatalf("running retirement: %v", err)
			}
			if err := owners.CommitExclusiveOperation(ctx, claim, json.RawMessage(`{"done":true}`), nil); err != nil {
				t.Fatal(err)
			}
			completedBefore, err := owners.ExclusiveOperationByID(ctx, account.ID, running.ID)
			if err != nil {
				t.Fatal(err)
			}
			var before exclusiveKey
			if err := atomic(ctx, func(tx exclusiveTransaction) error {
				key, err := tx.key(account.ID, op.KeyID)
				if err != nil {
					return err
				}
				before = key
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			retired, err := owners.RetireExclusiveWorkPolicy(ctx, account.ID, policy.Name)
			if err != nil || !retired.Retired || retired.ID != original.ID || retired.Revision != original.Revision+1 {
				t.Fatalf("retirement=%+v err=%v", retired, err)
			}
			repeat, err := owners.RetireExclusiveWorkPolicy(ctx, account.ID, policy.Name)
			if err != nil || !reflect.DeepEqual(retired, repeat) {
				t.Fatalf("repeat=%+v err=%v", repeat, err)
			}
			if _, err := owners.UpsertExclusiveWorkPolicy(ctx, account.ID, policy); !errors.Is(err, ErrConflict) {
				t.Fatalf("retired name reused: %v", err)
			}
			if _, _, err := owners.AdmitExclusiveOperation(ctx, admission); !errors.Is(err, ErrNotFound) {
				t.Fatalf("retired admission: %v", err)
			}
			history, err := owners.ExclusiveOperationByID(ctx, account.ID, op.ID)
			if err != nil || history.State != "cancelled" {
				t.Fatalf("history=%+v err=%v", history, err)
			}
			completedAfter, err := owners.ExclusiveOperationByID(ctx, account.ID, running.ID)
			if err != nil || !reflect.DeepEqual(completedBefore, completedAfter) {
				t.Fatalf("completed history changed: before=%+v after=%+v err=%v", completedBefore, completedAfter, err)
			}
			if _, err := owners.RetireExclusiveWorkPolicy(ctx, account.ID, "Bad_Name"); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("invalid name: %v", err)
			}
			if err := atomic(ctx, func(tx exclusiveTransaction) error {
				after, err := tx.key(account.ID, op.KeyID)
				if err == nil && !reflect.DeepEqual(before, after) {
					return fmt.Errorf("key changed: before=%+v after=%+v", before, after)
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
			foreign, err := base.CreateAccount(ctx, uuid.NewString()+"@retirement.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := owners.RetireExclusiveWorkPolicy(ctx, foreign.ID, policy.Name); !errors.Is(err, ErrNotFound) {
				t.Fatalf("foreign account retirement: %v", err)
			}
			retirementBindingRaces(t, base, owners, account.ID, app.ID, policy)
			retirementAdmissionRaces(t, owners, account.ID, policy, admission)
			retirementQuotaReuse(t, owners, account.ID, policy)
		})
	}
}

func retirementBindingRaces(t *testing.T, base Store, owners ExclusiveWorkStore, account, app string, policy exclusivework.Policy) {
	t.Helper()
	ctx := t.Context()
	cron, err := base.CreateCron(ctx, app, "0 0 1 1 *", "/sync", true)
	if err != nil {
		t.Fatal(err)
	}
	bindings := base.(ExclusiveTriggerBindingStore)
	for i := 0; i < 12; i++ {
		policy.Name = fmt.Sprintf("binding-race-%d", i)
		if _, err := owners.UpsertExclusiveWorkPolicy(ctx, account, policy); err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		var wg sync.WaitGroup
		var retireErr, bindingErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, retireErr = owners.RetireExclusiveWorkPolicy(ctx, account, policy.Name)
		}()
		go func() {
			defer wg.Done()
			<-start
			_, bindingErr = bindings.UpsertExclusiveTriggerBinding(ctx, ExclusiveTriggerBinding{AccountID: account, Source: "cron", TriggerID: cron.ID, PolicyName: policy.Name, Key: json.RawMessage(`"sync"`)})
		}()
		close(start)
		wg.Wait()
		switch {
		case retireErr == nil && errors.Is(bindingErr, ErrNotFound):
		case errors.Is(retireErr, ErrExclusivePolicyInUse) && bindingErr == nil:
			if err := bindings.DeleteExclusiveTriggerBinding(ctx, account, "cron", cron.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := owners.RetireExclusiveWorkPolicy(ctx, account, policy.Name); err != nil {
				t.Fatal(err)
			}
		default:
			t.Fatalf("retirement/binding race: retire=%v binding=%v", retireErr, bindingErr)
		}
	}
}

func retirementAdmissionRaces(t *testing.T, owners ExclusiveWorkStore, account string, policy exclusivework.Policy, admission ExclusiveAdmission) {
	t.Helper()
	ctx := t.Context()
	for i := 0; i < 12; i++ {
		policy.Name = fmt.Sprintf("admission-race-%d", i)
		if _, err := owners.UpsertExclusiveWorkPolicy(ctx, account, policy); err != nil {
			t.Fatal(err)
		}
		admission.PolicyName = policy.Name
		start := make(chan struct{})
		var wg sync.WaitGroup
		var retireErr, admitErr error
		var op ExclusiveOperation
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, retireErr = owners.RetireExclusiveWorkPolicy(ctx, account, policy.Name)
		}()
		go func() { defer wg.Done(); <-start; op, _, admitErr = owners.AdmitExclusiveOperation(ctx, admission) }()
		close(start)
		wg.Wait()
		switch {
		case retireErr == nil && errors.Is(admitErr, ErrNotFound):
		case errors.Is(retireErr, ErrExclusivePolicyInUse) && admitErr == nil:
			if err := owners.CancelExclusiveOperation(ctx, account, op.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := owners.RetireExclusiveWorkPolicy(ctx, account, policy.Name); err != nil {
				t.Fatal(err)
			}
		default:
			t.Fatalf("retirement/admission race: retire=%v admission=%v", retireErr, admitErr)
		}
	}
}

func retirementQuotaReuse(t *testing.T, owners ExclusiveWorkStore, account string, policy exclusivework.Policy) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < api.MaxExclusivePoliciesPerAccount; i++ {
		policy.Name = fmt.Sprintf("quota-%d", i)
		if _, err := owners.UpsertExclusiveWorkPolicy(ctx, account, policy); err != nil {
			t.Fatal(err)
		}
	}
	policy.Name = "over-quota"
	if _, err := owners.UpsertExclusiveWorkPolicy(ctx, account, policy); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("quota error: %v", err)
	}
	if _, err := owners.RetireExclusiveWorkPolicy(ctx, account, "quota-0"); err != nil {
		t.Fatal(err)
	}
	if _, err := owners.UpsertExclusiveWorkPolicy(ctx, account, policy); err != nil {
		t.Fatalf("retired quota slot not reusable: %v", err)
	}
	rows, err := owners.ListExclusiveWorkPolicies(ctx, account)
	if err != nil {
		t.Fatal(err)
	}
	active, retired := 0, 0
	for _, row := range rows {
		if row.Retired {
			retired++
		} else {
			active++
		}
	}
	if active != api.MaxExclusivePoliciesPerAccount || retired == 0 {
		t.Fatalf("active=%d retired=%d", active, retired)
	}
}

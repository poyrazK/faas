package state

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestWorkflowIdempotencyTenantNamespaces(t *testing.T) {
	workflowGuardStores(t, func(t *testing.T, store Store) {
		ctx := t.Context()
		account, err := store.CreateAccount(ctx, uuid.NewString()+"@example.com", api.PlanHobby)
		if err != nil {
			t.Fatal(err)
		}
		app, err := store.CreateApp(ctx, App{AccountID: account.ID, Slug: "idempotency-" + uuid.NewString(), Type: AppTypeFunction, Runtime: "node22", RAMMB: 256})
		if err != nil {
			t.Fatal(err)
		}
		required := true
		if _, err := store.UpdateApp(ctx, app.ID, UpdateAppParams{PlatformTenantRequired: &required, SetPlatformTenantRequired: true}); err != nil {
			t.Fatal(err)
		}
		tenants := store.(PlatformTenantStore)
		fingerprint := sha256.Sum256([]byte("same request"))
		var previousID string
		for _, name := range []string{"first", "second"} {
			tenant, _, err := tenants.CreatePlatformTenant(ctx, account.ID, name, name, 10)
			if err != nil {
				t.Fatal(err)
			}
			consumer, err := store.CreateAPIConsumer(ctx, account.ID, app.ID, name, name)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tenants.LinkPlatformTenantConsumer(ctx, account.ID, tenant.ID, consumer.ID); err != nil {
				t.Fatal(err)
			}
			run := &WorkflowRun{AppID: app.ID, PlatformTenantID: tenant.ID, WorkflowName: "test", DefinitionSnapshot: json.RawMessage(`{"name":"test","steps":[{"name":"send","run":"send"}]}`)}
			if _, replay, err := store.CreateWorkflowRunAdmittedWithIdempotencyKey(ctx, run, 10, 0, "same-key", fingerprint[:]); err != nil || replay {
				t.Fatalf("first creation: replay=%v err=%v", replay, err)
			}
			if run.ID == previousID {
				t.Fatal("tenant namespaces returned the same run")
			}
			previousID = run.ID
			lookup, err := store.GetWorkflowRunByIdempotencyKey(ctx, app.ID, "test", "same-key", fingerprint[:], tenant.ID)
			if err != nil || lookup.ID != run.ID {
				t.Fatalf("tenant lookup: run=%+v err=%v", lookup, err)
			}
			retry := &WorkflowRun{AppID: app.ID, PlatformTenantID: tenant.ID, WorkflowName: run.WorkflowName, DefinitionSnapshot: run.DefinitionSnapshot}
			if _, replay, err := store.CreateWorkflowRunAdmittedWithIdempotencyKey(ctx, retry, 10, 0, "same-key", fingerprint[:]); err != nil || !replay || retry.ID != run.ID {
				t.Fatalf("replay=%v run=%+v err=%v", replay, retry, err)
			}
		}
		if _, err := store.GetWorkflowRunByIdempotencyKey(ctx, app.ID, "test", "same-key", fingerprint[:]); !errors.Is(err, ErrWorkflowRunNotFound) {
			t.Fatalf("unscoped lookup exposed a tenant run: %v", err)
		}
	})
}

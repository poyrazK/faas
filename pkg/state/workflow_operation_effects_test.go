package state

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/exclusivework"
)

func TestTenantWorkflowManagedOperationEffectsStayTenantBound(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		ctx := context.Background()
		account, err := store.CreateAccount(ctx, uuid.NewString()+"@example.test", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		app, err := store.CreateApp(ctx, App{AccountID: account.ID, Slug: "tenant-effects-" + uuid.NewString()[:8], Type: AppTypeApp, RAMMB: 256})
		if err != nil {
			t.Fatal(err)
		}
		definition := api.WorkflowSpec{Name: "fulfill", Steps: []api.WorkflowStepSpec{{Name: "commit", Path: "/fulfill", ManagedOperation: true}}}
		definitions, err := json.Marshal([]api.WorkflowSpec{definition})
		if err != nil {
			t.Fatal(err)
		}
		deployment, err := store.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:effects", Status: DeployPending, Workflows: definitions})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
			t.Fatal(err)
		}
		required := true
		if _, err := store.UpdateApp(ctx, app.ID, UpdateAppParams{PlatformTenantRequired: &required, SetPlatformTenantRequired: true}); err != nil {
			t.Fatal(err)
		}

		tenants := store.(PlatformTenantStore)
		limits := api.MustLimitsFor(api.PlanPro)
		type tenantFixture struct {
			tenant  PlatformTenant
			hook    AppWebhook
			surface TenantSurface
		}
		makeTenant := func(name, target string) tenantFixture {
			t.Helper()
			tenant, _, err := tenants.CreatePlatformTenant(ctx, account.ID, name, name, 10)
			if err != nil {
				t.Fatal(err)
			}
			surface, err := store.CreateTenantSurfaceIfUnderQuota(ctx, CreateTenantSurfaceParams{
				AccountID: account.ID, AppID: app.ID, Name: "surface-" + name, CertKind: CertKindPerHostSAN,
			}, limits)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tenants.LinkPlatformTenantSurface(ctx, account.ID, tenant.ID, surface.ID); err != nil {
				t.Fatal(err)
			}
			if err := store.UpdateTenantSurfaceStatus(ctx, surface.ID, SurfaceStatusActive); err != nil {
				t.Fatal(err)
			}
			hook, err := store.(PlatformTenantWebhookStore).CreatePlatformTenantWebhookIfUnderQuota(ctx, AppWebhook{
				AccountID: account.ID, PlatformTenantID: tenant.ID, TargetURL: target,
				SecretSealed: []byte("sealed"), EventFilter: []string{OperationEffectEvent}, Enabled: true,
			}, limits)
			if err != nil {
				t.Fatal(err)
			}
			return tenantFixture{tenant: tenant, hook: hook, surface: surface}
		}
		customerA := makeTenant("customer-a", "https://a.example.test/events")
		customerB := makeTenant("customer-b", "https://b.example.test/events")
		appHook, err := store.CreateAppWebhook(ctx, AppWebhook{
			AccountID: account.ID, AppID: app.ID, TargetURL: "https://app.example.test/events",
			SecretSealed: []byte("sealed"), EventFilter: []string{OperationEffectEvent}, Enabled: true,
		})
		if err != nil {
			t.Fatal(err)
		}

		snapshot, err := json.Marshal(definition)
		if err != nil {
			t.Fatal(err)
		}
		run := &WorkflowRun{AppID: app.ID, PlatformTenantID: customerA.tenant.ID, WorkflowName: definition.Name, DefinitionSnapshot: snapshot}
		if err := store.CreateWorkflowRun(ctx, run); err != nil {
			t.Fatal(err)
		}
		if err := store.CreateWorkflowSteps(ctx, run.ID, []*WorkflowStep{{StepName: "commit"}}); err != nil {
			t.Fatal(err)
		}
		claimed, err := store.ClaimNextDueWorkflowRun(ctx)
		if err != nil || claimed.ID != run.ID {
			t.Fatalf("claim workflow run=%+v err=%v", claimed, err)
		}
		if _, err := store.StartWorkflowStep(ctx, run.ID, "commit", 1, json.RawMessage(`{"order_id":"o-42"}`)); err != nil {
			t.Fatal(err)
		}
		operationID, err := api.ManagedWorkflowStepOperationID(run.ID, "commit")
		if err != nil {
			t.Fatal(err)
		}
		commit := ManagedWorkflowStepCommit{
			RunID: run.ID, StepName: "commit", OperationID: operationID, Attempt: 1, HTTPStatus: 200,
			Output: json.RawMessage(`{"order_id":"o-42","status":"fulfilled"}`),
		}
		committer := store.(ManagedWorkflowStepCommitter)
		for _, hook := range []AppWebhook{customerB.hook, appHook} {
			attempt := commit
			attempt.Effects = []exclusivework.Effect{{Name: "notify", WebhookID: hook.ID, Type: "order.fulfilled", Payload: json.RawMessage(`{"order_id":"o-42"}`)}}
			if err := committer.CommitManagedWorkflowStep(ctx, attempt); !errors.Is(err, ErrOperationEffectDestination) {
				t.Fatalf("cross-scope receiver %s error=%v, want destination rejection", hook.ID, err)
			}
		}
		commit.Effects = []exclusivework.Effect{{Name: "notify", WebhookID: customerA.hook.ID, Type: "order.fulfilled", Payload: json.RawMessage(`{"order_id":"o-42"}`)}}
		if err := committer.CommitManagedWorkflowStep(ctx, commit); err != nil {
			t.Fatalf("commit tenant workflow result/effect: %v", err)
		}

		effectID := workflowOperationEffectID(operationID, "notify")
		delivery, err := store.AppWebhookDeliveryByID(ctx, effectID)
		if err != nil || delivery.WebhookID != customerA.hook.ID {
			t.Fatalf("tenant effect delivery=%+v err=%v", delivery, err)
		}
		var payload api.OperationEffectPayload
		if err := json.Unmarshal(delivery.Payload, &payload); err != nil || payload.PlatformTenantID != customerA.tenant.ID {
			t.Fatalf("effect tenant identity=%+v err=%v", payload, err)
		}
		guard := store.(OperationEffectDeliveryStore)
		if allowed, err := guard.OperationEffectDeliveryAllowed(ctx, delivery.ID); err != nil || !allowed {
			t.Fatalf("active tenant effect delivery allowed=%t err=%v", allowed, err)
		}
		if err := store.UpdateTenantSurfaceStatus(ctx, customerA.surface.ID, SurfaceStatusSuspended); err != nil {
			t.Fatal(err)
		}
		if allowed, err := guard.OperationEffectDeliveryAllowed(ctx, delivery.ID); err != nil || allowed {
			t.Fatalf("suspended tenant surface effect delivery allowed=%t err=%v", allowed, err)
		}
		if err := store.UpdateTenantSurfaceStatus(ctx, customerA.surface.ID, SurfaceStatusActive); err != nil {
			t.Fatal(err)
		}
		if _, err := tenants.SetPlatformTenantStatus(ctx, account.ID, customerA.tenant.ID, PlatformTenantSuspended); err != nil {
			t.Fatal(err)
		}
		if allowed, err := guard.OperationEffectDeliveryAllowed(ctx, delivery.ID); err != nil || allowed {
			t.Fatalf("suspended tenant effect delivery allowed=%t err=%v", allowed, err)
		}
	})
}

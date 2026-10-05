// adr: 488
package state

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"testing/quick"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/exclusivework"
)

type effectFixture struct {
	base                     Store
	owners                   ExclusiveWorkStore
	pool                     *pgxpool.Pool
	account                  Account
	app                      App
	incarnation              string
	tenant, otherTenant      PlatformTenant
	hook, otherHook, appHook AppWebhook
	surface                  TenantSurface
}

func newEffectFixture(t *testing.T, backend string) effectFixture {
	t.Helper()
	ctx := t.Context()
	f := effectFixture{}
	if backend == "postgres" {
		f.pool = pgtest.OpenMigrated(t)
		f.base = NewPgStore(f.pool)
	} else {
		f.base = NewMemStore()
	}
	f.owners = f.base.(ExclusiveWorkStore)
	var err error
	f.account, err = f.base.CreateAccount(ctx, uuid.NewString()+"@effects.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	f.app, err = f.base.CreateApp(ctx, App{AccountID: f.account.ID, Slug: "effects-" + uuid.NewString()[:8], Type: AppTypeApp, Runtime: "node22", RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := f.base.CreateDeployment(ctx, Deployment{AppID: f.app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:effects", Status: DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	nodeID := "effects-node"
	if f.pool != nil {
		node, err := f.base.(*PgStore).ComputeNodeByName(ctx, DefaultLocalNodeName)
		if err != nil {
			t.Fatal(err)
		}
		nodeID = node.ID
	}
	instance, err := f.base.CreateInstance(ctx, f.app.ID, dep.ID, string(StateRunning), 256, nodeID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	f.incarnation = ExclusiveIncarnation(instance)
	tenants := f.base.(PlatformTenantStore)
	f.tenant, _, err = tenants.CreatePlatformTenant(ctx, f.account.ID, "customer-a", "A", 100)
	if err != nil {
		t.Fatal(err)
	}
	f.otherTenant, _, err = tenants.CreatePlatformTenant(ctx, f.account.ID, "customer-b", "B", 100)
	if err != nil {
		t.Fatal(err)
	}
	for i, tenant := range []PlatformTenant{f.tenant, f.otherTenant} {
		surface, err := f.base.CreateTenantSurfaceIfUnderQuota(ctx, CreateTenantSurfaceParams{AccountID: f.account.ID, AppID: f.app.ID, Name: fmt.Sprintf("effects-%d", i), CertKind: CertKindPerHostSAN}, api.MustLimitsFor(api.PlanPro))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tenants.LinkPlatformTenantSurface(ctx, f.account.ID, tenant.ID, surface.ID); err != nil {
			t.Fatal(err)
		}
		if err := f.base.UpdateTenantSurfaceStatus(ctx, surface.ID, SurfaceStatusActive); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			f.surface = surface
		}
		hook, err := f.base.(PlatformTenantWebhookStore).CreatePlatformTenantWebhookIfUnderQuota(ctx, AppWebhook{AccountID: f.account.ID, PlatformTenantID: tenant.ID, TargetURL: "https://customer.example/events", SecretSealed: []byte("sealed"), EventFilter: []string{OperationEffectEvent}, Enabled: true}, api.MustLimitsFor(api.PlanPro))
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			f.hook = hook
		} else {
			f.otherHook = hook
		}
	}
	f.appHook, err = f.base.CreateAppWebhook(ctx, AppWebhook{AccountID: f.account.ID, AppID: f.app.ID, TargetURL: "https://app.example/events", SecretSealed: []byte("sealed"), EventFilter: []string{OperationEffectEvent}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"account", "platform_tenant"} {
		_, err := f.owners.UpsertExclusiveWorkPolicy(ctx, f.account.ID, exclusivework.Policy{Name: "effects-" + strings.ReplaceAll(scope, "_", "-"), Scope: scope, MemberAppIDs: []string{f.app.ID}, Contention: "queue", LeaseSeconds: 30, MaxAttemptSeconds: 300})
		if err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f effectFixture) claim(t *testing.T, tenant string) (ExclusiveOperation, exclusivework.Claim) {
	t.Helper()
	scope := "account"
	if tenant != "" {
		scope = "platform_tenant"
	}
	op, _, err := f.owners.AdmitExclusiveOperation(t.Context(), ExclusiveAdmission{AccountID: f.account.ID, AppID: f.app.ID, PlatformTenantID: tenant, PolicyName: "effects-" + strings.ReplaceAll(scope, "_", "-"), Key: json.RawMessage(`"` + uuid.NewString() + `"`), Request: json.RawMessage(`{"method":"POST","path":"/orders"}`)})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := f.owners.ClaimExclusiveOperation(t.Context(), f.account.ID, op.ID, f.incarnation)
	if err != nil {
		t.Fatal(err)
	}
	return op, claim
}

func webhookEffect(hook AppWebhook, name string) exclusivework.Effect {
	return exclusivework.Effect{Name: name, WebhookID: hook.ID, Type: "order.fulfilled", Payload: json.RawMessage(`{"order_id":123}`)}
}

func compactEffectJSON(raw []byte) string {
	var compact bytes.Buffer
	if json.Compact(&compact, raw) != nil {
		return ""
	}
	return compact.String()
}

func TestOperationWebhookEffectsAtomicIsolation(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newEffectFixture(t, backend)
			ctx := t.Context()
			op, claim := f.claim(t, f.tenant.ID)
			// A valid first effect must roll back when any later destination is invalid.
			if err := f.owners.CommitExclusiveOperation(ctx, claim, json.RawMessage(`{"ok":true}`), []exclusivework.Effect{webhookEffect(f.hook, "valid"), webhookEffect(f.otherHook, "foreign-customer")}); !errors.Is(err, ErrOperationEffectDestination) {
				t.Fatalf("cross customer commit: %v", err)
			}
			rows, _, err := f.base.ListAppWebhookDeliveries(ctx, f.app.ID, f.hook.ID, 100, "")
			if err != nil || len(rows) != 0 {
				t.Fatalf("partial enqueue: %+v %v", rows, err)
			}
			pending, err := f.owners.ExclusiveOperationByID(ctx, f.account.ID, op.ID)
			if err != nil || pending.State != "running" || len(pending.Effects) != 0 || len(pending.Result) != 0 {
				t.Fatalf("partial completion: %+v %v", pending, err)
			}
			for _, target := range []AppWebhook{f.appHook, {ID: uuid.NewString()}} {
				if err := f.owners.CommitExclusiveOperation(ctx, claim, json.RawMessage(`null`), []exclusivework.Effect{webhookEffect(target, "wrong-scope")}); !errors.Is(err, ErrOperationEffectDestination) {
					t.Fatalf("scope bypass: %v", err)
				}
			}
			const workers = 8
			var wg sync.WaitGroup
			results := make(chan error, workers)
			for range workers {
				wg.Add(1)
				go func() {
					defer wg.Done()
					results <- f.owners.CommitExclusiveOperation(ctx, claim, json.RawMessage(`{"ok":true}`), []exclusivework.Effect{webhookEffect(f.hook, "notify")})
				}()
			}
			wg.Wait()
			close(results)
			success := 0
			for err := range results {
				if err == nil {
					success++
				} else if !errors.Is(err, exclusivework.ErrStaleOwner) {
					t.Fatal(err)
				}
			}
			if success != 1 {
				t.Fatalf("successful commits=%d", success)
			}
			completed, err := f.owners.ExclusiveOperationByID(ctx, f.account.ID, op.ID)
			if err != nil || completed.State != "completed" || compactEffectJSON(completed.Result) != `{"ok":true}` || len(completed.Effects) != 1 {
				t.Fatalf("receipt=%+v err=%v", completed, err)
			}
			effect := completed.Effects[0]
			if effect.ID != effect.DeliveryID || effect.WebhookID != f.hook.ID || effect.Status != "pending" || effect.Generation != claim.Generation {
				t.Fatalf("correlation=%+v", effect)
			}
			delivery, err := f.base.AppWebhookDeliveryByID(ctx, effect.DeliveryID)
			if err != nil || delivery.Event != OperationEffectEvent {
				t.Fatalf("delivery=%+v err=%v", delivery, err)
			}
			var payload api.OperationEffectPayload
			if err := json.Unmarshal(delivery.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			if payload.OperationID != op.ID || payload.PlatformTenantID != f.tenant.ID || payload.Name != "notify" || payload.Type != "order.fulfilled" || compactEffectJSON(payload.Data) != `{"order_id":123}` {
				t.Fatalf("payload=%+v", payload)
			}
			guard := f.base.(OperationEffectDeliveryStore)
			if allowed, err := guard.OperationEffectDeliveryAllowed(ctx, effect.ID); err != nil || !allowed {
				t.Fatalf("live scope=%v %v", allowed, err)
			}
			if _, err := f.owners.ExclusiveOperationByID(ctx, uuid.NewString(), op.ID); !errors.Is(err, ErrNotFound) {
				t.Fatalf("receipt ownership: %v", err)
			}
			if _, err := f.base.(PlatformTenantStore).SetPlatformTenantStatus(ctx, f.account.ID, f.tenant.ID, PlatformTenantSuspended); err != nil {
				t.Fatal(err)
			}
			if allowed, err := guard.OperationEffectDeliveryAllowed(ctx, effect.ID); err != nil || allowed {
				t.Fatalf("suspended scope=%v %v", allowed, err)
			}
			if _, err := f.base.(PlatformTenantStore).SetPlatformTenantStatus(ctx, f.account.ID, f.tenant.ID, PlatformTenantActive); err != nil {
				t.Fatal(err)
			}
			if err := f.base.UpdateTenantSurfaceStatus(ctx, f.surface.ID, SurfaceStatusDeleted); err != nil {
				t.Fatal(err)
			}
			if allowed, err := guard.OperationEffectDeliveryAllowed(ctx, effect.ID); err != nil || allowed {
				t.Fatalf("offboarded surface=%v %v", allowed, err)
			}
			if err := f.base.DeleteAppWebhook(ctx, f.hook.ID); err != nil {
				t.Fatal(err)
			}
			history, err := f.owners.ExclusiveOperationByID(ctx, f.account.ID, op.ID)
			if err != nil || history.Effects[0].Status != "unavailable" || history.Effects[0].ID != effect.ID {
				t.Fatalf("retained identity=%+v err=%v", history, err)
			}
		})
	}
}

func TestOperationWebhookEffectScopeProperty(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newEffectFixture(t, backend)
			property := func(chooseB, wrong bool, seed uint8) bool {
				tenant, target := f.tenant.ID, f.hook
				if chooseB {
					tenant, target = f.otherTenant.ID, f.otherHook
				}
				if wrong {
					if chooseB {
						target = f.hook
					} else {
						target = f.otherHook
					}
				}
				op, claim := f.claim(t, tenant)
				effect := webhookEffect(target, "notify")
				// Payload values, even a forged tenant identity, confer no authority.
				effect.Payload = json.RawMessage(fmt.Sprintf(`{"platform_tenant_id":%q,"value":%d}`, target.PlatformTenantID, seed))
				err := f.owners.CommitExclusiveOperation(t.Context(), claim, json.RawMessage(`null`), []exclusivework.Effect{effect})
				if wrong {
					if !errors.Is(err, ErrOperationEffectDestination) {
						return false
					}
					return f.owners.FailExclusiveOperation(t.Context(), claim, "test rejected effect") == nil
				}
				if err != nil {
					return false
				}
				receipt, err := f.owners.ExclusiveOperationByID(t.Context(), f.account.ID, op.ID)
				return err == nil && len(receipt.Effects) == 1 && receipt.Effects[0].WebhookID == target.ID
			}
			if err := quick.Check(property, &quick.Config{MaxCount: 12}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestOperationWebhookEffectsValidation(t *testing.T) {
	f := newEffectFixture(t, "memory")
	_, claim := f.claim(t, "")
	for _, tc := range []struct {
		name    string
		effects []exclusivework.Effect
	}{
		{"duplicate", []exclusivework.Effect{webhookEffect(f.appHook, "notify"), webhookEffect(f.appHook, "notify")}},
		{"invalid-name", []exclusivework.Effect{webhookEffect(f.appHook, "Bad")}},
		{"missing-type", []exclusivework.Effect{{Name: "notify", WebhookID: f.appHook.ID, Payload: json.RawMessage(`null`)}}},
		{"invalid-id", []exclusivework.Effect{{Name: "notify", WebhookID: "bad", Type: "order.created", Payload: json.RawMessage(`null`)}}},
		{"invalid-type", []exclusivework.Effect{{Name: "notify", WebhookID: f.appHook.ID, Type: "order\ncreated", Payload: json.RawMessage(`null`)}}},
		{"oversized", []exclusivework.Effect{{Name: "notify", WebhookID: f.appHook.ID, Type: "order.created", Payload: json.RawMessage(`"` + strings.Repeat("x", api.MaxExclusiveEffectPayloadBytes) + `"`)}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := f.owners.CommitExclusiveOperation(t.Context(), claim, json.RawMessage(`null`), tc.effects); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("validation=%v", err)
			}
		})
	}
	if err := f.owners.CommitExclusiveOperation(t.Context(), claim, json.RawMessage(`null`), []exclusivework.Effect{webhookEffect(f.appHook, "notify")}); err != nil {
		t.Fatal(err)
	}
}

func TestOperationWebhookEffectsRequireReceiverOptInAndCurrentScope(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newEffectFixture(t, backend)
			ctx := t.Context()
			op, claim := f.claim(t, "")
			legacy, err := f.base.RecordAppWebhookDelivery(ctx, AppWebhookDelivery{WebhookID: f.appHook.ID, AppID: f.app.ID, AccountID: f.account.ID, Event: OperationEffectEvent, Payload: json.RawMessage(`{"legacy":true}`)})
			if err != nil {
				t.Fatal(err)
			}
			if allowed, err := f.base.(OperationEffectDeliveryStore).OperationEffectDeliveryAllowed(ctx, legacy.ID); allowed || !errors.Is(err, ErrNotOperationEffect) {
				t.Fatalf("ordinary outbox was reclassified: %v %v", allowed, err)
			}
			foreignAccount, err := f.base.CreateAccount(ctx, uuid.NewString()+"@foreign-effects.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			foreignApp, err := f.base.CreateApp(ctx, App{AccountID: foreignAccount.ID, Slug: "foreign-" + uuid.NewString()[:8], Type: AppTypeApp})
			if err != nil {
				t.Fatal(err)
			}
			foreign, err := f.base.CreateAppWebhook(ctx, AppWebhook{AccountID: foreignAccount.ID, AppID: foreignApp.ID, TargetURL: "https://foreign.example/events", SecretSealed: []byte("sealed"), EventFilter: []string{OperationEffectEvent}, Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			otherApp, err := f.base.CreateApp(ctx, App{AccountID: f.account.ID, Slug: "other-" + uuid.NewString()[:8], Type: AppTypeApp})
			if err != nil {
				t.Fatal(err)
			}
			other, err := f.base.CreateAppWebhook(ctx, AppWebhook{AccountID: f.account.ID, AppID: otherApp.ID, TargetURL: "https://other.example/events", SecretSealed: []byte("sealed"), EventFilter: []string{OperationEffectEvent}, Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			for _, target := range []AppWebhook{foreign, other, f.hook} {
				if err := f.owners.CommitExclusiveOperation(ctx, claim, json.RawMessage(`null`), []exclusivework.Effect{webhookEffect(target, "notify")}); !errors.Is(err, ErrOperationEffectDestination) {
					t.Fatalf("foreign scope=%v", err)
				}
			}
			empty := []string{}
			if _, err := f.base.UpdateAppWebhook(ctx, f.appHook.ID, UpdateAppWebhookParams{EventFilter: &empty}); err != nil {
				t.Fatal(err)
			}
			if err := f.owners.CommitExclusiveOperation(ctx, claim, json.RawMessage(`null`), []exclusivework.Effect{webhookEffect(f.appHook, "notify")}); !errors.Is(err, ErrOperationEffectDestination) {
				t.Fatalf("wildcard authorized effects: %v", err)
			}
			filter := []string{OperationEffectEvent}
			if _, err := f.base.UpdateAppWebhook(ctx, f.appHook.ID, UpdateAppWebhookParams{EventFilter: &filter}); err != nil {
				t.Fatal(err)
			}
			if err := f.owners.CommitExclusiveOperation(ctx, claim, json.RawMessage(`null`), []exclusivework.Effect{webhookEffect(f.appHook, "notify")}); err != nil {
				t.Fatal(err)
			}
			receipt, err := f.owners.ExclusiveOperationByID(ctx, f.account.ID, op.ID)
			if err != nil {
				t.Fatal(err)
			}
			id := receipt.Effects[0].DeliveryID
			guard := f.base.(OperationEffectDeliveryStore)
			if allowed, err := guard.OperationEffectDeliveryAllowed(ctx, id); err != nil || !allowed {
				t.Fatalf("authorized=%v %v", allowed, err)
			}
			for _, change := range []UpdateAppWebhookParams{{Enabled: boolEffectPointer(false)}, {EventFilter: &empty}} {
				if _, err := f.base.UpdateAppWebhook(ctx, f.appHook.ID, change); err != nil {
					t.Fatal(err)
				}
				if allowed, err := guard.OperationEffectDeliveryAllowed(ctx, id); err != nil || allowed {
					t.Fatalf("revoked=%v %v", allowed, err)
				}
				if _, err := f.base.UpdateAppWebhook(ctx, f.appHook.ID, UpdateAppWebhookParams{Enabled: boolEffectPointer(true), EventFilter: &filter}); err != nil {
					t.Fatal(err)
				}
			}
			// Customer deliveries enter the same claim ledger, even though their
			// receiver is tenant scoped and the delivery retains its source app.
			op, claim = f.claim(t, f.tenant.ID)
			if err := f.owners.CommitExclusiveOperation(ctx, claim, json.RawMessage(`null`), []exclusivework.Effect{webhookEffect(f.hook, "customer")}); err != nil {
				t.Fatal(err)
			}
			receipt, err = f.owners.ExclusiveOperationByID(ctx, f.account.ID, op.ID)
			if err != nil {
				t.Fatal(err)
			}
			claimed, err := f.base.ClaimDueAppWebhookDeliveries(ctx, 32, time.Now().Add(time.Second))
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, delivery := range claimed {
				if delivery.ID == receipt.Effects[0].DeliveryID && delivery.WebhookID == f.hook.ID && delivery.AppID == f.app.ID {
					found = true
				}
			}
			if !found {
				t.Fatalf("customer delivery was not claimable: %+v", claimed)
			}
		})
	}
}

func boolEffectPointer(value bool) *bool { return &value }

func TestOperationWebhookEffectsExpiredDuringDestinationLock(t *testing.T) {
	f := newEffectFixture(t, "postgres")
	op, claim := f.claim(t, f.tenant.ID)
	ctx := t.Context()
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT id FROM app_webhooks WHERE id=$1::uuid FOR UPDATE`, f.hook.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE exclusive_work_operations SET lease_expires_at=clock_timestamp()+interval '1 second' WHERE id=$1::uuid`, op.ID); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() {
		finished <- f.owners.CommitExclusiveOperation(ctx, claim, json.RawMessage(`null`), []exclusivework.Effect{webhookEffect(f.hook, "notify")})
	}()
	// The destination lock outlives the ownership lease.
	time.Sleep(1200 * time.Millisecond)
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-finished; !errors.Is(err, exclusivework.ErrStaleOwner) {
		t.Fatalf("late publisher=%v", err)
	}
	rows, _, err := f.base.ListAppWebhookDeliveries(ctx, f.app.ID, f.hook.ID, 100, "")
	if err != nil || len(rows) != 0 {
		t.Fatalf("expired worker published %+v %v", rows, err)
	}
	receipt, err := f.owners.ExclusiveOperationByID(ctx, f.account.ID, op.ID)
	if err != nil || receipt.State != "running" || len(receipt.Effects) != 0 {
		t.Fatalf("rollback=%+v %v", receipt, err)
	}
	newClaim, err := f.owners.ClaimExclusiveOperation(ctx, f.account.ID, op.ID, f.incarnation)
	if err != nil {
		t.Fatal(err)
	}
	if newClaim.Generation <= claim.Generation {
		t.Fatal("generation did not advance")
	}
	if err := f.owners.CommitExclusiveOperation(ctx, claim, json.RawMessage(`null`), []exclusivework.Effect{webhookEffect(f.hook, "notify")}); !errors.Is(err, exclusivework.ErrStaleOwner) {
		t.Fatalf("stale predecessor=%v", err)
	}
	if err := f.owners.CommitExclusiveOperation(ctx, newClaim, json.RawMessage(`null`), []exclusivework.Effect{webhookEffect(f.hook, "notify")}); err != nil {
		t.Fatal(err)
	}
}

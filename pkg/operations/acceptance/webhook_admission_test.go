// ADR-1193: a provider event admits exactly one customer operation, owned by
// the authenticated binding, independently of later rebinding.
package acceptance_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

type webhookOperationFixtureStore interface {
	operationFixtureStore
	state.OperationWebhookStore
	CreateInboundWebhookEndpointIfUnderQuota(context.Context, state.InboundWebhookEndpoint, api.Limits) (state.InboundWebhookEndpoint, error)
	CreateTenantSurfaceIfUnderQuota(context.Context, state.CreateTenantSurfaceParams, api.Limits) (state.TenantSurface, error)
	LinkPlatformTenantSurface(context.Context, string, string, string) (state.TenantSurface, error)
	UpdateTenantSurfaceStatus(context.Context, string, state.SurfaceStatus) error
}

type webhookOperationFixture struct {
	ctx        context.Context
	acct       state.Account
	app        state.App
	def        state.OperationDefinition
	alice, bob state.PlatformTenant
	endpoint   state.InboundWebhookEndpoint
}

func newWebhookOperationFixture(t *testing.T, s webhookOperationFixtureStore) webhookOperationFixture {
	t.Helper()
	ctx, acct, app, base, alice, bob := operationFixture(t, s)
	spec := operationSpec()
	spec.Name, spec.Path = "invoice-paid", "/invoices"
	spec.InputSchema = []byte(`{"type":"object","required":["webhook","payload"],"properties":{"webhook":{"type":"object","required":["event_id","event_type"]},"payload":{"type":"object"}}}`)
	def, err := s.PutOperationDefinition(ctx, state.OperationDefinition{AccountID: acct.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{AppID: app.ID, Scope: base.Scope, DeploymentID: base.DeploymentID, Spec: spec}})
	if err != nil {
		t.Fatal(err)
	}
	limits := api.MustLimitsFor(api.PlanPro)
	for i, tenant := range []state.PlatformTenant{alice, bob} {
		surface, err := s.CreateTenantSurfaceIfUnderQuota(ctx, state.CreateTenantSurfaceParams{AccountID: acct.ID, AppID: app.ID, Name: []string{"alice", "bob"}[i], CertKind: state.CertKindPerHostSAN}, limits)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.LinkPlatformTenantSurface(ctx, acct.ID, tenant.ID, surface.ID); err != nil {
			t.Fatal(err)
		}
		if err := s.UpdateTenantSurfaceStatus(ctx, surface.ID, state.SurfaceStatusActive); err != nil {
			t.Fatal(err)
		}
	}
	endpoint, err := s.CreateInboundWebhookEndpointIfUnderQuota(ctx, state.InboundWebhookEndpoint{AppID: app.ID, AccountID: acct.ID, Name: "stripe-invoices",
		Provider: state.InboundWebhookProviderStripe, TokenHash: make([]byte, 32), SigningSecretSealed: []byte("sealed"), DeliveryPath: "/", Enabled: true}, limits)
	if err != nil {
		t.Fatal(err)
	}
	return webhookOperationFixture{ctx: ctx, acct: acct, app: app, def: def, alice: alice, bob: bob, endpoint: endpoint}
}

func (f webhookOperationFixture) bind(t *testing.T, s state.OperationWebhookStore, expected int64, tenant string, eventTypes ...string) state.OperationWebhookBinding {
	t.Helper()
	binding, err := s.SaveOperationWebhookBinding(f.ctx, state.OperationWebhookBindingOptions{EndpointID: f.endpoint.ID, AccountID: f.acct.ID, AppID: f.app.ID,
		ExpectedVersion: expected, Scope: f.def.Scope, OperationName: f.def.Spec.Name, PlatformTenantID: tenant, EventTypes: eventTypes})
	if err != nil {
		t.Fatal(err)
	}
	return binding
}

func (f webhookOperationFixture) event(binding state.OperationWebhookBinding, eventID, body string) state.WebhookOperationAdmission {
	input, _ := json.Marshal(map[string]any{"webhook": map[string]string{"endpoint": f.endpoint.Name, "provider": "stripe", "event_id": eventID, "event_type": "invoice.paid"}, "payload": json.RawMessage(body)})
	return state.WebhookOperationAdmission{EndpointID: f.endpoint.ID, ProviderEventID: eventID, BodySHA256: sha256.Sum256([]byte(body)), BindingVersion: binding.Version,
		Admission: state.OperationAdmission{AccountID: f.acct.ID, DefinitionID: f.def.ID, PlatformTenantID: binding.PlatformTenantID, IdempotencyKey: "webhook:" + eventID, Input: input}}
}

func testWebhookOperationBinding(t *testing.T, s webhookOperationFixtureStore) {
	f := newWebhookOperationFixture(t, s)
	first := f.bind(t, s, 0, f.alice.ID, "invoice.paid")
	if first.Version <= 0 || first.PlatformTenantID != f.alice.ID || !first.Admits("invoice.paid") || first.Admits("invoice.voided") {
		t.Fatalf("binding = %+v", first)
	}
	opts := state.OperationWebhookBindingOptions{EndpointID: f.endpoint.ID, AccountID: f.acct.ID, AppID: f.app.ID, Scope: f.def.Scope, OperationName: f.def.Spec.Name, PlatformTenantID: f.bob.ID}
	if _, err := s.SaveOperationWebhookBinding(f.ctx, opts); !errors.Is(err, state.ErrOperationWebhookVersionConflict) {
		t.Fatalf("stale expected version: %v", err)
	}
	for name, change := range map[string]func(*state.OperationWebhookBindingOptions){
		"invalid event type": func(o *state.OperationWebhookBindingOptions) { o.EventTypes = []string{"Invoice Paid"} },
		"duplicate type":     func(o *state.OperationWebhookBindingOptions) { o.EventTypes = []string{"a.b", "a.b"} },
		"invalid name":       func(o *state.OperationWebhookBindingOptions) { o.OperationName = "Invoice" },
		"negative version":   func(o *state.OperationWebhookBindingOptions) { o.ExpectedVersion = -1 },
	} {
		invalid := opts
		invalid.ExpectedVersion = first.Version
		change(&invalid)
		if _, err := s.SaveOperationWebhookBinding(f.ctx, invalid); !errors.Is(err, state.ErrInvalidArgument) {
			t.Errorf("%s: %v", name, err)
		}
	}
	stranger, _, err := s.CreatePlatformTenant(f.ctx, f.acct.ID, "carol", "Carol", 100)
	if err != nil {
		t.Fatal(err)
	}
	opts.ExpectedVersion, opts.PlatformTenantID = first.Version, stranger.ID
	if _, err := s.SaveOperationWebhookBinding(f.ctx, opts); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("tenant without an app surface: %v", err)
	}
	if _, err := s.SetPlatformTenantStatus(f.ctx, f.acct.ID, f.bob.ID, string(state.PlatformTenantSuspended)); err != nil {
		t.Fatal(err)
	}
	opts.PlatformTenantID = f.bob.ID
	if _, err := s.SaveOperationWebhookBinding(f.ctx, opts); !errors.Is(err, state.ErrPlatformTenantSuspended) {
		t.Fatalf("suspended owner: %v", err)
	}
	got, err := s.OperationWebhookBinding(f.ctx, f.endpoint.ID)
	if err != nil || got.Version != first.Version || got.PlatformTenantID != f.alice.ID {
		t.Fatalf("failed writes changed the binding: %+v %v", got, err)
	}
	if err := s.DeleteOperationWebhookBinding(f.ctx, f.endpoint.ID, f.acct.ID, first.Version+1); !errors.Is(err, state.ErrOperationWebhookVersionConflict) {
		t.Fatalf("stale delete: %v", err)
	}
	if err := s.DeleteOperationWebhookBinding(f.ctx, f.endpoint.ID, f.acct.ID, first.Version); err != nil {
		t.Fatal(err)
	}
	if _, err := s.OperationWebhookBinding(f.ctx, f.endpoint.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("deleted binding still readable: %v", err)
	}
	if _, err := s.AdmitWebhookOperation(f.ctx, f.event(first, "evt_after_delete", `{"id":"evt_after_delete"}`)); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("unbound endpoint admitted work: %v", err)
	}
}

func testWebhookOperationAdmission(t *testing.T, s webhookOperationFixtureStore) {
	f := newWebhookOperationFixture(t, s)
	binding := f.bind(t, s, 0, f.alice.ID)
	event := f.event(binding, "evt_1", `{"id":"evt_1","type":"invoice.paid"}`)
	first, err := s.AdmitWebhookOperation(f.ctx, event)
	if err != nil || first.Duplicate || first.OperationID == "" {
		t.Fatalf("first delivery: %+v %v", first, err)
	}
	op, err := s.OperationByID(f.ctx, f.acct.ID, f.alice.ID, first.OperationID)
	if err != nil || op.DefinitionID != f.def.ID || op.State != api.OperationAccepted {
		t.Fatalf("admitted operation: %+v %v", op, err)
	}
	retry, err := s.AdmitWebhookOperation(f.ctx, event)
	if err != nil || !retry.Duplicate || retry.OperationID != first.OperationID {
		t.Fatalf("provider retry: %+v %v", retry, err)
	}
	receipt, digest, err := s.OperationWebhookReceipt(f.ctx, f.endpoint.ID, "evt_1")
	if err != nil || receipt.OperationID != first.OperationID || digest != event.BodySHA256 {
		t.Fatalf("receipt lookup: %+v %v", receipt, err)
	}
	changed := f.event(binding, "evt_1", `{"id":"evt_1","type":"invoice.paid","amount":2}`)
	if _, err := s.AdmitWebhookOperation(f.ctx, changed); !errors.Is(err, state.ErrOperationInputConflict) {
		t.Fatalf("changed body under one event id: %v", err)
	}
	// Rebinding to another customer cannot turn an accepted event into new work.
	rebound := f.bind(t, s, binding.Version, f.bob.ID)
	stale := f.event(binding, "evt_2", `{"id":"evt_2"}`)
	if _, err := s.AdmitWebhookOperation(f.ctx, stale); !errors.Is(err, state.ErrOperationWebhookBindingChanged) {
		t.Fatalf("admission against a stale binding: %v", err)
	}
	replayed, err := s.AdmitWebhookOperation(f.ctx, f.event(rebound, "evt_1", `{"id":"evt_1","type":"invoice.paid"}`))
	if err != nil || !replayed.Duplicate || replayed.OperationID != first.OperationID {
		t.Fatalf("retry after rebinding: %+v %v", replayed, err)
	}
	if _, err := s.OperationByID(f.ctx, f.acct.ID, f.bob.ID, first.OperationID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("rebinding moved ownership: %v", err)
	}
	second, err := s.AdmitWebhookOperation(f.ctx, f.event(rebound, "evt_2", `{"id":"evt_2"}`))
	if err != nil || second.Duplicate || second.OperationID == first.OperationID {
		t.Fatalf("new event after rebinding: %+v %v", second, err)
	}
	if _, err := s.OperationByID(f.ctx, f.acct.ID, f.bob.ID, second.OperationID); err != nil {
		t.Fatalf("new owner cannot read its operation: %v", err)
	}
	invalid := f.event(rebound, "evt_3", `{"id":"evt_3"}`)
	invalid.Admission.Input = []byte(`{"payload":{}}`)
	if _, err := s.AdmitWebhookOperation(f.ctx, invalid); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("schema-invalid envelope: %v", err)
	}
	if _, _, err := s.OperationWebhookReceipt(f.ctx, f.endpoint.ID, "evt_3"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("rejected event left a receipt: %v", err)
	}
	var wg sync.WaitGroup
	var created atomic.Int64
	ids := sync.Map{}
	concurrent := f.event(rebound, "evt_concurrent", `{"id":"evt_concurrent"}`)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := s.AdmitWebhookOperation(f.ctx, concurrent)
			if err != nil {
				t.Errorf("concurrent delivery: %v", err)
				return
			}
			if !got.Duplicate {
				created.Add(1)
			}
			ids.Store(got.OperationID, true)
		}()
	}
	wg.Wait()
	distinct := 0
	ids.Range(func(any, any) bool { distinct++; return true })
	if created.Load() != 1 || distinct != 1 {
		t.Fatalf("concurrent deliveries created %d operations with %d identities", created.Load(), distinct)
	}
	current, err := s.OperationWebhookBinding(f.ctx, f.endpoint.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteOperationWebhookBinding(f.ctx, f.endpoint.ID, f.acct.ID, current.Version); err != nil {
		t.Fatal(err)
	}
	late, err := s.AdmitWebhookOperation(f.ctx, event)
	if err != nil || !late.Duplicate || late.OperationID != first.OperationID {
		t.Fatalf("retry of an accepted event after unbinding: %+v %v", late, err)
	}
}

func TestMemOperationWebhookBinding(t *testing.T) {
	testWebhookOperationBinding(t, state.NewMemStore())
}
func TestPgOperationWebhookBinding(t *testing.T) {
	s, _ := pgStore(t)
	testWebhookOperationBinding(t, s)
}
func TestMemWebhookOperationAdmission(t *testing.T) {
	testWebhookOperationAdmission(t, state.NewMemStore())
}
func TestPgWebhookOperationAdmission(t *testing.T) {
	s, _ := pgStore(t)
	testWebhookOperationAdmission(t, s)
}

// The routing guard is a database trigger so older API binaries honor it.
func TestPgOperationWebhookRoutingGuard(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	s := state.NewPgStore(pool)
	f := newWebhookOperationFixture(t, s)
	binding := f.bind(t, s, 0, f.alice.ID)
	_, err := pool.Exec(f.ctx, `INSERT INTO workflow_webhook_bindings(endpoint_id, workflow_name, event_type, version) VALUES ($1, 'sync', 'invoice.paid', 1)`, f.endpoint.ID)
	if err == nil {
		t.Fatal("automation binding coexisted with an operation binding")
	}
	if err := s.DeleteOperationWebhookBinding(f.ctx, f.endpoint.ID, f.acct.ID, binding.Version); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(f.ctx, `INSERT INTO workflow_webhook_bindings(endpoint_id, workflow_name, event_type, version) VALUES ($1, 'sync', 'invoice.paid', 1)`, f.endpoint.ID); err != nil {
		t.Fatal(err)
	}
	_, err = s.SaveOperationWebhookBinding(f.ctx, state.OperationWebhookBindingOptions{EndpointID: f.endpoint.ID, AccountID: f.acct.ID, AppID: f.app.ID,
		ExpectedVersion: 0, Scope: f.def.Scope, OperationName: f.def.Spec.Name, PlatformTenantID: f.alice.ID})
	if !errors.Is(err, state.ErrOperationWebhookRoutingConflict) {
		t.Fatalf("operation binding over an automation: %v", err)
	}
}

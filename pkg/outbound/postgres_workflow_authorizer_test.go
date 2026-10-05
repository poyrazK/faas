package outbound

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/internalsvc"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
	"time"
)

func TestWorkflowOutboundPostgresAuthorization(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.InsertClusterSigningKey(ctx, state.ClusterSigningKey{KeyID: internalsvc.KidFromPub(public), PublicKeyPEM: string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})), SealedBlob: []byte("test-sealed-key")}); err != nil {
		t.Fatal(err)
	}
	account, err := store.CreateAccount(ctx, "workflow-outbound@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "outbound-workflow", Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	offer := state.OutboundIntegrationOffer{ID: uuid.NewString(), AccountID: account.ID, Name: "crm", Origin: "https://api.example.com", AllowedMethods: []string{"GET", "POST"}, AllowedPathPrefixes: []string{"/v1"}, Enabled: true, OwnerKind: "customer", CredentialSource: "customer_sealed", RequestPolicy: api.DefaultOutboundRequestPolicy()}
	if _, err := store.CreateOutboundIntegration(ctx, offer); err != nil {
		t.Fatal(err)
	}
	if err := store.SetOutboundCredential(ctx, account.ID, offer.ID, []byte("sealed-provider-key")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.BindOutboundIntegration(ctx, account.ID, app.ID, offer.ID); err != nil {
		t.Fatal(err)
	}
	spec := api.WorkflowSpec{Name: "crm", Steps: []api.WorkflowStepSpec{{Name: "send", Outbound: &api.WorkflowOutboundSpec{IntegrationID: offer.ID, Method: "POST", Path: "/v1/contacts", IdempotencySupported: true}}}}
	snapshot, _ := json.Marshal(spec)
	run := &state.WorkflowRun{AppID: app.ID, WorkflowName: spec.Name, DefinitionSnapshot: snapshot}
	if err := store.CreateWorkflowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateWorkflowSteps(ctx, run.ID, []*state.WorkflowStep{{StepName: "send"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimNextDueWorkflowRun(ctx); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"email":"a@example.com"}`)
	if _, err := store.StartWorkflowStep(ctx, run.ID, "send", 1, body); err != nil {
		t.Fatal(err)
	}
	lease, err := store.GetWorkflowOutboundAttempt(ctx, run.ID, "send", 1)
	if err != nil {
		t.Fatal(err)
	}
	identity := WorkflowIdentity{AccountID: account.ID, AppID: app.ID, RunID: run.ID, StepName: "send", Attempt: 1, AttemptToken: lease.Token}
	authorizer, err := NewPostgresWorkflowAuthorizer(pool)
	if err != nil {
		t.Fatal(err)
	}
	mint := func(identity WorkflowIdentity, path string) string {
		t.Helper()
		token, err := MintWorkflowIdentity(identity, offer.ID, "POST", path, body, private, internalsvc.KidFromPub(public), time.Now())
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	token := mint(identity, "/v1/contacts")
	if _, err := authorizer.AuthorizeWorkflow(ctx, token, offer.ID, "POST", "/v1/contacts", body); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"account", "app", "attempt_token", "route"} {
		t.Run(field, func(t *testing.T) {
			other := identity
			path := "/v1/contacts"
			switch field {
			case "account":
				other.AccountID = uuid.NewString()
			case "app":
				other.AppID = uuid.NewString()
			case "attempt_token":
				other.AttemptToken = uuid.NewString()
			case "route":
				path = "/v1/admin"
			}
			if _, err := authorizer.AuthorizeWorkflow(ctx, mint(other, path), offer.ID, "POST", path, body); !errors.Is(err, ErrWorkflowNotAuthorized) {
				t.Fatalf("accepted mismatched %s: %v", field, err)
			}
		})
	}
	// Emulate a legacy recovery that rewound the attempt counter. Even reusing
	// the numeric attempt must mint a fresh private nonce.
	if err := store.MarkWorkflowStepStatus(ctx, run.ID, "send", state.WorkflowStepStatusPending, 0, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.StartWorkflowStep(ctx, run.ID, "send", 1, body); err != nil {
		t.Fatal(err)
	}
	if _, err := authorizer.AuthorizeWorkflow(ctx, token, offer.ID, "POST", "/v1/contacts", body); !errors.Is(err, ErrWorkflowNotAuthorized) {
		t.Fatalf("stale attempt accepted after restart: %v", err)
	}
	lease, err = store.GetWorkflowOutboundAttempt(ctx, run.ID, "send", 1)
	if err != nil {
		t.Fatal(err)
	}
	identity.AttemptToken = lease.Token
	token = mint(identity, "/v1/contacts")
	if _, err := authorizer.AuthorizeWorkflow(ctx, token, offer.ID, "POST", "/v1/contacts", body); err != nil {
		t.Fatal(err)
	}
	if err := store.UnbindOutboundIntegration(ctx, account.ID, app.ID, offer.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := authorizer.AuthorizeWorkflow(ctx, token, offer.ID, "POST", "/v1/contacts", body); !errors.Is(err, ErrWorkflowNotAuthorized) {
		t.Fatalf("revoked binding accepted: %v", err)
	}
	if _, err := store.BindOutboundIntegration(ctx, account.ID, app.ID, offer.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CancelWorkflowRun(ctx, run.ID, "cancelled"); err != nil {
		t.Fatal(err)
	}
	if _, err := authorizer.AuthorizeWorkflow(ctx, token, offer.ID, "POST", "/v1/contacts", body); !errors.Is(err, ErrWorkflowNotAuthorized) {
		t.Fatalf("cancelled run accepted: %v", err)
	}
}

func TestTenantWorkflowOutboundPostgresAuthorization(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	keyID := internalsvc.KidFromPub(public)
	if err := store.InsertClusterSigningKey(ctx, state.ClusterSigningKey{KeyID: keyID, PublicKeyPEM: string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})), SealedBlob: []byte("test-sealed-key")}); err != nil {
		t.Fatal(err)
	}
	account, err := store.CreateAccount(ctx, uuid.NewString()+"@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "tenant-outbound-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 256, PlatformTenantRequired: true})
	if err != nil {
		t.Fatal(err)
	}
	tenant, _, err := store.CreatePlatformTenant(ctx, account.ID, "tenant-"+uuid.NewString(), "Tenant", 10)
	if err != nil {
		t.Fatal(err)
	}
	consumer, err := store.CreateAPIConsumer(ctx, account.ID, app.ID, "consumer-"+uuid.NewString(), "Tenant consumer")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.LinkPlatformTenantConsumer(ctx, account.ID, tenant.ID, consumer.ID); err != nil {
		t.Fatal(err)
	}
	offer := state.OutboundIntegrationOffer{ID: uuid.NewString(), AccountID: account.ID, Name: "crm", Origin: "https://api.example.com", AllowedMethods: []string{"POST"}, AllowedPathPrefixes: []string{"/v1"}, Enabled: true, OwnerKind: "customer", CredentialSource: "customer_sealed", RequestPolicy: api.DefaultOutboundRequestPolicy()}
	if _, err := store.CreateOutboundIntegration(ctx, offer); err != nil {
		t.Fatal(err)
	}
	if err := store.SetOutboundCredential(ctx, account.ID, offer.ID, []byte("sealed-provider-key")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.BindOutboundIntegration(ctx, account.ID, app.ID, offer.ID); err != nil {
		t.Fatal(err)
	}
	spec := api.WorkflowSpec{Name: "crm", Steps: []api.WorkflowStepSpec{{Name: "send", Outbound: &api.WorkflowOutboundSpec{IntegrationID: offer.ID, Method: "POST", Path: "/v1/contacts", IdempotencySupported: true}}}}
	snapshot, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	run := &state.WorkflowRun{AppID: app.ID, PlatformTenantID: tenant.ID, WorkflowName: spec.Name, DefinitionSnapshot: snapshot}
	if err := store.CreateWorkflowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateWorkflowSteps(ctx, run.ID, []*state.WorkflowStep{{StepName: "send"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimNextDueWorkflowRun(ctx); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"tenant":"customer"}`)
	if _, err := store.StartWorkflowStep(ctx, run.ID, "send", 1, body); err != nil {
		t.Fatal(err)
	}
	lease, err := store.GetWorkflowOutboundAttempt(ctx, run.ID, "send", 1)
	if err != nil || lease.PlatformTenantID != tenant.ID {
		t.Fatalf("outbound lease=%+v err=%v", lease, err)
	}
	authorizer, err := NewPostgresWorkflowAuthorizer(pool)
	if err != nil {
		t.Fatal(err)
	}
	mint := func(identity WorkflowIdentity) string {
		t.Helper()
		token, err := MintWorkflowIdentity(identity, offer.ID, "POST", "/v1/contacts", body, private, keyID, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	identity := WorkflowIdentity{AccountID: account.ID, AppID: app.ID, PlatformTenantID: tenant.ID, RunID: run.ID, StepName: "send", Attempt: 1, AttemptToken: lease.Token}
	valid := mint(identity)
	if _, err := authorizer.AuthorizeWorkflow(ctx, valid, offer.ID, "POST", "/v1/contacts", body); err != nil {
		t.Fatalf("active linked tenant outbound call denied: %v", err)
	}
	for _, claimedTenant := range []string{"", uuid.NewString()} {
		wrong := identity
		wrong.PlatformTenantID = claimedTenant
		if _, err := authorizer.AuthorizeWorkflow(ctx, mint(wrong), offer.ID, "POST", "/v1/contacts", body); !errors.Is(err, ErrWorkflowNotAuthorized) {
			t.Fatalf("accepted tenant claim %q: %v", claimedTenant, err)
		}
	}
	if _, err := store.SetPlatformTenantStatus(ctx, account.ID, tenant.ID, state.PlatformTenantSuspended); err != nil {
		t.Fatal(err)
	}
	if _, err := authorizer.AuthorizeWorkflow(ctx, valid, offer.ID, "POST", "/v1/contacts", body); !errors.Is(err, ErrWorkflowNotAuthorized) {
		t.Fatalf("suspended tenant outbound call authorized: %v", err)
	}
	if _, err := store.SetPlatformTenantStatus(ctx, account.ID, tenant.ID, state.PlatformTenantActive); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RevokeAPIConsumer(ctx, account.ID, consumer.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := authorizer.AuthorizeWorkflow(ctx, valid, offer.ID, "POST", "/v1/contacts", body); !errors.Is(err, ErrWorkflowNotAuthorized) {
		t.Fatalf("revoked tenant app link outbound call authorized: %v", err)
	}
}

func TestWorkflowForEachOutboundPostgresAuthorization(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	keyID := internalsvc.KidFromPub(public)
	if err := store.InsertClusterSigningKey(ctx, state.ClusterSigningKey{KeyID: keyID, PublicKeyPEM: string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})), SealedBlob: []byte("fixture")}); err != nil {
		t.Fatal(err)
	}
	account, err := store.CreateAccount(ctx, uuid.NewString()+"@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "batch-" + uuid.NewString(), Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	offer := state.OutboundIntegrationOffer{ID: uuid.NewString(), AccountID: account.ID, Name: "crm", Origin: "https://api.example.com", AllowedMethods: []string{"POST"}, AllowedPathPrefixes: []string{"/v1"}, Enabled: true, OwnerKind: "customer", CredentialSource: "customer_sealed", RequestPolicy: api.DefaultOutboundRequestPolicy()}
	if _, err := store.CreateOutboundIntegration(ctx, offer); err != nil {
		t.Fatal(err)
	}
	if err := store.SetOutboundCredential(ctx, account.ID, offer.ID, []byte("sealed")); err != nil {
		t.Fatal(err)
	}
	spec := api.WorkflowSpec{Name: "batch", Steps: []api.WorkflowStepSpec{{Name: "send", ForEach: &api.WorkflowForEachSpec{Items: "input.items", Action: api.WorkflowForEachActionSpec{Outbound: &api.WorkflowOutboundSpec{IntegrationID: offer.ID, Method: "POST", Path: "/v1/contacts", IdempotencySupported: true}}}}}}
	if err := store.ValidateWorkflowOutboundBindings(ctx, app.ID, spec); !errors.Is(err, state.ErrAutomationInvalid) {
		t.Fatalf("nested action bypassed binding validation: %v", err)
	}
	if _, err := store.BindOutboundIntegration(ctx, account.ID, app.ID, offer.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.ValidateWorkflowOutboundBindings(ctx, app.ID, spec); err != nil {
		t.Fatal(err)
	}
	snapshot, _ := json.Marshal(spec)
	run := &state.WorkflowRun{AppID: app.ID, WorkflowName: spec.Name, DefinitionSnapshot: snapshot, Input: json.RawMessage(`{"items":[{"email":"a@example.com"},{"email":"b@example.com"}]}`)}
	if err := store.CreateWorkflowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateWorkflowSteps(ctx, run.ID, []*state.WorkflowStep{{StepName: "send"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimNextDueWorkflowRun(ctx); err != nil {
		t.Fatal(err)
	}
	outcome, err := store.ResolveWorkflowForEach(ctx, run.ID, "send")
	if err != nil {
		t.Fatal(err)
	}
	body, err := store.StartWorkflowStep(ctx, run.ID, outcome.Item.StepName, 1, outcome.Item.Input)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := store.GetWorkflowOutboundAttempt(ctx, run.ID, outcome.Item.StepName, 1)
	if err != nil {
		t.Fatal(err)
	}
	identity := WorkflowIdentity{AccountID: account.ID, AppID: app.ID, RunID: run.ID, StepName: outcome.Item.StepName, Attempt: 1, AttemptToken: lease.Token}
	authorizer, err := NewPostgresWorkflowAuthorizer(pool)
	if err != nil {
		t.Fatal(err)
	}
	mint := func(identity WorkflowIdentity, path string) string {
		t.Helper()
		token, err := MintWorkflowIdentity(identity, offer.ID, "POST", path, body, private, keyID, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	token := mint(identity, "/v1/contacts")
	if _, err := authorizer.AuthorizeWorkflow(ctx, token, offer.ID, "POST", "/v1/contacts", body); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"queued item", "parent", "route"} {
		other := identity
		path := "/v1/contacts"
		switch field {
		case "queued item":
			other.StepName = api.WorkflowForEachItemName("send", 1)
		case "parent":
			other.StepName = "send"
		case "route":
			path = "/v1/admin"
		}
		if _, err := authorizer.AuthorizeWorkflow(ctx, mint(other, path), offer.ID, "POST", path, body); !errors.Is(err, ErrWorkflowNotAuthorized) {
			t.Fatalf("accepted %s: %v", field, err)
		}
	}
	if err := store.RecoverWorkflowRun(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimNextDueWorkflowRun(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := store.StartWorkflowStep(ctx, run.ID, identity.StepName, 2, body); err != nil {
		t.Fatal(err)
	}
	if _, err := authorizer.AuthorizeWorkflow(ctx, token, offer.ID, "POST", "/v1/contacts", body); !errors.Is(err, ErrWorkflowNotAuthorized) {
		t.Fatalf("stale item assertion accepted: %v", err)
	}
	if _, err := store.CancelWorkflowRun(ctx, run.ID, "cancel"); err != nil {
		t.Fatal(err)
	}
	if _, err := authorizer.AuthorizeWorkflow(ctx, token, offer.ID, "POST", "/v1/contacts", body); !errors.Is(err, ErrWorkflowNotAuthorized) {
		t.Fatalf("cancelled item authorized: %v", err)
	}
}

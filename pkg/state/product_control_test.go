package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productcontrol"
)

type productContractStore interface {
	ProductControlStore
	OperationStore
	CreateAccount(context.Context, string, api.Plan) (Account, error)
	CreateApp(context.Context, App) (App, error)
	CreateDeployment(context.Context, Deployment) (Deployment, error)
	MarkDeploymentLive(context.Context, string) error
	CreatePlatformTenant(context.Context, string, string, string, int) (PlatformTenant, bool, error)
	CreateComputeNode(context.Context, ComputeNode) (ComputeNode, error)
	CreateInstance(context.Context, string, string, string, int, string, string) (Instance, error)
	ClaimInvocationWithCap(context.Context, string, string, int, int) (Invocation, error)
	CompleteKeyedInvocation(context.Context, string, int, json.RawMessage) error
}

func TestProductControlMemory(t *testing.T) { productControlContract(t, NewMemStore()) }

func productControlContract(t *testing.T, s productContractStore) {
	t.Helper()
	ctx := t.Context()
	account, err := s.CreateAccount(ctx, "product-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, App{AccountID: account.ID, Slug: "product-export", Manifest: AppManifest{RevisionPinTTLSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := s.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:product"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	tenant, _, err := s.CreatePlatformTenant(ctx, account.ID, "alice", "Alice", 100)
	if err != nil {
		t.Fatal(err)
	}
	other, _, err := s.CreatePlatformTenant(ctx, account.ID, "bob", "Bob", 100)
	if err != nil {
		t.Fatal(err)
	}
	def, err := s.PutOperationDefinition(ctx, OperationDefinition{AccountID: account.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{AppID: app.ID, DeploymentID: dep.ID, Scope: dep.Scope, Spec: api.OperationDefinitionSpec{Name: "export", Method: "POST", Path: "/exports", Owner: api.OperationOwnerPlatformTenant, ProgressStages: []string{"generating"}, InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object","required":["file"],"properties":{"file":{"type":"string"}}}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	spec := api.ProductCatalogSpec{Currency: "EUR", Features: map[string]api.ProductFeature{"export": {Label: "Export", DefinitionID: def.ID}}, Plans: map[string]api.ProductPlan{"starter": {Label: "Starter", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 1, UnitPriceMillicents: 25000}}}}}
	catalog, err := s.PutProductCatalog(ctx, account.ID, app.ID, spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetProductCatalog(ctx, uuid.NewString(), app.ID, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign account read: %v", err)
	}
	customer, err := s.SetProductCustomer(ctx, account.ID, app.ID, api.ProductCustomer{TenantID: tenant.ID, Plan: "starter"})
	if err != nil || customer.Revision != catalog.Revision {
		t.Fatalf("assignment: %+v %v", customer, err)
	}
	base := OperationAdmission{AccountID: account.ID, PlatformTenantID: tenant.ID, DefinitionID: def.ID, ProductFeature: "export", ProductControlEnabled: true, Input: json.RawMessage(`{}`)}
	for _, tc := range []struct {
		name   string
		change func(*OperationAdmission)
	}{
		{"direct bypass", func(a *OperationAdmission) { a.ProductFeature = "" }},
		{"flag disabled", func(a *OperationAdmission) { a.ProductControlEnabled = false }},
		{"unassigned tenant", func(a *OperationAdmission) { a.PlatformTenantID = other.ID }},
		{"wrong feature", func(a *OperationAdmission) { a.ProductFeature = "other" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := base
			a.IdempotencyKey = tc.name
			tc.change(&a)
			if _, _, err := s.AdmitOperation(ctx, a); !errors.Is(err, ErrProductAccess) {
				t.Fatalf("admission: %v", err)
			}
		})
	}
	var mu sync.Mutex
	var accepted Operation
	var acceptedKey string
	created := 0
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Go(func() {
			a := base
			a.IdempotencyKey = fmt.Sprintf("work-%d", i)
			op, fresh, err := s.AdmitOperation(ctx, a)
			mu.Lock()
			defer mu.Unlock()
			if err == nil && fresh {
				created++
				accepted = op
				acceptedKey = a.IdempotencyKey
			} else if !errors.Is(err, ErrOperationQuota) {
				t.Errorf("concurrent admission: %v", err)
			}
		})
	}
	wg.Wait()
	if created != 1 {
		t.Fatalf("created %d operations at a one-start limit", created)
	}
	if accepted.ProductControl == nil || accepted.ProductControl.UnitPriceMillicents != 25000 {
		t.Fatalf("missing price evidence: %+v", accepted.ProductControl)
	}
	retry := base
	retry.IdempotencyKey = acceptedKey
	op, fresh, err := s.AdmitOperation(ctx, retry)
	if err != nil || fresh || op.ID != accepted.ID {
		t.Fatalf("retry used another start: %+v %v %v", op, fresh, err)
	}
	grant := spec.Plans["starter"].Features["export"]
	grant.UnitPriceMillicents = 99000
	spec.Plans["starter"].Features["export"] = grant
	next, err := s.PutProductCatalog(ctx, account.ID, app.ID, spec)
	if err != nil || next.Revision == catalog.Revision {
		t.Fatalf("revision: %+v %v", next, err)
	}
	if got, err := s.GetProductCustomer(ctx, account.ID, app.ID, tenant.ID); err != nil || got.Revision != catalog.Revision {
		t.Fatalf("publish changed existing customer: %+v %v", got, err)
	}
	if _, err := s.SetProductCustomer(ctx, account.ID, app.ID, api.ProductCustomer{TenantID: tenant.ID, Plan: "starter", Suspended: true}); err != nil {
		t.Fatal(err)
	}
	a := base
	a.IdempotencyKey = "suspended"
	if _, _, err := s.AdmitOperation(ctx, a); !errors.Is(err, ErrProductAccess) {
		t.Fatalf("suspended customer admitted: %v", err)
	}
	if _, err := s.SetProductCustomer(ctx, account.ID, app.ID, api.ProductCustomer{TenantID: tenant.ID, Plan: "starter"}); err != nil {
		t.Fatal(err)
	}
	a.IdempotencyKey = "new-revision"
	if _, _, err := s.AdmitOperation(ctx, a); !errors.Is(err, ErrOperationQuota) {
		t.Fatalf("revision reset allowance: %v", err)
	}
	node, err := s.CreateComputeNode(ctx, ComputeNode{Name: "product-" + uuid.NewString(), TargetURL: "unix:///tmp/product-test.sock", VPCPUs: 8, MemMB: 8192, MaxConcurrency: 32, AdmissionCeilingMB: 8192, VCPUBudget: 32, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := s.CreateInstance(ctx, app.ID, dep.ID, string(StateRunning), 128, node.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	inv, err := s.ClaimInvocationWithCap(ctx, accepted.CurrentInvocationID, instance.ID, 60, 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteKeyedInvocation(ctx, inv.ID, inv.Attempts, json.RawMessage(`{"file":"export.csv"}`)); err != nil {
		t.Fatal(err)
	}
	// A repeated completion cannot generate another billable unit.
	_ = s.CompleteKeyedInvocation(ctx, inv.ID, inv.Attempts, json.RawMessage(`{"file":"export.csv"}`))
	usage, err := s.GetProductUsage(ctx, account.ID, app.ID, tenant.ID, productcontrol.Period(time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	if len(usage.Lines) != 1 || usage.Lines[0].Starts != 1 || usage.Lines[0].Succeeded != 1 || usage.Lines[0].AmountMillicents != 25000 || !usage.Provisional {
		t.Fatalf("immutable success pricing: %+v", usage)
	}
	otherUsage, err := s.GetProductUsage(ctx, account.ID, app.ID, other.ID, usage.Period)
	if err != nil || len(otherUsage.Lines) != 0 {
		t.Fatalf("cross-customer usage: %+v %v", otherUsage, err)
	}
	// A deployment/definition-name change must not turn a retry into new work.
	newDefinition := def
	newDefinition.ID = ""
	newDefinition.Spec.Name = "export-v2"
	newDefinition.Spec.Path = "/exports-v2"
	newDefinition, err = s.PutOperationDefinition(ctx, newDefinition)
	if err != nil {
		t.Fatal(err)
	}
	unlisted := base
	unlisted.DefinitionID = newDefinition.ID
	unlisted.ProductFeature = ""
	unlisted.IdempotencyKey = "new-unlisted-definition"
	if _, _, err := s.AdmitOperation(ctx, unlisted); !errors.Is(err, ErrProductAccess) {
		t.Fatalf("new definition bypassed app product policy: %v", err)
	}
	spec.Features["export"] = api.ProductFeature{Label: "Export", DefinitionID: newDefinition.ID}
	if _, err := s.PutProductCatalog(ctx, account.ID, app.ID, spec); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetProductCustomer(ctx, account.ID, app.ID, api.ProductCustomer{TenantID: tenant.ID, Plan: "starter"}); err != nil {
		t.Fatal(err)
	}
	retry.DefinitionID = newDefinition.ID
	if got, fresh, err := s.AdmitOperation(ctx, retry); err != nil || fresh || got.ID != accepted.ID {
		t.Fatalf("definition change duplicated work: %+v %v %v", got, fresh, err)
	}
	retry.Input = json.RawMessage(`{"changed":true}`)
	if _, _, err := s.AdmitOperation(ctx, retry); !errors.Is(err, ErrOperationInputConflict) {
		t.Fatalf("changed retry input: %v", err)
	}
	// Invalid output remains unresolved and never creates a success charge.
	if _, err := s.SetProductCustomer(ctx, account.ID, app.ID, api.ProductCustomer{TenantID: other.ID, Plan: "starter"}); err != nil {
		t.Fatal(err)
	}
	b := base
	b.PlatformTenantID = other.ID
	b.DefinitionID = newDefinition.ID
	b.IdempotencyKey = "invalid-output"
	unresolved, _, err := s.AdmitOperation(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := s.ClaimInvocationWithCap(ctx, unresolved.CurrentInvocationID, instance.ID, 60, 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteKeyedInvocation(ctx, claim.ID, claim.Attempts, json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	otherUsage, err = s.GetProductUsage(ctx, account.ID, app.ID, other.ID, usage.Period)
	if err != nil || len(otherUsage.Lines) != 1 || otherUsage.Lines[0].Starts != 1 || otherUsage.Lines[0].Succeeded != 0 || otherUsage.Lines[0].AmountMillicents != 0 {
		t.Fatalf("unverified outcome billed: %+v %v", otherUsage, err)
	}
	spec.Currency = "USD"
	if _, err := s.PutProductCatalog(ctx, account.ID, app.ID, spec); !errors.Is(err, ErrConflict) {
		t.Fatalf("currency changed: %v", err)
	}
	if _, err := s.GetProductUsage(ctx, account.ID, app.ID, tenant.ID, "2026-99"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid month: %v", err)
	}
}

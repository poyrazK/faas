package state

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type productBindingPilotStore interface {
	productContractStore
	MarkDeploymentSuperseded(context.Context, string) error
}

type productPilotResult struct {
	account, app string
	operation    Operation
}

func TestProductControlDeploymentPilotMemory(t *testing.T) {
	s := NewMemStore()
	result := productDeploymentPilot(t, s)
	s.mu.Lock()
	delete(s.operationMemoryLocked().operations, result.operation.ID)
	s.mu.Unlock()
	checkProductRetainedReceipt(t, s, result)
}

func productDeploymentPilot(t *testing.T, s productBindingPilotStore) productPilotResult {
	t.Helper()
	ctx := t.Context()
	account, err := s.CreateAccount(ctx, uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, App{AccountID: account.ID, Slug: "product-pilot", Manifest: AppManifest{RevisionPinTTLSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	spec := api.ProductCatalogSpec{Currency: "EUR", Features: map[string]api.ProductFeature{"export": {Label: "Export", Operation: &api.ProductOperationBinding{Name: "export", Scope: api.DefaultEnvScope}}}, Plans: map[string]api.ProductPlan{
		"starter": {Label: "Starter", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 10, UnitPriceMillicents: 25000}}},
		"pro":     {Label: "Pro", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 20, UnitPriceMillicents: 10000}}},
	}}
	// Commercial configuration can precede the first deployment.
	catalog, err := s.PutProductCatalog(ctx, account.ID, app.ID, spec)
	if err != nil {
		t.Fatal(err)
	}
	customers := make([]PlatformTenant, 3)
	for i, name := range []string{"alice", "bob", "carol"} {
		customers[i], _, err = s.CreatePlatformTenant(ctx, account.ID, name, name, 100)
		if err != nil {
			t.Fatal(err)
		}
		plan := "starter"
		if i == 1 {
			plan = "pro"
		}
		if _, err := s.SetProductCustomer(ctx, account.ID, app.ID, api.ProductCustomer{TenantID: customers[i].ID, Plan: plan}); err != nil {
			t.Fatal(err)
		}
	}
	request := func(tenant, key string) ProductFeatureRequest {
		return ProductFeatureRequest{AccountID: account.ID, AppID: app.ID, TenantID: tenant, Feature: "export", IdempotencyKey: key, Input: json.RawMessage(`{}`)}
	}
	if _, err := s.ResolveProductFeature(ctx, request(customers[0].ID, "not-deployed")); !errors.Is(err, ErrProductBindingUnavailable) {
		t.Fatalf("undeployed feature: %v", err)
	}
	deploy := func(scope string, install bool) (Deployment, OperationDefinition) {
		t.Helper()
		dep, err := s.CreateDeployment(ctx, Deployment{AppID: app.ID, Scope: scope, Kind: DeploymentKindImage, ImageDigest: "sha256:" + uuid.NewString()})
		if err != nil {
			t.Fatal(err)
		}
		var def OperationDefinition
		if install {
			def, err = s.PutOperationDefinition(ctx, OperationDefinition{AccountID: account.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{AppID: app.ID, DeploymentID: dep.ID, Scope: dep.Scope, Spec: api.OperationDefinitionSpec{Name: "export", Method: "POST", Path: "/exports", Owner: api.OperationOwnerPlatformTenant, ProgressStages: []string{"generating"}, InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object","required":["file"],"properties":{"file":{"type":"string"}}}`)}}})
			if err != nil {
				t.Fatal(err)
			}
		}
		if err := s.MarkDeploymentLive(ctx, dep.ID); err != nil {
			t.Fatal(err)
		}
		return dep, def
	}
	admit := func(req ProductFeatureRequest) (Operation, bool) {
		t.Helper()
		def, err := s.ResolveProductFeature(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		op, fresh, err := s.AdmitOperation(ctx, OperationAdmission{AccountID: req.AccountID, PlatformTenantID: req.TenantID, DefinitionID: def.ID, ProductFeature: req.Feature, ProductControlEnabled: true, IdempotencyKey: req.IdempotencyKey, Input: req.Input})
		if err != nil {
			t.Fatal(err)
		}
		return op, fresh
	}
	dep1, def1 := deploy(api.DefaultEnvScope, true)
	first, fresh := admit(request(customers[0].ID, "original"))
	if !fresh || first.ProductControl.Admission.DefinitionID != def1.ID || first.ProductControl.Admission.StartsBefore != 0 {
		t.Fatalf("missing admission evidence: %+v", first.ProductControl)
	}
	grant := spec.Plans["starter"].Features["export"]
	grant.UnitPriceMillicents = 50000
	spec.Plans["starter"].Features["export"] = grant
	newCatalog, err := s.PutProductCatalog(ctx, account.ID, app.ID, spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetProductCustomer(ctx, account.ID, app.ID, api.ProductCustomer{TenantID: customers[2].ID, Plan: "starter"}); err != nil {
		t.Fatal(err)
	}
	dep2, def2 := deploy(api.DefaultEnvScope, true)
	_, staging := deploy("staging", true)
	if staging.ID == def2.ID {
		t.Fatal("test fixture reused definition")
	}
	for i, price := range []int64{25000, 10000, 50000} {
		op, fresh := admit(request(customers[i].ID, "after-deploy"))
		if !fresh || op.DefinitionID != def2.ID || op.DeploymentID != dep2.ID || op.ProductControl.UnitPriceMillicents != price {
			t.Fatalf("deployment changed commercial terms for %d: %+v", i, op)
		}
		wantRevision := catalog.Revision
		if i == 2 {
			wantRevision = newCatalog.Revision
		}
		if op.ProductControl.Revision != wantRevision {
			t.Fatal("deployment migrated a customer revision")
		}
	}
	// Current assignment and deployment cannot redirect an accepted request.
	if _, err := s.SetProductCustomer(ctx, account.ID, app.ID, api.ProductCustomer{TenantID: customers[0].ID, Plan: "starter", Revision: catalog.Revision, Suspended: true}); err != nil {
		t.Fatal(err)
	}
	missing, _ := deploy(api.DefaultEnvScope, false)
	if _, err := s.ResolveProductFeature(ctx, request(customers[2].ID, "missing-definition")); !errors.Is(err, ErrProductBindingUnavailable) {
		t.Fatalf("fell back to an older contract: %v", err)
	}
	if _, err := s.ResolveProductFeature(ctx, request(customers[0].ID, "new-suspended")); !errors.Is(err, ErrProductAccess) {
		t.Fatalf("suspension bypass: %v", err)
	}
	retried, fresh := admit(request(customers[0].ID, "original"))
	if fresh || retried.ID != first.ID || retried.DefinitionID != def1.ID {
		t.Fatal("retry moved work to a new definition")
	}
	changed := request(customers[0].ID, "original")
	changed.Input = json.RawMessage(`{"changed":true}`)
	if _, err := s.ResolveProductFeature(ctx, changed); !errors.Is(err, ErrOperationInputConflict) {
		t.Fatalf("retry input changed: %v", err)
	}
	if err := s.MarkDeploymentSuperseded(ctx, missing.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveProductFeature(ctx, request(customers[2].ID, "no-live")); !errors.Is(err, ErrProductBindingUnavailable) {
		t.Fatalf("no live deployment: %v", err)
	}
	if _, err := s.ResolveProductFeature(ctx, request(customers[0].ID, "original")); err != nil {
		t.Fatalf("lost retry without a live deployment: %v", err)
	}
	// Settle the original run after code and commercial configuration changed.
	node, err := s.CreateComputeNode(ctx, ComputeNode{Name: uuid.NewString(), TargetURL: "unix:///tmp/product-pilot.sock", VPCPUs: 8, MemMB: 8192, MaxConcurrency: 32, AdmissionCeilingMB: 8192, VCPUBudget: 32, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := s.CreateInstance(ctx, app.ID, dep1.ID, string(StateRunning), 128, node.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	inv, err := s.ClaimInvocationWithCap(ctx, first.CurrentInvocationID, instance.ID, 60, 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteKeyedInvocation(ctx, inv.ID, inv.Attempts, json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	before, err := s.GetProductReceipt(ctx, account.ID, app.ID, first.ID)
	if err != nil || before.SucceededAt != nil {
		t.Fatalf("invalid output billed: %+v %v", before, err)
	}
	recovery := api.OperationRecoveryRequest{RecoveryID: "verified-export", ExpectedGeneration: 1, Resolution: "succeeded", Evidence: "internal export fixture verified file", Result: json.RawMessage(`{"file":"export.csv"}`)}
	if _, err := s.RecoverOperation(ctx, account.ID, "", first.ID, recovery); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecoverOperation(ctx, account.ID, "", first.ID, recovery); err != nil {
		t.Fatal(err)
	}
	receipt, err := s.GetProductReceipt(ctx, account.ID, app.ID, first.ID)
	if err != nil || receipt.SucceededAt == nil || receipt.Receipt.UnitPriceMillicents != 25000 || receipt.Receipt.Admission.DefinitionID != def1.ID {
		t.Fatalf("lost original settlement evidence: %+v %v", receipt, err)
	}
	usage, err := s.GetProductUsage(ctx, account.ID, app.ID, customers[0].ID, receipt.Receipt.Period)
	if err != nil || len(usage.Lines) != 1 || usage.Lines[0].Starts != 2 || usage.Lines[0].Succeeded != 1 || usage.Lines[0].AmountMillicents != 25000 {
		t.Fatalf("pilot usage: %+v %v", usage, err)
	}
	for _, selector := range []struct{ account, app string }{{uuid.NewString(), app.ID}, {account.ID, uuid.NewString()}} {
		if _, err := s.GetProductReceipt(ctx, selector.account, selector.app, first.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("receipt ownership: %v", err)
		}
	}
	// A pending canary cannot take over; a split live canary pauses new work.
	_, serving := deploy(api.DefaultEnvScope, true)
	candidate, err := s.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:canary", CanaryTotalSteps: 2, TrafficPercent: 25})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutOperationDefinition(ctx, OperationDefinition{AccountID: account.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{AppID: app.ID, DeploymentID: candidate.ID, Scope: candidate.Scope, Spec: serving.Spec}}); err != nil {
		t.Fatal(err)
	}
	if resolved, err := s.ResolveProductFeature(ctx, request(customers[2].ID, "before-canary-live")); err != nil || resolved.ID != serving.ID {
		t.Fatalf("pending canary took over: %+v %v", resolved, err)
	}
	if err := s.MarkDeploymentLive(ctx, candidate.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveProductFeature(ctx, request(customers[2].ID, "split-canary")); !errors.Is(err, ErrProductBindingUnavailable) {
		t.Fatalf("split canary admitted new work: %v", err)
	}
	if _, _, err := s.AdmitOperation(ctx, OperationAdmission{AccountID: account.ID, PlatformTenantID: customers[2].ID, DefinitionID: serving.ID, ProductFeature: "export", ProductControlEnabled: true, IdempotencyKey: "stale-resolved-binding", Input: json.RawMessage(`{}`)}); !errors.Is(err, ErrProductBindingUnavailable) {
		t.Fatalf("admission trusted a stale resolution: %v", err)
	}
	if _, fresh := admit(request(customers[0].ID, "original")); fresh {
		t.Fatal("split canary prevented original retry")
	}
	return productPilotResult{account: account.ID, app: app.ID, operation: first}
}

func checkProductRetainedReceipt(t *testing.T, s ProductControlStore, result productPilotResult) {
	t.Helper()
	receipt, err := s.GetProductReceipt(t.Context(), result.account, result.app, result.operation.ID)
	if err != nil || receipt.SucceededAt == nil || receipt.Receipt.Admission == nil || receipt.Receipt.Admission.MonthlyStarts != 10 || receipt.Receipt.Admission.StartsBefore != 0 {
		t.Fatalf("commercial evidence disappeared with result retention: %+v %v", receipt, err)
	}
	if receipt.Receipt.Period != result.operation.CreatedAt.UTC().Format("2006-01") || receipt.SucceededAt.After(time.Now()) {
		t.Fatal("settlement period or timestamp changed")
	}
}

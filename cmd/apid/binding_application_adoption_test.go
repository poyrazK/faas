package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func adoptionPromotionFixture(t *testing.T) (testEnv, state.App, state.Deployment, state.Deployment, state.Instance) {
	t.Helper()
	e, app, serving, candidate := promotionFixture(t)
	rows, err := e.s.managedPostgresBindings.ListForApp(context.Background(), e.acct.ID, app.ID, "default")
	if err != nil || len(rows) != 1 {
		t.Fatalf("catalog: %+v %v", rows, err)
	}
	if err := e.store.PutManagedPostgresSecret(context.Background(), state.AppSecret{AccountID: e.acct.ID, AppID: app.ID, Scope: "default", Key: "DATABASE_URL", ManagedPostgresBindingID: rows[0].BindingID, ManagedPostgresAccess: string(rows[0].Access), ManagedCredentialRef: "PRIVATE_REFERENCE", ManagedCredentialGeneration: 1, Kid: "PRIVATE_KID", Ciphertext: []byte("PRIVATE_CIPHERTEXT"), ValueHash: "0123456789abcdef"}); err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkAppRuntimeConfigChanged(context.Background(), app.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.store.SetDeploymentSecretReloadSignal(context.Background(), candidate.ID, "SIGHUP"); err != nil {
		t.Fatal(err)
	}
	instance, err := e.store.CreateInstance(context.Background(), app.ID, candidate.ID, "running", 512, state.DefaultLocalNodeName, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	completePromotionProbe(t, e, app, candidate, passedPostgresVerification)
	return e, app, serving, candidate, instance
}

func recordBindingAdoption(t *testing.T, e testEnv, app state.App, instance state.Instance, status state.SecretApplicationReloadAckStatus, workloads ...string) {
	t.Helper()
	workload := ""
	if len(workloads) > 0 {
		workload = workloads[0]
	}
	generation := strings.Repeat("a", 32)
	if err := e.store.BeginAppSecretRuntimeProcess(context.Background(), state.AppSecretRuntimeProcess{AccountID: e.acct.ID, AppID: app.ID, InstanceID: instance.ID, WorkloadName: workload, Generation: generation}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	candidates := []state.AppSecretDeliveryCandidate{{Scope: "default", Key: "DATABASE_URL", Version: 1}}
	if updated, err := e.store.RecordAppSecretRuntimeReload(context.Background(), state.AppSecretRuntimeReloadResult{AccountID: e.acct.ID, AppID: app.ID, InstanceID: instance.ID, WorkloadName: workload, Revision: strings.Repeat("a", 64), Projection: state.SecretReloadProjectionUpdated, Signal: state.SecretReloadSignalSent, AttemptedAt: now, Candidates: candidates}); err != nil || updated != 1 {
		t.Fatalf("reload: %d %v", updated, err)
	}
	errorCode := ""
	if status == state.SecretApplicationReloadAckFailed {
		errorCode = "application_reload_failed"
	}
	if updated, err := e.store.RecordAppSecretRuntimeReloadAck(context.Background(), state.AppSecretRuntimeReloadAckResult{AccountID: e.acct.ID, AppID: app.ID, InstanceID: instance.ID, WorkloadName: workload, Revision: strings.Repeat("a", 64), Status: status, Generation: generation, ErrorCode: errorCode, AttemptedAt: time.Now().UTC(), Candidates: candidates}); err != nil || updated != 1 {
		t.Fatalf("ack: %d %v", updated, err)
	}
}

func TestApplicationAckPromotionWithMainAndSidecarRequiresBothConsumers(t *testing.T) {
	e, app, serving, base, baseRuntime := adoptionPromotionFixture(t)
	ctx := context.Background()
	recordBindingAdoption(t, e, app, baseRuntime, state.SecretApplicationReloadAckApplied)
	base.ID, base.CreatedAt = "", time.Now().UTC()
	base.OverrideEnvSecrets = json.RawMessage(`{"DATABASE_URL":"secret:DATABASE_URL"}`)
	base.Sidecars = json.RawMessage(`[{"name":"proxy","type":"sidecar","env_secrets":{"DATABASE_URL":"secret:DATABASE_URL"}}]`)
	candidate, err := e.store.CreateDeployment(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.SetDeploymentSecretReloadSignal(ctx, candidate.ID, "SIGHUP"); err != nil {
		t.Fatal(err)
	}
	if err := e.store.SetDeploymentSidecarSecretReloadSignal(ctx, candidate.ID, "proxy", "SIGUSR1"); err != nil {
		t.Fatal(err)
	}
	runtime, err := e.store.CreateInstance(ctx, app.ID, candidate.ID, "running", 512, state.DefaultLocalNodeName, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	completePromotionProbe(t, e, app, candidate, passedPostgresVerification)
	recordBindingAdoption(t, e, app, runtime, state.SecretApplicationReloadAckApplied)
	path := "/v1/deployments/" + candidate.ID + "/promote-with-application-ack"
	request := api.BindingPromotionRequest{ExpectedServingDeploymentID: &serving.ID}
	response := e.do(t, http.MethodPost, path, request, nil)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "application_adoption_unknown") {
		t.Fatalf("main receipt satisfied missing sidecar: %d %s", response.Code, response.Body.String())
	}
	assertPromotionWeights(t, e, serving, candidate, 0)
	recordBindingAdoption(t, e, app, runtime, state.SecretApplicationReloadAckFailed, "proxy")
	response = e.do(t, http.MethodPost, path, request, nil)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "application_adoption_failed") {
		t.Fatalf("failed sidecar receipt accepted: %d %s", response.Code, response.Body.String())
	}
	assertPromotionWeights(t, e, serving, candidate, 0)
	recordBindingAdoption(t, e, app, runtime, state.SecretApplicationReloadAckApplied, "proxy")
	response = e.do(t, http.MethodPost, path, request, nil)
	var promoted api.BindingPromotionResponse
	if err := json.Unmarshal(response.Body.Bytes(), &promoted); err != nil || response.Code != http.StatusOK || promoted.BindingsCheck == nil || !promoted.BindingsCheck.Passed {
		t.Fatalf("current main+sidecar receipts blocked: %d %s %v", response.Code, response.Body.String(), err)
	}
	assertPromotionWeights(t, e, serving, candidate, 100)
}

func TestApplicationAckPromotionRouteForcesPolicyAndReportsSafeInventory(t *testing.T) {
	e, app, serving, candidate, instance := adoptionPromotionFixture(t)
	path := "/v1/deployments/" + candidate.ID + "/promote-with-application-ack"
	response := e.do(t, http.MethodPost, path, api.BindingPromotionRequest{}, nil)
	if response.Code != 409 || !strings.Contains(response.Body.String(), "application_adoption_unknown") || !strings.Contains(response.Body.String(), `"require_application_ack":true`) {
		t.Fatalf("missing ack accepted: %d %s", response.Code, response.Body.String())
	}
	assertPromotionWeights(t, e, serving, candidate, 0)
	recordBindingAdoption(t, e, app, instance, state.SecretApplicationReloadAckApplied)
	inventoryResponse := e.do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/bindings?deployment_id="+candidate.ID, nil, nil)
	var inventory api.AppBindingInventory
	if err := json.Unmarshal(inventoryResponse.Body.Bytes(), &inventory); err != nil || inventoryResponse.Code != 200 || len(inventory.Bindings) != 1 {
		t.Fatalf("inventory: %d %s %v", inventoryResponse.Code, inventoryResponse.Body.String(), err)
	}
	adoption := inventory.Bindings[0].ApplicationAdoption
	if adoption == nil || adoption.Status != "current" || adoption.Application.Current != 1 || adoption.Reload.Current != 1 || adoption.Targets[0].DeploymentID != candidate.ID {
		t.Fatalf("adoption: %+v", adoption)
	}
	for _, secret := range []string{"PRIVATE_REFERENCE", "PRIVATE_KID", "PRIVATE_CIPHERTEXT", "0123456789abcdef", "managed_postgres_binding_id", "application_ack_error_code"} {
		if strings.Contains(inventoryResponse.Body.String(), secret) {
			t.Fatalf("private metadata leaked: %s", secret)
		}
	}
	response = e.do(t, http.MethodPost, path, api.BindingPromotionRequest{RequireApplicationAck: false, ExpectedServingDeploymentID: &serving.ID}, nil)
	var receipt api.BindingPromotionResponse
	if err := json.Unmarshal(response.Body.Bytes(), &receipt); err != nil || response.Code != 200 || receipt.BindingsCheck == nil || !receipt.BindingsCheck.Passed || !receipt.BindingsCheck.RequireApplicationAck {
		t.Fatalf("strict promotion: %d %s %v", response.Code, response.Body.String(), err)
	}
	assertPromotionWeights(t, e, serving, candidate, 100)
	recordBindingAdoption(t, e, app, instance, state.SecretApplicationReloadAckFailed)
	response = e.do(t, http.MethodPost, path, api.BindingPromotionRequest{}, nil)
	if response.Code != 409 || !strings.Contains(response.Body.String(), "application_adoption_failed") {
		t.Fatalf("retry ignored failed ack: %d %s", response.Code, response.Body.String())
	}
}

func TestApplicationAckPromotionRejectsServingOnlyAndPostCheckChanges(t *testing.T) {
	for _, change := range []string{"receipt", "reload_support", "credentials", "candidate_absent"} {
		t.Run(change, func(t *testing.T) {
			e, app, serving, candidate, instance := adoptionPromotionFixture(t)
			recordBindingAdoption(t, e, app, instance, state.SecretApplicationReloadAckApplied)
			if change == "candidate_absent" {
				if err := e.store.UpdateInstanceState(context.Background(), instance.ID, "stopped"); err != nil {
					t.Fatal(err)
				}
				if err := e.store.SetDeploymentSecretReloadSignal(context.Background(), serving.ID, "SIGHUP"); err != nil {
					t.Fatal(err)
				}
				servingInstance, err := e.store.CreateInstance(context.Background(), app.ID, serving.ID, "running", 512, state.DefaultLocalNodeName, uuid.NewString())
				if err != nil {
					t.Fatal(err)
				}
				recordBindingAdoption(t, e, app, servingInstance, state.SecretApplicationReloadAckApplied)
			} else {
				e.s.store = &changingBindingPromotionStore{MemStore: e.store, before: func() {
					switch change {
					case "receipt":
						recordBindingAdoption(t, e, app, instance, state.SecretApplicationReloadAckFailed)
					case "reload_support":
						if err := e.store.SetDeploymentSecretReloadSignal(context.Background(), candidate.ID, ""); err != nil {
							t.Fatal(err)
						}
					case "credentials":
						secret, err := e.store.GetAppSecretInScope(context.Background(), e.acct.ID, app.ID, "default", "DATABASE_URL")
						if err != nil {
							t.Fatal(err)
						}
						secret.ManagedCredentialGeneration, secret.ValueHash = 2, "fedcba9876543210"
						if err := e.store.PutManagedPostgresSecret(context.Background(), *secret); err != nil {
							t.Fatal(err)
						}
					}
				}}
			}
			response := e.do(t, http.MethodPost, "/v1/deployments/"+candidate.ID+"/promote-with-application-ack", api.BindingPromotionRequest{}, nil)
			code := "bindings_check_changed"
			if change == "candidate_absent" {
				code = "application_ack_candidate_unobserved"
			}
			if response.Code != 409 || !strings.Contains(response.Body.String(), code) {
				t.Fatalf("changed %s: %d %s", change, response.Code, response.Body.String())
			}
			assertPromotionWeights(t, e, serving, candidate, 0)
		})
	}
}

type unavailableBindingAdoptionStore struct{ *state.MemStore }

func (s *unavailableBindingAdoptionStore) ReadBindingApplicationAdoption(context.Context, string, string, []state.BindingAdoptionSelector) ([]state.BindingApplicationAdoptionRow, error) {
	return nil, errors.New("PRIVATE_PROVIDER_ERROR")
}

func TestApplicationAdoptionUnavailableReadIsOptionalUntilRequired(t *testing.T) {
	e, app, serving, candidate, _ := adoptionPromotionFixture(t)
	e.s.store = &unavailableBindingAdoptionStore{MemStore: e.store}
	response := e.do(t, http.MethodPost, "/v1/deployments/"+candidate.ID+"/promote-with-application-ack", api.BindingPromotionRequest{}, nil)
	if response.Code != 409 || !strings.Contains(response.Body.String(), "application_adoption_unknown") || strings.Contains(response.Body.String(), "PRIVATE_PROVIDER_ERROR") {
		t.Fatalf("unavailable: %d %s", response.Code, response.Body.String())
	}
	assertPromotionWeights(t, e, serving, candidate, 0)
	response = e.do(t, http.MethodPost, "/v1/deployments/"+candidate.ID+"/promote", api.BindingPromotionRequest{}, nil)
	if response.Code != 200 {
		t.Fatalf("default policy changed: %d %s", response.Code, response.Body.String())
	}
	assertPromotionWeights(t, e, serving, candidate, 100)
	_ = app
}

type countingBindingAdoptionStore struct {
	*state.MemStore
	calls int
}

func (s *countingBindingAdoptionStore) ReadBindingApplicationAdoption(ctx context.Context, accountID, appID string, selectors []state.BindingAdoptionSelector) ([]state.BindingApplicationAdoptionRow, error) {
	s.calls++
	return s.MemStore.ReadBindingApplicationAdoption(ctx, accountID, appID, selectors)
}

func TestApplicationAdoptionReadsOnlyAuthorizedCatalogSelectors(t *testing.T) {
	e, app, _, candidate, _ := adoptionPromotionFixture(t)
	counted := &countingBindingAdoptionStore{MemStore: e.store}
	e.s.store = counted
	plain, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.CreateAPIKey(context.Background(), e.acct.ID, hash, "inventory", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	e.key = plain
	response := e.do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/bindings?deployment_id="+candidate.ID, nil, nil)
	if response.Code != 200 || counted.calls != 0 || strings.Contains(response.Body.String(), "application_adoption") {
		t.Fatalf("unauthorized observation read: calls=%d %d %s", counted.calls, response.Code, response.Body.String())
	}
}

func TestApplicationAckPromotionRequiresWriteAndCatalogPermissions(t *testing.T) {
	for _, scopes := range [][]string{{api.ScopeAppsRead}, {api.ScopeDeployWrite}, {api.ScopeDeployWrite, api.ScopeAppsRead, api.ScopeManagedPostgresRead, api.ScopeStorageManage}} {
		t.Run(strings.Join(scopes, ","), func(t *testing.T) {
			e, app, serving, candidate, instance := adoptionPromotionFixture(t)
			recordBindingAdoption(t, e, app, instance, state.SecretApplicationReloadAckApplied)
			plain, hash, err := api.GenerateAPIKey()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := e.store.CreateAPIKey(context.Background(), e.acct.ID, hash, "promotion", scopes); err != nil {
				t.Fatal(err)
			}
			e.key = plain
			response := e.do(t, http.MethodPost, "/v1/deployments/"+candidate.ID+"/promote-with-application-ack", api.BindingPromotionRequest{}, nil)
			status, percent := http.StatusOK, 100
			if len(scopes) == 1 {
				status, percent = http.StatusConflict, 0
				if scopes[0] == api.ScopeAppsRead {
					status = http.StatusForbidden
				}
			}
			if response.Code != status {
				t.Fatalf("permissions: %d %s", response.Code, response.Body.String())
			}
			assertPromotionWeights(t, e, serving, candidate, percent)
		})
	}
}

// ADR-508: neither a legacy receipt nor a previous execution can promote a
// same-version candidate; only the replacement's own accepted ACK can do so.
func TestApplicationAckPromotionRequiresCurrentProcessGeneration(t *testing.T) {
	e, app, serving, candidate, instance := adoptionPromotionFixture(t)
	ctx := context.Background()
	ack := state.AppSecretRuntimeReloadAckResult{AccountID: e.acct.ID, AppID: app.ID, InstanceID: instance.ID, Revision: strings.Repeat("a", 64), Status: state.SecretApplicationReloadAckApplied, Candidates: []state.AppSecretDeliveryCandidate{{Scope: "default", Key: "DATABASE_URL", Version: 1}}}
	if _, err := e.store.RecordAppSecretRuntimeReload(ctx, state.AppSecretRuntimeReloadResult{AccountID: ack.AccountID, AppID: ack.AppID, InstanceID: ack.InstanceID, Revision: ack.Revision, Projection: state.SecretReloadProjectionUpdated, Signal: state.SecretReloadSignalSent, Candidates: ack.Candidates}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.RecordAppSecretRuntimeReloadAck(ctx, ack); err != nil {
		t.Fatal(err)
	}
	path := "/v1/deployments/" + candidate.ID + "/promote-with-application-ack"
	assertUnknown := func() {
		t.Helper()
		response := e.do(t, http.MethodPost, path, api.BindingPromotionRequest{}, nil)
		if response.Code != 409 || !strings.Contains(response.Body.String(), "application_adoption_unknown") {
			t.Fatalf("accepted previous/legacy process: %d %s", response.Code, response.Body.String())
		}
		assertPromotionWeights(t, e, serving, candidate, 0)
	}
	assertUnknown()
	recordBindingAdoption(t, e, app, instance, state.SecretApplicationReloadAckApplied)
	next := state.AppSecretRuntimeProcess{AccountID: e.acct.ID, AppID: app.ID, InstanceID: instance.ID, Generation: strings.Repeat("b", 32), PreviousGeneration: strings.Repeat("a", 32)}
	if err := e.store.BeginAppSecretRuntimeProcess(ctx, next); err != nil {
		t.Fatal(err)
	}
	assertUnknown()
	ack.Generation = next.PreviousGeneration
	if _, err := e.store.RecordAppSecretRuntimeReloadAck(ctx, ack); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("late ACK accepted: %v", err)
	}
	assertUnknown()
	ack.Generation = next.Generation
	if _, err := e.store.RecordAppSecretRuntimeReloadAck(ctx, ack); err != nil {
		t.Fatal(err)
	}
	response := e.do(t, http.MethodPost, path, api.BindingPromotionRequest{}, nil)
	if response.Code != 200 {
		t.Fatalf("replacement ACK did not unblock promotion: %d %s", response.Code, response.Body.String())
	}
	assertPromotionWeights(t, e, serving, candidate, 100)
}

// adr: 426 — object-storage canaries report bounded read-access evidence.
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const passedObjectStorageVerification = `{"prefix":"ASSETS","environment":{"status":"passed","detail":"PRIVATE_DETAIL"},"configuration":{"status":"passed"},"connection":{"status":"passed"},"authorization":{"status":"passed"},"bucket_access":{"status":"passed"}}`

func readyObjectStorageVerificationBinding(t *testing.T, e testEnv, app state.App, scope string) state.ObjectS3Credential {
	t.Helper()
	ctx := context.Background()
	bucketID := uuid.NewString()
	bucket, err := e.store.ReserveObjectBucket(ctx, state.ObjectBucket{ID: bucketID, AccountID: e.acct.ID, AppID: app.ID,
		Name: "assets", Scope: scope, Region: "eu", BackendID: "test", BackendFingerprint: strings.Repeat("a", 64),
		PhysicalName: "verification-" + strings.ReplaceAll(bucketID, "-", "")}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.ClaimObjectBucket(ctx, e.acct.ID, app.ID, bucket.ID, "provision", "provisioning"); err != nil {
		t.Fatal(err)
	}
	if err := e.store.FinishObjectBucket(ctx, bucket.ID, "provision", "ready"); err != nil {
		t.Fatal(err)
	}
	access, secret, err := api.GenerateObjectS3Credential()
	if err != nil {
		t.Fatal(err)
	}
	credential := state.ObjectS3Credential{ID: uuid.NewString(), AccountID: e.acct.ID, BucketID: bucket.ID,
		AccessKeyID: access, SecretSealed: []byte(secret), KID: "age1test", Label: "compute", Permission: "read", Status: "active",
		ManagedAppID: app.ID, ManagedScope: scope, ManagedPrefix: "ASSETS"}
	request := state.ObjectS3ComputeBindingCreateRequest{Credential: credential, MaxCredentialsPerBucket: 10, MaxSecretsPerApp: 30}
	for _, suffix := range []string{"_ENDPOINT", "_REGION", "_BUCKET", "_ACCESS_KEY_ID", "_SECRET_ACCESS_KEY", "_ADDRESSING_STYLE"} {
		request.Secrets = append(request.Secrets, state.AppSecret{AccountID: e.acct.ID, AppID: app.ID, Scope: scope,
			Key: "ASSETS" + suffix, Ciphertext: []byte("PRIVATE_SECRET"), Kid: "age1test", ValueHash: "0123456789abcdef", ManagedObjectStorageCredentialID: credential.ID})
	}
	created, err := e.store.CreateObjectS3ComputeBinding(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	return created
}

func selectedObjectStorageVerification(t *testing.T, e testEnv, app state.App, privateID string) (api.AppBindingInventoryItem, api.AppBindingInventory) {
	t.Helper()
	response := e.do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/bindings", nil, nil)
	inventory := decodeBindingInventory(t, response)
	if strings.Contains(response.Body.String(), "PRIVATE_") || strings.Contains(response.Body.String(), privateID) {
		t.Fatalf("private identity or diagnostics leaked: %s", response.Body.String())
	}
	for _, item := range inventory.Bindings {
		if item.Type == api.BindingTypeObjectStorage && item.Scope == "default" {
			return item, inventory
		}
	}
	t.Fatal("no object-storage binding")
	return api.AppBindingInventoryItem{}, inventory
}

func TestObjectStorageBindingVerificationRotationAndStaleServingInstance(t *testing.T) {
	e := setup(t, api.PlanPro)
	enableAppTaskAPIForTest(&e)
	app, dep := seedAppTaskDeployment(t, e, "verified-storage")
	binding := readyObjectStorageVerificationBinding(t, e, app, "default")
	ctx := context.Background()
	old, err := e.store.CreateInstance(ctx, app.ID, dep.ID, "running", 512, state.DefaultLocalNodeName, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	e.store.BackdateForTest(old.ID, time.Now().Add(-time.Hour))
	request := api.CreateAppTaskRequest{Command: []string{api.AppTaskObjectStorageBindingProbeCommand, "ASSETS"}, MaxOutputBytes: 4096}
	first := createAppTaskForTest(t, e, app.Slug, request)
	completeVerificationTask(t, e, beginVerificationTask(t, e, first.ID), passedObjectStorageVerification)
	item, _ := selectedObjectStorageVerification(t, e, app, binding.ID)
	if item.VerificationStatus != "passed" || item.Verification.Source != "task_guest" || len(item.Verification.Checks) != 5 {
		t.Fatalf("first evidence=%+v", item)
	}
	previous := createAppTaskForTest(t, e, app.Slug, request)
	running := beginVerificationTask(t, e, previous.ID)
	access, _, err := api.GenerateObjectS3Credential()
	if err != nil {
		t.Fatal(err)
	}
	rotation := state.ObjectS3CredentialRotationRequest{AccountID: e.acct.ID, BucketID: binding.BucketID, BindingID: binding.ID,
		WakeID: uuid.NewString(), AccessKeyID: access, KID: "age1test", SecretSealed: []byte("PRIVATE_ROTATED")}
	for _, suffix := range []string{"_ACCESS_KEY_ID", "_SECRET_ACCESS_KEY"} {
		rotation.Secrets = append(rotation.Secrets, state.AppSecret{AccountID: e.acct.ID, AppID: app.ID, Scope: "default",
			Key: "ASSETS" + suffix, Ciphertext: []byte("PRIVATE_ROTATED"), Kid: "age1test", ValueHash: "fedcba9876543210", ManagedObjectStorageCredentialID: binding.ID})
	}
	if _, err := e.store.StageObjectS3CredentialRotation(ctx, rotation); err != nil {
		t.Fatal(err)
	}
	item, _ = selectedObjectStorageVerification(t, e, app, binding.ID)
	if item.VerificationStatus != "stale" || item.Verification.Reason != "configuration_changed" {
		t.Fatalf("rotation retained current evidence=%+v", item)
	}
	if err := e.store.StampObjectS3CredentialRotation(ctx, app.ID, rotation.WakeID); err != nil {
		t.Fatal(err)
	}
	fresh := createAppTaskForTest(t, e, app.Slug, request)
	completeVerificationTask(t, e, beginVerificationTask(t, e, fresh.ID), passedObjectStorageVerification)
	completeVerificationTask(t, e, running, `{ "prefix":"ASSETS","error":"PRIVATE_LATE_FAILURE" }`)
	item, inventory := selectedObjectStorageVerification(t, e, app, binding.ID)
	if item.VerificationStatus != "passed" || inventory.RuntimeFreshness.Deployments[0].Serving.Stale != 1 {
		t.Fatalf("passing probe hid old serving instance: %+v %+v", item, inventory.RuntimeFreshness)
	}
	if err := e.store.FinalizeObjectS3CredentialRotationsForApp(ctx, app.ID, rotation.WakeID); err != nil {
		t.Fatal(err)
	}
	item, _ = selectedObjectStorageVerification(t, e, app, binding.ID)
	if item.VerificationStatus != "passed" {
		t.Fatalf("rotation cleanup changed credential identity: %+v", item)
	}
	rows, err := e.store.ListObjectStorageBindingsForApp(ctx, e.acct.ID, app.ID, "default")
	if err != nil || len(rows) != 1 {
		t.Fatalf("rotation identity=%+v err=%v", rows, err)
	}
	// Replacing the internal binding identity invalidates evidence even with
	// unchanged public metadata and without relying on a configuration stamp.
	rows[0].BindingID = uuid.NewString()
	reads := &inventoryReadStore{MemStore: e.store, objects: rows}
	e.s.store = reads
	item, _ = selectedObjectStorageVerification(t, e, app, binding.ID)
	if item.VerificationStatus != "stale" || item.Verification.Reason != "configuration_changed" {
		t.Fatalf("replacement reused evidence: %+v", item)
	}
}

func TestObjectStorageBindingVerificationScopeAndPermission(t *testing.T) {
	for _, scope := range []string{"default", "staging"} {
		t.Run(scope, func(t *testing.T) {
			e := setup(t, api.PlanPro)
			enableAppTaskAPIForTest(&e)
			app, _ := seedAppTaskDeployment(t, e, "storage-scope")
			readyObjectStorageVerificationBinding(t, e, app, scope)
			task := createAppTaskForTest(t, e, app.Slug, api.CreateAppTaskRequest{Command: []string{api.AppTaskObjectStorageBindingProbeCommand, "ASSETS"}})
			stored, err := e.store.AppTaskByID(context.Background(), e.acct.ID, app.ID, task.ID)
			if err != nil || (stored.BindingVerification != nil) != (scope == "default") {
				t.Fatalf("scope=%s pin=%+v err=%v", scope, stored.BindingVerification, err)
			}
		})
	}
	e := setupWithScopes(t, []string{api.ScopeAppsRead, api.ScopeDeployWrite})
	enableAppTaskAPIForTest(&e)
	app, _ := seedAppTaskDeployment(t, e, "storage-permission")
	readyObjectStorageVerificationBinding(t, e, app, "default")
	task := createAppTaskForTest(t, e, app.Slug, api.CreateAppTaskRequest{Command: []string{api.AppTaskObjectStorageBindingProbeCommand, "ASSETS"}})
	stored, err := e.store.AppTaskByID(context.Background(), e.acct.ID, app.ID, task.ID)
	if err != nil || stored.BindingVerification != nil {
		t.Fatalf("restricted task acquired evidence: %+v %v", stored, err)
	}
	inventory := decodeBindingInventory(t, e.do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/bindings", nil, nil))
	for _, item := range inventory.Bindings {
		if item.Type == api.BindingTypeObjectStorage {
			t.Fatal("forbidden storage metadata exposed")
		}
	}
}

func TestObjectStorageBindingVerificationRejectsUnsafeReports(t *testing.T) {
	for _, tc := range []struct {
		name, stdout, reason string
		exit                 *int
	}{
		{"wrong prefix", strings.ReplaceAll(passedObjectStorageVerification, "ASSETS", "OTHER"), "report_invalid", new(int)},
		{"missing stage", `{"prefix":"ASSETS"}`, "report_invalid", new(int)},
		{"invalid status", strings.ReplaceAll(passedObjectStorageVerification, "passed", "ready"), "report_invalid", new(int)},
		{"contradictory error", strings.TrimSuffix(passedObjectStorageVerification, "}") + `,"error":"PRIVATE_ERROR"}`, "check_failed", new(int)},
		{"missing exit code", passedObjectStorageVerification, "check_failed", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := normalizeBindingVerification(state.BindingVerificationTask{Pin: state.BindingVerificationPin{Type: api.BindingTypeObjectStorage, Binding: "ASSETS"}, Status: "succeeded", Stdout: tc.stdout, ExitCode: tc.exit})
			raw, _ := json.Marshal(result)
			if result.Result == "passed" || result.Reason != tc.reason || strings.Contains(string(raw), "PRIVATE_") {
				t.Fatalf("unsafe report=%s", raw)
			}
		})
	}
}

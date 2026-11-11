// adr: 20261103222927
package mcpagent_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAppResourceInventoryMemory(t *testing.T) {
	appResourceInventoryContract(t, state.NewMemStore())
}
func TestAppResourceInventoryPostgres(t *testing.T) {
	appResourceInventoryContract(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}

func appResourceInventoryContract(t *testing.T, store state.Store) {
	t.Helper()
	account, app := newBatchApp(t, store)
	neighbor, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "neighbor-" + uuid.NewString()[:8], Type: state.AppTypeApp, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"b-" + app.Slug + ".test", "a-" + app.Slug + ".test"} {
		if _, err := store.CreateCustomDomain(t.Context(), name, app.ID, "private-challenge-value"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.CreateCustomDomain(t.Context(), "aaa-neighbor-"+app.Slug+".test", neighbor.ID, "private-neighbor-challenge"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/tick", "/other"} {
		if _, err := store.CreateCron(t.Context(), app.ID, "*/5 * * * *", path, true); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.CreateCron(t.Context(), neighbor.ID, "*/5 * * * *", "/private", true); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ appID, name, scope string }{{app.ID, "b-assets", "default"}, {app.ID, "a-assets", "default"}, {app.ID, "aaa-other-scope", "staging"}, {neighbor.ID, "aaa-neighbor", "default"}} {
		seedResourceMetadataBucket(t, store, account.ID, tc.appID, tc.name, tc.scope)
	}
	reader := store.(state.AppResourceInventoryReader)
	for _, kind := range []string{"domains", "crons", "buckets", "storage_bindings"} {
		t.Run(kind, func(t *testing.T) {
			seen := map[string]bool{}
			for _, offset := range []int{0, 1, 9} {
				q := state.AppResourceInventoryRequest{AccountID: account.ID, AppID: app.ID, OrgID: app.OrgID, Scope: "default", Kind: kind, Limit: 1, Offset: offset}
				page, err := reader.ListAppResourceInventory(t.Context(), q)
				if err != nil || page.Total != 2 || page.HasMore != (offset == 0) || len(page.Items) != map[bool]int{true: 1, false: 0}[offset < 2] {
					t.Fatalf("offset %d: %+v %v", offset, page, err)
				}
				for _, item := range page.Items {
					if item.AppID != app.ID || item.Scope != "default" || seen[item.ID] {
						t.Fatal("page crossed identity/scope or repeated an item")
					}
					seen[item.ID] = true
				}
				raw, _ := json.Marshal(page.Items)
				for _, secret := range []string{"private-", "aaa-neighbor", "aaa-other-scope", "access_key_id", "secret_sealed", "credential_ref", "physical_name", "backend_fingerprint", "provider_resource_id"} {
					if strings.Contains(string(raw), secret) {
						t.Fatalf("metadata exposed %q", secret)
					}
				}
			}
			if len(seen) != 2 {
				t.Fatal("pagination lost a resource")
			}
		})
	}
	page, err := reader.ListAppResourceInventory(t.Context(), state.AppResourceInventoryRequest{AccountID: account.ID, AppID: app.ID, OrgID: app.OrgID, Scope: "staging", Kind: "buckets", Limit: 1})
	if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].Scope != "staging" {
		t.Fatalf("selected environment: %+v %v", page, err)
	}
}

func seedResourceMetadataBucket(t *testing.T, store state.Store, accountID, appID, name, scope string) {
	t.Helper()
	buckets := store.(state.ObjectBucketStore)
	bucket, err := buckets.ReserveObjectBucket(t.Context(), state.ObjectBucket{ID: uuid.NewString(), AccountID: accountID, AppID: appID, Name: name, Scope: scope, Region: "eu", BackendID: "test", BackendFingerprint: strings.Repeat("a", 64), PhysicalName: "private-physical-" + uuid.NewString()}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := buckets.ClaimObjectBucket(t.Context(), accountID, appID, bucket.ID, "metadata-seed", "provisioning"); err != nil {
		t.Fatal(err)
	}
	if err := buckets.FinishObjectBucket(t.Context(), bucket.ID, "metadata-seed", "ready"); err != nil {
		t.Fatal(err)
	}
	access, secret, err := api.GenerateObjectS3Credential()
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	prefix := "GREGALE_S3_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
	request := state.ObjectS3ComputeBindingCreateRequest{Credential: state.ObjectS3Credential{ID: id, AccountID: accountID, BucketID: bucket.ID, AccessKeyID: access, SecretSealed: []byte(secret), KID: "age1test", Label: "compute", Permission: "read", Status: "active", ManagedAppID: appID, ManagedScope: scope, ManagedPrefix: prefix}, MaxCredentialsPerBucket: 10, MaxSecretsPerApp: 40}
	for _, suffix := range []string{"_ENDPOINT", "_REGION", "_BUCKET", "_ACCESS_KEY_ID", "_SECRET_ACCESS_KEY", "_ADDRESSING_STYLE"} {
		request.Secrets = append(request.Secrets, state.AppSecret{AccountID: accountID, AppID: appID, Scope: scope, Key: prefix + suffix, Ciphertext: []byte("private-sealed-value"), Kid: "age1test", ValueHash: "0123456789abcdef", ManagedObjectStorageCredentialID: id})
	}
	if _, err := store.(state.ObjectS3CredentialBindingStore).CreateObjectS3ComputeBinding(t.Context(), request); err != nil {
		t.Fatal(err)
	}
}

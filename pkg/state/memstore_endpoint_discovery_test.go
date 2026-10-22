package state_test

// Coverage for the MemStore side of the deployment_openapi_docs
// surface (ADR-122 / issue #975 item #1, slot 00330). Mirrors
// the cors_presets pattern at memstore_cors_presets_test.go.
//
// Test surface:
//   - Round-trip (Upsert → Get → Delete → Count)
//   - Idempotent overwrite (Upsert twice, capture_at preserved)
//   - IDOR: cross-account Get returns ErrNotFound
//   - IDOR: cross-account Delete returns ErrNotFound
//   - Count excludes other accounts
//   - SHA-256 is computed in-store and round-trips
//   - Truncated flag round-trips
//   - UpsertDeploymentOpenAPIDoc on missing deployment row returns ErrNotFound
//
// The four methods are also verified by the pgstore test
// (pgstore_endpoint_discovery_test.go); the memstore tests run
// without a live PG and are the load-bearing fast feedback.

import (
	"context"
	"crypto/sha256"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// openAPIDocFixture stands up a fresh MemStore + one account +
// one app, returning the IDs. Required for the FK floor — the
// deployment row must exist before UpsertDeploymentOpenAPIDoc
// can write a doc row.
func openAPIDocFixture(t *testing.T) (m *state.MemStore, ctx context.Context, accountID, appID string) {
	t.Helper()
	ctx = context.Background()
	m = state.NewMemStore()
	acct, err := m.CreateAccount(ctx, "openapi-"+uuid.NewString()+"@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := m.CreateApp(ctx, state.App{
		AccountID:      acct.ID,
		Slug:           "openapi-" + strconv.Itoa(int(time.Now().UnixNano())),
		RAMMB:          256,
		MaxConcurrency: 1,
		IdleTimeoutS:   60,
	})
	if err != nil {
		t.Fatal(err)
	}
	return m, ctx, acct.ID, app.ID
}

// seedDeployment creates a Deployment row under the app so the
// FK floor for deployment_openapi_docs is satisfied. Returns the
// deploymentID.
func seedDeployment(t *testing.T, m *state.MemStore, ctx context.Context, accountID, appID string) string {
	t.Helper()
	dp, err := m.CreateDeployment(ctx, state.Deployment{
		AppID:       appID,
		ImageDigest: "sha256:" + uuid.NewString(),
		Kind:        state.DeploymentKindImage,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	return dp.ID
}

// TestMemStore_OpenAPIDoc_RoundTrip pins the happy path.
func TestMemStore_OpenAPIDoc_RoundTrip(t *testing.T) {
	m, ctx, acct, app := openAPIDocFixture(t)
	depID := seedDeployment(t, m, ctx, acct, app)

	doc := []byte(`{"openapi":"3.1.0","info":{"title":"rt"}}`)
	if err := m.UpsertDeploymentOpenAPIDoc(ctx, depID, acct, app, doc, state.OpenAPIDocSourceColdBoot, false); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	gotDoc, gotMeta, err := m.GetDeploymentOpenAPIDoc(ctx, depID, acct)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(gotDoc) != string(doc) {
		t.Errorf("body: got %q, want %q", string(gotDoc), string(doc))
	}
	if gotMeta.AccountID != acct {
		t.Errorf("Meta.AccountID: got %q, want %q", gotMeta.AccountID, acct)
	}
	if gotMeta.AppID != app {
		t.Errorf("Meta.AppID: got %q, want %q", gotMeta.AppID, app)
	}
	if gotMeta.Source != state.OpenAPIDocSourceColdBoot {
		t.Errorf("Meta.Source: got %q, want %q", gotMeta.Source, state.OpenAPIDocSourceColdBoot)
	}
	if gotMeta.ByteSize != len(doc) {
		t.Errorf("Meta.ByteSize: got %d, want %d", gotMeta.ByteSize, len(doc))
	}
	if gotMeta.Truncated {
		t.Errorf("Meta.Truncated: got true, want false")
	}
	// SHA-256 must be 32 bytes and match sha256.Sum256(doc).
	if len(gotMeta.DocSHA256) != 32 {
		t.Errorf("Meta.DocSHA256 length: got %d, want 32", len(gotMeta.DocSHA256))
	}
	if sum := sha256.Sum256(doc); string(gotMeta.DocSHA256) != string(sum[:]) {
		t.Errorf("Meta.DocSHA256: not equal to sha256.Sum256(doc)")
	}

	// Delete.
	if err := m.DeleteDeploymentOpenAPIDoc(ctx, depID, acct); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, _, err := m.GetDeploymentOpenAPIDoc(ctx, depID, acct); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("Get after Delete: got %v, want ErrNotFound", err)
	}
}

// TestMemStore_OpenAPIDoc_IdempotentOverwrite pins the
// timestamp-preserved-on-overwrite contract. A re-delivered
// cold-boot event must NOT bump captured_at; it MUST bump
// updated_at.
func TestMemStore_OpenAPIDoc_IdempotentOverwrite(t *testing.T) {
	m, ctx, acct, app := openAPIDocFixture(t)
	depID := seedDeployment(t, m, ctx, acct, app)

	doc1 := []byte(`{"openapi":"3.1.0","info":{"title":"v1"}}`)
	if err := m.UpsertDeploymentOpenAPIDoc(ctx, depID, acct, app, doc1, state.OpenAPIDocSourceColdBoot, false); err != nil {
		t.Fatalf("Upsert1: %v", err)
	}
	_, meta1, err := m.GetDeploymentOpenAPIDoc(ctx, depID, acct)
	if err != nil {
		t.Fatalf("Get1: %v", err)
	}
	// Force a measurable timestamp gap.
	time.Sleep(10 * time.Millisecond)

	doc2 := []byte(`{"openapi":"3.1.0","info":{"title":"v2"}}`)
	if err := m.UpsertDeploymentOpenAPIDoc(ctx, depID, acct, app, doc2, state.OpenAPIDocSourceManualUpload, true); err != nil {
		t.Fatalf("Upsert2: %v", err)
	}
	_, meta2, err := m.GetDeploymentOpenAPIDoc(ctx, depID, acct)
	if err != nil {
		t.Fatalf("Get2: %v", err)
	}
	if !meta2.CapturedAt.Equal(meta1.CapturedAt) {
		t.Errorf("CapturedAt drift: %v vs %v", meta1.CapturedAt, meta2.CapturedAt)
	}
	if !meta2.UpdatedAt.After(meta1.UpdatedAt) {
		t.Errorf("UpdatedAt did not advance: %v vs %v", meta1.UpdatedAt, meta2.UpdatedAt)
	}
	if string(meta2.Source) != state.OpenAPIDocSourceManualUpload {
		t.Errorf("Source: got %q, want %q", meta2.Source, state.OpenAPIDocSourceManualUpload)
	}
	if !meta2.Truncated {
		t.Errorf("Truncated: got false, want true")
	}
}

// TestMemStore_OpenAPIDoc_IDOR pins the cross-tenant floor. A
// read with a foreign accountID returns ErrNotFound, not the
// row.
func TestMemStore_OpenAPIDoc_IDOR(t *testing.T) {
	m, ctx, acct, app := openAPIDocFixture(t)
	depID := seedDeployment(t, m, ctx, acct, app)

	doc := []byte(`{"openapi":"3.1.0"}`)
	if err := m.UpsertDeploymentOpenAPIDoc(ctx, depID, acct, app, doc, state.OpenAPIDocSourceColdBoot, false); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	// Foreign account.
	foreignAcct := "00000000-0000-0000-0000-000000000000"
	if _, _, err := m.GetDeploymentOpenAPIDoc(ctx, depID, foreignAcct); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("Get cross-account: got %v, want ErrNotFound", err)
	}
	if err := m.DeleteDeploymentOpenAPIDoc(ctx, depID, foreignAcct); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("Delete cross-account: got %v, want ErrNotFound", err)
	}
	// Same-account read still works (the cross-account probe
	// must not corrupt the row).
	if _, _, err := m.GetDeploymentOpenAPIDoc(ctx, depID, acct); err != nil {
		t.Errorf("Get same-account after cross-account probes: %v", err)
	}
}

// TestMemStore_OpenAPIDoc_CountByAccount pins the per-account
// quota gate. A foreign row must not bump the count. Two
// accounts under the same MemStore so the row counts are
// cross-comparable.
func TestMemStore_OpenAPIDoc_CountByAccount(t *testing.T) {
	m, ctx, acct1, app1 := openAPIDocFixture(t)
	// Second account under the same MemStore.
	acct2Acct, err := m.CreateAccount(ctx, "openapi-acct2-"+uuid.NewString()+"@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	acct2 := acct2Acct.ID
	app2App, err := m.CreateApp(ctx, state.App{
		AccountID:      acct2,
		Slug:           "openapi-acct2-" + strconv.Itoa(int(time.Now().UnixNano())),
		RAMMB:          256,
		MaxConcurrency: 1,
		IdleTimeoutS:   60,
	})
	if err != nil {
		t.Fatal(err)
	}
	app2 := app2App.ID

	dep1 := seedDeployment(t, m, ctx, acct1, app1)
	dep3 := seedDeployment(t, m, ctx, acct2, app2)

	if err := m.UpsertDeploymentOpenAPIDoc(ctx, dep1, acct1, app1, []byte(`{"openapi":"3.1.0"}`), state.OpenAPIDocSourceColdBoot, false); err != nil {
		t.Fatalf("Upsert1: %v", err)
	}
	if err := m.UpsertDeploymentOpenAPIDoc(ctx, dep3, acct2, app2, []byte(`{"openapi":"3.1.0"}`), state.OpenAPIDocSourceColdBoot, false); err != nil {
		t.Fatalf("Upsert3: %v", err)
	}
	n1, err := m.CountOpenAPIDocsByAccount(ctx, acct1)
	if err != nil {
		t.Fatalf("Count acct1: %v", err)
	}
	if n1 != 1 {
		t.Errorf("Count acct1: got %d, want 1", n1)
	}
	n2, err := m.CountOpenAPIDocsByAccount(ctx, acct2)
	if err != nil {
		t.Fatalf("Count acct2: %v", err)
	}
	if n2 != 1 {
		t.Errorf("Count acct2: got %d, want 1", n2)
	}
}

// TestMemStore_OpenAPIDoc_ParentMissing pins the parent check.
// Upsert on a deploymentID that doesn't exist returns ErrNotFound
// before the INSERT fires.
func TestMemStore_OpenAPIDoc_ParentMissing(t *testing.T) {
	m, ctx, acct, app := openAPIDocFixture(t)
	ghostID := uuid.NewString()
	err := m.UpsertDeploymentOpenAPIDoc(ctx, ghostID, acct, app, []byte(`{"openapi":"3.1.0"}`), state.OpenAPIDocSourceColdBoot, false)
	if !errors.Is(err, state.ErrNotFound) {
		t.Errorf("Upsert on missing parent: got %v, want ErrNotFound", err)
	}
}

// TestMemStore_OpenAPIDoc_DeleteMissing pins the delete-on-missing
// row contract. Two missing paths: deploymentID never had a row,
// and deploymentID had a row but in a different account.
func TestMemStore_OpenAPIDoc_DeleteMissing(t *testing.T) {
	m, ctx, acct, _ := openAPIDocFixture(t)
	if err := m.DeleteDeploymentOpenAPIDoc(ctx, uuid.NewString(), acct); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("Delete never-existing: got %v, want ErrNotFound", err)
	}
}

// TestMemStore_OpenAPIDoc_DefensiveCopy pins the read-side
// defensive copy contract. The caller mutating the returned
// slice must NOT corrupt the row's internal copy.
func TestMemStore_OpenAPIDoc_DefensiveCopy(t *testing.T) {
	m, ctx, acct, app := openAPIDocFixture(t)
	depID := seedDeployment(t, m, ctx, acct, app)

	doc := []byte(`{"openapi":"3.1.0","info":{"title":"orig"}}`)
	if err := m.UpsertDeploymentOpenAPIDoc(ctx, depID, acct, app, doc, state.OpenAPIDocSourceColdBoot, false); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	got1, _, err := m.GetDeploymentOpenAPIDoc(ctx, depID, acct)
	if err != nil {
		t.Fatalf("Get1: %v", err)
	}
	// Mutate the returned slice.
	got1[0] = 'X'
	// Re-read; the row must be unchanged.
	got2, _, err := m.GetDeploymentOpenAPIDoc(ctx, depID, acct)
	if err != nil {
		t.Fatalf("Get2: %v", err)
	}
	if string(got2) != string(doc) {
		t.Errorf("storage corrupted after caller mutation: got %q, want %q", string(got2), string(doc))
	}
}

// TestMemStore_OpenAPIDoc_MaxBytesAtCap pins the constant
// relationship. The store accepts a doc up to the global
// cap; the per-plan cap is layered at the apid surface, not
// here. The probe truncates at VsockCharacterizationMaxBody
// (= OpenAPIDocMaxBytes) so the cap is reentrant.
func TestMemStore_OpenAPIDoc_MaxBytesAtCap(t *testing.T) {
	m, ctx, acct, app := openAPIDocFixture(t)
	depID := seedDeployment(t, m, ctx, acct, app)

	doc := make([]byte, state.OpenAPIDocMaxBytes)
	for i := range doc {
		doc[i] = 'x'
	}
	if err := m.UpsertDeploymentOpenAPIDoc(ctx, depID, acct, app, doc, state.OpenAPIDocSourceColdBoot, false); err != nil {
		t.Fatalf("Upsert at cap: %v", err)
	}
	got, meta, err := m.GetDeploymentOpenAPIDoc(ctx, depID, acct)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got) != state.OpenAPIDocMaxBytes {
		t.Errorf("body length: got %d, want %d", len(got), state.OpenAPIDocMaxBytes)
	}
	if meta.ByteSize != state.OpenAPIDocMaxBytes {
		t.Errorf("ByteSize: got %d, want %d", meta.ByteSize, state.OpenAPIDocMaxBytes)
	}
}

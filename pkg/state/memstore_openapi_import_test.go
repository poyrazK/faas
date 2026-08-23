package state

// Coverage for the MemStore side of the app_openapi_docs
// surface (ADR-126 / issue #975 item #2, slot 00382). Mirrors
// the memstore_endpoint_discovery_test.go precedent from
// item #1.
//
// Test surface:
//   - Round-trip (Upsert → Get → Delete → Count)
//   - Idempotent overwrite (Upsert twice, capture_at preserved)
//   - IDOR: cross-account Get returns ErrNotFound
//   - IDOR: cross-account Delete returns ErrNotFound
//   - Count excludes other accounts
//   - SHA-256 is computed in-store and round-trips
//   - EndpointCount + OpenAPIVersion round-trip
//   - UpsertAppOpenAPIDoc on missing app row returns ErrNotFound
//
// The four methods are also verified by the pgstore test
// (pgstore_openapi_import_test.go); the memstore tests run
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
)

// openAPIImportFixture stands up a fresh MemStore + one account +
// one app, returning the IDs. Required for the FK floor — the
// app row must exist before UpsertAppOpenAPIDoc can write an
// import row.
func openAPIImportFixture(t *testing.T) (m *MemStore, ctx context.Context, accountID, appID string) {
	t.Helper()
	ctx = context.Background()
	m = NewMemStore()
	acct, err := m.CreateAccount(ctx, "oimport-"+uuid.NewString()+"@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := m.CreateApp(ctx, App{
		AccountID:      acct.ID,
		Slug:           "oimport-" + strconv.Itoa(int(time.Now().UnixNano())),
		RAMMB:          256,
		MaxConcurrency: 1,
		IdleTimeoutS:   60,
	})
	if err != nil {
		t.Fatal(err)
	}
	return m, ctx, acct.ID, app.ID
}

// TestMemStore_OpenAPIImport_RoundTrip pins the happy path.
func TestMemStore_OpenAPIImport_RoundTrip(t *testing.T) {
	m, ctx, acct, app := openAPIImportFixture(t)

	doc := []byte(`{"openapi":"3.1.0","info":{"title":"rt"},"paths":{"/foo":{"get":{}}}}`)
	if err := m.UpsertAppOpenAPIDoc(ctx, app, acct, doc, 1, "3.1.0"); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	gotDoc, gotMeta, err := m.GetAppOpenAPIDoc(ctx, app, acct)
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
	if gotMeta.Source != OpenAPIImportSourceManualImport {
		t.Errorf("Meta.Source: got %q, want %q", gotMeta.Source, OpenAPIImportSourceManualImport)
	}
	if gotMeta.OpenAPIVersion != "3.1.0" {
		t.Errorf("Meta.OpenAPIVersion: got %q, want 3.1.0", gotMeta.OpenAPIVersion)
	}
	if gotMeta.EndpointCount != 1 {
		t.Errorf("Meta.EndpointCount: got %d, want 1", gotMeta.EndpointCount)
	}
	if gotMeta.ByteSize != len(doc) {
		t.Errorf("Meta.ByteSize: got %d, want %d", gotMeta.ByteSize, len(doc))
	}
	// SHA-256 must be 32 bytes and match sha256.Sum256(doc).
	if len(gotMeta.DocSHA256) != 32 {
		t.Errorf("Meta.DocSHA256 length: got %d, want 32", len(gotMeta.DocSHA256))
	}
	if sum := sha256.Sum256(doc); string(gotMeta.DocSHA256) != string(sum[:]) {
		t.Errorf("Meta.DocSHA256: not equal to sha256.Sum256(doc)")
	}

	// Delete.
	if err := m.DeleteAppOpenAPIDoc(ctx, app, acct); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, _, err := m.GetAppOpenAPIDoc(ctx, app, acct); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after Delete: got %v, want ErrNotFound", err)
	}
}

// TestMemStore_OpenAPIImport_IdempotentOverwrite pins the
// timestamp-preserved-on-overwrite contract. A re-delivered
// import event must NOT bump captured_at; it MUST bump
// updated_at. EndpointCount + OpenAPIVersion update to the new
// values.
func TestMemStore_OpenAPIImport_IdempotentOverwrite(t *testing.T) {
	m, ctx, acct, app := openAPIImportFixture(t)

	doc1 := []byte(`{"openapi":"3.1.0","info":{"title":"v1"}}`)
	if err := m.UpsertAppOpenAPIDoc(ctx, app, acct, doc1, 0, "3.1.0"); err != nil {
		t.Fatalf("Upsert1: %v", err)
	}
	_, meta1, err := m.GetAppOpenAPIDoc(ctx, app, acct)
	if err != nil {
		t.Fatalf("Get1: %v", err)
	}
	// Force a measurable timestamp gap.
	time.Sleep(10 * time.Millisecond)

	doc2 := []byte(`{"openapi":"3.1.0","info":{"title":"v2"},"paths":{"/bar":{"get":{}}}}`)
	if err := m.UpsertAppOpenAPIDoc(ctx, app, acct, doc2, 1, "3.1.0"); err != nil {
		t.Fatalf("Upsert2: %v", err)
	}
	_, meta2, err := m.GetAppOpenAPIDoc(ctx, app, acct)
	if err != nil {
		t.Fatalf("Get2: %v", err)
	}
	if !meta2.CapturedAt.Equal(meta1.CapturedAt) {
		t.Errorf("CapturedAt drift: %v vs %v", meta1.CapturedAt, meta2.CapturedAt)
	}
	if !meta2.UpdatedAt.After(meta1.UpdatedAt) {
		t.Errorf("UpdatedAt did not advance: %v vs %v", meta1.UpdatedAt, meta2.UpdatedAt)
	}
	if meta2.EndpointCount != 1 {
		t.Errorf("EndpointCount: got %d, want 1", meta2.EndpointCount)
	}
	if meta2.OpenAPIVersion != "3.1.0" {
		t.Errorf("OpenAPIVersion: got %q, want 3.1.0", meta2.OpenAPIVersion)
	}
}

// TestMemStore_OpenAPIImport_IDOR pins the cross-tenant floor.
// A read with a foreign accountID returns ErrNotFound, not the
// row.
func TestMemStore_OpenAPIImport_IDOR(t *testing.T) {
	m, ctx, acct, app := openAPIImportFixture(t)

	doc := []byte(`{"openapi":"3.1.0"}`)
	if err := m.UpsertAppOpenAPIDoc(ctx, app, acct, doc, 0, "3.1.0"); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	// Foreign account.
	foreignAcct := "00000000-0000-0000-0000-000000000000"
	if _, _, err := m.GetAppOpenAPIDoc(ctx, app, foreignAcct); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get cross-account: got %v, want ErrNotFound", err)
	}
	if err := m.DeleteAppOpenAPIDoc(ctx, app, foreignAcct); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete cross-account: got %v, want ErrNotFound", err)
	}
	// Same-account read still works (the cross-account probe
	// must not corrupt the row).
	if _, _, err := m.GetAppOpenAPIDoc(ctx, app, acct); err != nil {
		t.Errorf("Get same-account after cross-account probes: %v", err)
	}
}

// TestMemStore_OpenAPIImport_CountByAccount pins the per-account
// quota gate. A foreign row must not bump the count. Two
// accounts under the same MemStore so the row counts are
// cross-comparable.
func TestMemStore_OpenAPIImport_CountByAccount(t *testing.T) {
	m, ctx, acct1, app1 := openAPIImportFixture(t)
	// Second account under the same MemStore.
	acct2Acct, err := m.CreateAccount(ctx, "oimport-acct2-"+uuid.NewString()+"@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	acct2 := acct2Acct.ID
	app2App, err := m.CreateApp(ctx, App{
		AccountID:      acct2,
		Slug:           "oimport-acct2-" + strconv.Itoa(int(time.Now().UnixNano())),
		RAMMB:          256,
		MaxConcurrency: 1,
		IdleTimeoutS:   60,
	})
	if err != nil {
		t.Fatal(err)
	}
	app2 := app2App.ID

	if err := m.UpsertAppOpenAPIDoc(ctx, app1, acct1, []byte(`{"openapi":"3.1.0"}`), 0, "3.1.0"); err != nil {
		t.Fatalf("Upsert1: %v", err)
	}
	if err := m.UpsertAppOpenAPIDoc(ctx, app2, acct2, []byte(`{"openapi":"3.1.0"}`), 0, "3.1.0"); err != nil {
		t.Fatalf("Upsert2: %v", err)
	}
	n1, err := m.CountOpenAPIImportsByAccount(ctx, acct1)
	if err != nil {
		t.Fatalf("Count acct1: %v", err)
	}
	if n1 != 1 {
		t.Errorf("Count acct1: got %d, want 1", n1)
	}
	n2, err := m.CountOpenAPIImportsByAccount(ctx, acct2)
	if err != nil {
		t.Fatalf("Count acct2: %v", err)
	}
	if n2 != 1 {
		t.Errorf("Count acct2: got %d, want 1", n2)
	}
}

// TestMemStore_OpenAPIImport_ParentMissing pins the parent
// check. Upsert on an appID that doesn't exist returns
// ErrNotFound before the INSERT fires.
func TestMemStore_OpenAPIImport_ParentMissing(t *testing.T) {
	m, ctx, acct, _ := openAPIImportFixture(t)
	ghostID := uuid.NewString()
	err := m.UpsertAppOpenAPIDoc(ctx, ghostID, acct, []byte(`{"openapi":"3.1.0"}`), 0, "3.1.0")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Upsert on missing parent: got %v, want ErrNotFound", err)
	}
}

// TestMemStore_OpenAPIImport_DeleteMissing pins the
// delete-on-missing row contract.
func TestMemStore_OpenAPIImport_DeleteMissing(t *testing.T) {
	m, ctx, acct, _ := openAPIImportFixture(t)
	if err := m.DeleteAppOpenAPIDoc(ctx, uuid.NewString(), acct); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete never-existing: got %v, want ErrNotFound", err)
	}
}

// TestMemStore_OpenAPIImport_DefensiveCopy pins the read-side
// defensive copy contract. The caller mutating the returned
// slice must NOT corrupt the row's internal copy.
func TestMemStore_OpenAPIImport_DefensiveCopy(t *testing.T) {
	m, ctx, acct, app := openAPIImportFixture(t)

	doc := []byte(`{"openapi":"3.1.0","info":{"title":"orig"}}`)
	if err := m.UpsertAppOpenAPIDoc(ctx, app, acct, doc, 0, "3.1.0"); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	got1, _, err := m.GetAppOpenAPIDoc(ctx, app, acct)
	if err != nil {
		t.Fatalf("Get1: %v", err)
	}
	// Mutate the returned slice.
	got1[0] = 'X'
	// Re-read; the row must be unchanged.
	got2, _, err := m.GetAppOpenAPIDoc(ctx, app, acct)
	if err != nil {
		t.Fatalf("Get2: %v", err)
	}
	if string(got2) != string(doc) {
		t.Errorf("storage corrupted after caller mutation: got %q, want %q", string(got2), string(doc))
	}
}

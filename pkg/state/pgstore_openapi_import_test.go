package state_test

// Round-trip tests for the app_openapi_docs Store surface
// (ADR-126 / issue #975 item #2, slot 00378). Exercises the four
// methods — Get / Upsert / Delete / Count — plus the load-bearing
// IDOR guard: a cross-tenant read returns ErrNotFound, not the
// row.
//
// Mirrors the pgstore_endpoint_discovery_test.go pattern from
// item #1. The (app_id, account_id) WHERE clause, the closed-set
// source + openapi_version CHECKs, and the IDOR predicates are
// all pinned — a silent weakening here lets a foreign tenant
// enumerate another tenant's imports.
//
// Insert path uses the Store.UpsertAppOpenAPIDoc method (not raw
// SQL) so a regression in the pg path's INSERT (column order,
// NULL coercion, sha256 length, source enum, version enum) fails
// the test, not a follow-up real call.

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// seedOpenAPIImportPg inserts a fresh account + app via the Store
// path. Returns the IDs. Required because app_openapi_docs FKs
// point at apps + accounts — a pgstore test that synthesises
// UUIDs out of thin air will hit SQLSTATE 23503 on the first
// UpsertAppOpenAPIDoc. Mirrors seedOpenAPIDocFixture but stops at
// the app layer (no deployment needed for the per-app surface).
func seedOpenAPIImportFixture(t *testing.T, ctx context.Context, st state.Store) (string, string) {
	t.Helper()
	email := fmt.Sprintf("oimporttest+%s@example.com", strings.ReplaceAll(t.Name(), "/", "-"))
	acct, err := st.CreateAccount(ctx, email, api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := st.CreateApp(ctx, state.App{
		AccountID: acct.ID, Slug: fmt.Sprintf("oimport-%s", strings.ReplaceAll(t.Name(), "/", "-")),
		Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 2, IdleTimeoutS: 60,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	return acct.ID, app.ID
}

func TestPgStoreOpenAPIImport_RoundTrip(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)

	accountID, appID := seedOpenAPIImportFixture(t, ctx, store)

	doc := []byte(`{"openapi":"3.1.0","info":{"title":"rt"},"paths":{"/foo":{"get":{}}}}`)
	if err := store.UpsertAppOpenAPIDoc(ctx, appID, accountID, doc, 1, "3.1.0"); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	gotDoc, gotMeta, err := store.GetAppOpenAPIDoc(ctx, appID, accountID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(gotDoc) != string(doc) {
		// Postgres re-serialises JSONB with whitespace; compare
		// semantically via json.Unmarshal both sides into map[string]any.
		var wantMap, gotMap map[string]any
		if jerr1 := json.Unmarshal(doc, &wantMap); jerr1 != nil {
			t.Errorf("body: byte-equal mismatch %q vs %q, and input did not parse as JSON: %v",
				string(gotDoc), string(doc), jerr1)
		} else if jerr2 := json.Unmarshal(gotDoc, &gotMap); jerr2 != nil {
			t.Errorf("body: byte-equal mismatch %q vs %q, and Postgres output did not parse as JSON: %v",
				string(gotDoc), string(doc), jerr2)
		} else if !reflect.DeepEqual(gotMap, wantMap) {
			t.Errorf("body: semantic mismatch after Postgres JSONB re-serialisation:\n got=%#v\nwant=%#v",
				gotMap, wantMap)
		}
	}
	if gotMeta.AccountID != accountID {
		t.Errorf("Meta.AccountID: got %q, want %q", gotMeta.AccountID, accountID)
	}
	if gotMeta.AppID != appID {
		t.Errorf("Meta.AppID: got %q, want %q", gotMeta.AppID, appID)
	}
	if gotMeta.Source != state.OpenAPIImportSourceManualImport {
		t.Errorf("Meta.Source: got %q, want %q", gotMeta.Source, state.OpenAPIImportSourceManualImport)
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
	if len(gotMeta.DocSHA256) != 32 {
		t.Errorf("Meta.DocSHA256 length: got %d, want 32", len(gotMeta.DocSHA256))
	}
	if sum := sha256.Sum256(doc); string(gotMeta.DocSHA256) != string(sum[:]) {
		t.Errorf("Meta.DocSHA256: not equal to sha256.Sum256(doc)")
	}

	// Delete.
	if err := store.DeleteAppOpenAPIDoc(ctx, appID, accountID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, _, err := store.GetAppOpenAPIDoc(ctx, appID, accountID); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("Get after Delete: got %v, want ErrNotFound", err)
	}
}

// TestPgStoreOpenAPIImport_IdempotentOverwrite pins the
// timestamp-preserved-on-overwrite contract.
func TestPgStoreOpenAPIImport_IdempotentOverwrite(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	accountID, appID := seedOpenAPIImportFixture(t, ctx, store)

	doc1 := []byte(`{"openapi":"3.1.0","info":{"title":"v1"}}`)
	if err := store.UpsertAppOpenAPIDoc(ctx, appID, accountID, doc1, 0, "3.1.0"); err != nil {
		t.Fatalf("Upsert1: %v", err)
	}
	_, meta1, err := store.GetAppOpenAPIDoc(ctx, appID, accountID)
	if err != nil {
		t.Fatalf("Get1: %v", err)
	}

	doc2 := []byte(`{"openapi":"3.1.0","info":{"title":"v2"},"paths":{"/bar":{"get":{}}}}`)
	if err := store.UpsertAppOpenAPIDoc(ctx, appID, accountID, doc2, 1, "3.1.0"); err != nil {
		t.Fatalf("Upsert2: %v", err)
	}
	_, meta2, err := store.GetAppOpenAPIDoc(ctx, appID, accountID)
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
		t.Errorf("EndpointCount after overwrite: got %d, want 1", meta2.EndpointCount)
	}
}

// TestPgStoreOpenAPIImport_IDOR pins the cross-tenant floor. A
// read with a foreign accountID returns ErrNotFound, not the row.
func TestPgStoreOpenAPIImport_IDOR(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	accountID, appID := seedOpenAPIImportFixture(t, ctx, store)

	doc := []byte(`{"openapi":"3.1.0"}`)
	if err := store.UpsertAppOpenAPIDoc(ctx, appID, accountID, doc, 0, "3.1.0"); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	// Foreign account.
	foreignAcct := uuid.NewString()
	if _, _, err := store.GetAppOpenAPIDoc(ctx, appID, foreignAcct); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("Get cross-account: got %v, want ErrNotFound", err)
	}
	if err := store.DeleteAppOpenAPIDoc(ctx, appID, foreignAcct); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("Delete cross-account: got %v, want ErrNotFound", err)
	}
	// Same-account read still works.
	if _, _, err := store.GetAppOpenAPIDoc(ctx, appID, accountID); err != nil {
		t.Errorf("Get same-account after cross-account probes: %v", err)
	}
}

// TestPgStoreOpenAPIImport_CountByAccount pins the per-account
// quota gate. A foreign row must not bump the count.
func TestPgStoreOpenAPIImport_CountByAccount(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	acct1, app1 := seedOpenAPIImportFixture(t, ctx, store)
	// Second account under the same store. Different slug prefix
	// so the unique slug constraint is satisfied.
	email2 := fmt.Sprintf("oimport-acct2-%s@example.com", strings.ReplaceAll(t.Name(), "/", "-"))
	acct2, err := store.CreateAccount(ctx, email2, api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount acct2: %v", err)
	}
	app2, err := store.CreateApp(ctx, state.App{
		AccountID:      acct2.ID,
		Slug:           fmt.Sprintf("oimport-acct2-%s", strings.ReplaceAll(t.Name(), "/", "-")),
		Type:           state.AppTypeApp,
		RAMMB:          256,
		MaxConcurrency: 2,
		IdleTimeoutS:   60,
	})
	if err != nil {
		t.Fatalf("CreateApp acct2: %v", err)
	}

	if err := store.UpsertAppOpenAPIDoc(ctx, app1, acct1, []byte(`{"openapi":"3.1.0"}`), 0, "3.1.0"); err != nil {
		t.Fatalf("Upsert1: %v", err)
	}
	if err := store.UpsertAppOpenAPIDoc(ctx, app2.ID, acct2.ID, []byte(`{"openapi":"3.1.0"}`), 0, "3.1.0"); err != nil {
		t.Fatalf("Upsert2: %v", err)
	}
	n1, err := store.CountOpenAPIImportsByAccount(ctx, acct1)
	if err != nil {
		t.Fatalf("Count acct1: %v", err)
	}
	if n1 != 1 {
		t.Errorf("Count acct1: got %d, want 1", n1)
	}
	n2, err := store.CountOpenAPIImportsByAccount(ctx, acct2.ID)
	if err != nil {
		t.Fatalf("Count acct2: %v", err)
	}
	if n2 != 1 {
		t.Errorf("Count acct2: got %d, want 1", n2)
	}
}

// TestPgStoreOpenAPIImport_ParentMissing pins the parent check.
// Upsert on an appID that doesn't exist returns ErrNotFound
// before the INSERT fires.
func TestPgStoreOpenAPIImport_ParentMissing(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	acct, _ := seedOpenAPIImportFixture(t, ctx, store)
	ghostID := uuid.NewString()
	err := store.UpsertAppOpenAPIDoc(ctx, ghostID, acct, []byte(`{"openapi":"3.1.0"}`), 0, "3.1.0")
	if !errors.Is(err, state.ErrNotFound) {
		t.Errorf("Upsert on missing parent: got %v, want ErrNotFound", err)
	}
}

// TestPgStoreOpenAPIImport_DeleteMissing pins the
// delete-on-missing row contract.
func TestPgStoreOpenAPIImport_DeleteMissing(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	acct, _ := seedOpenAPIImportFixture(t, ctx, store)
	if err := store.DeleteAppOpenAPIDoc(ctx, uuid.NewString(), acct); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("Delete never-existing: got %v, want ErrNotFound", err)
	}
}

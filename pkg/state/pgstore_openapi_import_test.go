package state_test

// Round-trip tests for the app_openapi_docs Store surface
// (ADR-126 / issue #975 item #2, slot 00416). Exercises the four
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

// TestPgStoreOpenAPIImport_UpsertForeignAccountID pins the
// store-layer IDOR defence-in-depth (per CLAUDE.md "IDOR floor
// at SQL WHERE clause"). UpsertAppOpenAPIDoc with a caller-
// supplied accountID that doesn't own the parent app must
// return ErrNotFound — the apid handler's loadApp already
// gates this, but the store must enforce it too so a future
// caller that bypasses the handler (admin tool, test
// harness, internal API) can't write a row into a foreign
// tenant's app_id and then read it back via GetAppOpenAPIDoc
// (the WHERE clause matches the row's frozen account_id).
func TestPgStoreOpenAPIImport_UpsertForeignAccountID(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	_, appID := seedOpenAPIImportFixture(t, ctx, store)
	foreignAcct := uuid.NewString()
	err := store.UpsertAppOpenAPIDoc(ctx, appID, foreignAcct, []byte(`{"openapi":"3.1.0"}`), 0, "3.1.0")
	if !errors.Is(err, state.ErrNotFound) {
		t.Errorf("Upsert on foreign account: got %v, want ErrNotFound", err)
	}
	// Sanity: the foreign account must NOT see any row.
	if _, _, err := store.GetAppOpenAPIDoc(ctx, appID, foreignAcct); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("Get on foreign account after rejected upsert: got %v, want ErrNotFound", err)
	}
}

// TestPgStoreOpenAPIImport_IfUnderQuota exercises the three branches
// of UpsertAppOpenAPIDocIfUnderQuota (issue #975 item #2 / ADR-126):
//
//  1. planMax <= 0  — fail-closed: QuotaError with NotAllowed=true,
//     before any tx work (no FOR UPDATE on the account row).
//  2. observed >= planMax — quota-exceeded branch: a second upsert
//     after the first row lands must return *QuotaError
//     {Kind: openapi_imports, Limit: planMax, Observed: planMax,
//     NotAllowed: false}. Pins the FOR UPDATE / count predicate.
//  3. happy path — under quota, with valid (app, account) ownership:
//     INSERT lands; subsequent Get reads back the row.
//
// Branch 4 (missing account or app) is implicitly covered by the
// IDOR test above (foreignAcct trip into ErrNotFound), so this
// function pins the quota branches specifically.
func TestPgStoreOpenAPIImport_IfUnderQuota(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	accountID, appID := seedOpenAPIImportFixture(t, ctx, store)
	doc := []byte(`{"openapi":"3.1.0","info":{"title":"quota"}}`)

	// --- Branch 1: planMax <= 0 — fail-closed path. The handler
	// resolves this when the resolved plan tier lacks an
	// OpenAPIImportsPerAccount cap (or a tier-down set 0). The
	// function rejects BEFORE the tx begin, so no account row
	// lock fires — coverage of the early-return block.
	for _, planMax := range []int{0, -1} {
		err := store.UpsertAppOpenAPIDocIfUnderQuota(ctx, appID, accountID, doc, 1, "3.1.0", planMax)
		if err == nil {
			t.Fatalf("planMax=%d: expected QuotaError(NotAllowed=true), got nil", planMax)
		}
		var qe *state.QuotaError
		if !errors.As(err, &qe) {
			t.Fatalf("planMax=%d: expected *QuotaError, got %T: %v", planMax, err, err)
		}
		if qe.Kind != state.QuotaErrorKindOpenAPIImports {
			t.Errorf("planMax=%d: Kind = %q, want %q", planMax, qe.Kind, state.QuotaErrorKindOpenAPIImports)
		}
		if !qe.NotAllowed {
			t.Errorf("planMax=%d: NotAllowed = false, want true (fail-closed path)", planMax)
		}
		if qe.Limit != planMax {
			t.Errorf("planMax=%d: Limit = %d, want %d", planMax, qe.Limit, planMax)
		}
	}

	// --- Branch 3: happy path at planMax=1 — first upsert lands.
	if err := store.UpsertAppOpenAPIDocIfUnderQuota(ctx, appID, accountID, doc, 1, "3.1.0", 1); err != nil {
		t.Fatalf("first upsert at planMax=1: %v", err)
	}

	// An existing document reuses its slot, including at the quota boundary.
	if err := store.UpsertAppOpenAPIDocIfUnderQuota(ctx, appID, accountID, doc, 1, "3.1.0", 1); err != nil {
		t.Fatalf("replacement at planMax=1: %v", err)
	}
	other, err := store.CreateApp(ctx, state.App{AccountID: accountID, Slug: "import-quota-other", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	// A different app still needs another slot and must refuse at the cap.
	err = store.UpsertAppOpenAPIDocIfUnderQuota(ctx, other.ID, accountID, doc, 1, "3.1.0", 1)
	if err == nil {
		t.Fatal("second upsert at planMax=1: expected QuotaError, got nil")
	}
	var qe *state.QuotaError
	if !errors.As(err, &qe) {
		t.Fatalf("second upsert: expected *QuotaError, got %T: %v", err, err)
	}
	if qe.Kind != state.QuotaErrorKindOpenAPIImports {
		t.Errorf("Kind = %q, want %q", qe.Kind, state.QuotaErrorKindOpenAPIImports)
	}
	if qe.Limit != 1 {
		t.Errorf("Limit = %d, want 1", qe.Limit)
	}
	if qe.Observed != 1 {
		t.Errorf("Observed = %d, want 1", qe.Observed)
	}
	if qe.NotAllowed {
		t.Errorf("NotAllowed = true on observed>=planMax path, want false")
	}

	// --- Branch 4: missing parent (app belongs to a different
	// account) — the IDOR floor must reject with ErrNotFound even
	// when planMax is generous. Pins the parent-exists SELECT.
	foreignAcct := uuid.NewString()
	if err := store.UpsertAppOpenAPIDocIfUnderQuota(ctx, appID, foreignAcct, doc, 1, "3.1.0", 5); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("foreign-account upsert: got %v, want ErrNotFound", err)
	}

	// --- Sanity: the happy-path row is still readable (the
	// quota-rejected upsert didn't roll back the original tx — it
	// returned before the commit, so the row from branch 3 stays).
	gotDoc, gotMeta, err := store.GetAppOpenAPIDoc(ctx, appID, accountID)
	if err != nil {
		t.Fatalf("post-quota Get: %v", err)
	}
	if gotMeta.EndpointCount != 1 {
		t.Errorf("post-quota EndpointCount = %d, want 1", gotMeta.EndpointCount)
	}
	if len(gotDoc) == 0 {
		t.Errorf("post-quota doc body is empty")
	}
}

// TestPgStoreOpenAPIImport_UpsertIfUnderQuota pins the quota-bundled
// happy + sad paths on the bundle+lock+upsert surface (pgstore_
// openapi_import.go::UpsertAppOpenAPIDocIfUnderQuota). PlanMax ≤ 0
// returns QuotaErrorKindOpenAPIImports; PlanMax ≥ 1 succeeds and the
// row is readable via GetAppOpenAPIDoc. Coverage-bump: this bundle
// function had 0% coverage at round-6 rebase, dropping the pkg/state
// floor below 70%. Zero-source-change coverage test.
func TestPgStoreOpenAPIImport_UpsertIfUnderQuota(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	acct, appID := seedOpenAPIImportFixture(t, ctx, store)

	// Sad path: planMax ≤ 0 ⇒ QuotaErrorKindOpenAPIImports, NotAllowed.
	if err := store.UpsertAppOpenAPIDocIfUnderQuota(ctx, appID, acct, []byte(`{"openapi":"3.1.0"}`), 1, "3.1.0", 0); err == nil {
		t.Fatal("planMax=0: want QuotaError, got nil")
	} else {
		var qe *state.QuotaError
		if !errors.As(err, &qe) {
			t.Fatalf("planMax=0: want QuotaError, got %T %v", err, err)
		} else if qe.Kind != state.QuotaErrorKindOpenAPIImports {
			t.Errorf("planMax=0: Kind = %v, want QuotaErrorKindOpenAPIImports", qe.Kind)
		}
	}

	// Happy path: planMax = 5 (Hobby), endpointCount = 1. The row
	// becomes readable via the store's read path so we know the
	// bundle transaction committed.
	if err := store.UpsertAppOpenAPIDocIfUnderQuota(ctx, appID, acct, []byte(`{"openapi":"3.1.0","paths":{}}`), 1, "3.1.0", 5); err != nil {
		t.Fatalf("UpsertAppOpenAPIDocIfUnderQuota happy: %v", err)
	}
	if doc, _, err := store.GetAppOpenAPIDoc(ctx, appID, acct); err != nil {
		t.Fatalf("GetAppOpenAPIDoc after quota upsert: %v", err)
	} else if len(doc) == 0 {
		t.Fatal("GetAppOpenAPIDoc returned empty doc after quota upsert")
	}
}

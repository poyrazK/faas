package state_test

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 400
func TestObjectVersionReferencesMem(t *testing.T) {
	objectVersionReferencesSuite(t, state.NewMemStore())
}
func TestObjectVersionReferencesPG(t *testing.T) {
	st, _ := pgStore(t)
	objectVersionReferencesSuite(t, st)
}

func objectVersionReferencesSuite(t *testing.T, st accountingStore) {
	ctx := t.Context()
	b, _ := seedAccounting(t, st)
	other, _ := seedAccounting(t, st)
	refs := st.(state.ObjectVersionReferenceStore)
	items := []state.ObjectVersionIdentity{{Key: "目录 /+%.txt", ProviderVersionID: "private/+%version"}, {Key: "other-key", ProviderVersionID: "private/+%version"}, {Key: "null-key", ProviderVersionID: "null"}}
	out, err := refs.RecordObjectVersions(ctx, b.AccountID, b.ID, items)
	if err != nil || len(out) != 3 || !state.ValidObjectVersionID(out[0].ID) || out[0].ID == out[1].ID || out[2].ID != "null" {
		t.Fatal(out, err)
	}
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), "private") {
		t.Fatal("native identity serialized", string(raw))
	}
	for _, tc := range []struct{ account, bucket, key, id string }{
		{other.AccountID, b.ID, items[0].Key, out[0].ID}, {b.AccountID, other.ID, items[0].Key, out[0].ID}, {b.AccountID, b.ID, items[1].Key, out[0].ID}, {b.AccountID, b.ID, items[0].Key, uuid.NewString()}, {b.AccountID, b.ID, items[0].Key, "private/+%version"}, {b.AccountID, b.ID, items[0].Key, strings.ToUpper(out[0].ID)},
	} {
		if _, err = refs.ResolveObjectVersion(ctx, tc.account, tc.bucket, tc.key, tc.id); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("identity crossed ownership boundary", tc, err)
		}
	}
	got, err := refs.ResolveObjectVersion(ctx, b.AccountID, b.ID, items[0].Key, out[0].ID)
	if err != nil || got != items[0].ProviderVersionID {
		t.Fatal(got, err)
	}
	if got, err = refs.ResolveObjectVersion(ctx, b.AccountID, b.ID, "unseen-null-key", "null"); err != nil || got != "null" {
		t.Fatal(got, err)
	}
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			again, e := refs.RecordObjectVersions(ctx, b.AccountID, b.ID, items)
			if e != nil || len(again) != len(out) || again[0].ID != out[0].ID || again[1].ID != out[1].ID {
				t.Error("concurrent retry changed identities", again, e)
			}
		}()
	}
	wg.Wait()
	for _, bad := range [][]state.ObjectVersionIdentity{
		{items[0], items[0]}, {{Key: "good", ProviderVersionID: "new"}, {Key: "bad\n", ProviderVersionID: "new"}}, {{Key: "", ProviderVersionID: "v"}}, {{Key: "key", ProviderVersionID: ""}}, {{Key: "key", ProviderVersionID: strings.Repeat("v", api.ObjectProviderVersionIDMaxBytes+1)}}, make([]state.ObjectVersionIdentity, api.ObjectVersionReferenceBatchMax+1),
	} {
		if _, err = refs.RecordObjectVersions(ctx, b.AccountID, b.ID, bad); !errors.Is(err, state.ErrConflict) {
			t.Fatal("invalid batch accepted", err)
		}
	}
	status, err := st.(state.ObjectVersionInventoryStore).ObjectVersionAccountingStatus(ctx, b.AccountID, b.ID)
	if err != nil || !status.VersionsObserved || status.Scope != state.ObjectInventoryCurrent {
		t.Fatal(status, err)
	}
	if err = st.AdmitObjectURL(ctx, b.AccountID, b.ID, "new-write", 1, true, accountingPolicy()); !errors.Is(err, state.ErrConflict) {
		t.Fatal("read-observed versions did not fence writes", err)
	}
	if err = st.ClaimObjectInventory(ctx, b.ID, "stale-current"); err != nil {
		t.Fatal(err)
	}
	if err = st.FinishObjectInventory(ctx, b.ID, "stale-current", 0, 0); !errors.Is(err, state.ErrConflict) {
		t.Fatal("current scan reclaimed retained history", err)
	}
	capacity := st.(state.ObjectCapacityStore)
	j, err := capacity.RequestObjectCapacityReconciliation(ctx, b.AccountID, b.AppID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	j, err = capacity.ClaimObjectCapacityReconciliation(ctx, j.ID, "native-read")
	if err != nil || j.InventoryScope != state.ObjectInventoryAllVersions {
		t.Fatal(j, err)
	}
	j, err = st.(state.ObjectVersionInventoryStore).StageObjectVersionInventoryPage(ctx, j.ID, j.Token, "", []state.ObjectVersionInventoryRecord{versionRecord("v1", 10)})
	if err != nil || j.State != "completed" {
		t.Fatal(j, err)
	}
	tracked := st.(state.ObjectTrackedGatewayUploadStore)
	if _, err = tracked.BeginTrackedGatewayUpload(ctx, versionCandidate(b, 1), accountingPolicy()); err != nil {
		t.Fatal("verified native baseline did not admit a tracked write", err)
	}
}

func TestNullVersionObservationMem(t *testing.T) { nullVersionObservation(t, state.NewMemStore()) }
func TestNullVersionObservationPG(t *testing.T)  { st, _ := pgStore(t); nullVersionObservation(t, st) }
func nullVersionObservation(t *testing.T, st accountingStore) {
	b, _ := seedAccounting(t, st)
	refs := st.(state.ObjectVersionReferenceStore)
	item := state.ObjectVersionIdentity{Key: "key", ProviderVersionID: "null"}
	if _, err := refs.RecordObjectVersions(t.Context(), b.AccountID, b.ID, []state.ObjectVersionIdentity{item}); err != nil {
		t.Fatal(err)
	}
	status, _ := st.(state.ObjectVersionInventoryStore).ObjectVersionAccountingStatus(t.Context(), b.AccountID, b.ID)
	if status.VersionsObserved {
		t.Fatal("unversioned null data froze writes", status)
	}
	item.DeleteMarker = true
	if _, err := refs.RecordObjectVersions(t.Context(), b.AccountID, b.ID, []state.ObjectVersionIdentity{item}); err != nil {
		t.Fatal(err)
	}
	item.DeleteMarker = false
	if out, err := refs.RecordObjectVersions(t.Context(), b.AccountID, b.ID, []state.ObjectVersionIdentity{item}); err != nil || out[0].DeleteMarker {
		t.Fatal(err)
	}
	status, _ = st.(state.ObjectVersionInventoryStore).ObjectVersionAccountingStatus(t.Context(), b.AccountID, b.ID)
	if !status.VersionsObserved {
		t.Fatal("null marker observation was forgotten", status)
	}
}

func TestObjectVersionReferenceRestartAndGuardsPG(t *testing.T) {
	st, pool, ctx := pgStoreWithPool(t)
	b, _ := seedAccounting(t, st)
	items := []state.ObjectVersionIdentity{{Key: "key\u0085", ProviderVersionID: "native\u0085"}}
	out, err := st.RecordObjectVersions(ctx, b.AccountID, b.ID, items)
	if err != nil {
		t.Fatal("Go and database control validation diverged", err)
	}
	restarted := state.NewPgStore(pool)
	again, err := restarted.RecordObjectVersions(ctx, b.AccountID, b.ID, items)
	if err != nil || again[0].ID != out[0].ID {
		t.Fatal("restart changed identity", again, err)
	}
	if native, err := restarted.ResolveObjectVersion(ctx, b.AccountID, b.ID, items[0].Key, out[0].ID); err != nil || native != items[0].ProviderVersionID {
		t.Fatal(native, err)
	}
	for _, query := range []string{
		`UPDATE object_version_references SET id=gen_random_uuid() WHERE bucket_id=$1`,
		`UPDATE object_version_references SET native_version_id='other' WHERE bucket_id=$1`,
		`UPDATE object_version_references SET object_key='other' WHERE bucket_id=$1`,
		`DELETE FROM object_version_references WHERE bucket_id=$1`,
		`INSERT INTO object_storage_key_grants(bucket_id,key_hash,max_bytes) VALUES($1,repeat('a',64),1)`,
	} {
		_, err = pool.Exec(ctx, query, b.ID)
		var constraint *pgconn.PgError
		if !errors.As(err, &constraint) || constraint.Code != "23514" {
			t.Fatal("database guard bypassed", query)
		}
	}
	if _, err = pool.Exec(ctx, `UPDATE object_version_references SET versions_observed=false WHERE bucket_id=$1`, b.ID); err != nil {
		t.Fatal(err)
	}
	var observed bool
	if err = pool.QueryRow(ctx, `SELECT versions_observed FROM object_version_references WHERE bucket_id=$1`, b.ID).Scan(&observed); err != nil || !observed {
		t.Fatal("observation not sticky", observed, err)
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM object_version_references`).Scan(&count); err != nil || count != 1 {
		t.Fatal("invalid mutation changed batch", count, err)
	}
	if _, err = pool.Exec(ctx, `DELETE FROM object_buckets WHERE id=$1`, b.ID); err != nil {
		t.Fatal("parent deletion could not cascade", err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM object_version_references`).Scan(&count); err != nil || count != 0 {
		t.Fatal("references survived bucket deletion", count, err)
	}
}

func TestObjectVersionReferenceMigrationRoundTripPG(t *testing.T) {
	st, pool, ctx := pgStoreWithPool(t)
	raw, err := migrations.FS.ReadFile("20261002120000001_object_version_references.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(raw), "-- +goose Down")
	if len(parts) != 2 {
		t.Fatal("migration must have one rollback")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, parts[1]); err != nil {
		t.Fatal("empty rollback failed", err)
	}
	if _, err = tx.Exec(ctx, parts[0]); err != nil {
		t.Fatal("reapply failed", err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	b, _ := seedAccounting(t, st)
	out, err := st.RecordObjectVersions(ctx, b.AccountID, b.ID, []state.ObjectVersionIdentity{{Key: "key", ProviderVersionID: "native"}})
	if err != nil {
		t.Fatal(err)
	}
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, parts[1]); err == nil || !strings.Contains(err.Error(), "Cannot discard customer version identities") {
		t.Fatal("rollback discarded public IDs", err)
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if native, err := st.ResolveObjectVersion(ctx, b.AccountID, b.ID, "key", out[0].ID); err != nil || native != "native" {
		t.Fatal("rollback changed a customer identity", native, err)
	}
}

func TestNullVersionInternalUUIDIsNotPublicPG(t *testing.T) {
	st, pool, ctx := pgStoreWithPool(t)
	b, _ := seedAccounting(t, st)
	out, err := st.RecordObjectVersions(ctx, b.AccountID, b.ID, []state.ObjectVersionIdentity{{Key: "key", ProviderVersionID: "null"}})
	if err != nil || out[0].ID != "null" {
		t.Fatal(out, err)
	}
	var internalID string
	if err = pool.QueryRow(ctx, `SELECT id::text FROM object_version_references WHERE bucket_id=$1 AND object_key='key'`, b.ID).Scan(&internalID); err != nil {
		t.Fatal(err)
	}
	if _, err = st.ResolveObjectVersion(ctx, b.AccountID, b.ID, "key", internalID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("internal null row became an immutable public ID", err)
	}
}

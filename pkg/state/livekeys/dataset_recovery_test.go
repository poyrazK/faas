package livekeys_test

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state/livekeys"
)

func TestLegacyLiveLoginDatasetRecoveryIsDurableAndCompatible(t *testing.T) {
	pool := open(t)
	store := livekeys.New(pool)
	ctx := t.Context()
	const pin = "project-a/br-original"
	if _, err := pool.Exec(ctx, `UPDATE managed_postgres_databases SET provider_resource_id='project-a',data_resource_id=$2 WHERE id=$1`, dbID, pin); err != nil {
		t.Fatal(err)
	}
	if err := store.Enable(ctx, account, dbID, key("aaaaaaaaaaaaaaaa"), nil); err != nil {
		t.Fatal(err)
	}
	// Model an N-1 writer: no dataset authorization columns in its INSERT.
	if _, err := pool.Exec(ctx, `INSERT INTO managed_postgres_live_logins(database_id,account_id,sealed_login,recipient,provider_identity_id,slot) VALUES($1,$2,$3,$4,'gregale_live_old','slot')`, dbID, account, []byte("legacy"), recipient); err != nil {
		t.Fatal(err)
	}
	checkDue := func(want int) {
		t.Helper()
		refs, err := store.DatabasesWithoutLogin(ctx, 20)
		if err != nil || len(refs) != want {
			t.Fatalf("repair discovery=%+v err=%v want=%d", refs, err, want)
		}
	}
	checkDue(1)
	calls := 0
	issue := func(context.Context) (*livekeys.Login, error) {
		calls++
		return &livekeys.Login{Sealed: []byte("pinned"), Recipient: recipient, Identity: "gregale_live_current", Slot: "slot", DataResourceID: pin}, nil
	}
	if _, err := pool.Exec(ctx, `CREATE FUNCTION reject_live_dataset() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'receipt unavailable'; END $$;
 CREATE TRIGGER reject_live_dataset BEFORE UPDATE ON managed_postgres_live_logins FOR EACH ROW EXECUTE FUNCTION reject_live_dataset()`); err != nil {
		t.Fatal(err)
	}
	if stored, err := store.IssueMissingLogin(ctx, account, dbID, issue); err == nil || stored {
		t.Fatalf("failed receipt was accepted: stored=%t err=%v", stored, err)
	}
	checkDue(1)
	if _, err := pool.Exec(ctx, `DROP TRIGGER reject_live_dataset ON managed_postgres_live_logins`); err != nil {
		t.Fatal(err)
	}
	if stored, err := store.IssueMissingLogin(ctx, account, dbID, issue); err != nil || !stored || calls != 2 {
		t.Fatalf("repair retry=%t calls=%d err=%v", stored, calls, err)
	}
	checkDue(0)
	if stored, err := store.IssueMissingLogin(ctx, account, dbID, issue); err != nil || stored || calls != 2 {
		t.Fatalf("already repaired login reissued=%t calls=%d err=%v", stored, calls, err)
	}
	var capturedPin, capturedIdentity string
	if err := pool.QueryRow(ctx, `SELECT data_resource_id,authorized_identity_id FROM managed_postgres_live_logins WHERE database_id=$1`, dbID).Scan(&capturedPin, &capturedIdentity); err != nil || capturedPin != pin || capturedIdentity != "gregale_live_current" {
		t.Fatalf("authorization receipt missing: %q %q %v", capturedPin, capturedIdentity, err)
	}
	// An older writer can replace the identity, but cannot reuse the newer
	// authorization receipt to hide that change from recovery.
	if _, err := pool.Exec(ctx, `UPDATE managed_postgres_live_logins SET provider_identity_id='gregale_live_old',sealed_login=$2 WHERE database_id=$1`, dbID, []byte("legacy-again")); err != nil {
		t.Fatal(err)
	}
	checkDue(1)
	if stored, err := store.IssueMissingLogin(ctx, account, dbID, issue); err != nil || !stored {
		t.Fatal(stored, err)
	}
	checkDue(0)
	keys, err := store.Keys(ctx, account, dbID)
	if err != nil || len(keys) != 1 || keys[0].KID != "aaaaaaaaaaaaaaaa" {
		t.Fatal("repair replaced the customer's live secret", keys, err)
	}
	// A provider response prepared before a dataset replacement must not
	// overwrite that replacement's stored credentials.
	if _, err := pool.Exec(ctx, `UPDATE managed_postgres_databases SET data_resource_id='project-a/br-replacement' WHERE id=$1`, dbID); err != nil {
		t.Fatal(err)
	}
	if stored, err := store.IssueMissingLogin(ctx, account, dbID, issue); !errors.Is(err, livekeys.ErrNotFound) || stored {
		t.Fatalf("stale dataset result stored=%t err=%v", stored, err)
	}
	checkDue(1)
}

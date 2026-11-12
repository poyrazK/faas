//go:build !no_pg

package state_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-1151: in SQL state lists FROZEN goes wherever WARM goes. A natively
// managed application-standard instance that freezes must thaw: the residency
// guard listed every resident state but frozen, so FROZEN -> RUNNING was
// refused as "historical native receipt cannot recreate residency"
// (implicit state machines audit 2026-11-08, finding 3).
func TestPgStandardNativeInstanceFreezesAndThaws(t *testing.T) {
	s, pool := state.RuntimeCapturePGStore(t)
	f := rawStandardRestoreFixture(t, s)
	if err := insertRawRestoreGrant(t, pool, f); err != nil {
		t.Fatal(err)
	}
	r := state.ConsumedNativeReceipt(f.Binding, f.Capture)
	if err := recordRawRestoreReceipt(t, pool, r); err != nil {
		t.Fatal(err)
	}
	if err := publishRawRestoreTuple(t, pool, r, f.Target.State); err != nil {
		t.Fatal(err)
	}
	if err := publishRawRestoreReceipt(t, pool, r); err != nil {
		t.Fatal("current raw publication refused", err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE instances SET state='frozen' WHERE id=$1`, f.Target.ID); err != nil {
		t.Fatal("freeze refused", err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE instances SET state='running',started_at=clock_timestamp() WHERE id=$1`, f.Target.ID); err != nil {
		t.Fatal("thaw of a frozen native instance refused:", err)
	}
	// Leaving residency for history still cannot come back.
	if _, err := pool.Exec(t.Context(), `UPDATE instances SET state='parked' WHERE id=$1`, f.Target.ID); err != nil {
		t.Fatal(err)
	}
	_, err := pool.Exec(t.Context(), `UPDATE instances SET state='running' WHERE id=$1`, f.Target.ID)
	assertRawRestoreRefused(t, err, false)
}

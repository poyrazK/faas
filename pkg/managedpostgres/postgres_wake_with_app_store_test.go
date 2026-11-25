package managedpostgres

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

// The claim is the only guard against two apid replicas starting one
// database for one wake: it must return a database once per gap, only for an
// opted-in ready scale-to-zero database with a ready binding to the app.
func TestWakeWithAppPGClaimsOncePerGap(t *testing.T) {
	s, pool, ctx, accountID := postgresStoreFixture(t)
	database := postgresReadyDatabase(t, s, accountID, "wake-"+uuid.NewString()[:8], time.Now().UTC())
	if !database.Spec.ScaleToZero {
		if _, err := pool.Exec(ctx, `UPDATE managed_postgres_databases SET scale_to_zero=true WHERE id=$1`, database.ID); err != nil {
			t.Fatal(err)
		}
	}
	app, err := state.NewPgStore(pool).CreateApp(ctx, state.App{AccountID: accountID, Slug: "wake-" + uuid.NewString()[:8], MaxConcurrency: 1, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	binding := testBinding(accountID, database.ID, app.ID, uuid.NewString(), time.Now().UTC())
	if _, _, err = s.ReserveBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}
	var last []WakeStartClaim
	claim := func() int {
		t.Helper()
		claims, err := s.ClaimWakeStarts(ctx, app.ID, WakeStartGap)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range claims {
			if c.DatabaseID != database.ID || c.ProviderResourceID == "" || c.BackendID == "" || c.ClaimedAt.IsZero() {
				t.Fatalf("claim = %+v", c)
			}
		}
		last = claims
		return len(claims)
	}

	// Off: nothing to claim, and the app is not listed.
	if n := claim(); n != 0 {
		t.Fatalf("claimed %d while off", n)
	}
	if err := s.SetWakeWithApp(ctx, database.ID, true); err != nil {
		t.Fatal(err)
	}
	// A binding still provisioning does not count.
	if n := claim(); n != 0 {
		t.Fatalf("claimed %d through a provisioning binding", n)
	}
	if _, err := pool.Exec(ctx, `UPDATE managed_postgres_bindings SET state='ready', provider_identity_id='role-wake', credential_ref='cred-wake' WHERE database_id=$1`, database.ID); err != nil {
		t.Fatal(err)
	}
	apps, err := s.WakeWithAppApps(ctx)
	if err != nil || !apps[app.ID] {
		t.Fatalf("apps = %v, %v", apps, err)
	}
	if on, err := s.WakeWithApp(ctx, []string{database.ID}); err != nil || !on[database.ID] {
		t.Fatalf("WakeWithApp = %v, %v", on, err)
	}
	// Once, then not again within the gap (another replica's claim).
	if n := claim(); n != 1 {
		t.Fatalf("first claim = %d", n)
	}
	if n := claim(); n != 0 {
		t.Fatalf("second claim within the gap = %d", n)
	}
	// After the gap it is claimed again.
	if _, err := pool.Exec(ctx, `UPDATE managed_postgres_wake_with_app SET last_started_at=now()-interval '2 minutes' WHERE database_id=$1`, database.ID); err != nil {
		t.Fatal(err)
	}
	if n := claim(); n != 1 {
		t.Fatalf("claim after the gap = %d", n)
	}
	// A claim that started nothing is deferred: claimable again after retry
	// (zero here), not after the gap.
	stale := last[0]
	if err := s.DeferWakeStart(ctx, stale, WakeStartGap, 0); err != nil {
		t.Fatal(err)
	}
	if n := claim(); n != 1 {
		t.Fatalf("claim after a deferred claim = %d", n)
	}
	// Deferring an older claim leaves the newer claim's stamp alone.
	if err := s.DeferWakeStart(ctx, stale, WakeStartGap, 0); err != nil {
		t.Fatal(err)
	}
	if n := claim(); n != 0 {
		t.Fatalf("an old claim's defer released the new claim: %d", n)
	}
	// Another app's wake claims nothing.
	if claims, err := s.ClaimWakeStarts(ctx, uuid.NewString(), WakeStartGap); err != nil || len(claims) != 0 {
		t.Fatalf("other app claims = %v, %v", claims, err)
	}
	// Turned off, it is no longer claimed or listed.
	if err := s.SetWakeWithApp(ctx, database.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE managed_postgres_wake_with_app SET last_started_at=NULL`); err != nil {
		t.Fatal(err)
	}
	if n := claim(); n != 0 {
		t.Fatalf("claimed %d after off", n)
	}
}

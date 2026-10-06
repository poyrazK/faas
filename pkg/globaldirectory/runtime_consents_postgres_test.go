//go:build !no_pg

package globaldirectory

// adr: 646
import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestDirectoryRuntimeConsentPostgresContract(t *testing.T) {
	_, f := postgresQuotaFixture(t, false, 1)
	exerciseRuntimeConsent(t, f)
}
func TestDirectoryRuntimeConsentPostgresBound(t *testing.T) {
	_, f := postgresQuotaFixture(t, false, 1)
	exerciseRuntimeConsentBound(t, f)
}

func TestDirectoryRuntimeConsentPostgresRevokeWinsWaitingObservation(t *testing.T) {
	pool, f := postgresQuotaFixture(t, false, 1)
	j, c := consentFixture(t, f)
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	if _, err := j.PrepareRuntimeConsent(ctx, f.owner.ID, f.request.Slug, c); err != nil {
		t.Fatal(err)
	}
	tx := beginDirectoryTx(t, pool)
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended('gregale-global-runtime-consent:'||$1::uuid::text,0))", c.GlobalAppID); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := j.ObserveRuntimeConsent(ctx, f.owner.ID, f.request.Slug, c.ID, state.RegionalRuntimeConsent{Consent: c, CreatedAt: time.Now().UTC().Truncate(time.Microsecond)})
		result <- err
	}()
	blocked := false
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if err := pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND wait_event_type='Lock' AND query LIKE '%gregale-global-runtime-consent:%')").Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !blocked {
		t.Fatal("observation did not wait on the journal")
	}
	if _, err := tx.Exec(ctx, "UPDATE global_runtime_consents SET status='revoking' WHERE id=$1", c.ID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, ErrRuntimeConsentUnavailable) {
		t.Fatal("stale setup observation escaped revocation", err)
	}
}

func TestDirectoryRuntimeConsentPostgresRetentionAndGuards(t *testing.T) {
	pool, f := postgresQuotaFixture(t, false, 1)
	j, c := consentFixture(t, f)
	ctx := t.Context()
	owner, slug := f.owner.ID, f.request.Slug
	if _, err := j.PrepareRuntimeConsent(ctx, owner, slug, c); err != nil {
		t.Fatal(err)
	}
	meta := state.RegionalRuntimeConsent{Consent: c, CreatedAt: time.Now().UTC().Truncate(time.Microsecond)}
	if _, err := j.ObserveRuntimeConsent(ctx, owner, slug, c.ID, meta); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		"UPDATE global_runtime_consents SET id=gen_random_uuid() WHERE id=$1",
		"UPDATE global_runtime_consents SET issuer_id=gen_random_uuid() WHERE id=$1",
		"UPDATE global_runtime_consents SET owner_id=gen_random_uuid() WHERE id=$1",
		"UPDATE global_runtime_consents SET global_app_id=gen_random_uuid() WHERE id=$1",
		"UPDATE global_runtime_consents SET expires_at=expires_at+interval '1 second' WHERE id=$1",
		"UPDATE global_runtime_consents SET created_at=created_at-interval '1 second' WHERE id=$1",
		"UPDATE global_runtime_consents SET regional_created_at=regional_created_at-interval '1 second' WHERE id=$1",
		"UPDATE global_runtime_consents SET binding=jsonb_set(binding,'{APIBase}','null'::jsonb) WHERE id=$1",
		"DELETE FROM global_runtime_consents WHERE id=$1",
	} {
		if _, err := pool.Exec(ctx, stmt, c.ID); err == nil {
			t.Fatal("SQL identity bypass", stmt)
		}
	}
	if _, err := pool.Exec(ctx, "TRUNCATE global_runtime_consents"); err == nil {
		t.Fatal("retention truncate")
	}
	for _, field := range []string{"CellID", "Region", "CellGeneration", "RegionalAccountID", "RegionalOrgID", "RegionalAppID", "RegionalAppSlug", "APIBase"} {
		// A new row cannot acquire nullable scope through direct SQL either.
		_, err := pool.Exec(ctx, "INSERT INTO global_runtime_consents(id,issuer_id,global_app_id,owner_id,global_app_slug,binding,expires_at) SELECT gen_random_uuid(),issuer_id,global_app_id,owner_id,global_app_slug,jsonb_set(binding,ARRAY[$2]::text[],'null'::jsonb),expires_at FROM global_runtime_consents WHERE id=$1", c.ID, field)
		if err == nil {
			t.Fatal("null scope accepted", field)
		}
	}
	if _, err := pool.Exec(ctx, "DELETE FROM global_apps WHERE id=$1", c.GlobalAppID); err != nil {
		t.Fatal("collection", err)
	}
	restarted := NewPgStore(pool)
	if got, err := restarted.RuntimeConsent(ctx, owner, slug, c.ID); err != nil || got.Consent != c || got.RegionalCreatedAt == nil || !got.RegionalCreatedAt.Equal(meta.CreatedAt) {
		t.Fatal("original record lost at restart/collection", err)
	}
	if _, err := restarted.CheckRuntimeConsent(ctx, owner, slug, c.ID); !errors.Is(err, ErrRuntimeConsentUnavailable) {
		t.Fatal("collected app dispatch", err)
	}
	if _, err := restarted.BeginRevokeRuntimeConsent(ctx, owner, slug, c.ID); err != nil {
		t.Fatal("collected revoke", err)
	}
	stamp := time.Now().UTC().Truncate(time.Microsecond)
	meta.RevokedAt = &stamp
	if _, err := restarted.ObserveRuntimeConsent(ctx, owner, slug, c.ID, meta); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "UPDATE global_runtime_consents SET status='active',regional_revoked_at=NULL WHERE id=$1", c.ID); err == nil {
		t.Fatal("revoked consent resurrected")
	}
	m, err := os.ReadFile("../../migrations/20261006192905057_global_runtime_delivery_consents.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(m), "-- +goose Down")
	if len(parts) != 2 {
		t.Fatal("migration parts")
	}
	if _, err := pool.Exec(ctx, parts[1]); err == nil {
		t.Fatal("retained consent downgraded")
	}
}

func TestDirectoryRuntimeConsentPostgresExpiryAfterOwnerWait(t *testing.T) {
	for _, mode := range []string{"fresh", "retry", "observe", "dispatch", "direct-observe"} {
		t.Run(mode, func(t *testing.T) {
			pool, f := postgresQuotaFixture(t, false, 1)
			j, c := consentFixture(t, f)
			c.ExpiresAtMicros = time.Now().Add(time.Second).UnixMicro()
			ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
			defer cancel()
			meta := state.RegionalRuntimeConsent{Consent: c, CreatedAt: time.Now().UTC().Truncate(time.Microsecond)}
			if mode != "fresh" {
				if _, err := j.PrepareRuntimeConsent(ctx, f.owner.ID, f.request.Slug, c); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "dispatch" {
				if _, err := j.ObserveRuntimeConsent(ctx, f.owner.ID, f.request.Slug, c.ID, meta); err != nil {
					t.Fatal(err)
				}
			}
			tx := beginDirectoryTx(t, pool)
			if _, err := tx.Exec(ctx, "SELECT id FROM accounts WHERE id=$1 FOR UPDATE", f.actor.ID); err != nil {
				t.Fatal(err)
			}
			result := make(chan error, 1)
			go func() {
				var err error
				switch mode {
				case "fresh", "retry":
					_, err = j.PrepareRuntimeConsent(ctx, f.owner.ID, f.request.Slug, c)
				case "observe":
					_, err = j.ObserveRuntimeConsent(ctx, f.owner.ID, f.request.Slug, c.ID, meta)
				case "dispatch":
					_, err = j.CheckRuntimeConsent(ctx, f.owner.ID, f.request.Slug, c.ID)
				case "direct-observe":
					_, err = pool.Exec(ctx, "UPDATE global_runtime_consents SET status='active',regional_created_at=$2 WHERE id=$1", c.ID, meta.CreatedAt)
				}
				result <- err
			}()
			blocked := false
			deadline := time.Now().Add(4 * time.Second)
			for time.Now().Before(deadline) {
				if err := pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND wait_event_type='Lock' AND query LIKE '%global_runtime_consent%')").Scan(&blocked); err != nil {
					t.Fatal(err)
				}
				if blocked {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if !blocked {
				t.Fatal("operation never reached the observed owner lock")
			}
			time.Sleep(time.Until(time.UnixMicro(c.ExpiresAtMicros)) + 5*time.Millisecond)
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			err := <-result
			if mode == "direct-observe" {
				if err == nil {
					t.Fatal("SQL activation accepted expired wait")
				}
			} else if !errors.Is(err, ErrRuntimeConsentUnavailable) {
				t.Fatal("expired owner wait accepted", err)
			}
			var count int
			if err := pool.QueryRow(ctx, "SELECT count(*) FROM global_runtime_consents WHERE id=$1 AND status='active'", c.ID).Scan(&count); err != nil {
				t.Fatal(err)
			}
			want := 0
			if mode == "dispatch" {
				want = 1
			}
			if count != want {
				t.Fatal("failed operation changed observation", count, want)
			}
		})
	}
}

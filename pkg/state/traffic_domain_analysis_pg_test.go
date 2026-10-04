//go:build !no_pg

// adr: 531
package state

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestPgTrafficDomainVerificationRollback(t *testing.T) {
	for _, mode := range trafficDomainVerificationModes {
		t.Run(mode, func(t *testing.T) {
			store, pool, account, app := trafficHostPGFixture(t)
			testTrafficDomainVerification(t, store, account, app, mode, func(owner Account, source App, pattern string) {
				in := globalTrafficRule(owner, source, pattern, 520)
				encoded, err := json.Marshal(in.Action)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := pool.Exec(t.Context(), `INSERT INTO edge_rules(id,account_id,app_id,match_host,match_path,enabled,kind,action) VALUES('00000000-0000-0000-0000-000000000377',$1,$2,$3,'/',true,'route',$4::jsonb)`, owner.ID, source.ID, pattern, encoded); err != nil {
					t.Fatal(err)
				}
			})
		})
	}
}

func TestPgTrafficDomainOwnerFenceDuringAccountWait(t *testing.T) {
	for _, challengeBound := range []bool{false, true} {
		name := "plain"
		if challengeBound {
			name = "challenge"
		}
		t.Run(name, func(t *testing.T) {
			store, pool, account, app := trafficHostPGFixture(t)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			peer, err := store.CreateAccount(ctx, "reclaimed-domain-owner@example.test", api.PlanScale)
			if err != nil {
				t.Fatal(err)
			}
			target, err := store.CreateApp(ctx, App{AccountID: peer.ID, Slug: "reclaimed-domain-owner", Status: AppActive})
			if err != nil {
				t.Fatal(err)
			}
			domain, err := store.CreateCustomDomain(ctx, "owner-fence.example.test", app.ID, "old-token")
			if err != nil {
				t.Fatal(err)
			}
			lock, err := pool.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Release()
			key := "gregale.traffic.account.v1:" + account.ID
			queries := sqlc.New()
			if acquired, err := queries.TryLockTrafficPolicySession(ctx, lock, key); err != nil || !acquired {
				t.Fatalf("test account lock: acquired=%v err=%v", acquired, err)
			}
			locked := true
			defer func() {
				if locked {
					_, _ = queries.UnlockTrafficPolicySession(context.WithoutCancel(ctx), lock, key)
				}
			}()
			waiting, application := domainRemovalWaitingStore(t, pool)
			result := make(chan error, 1)
			go func() {
				if challengeBound {
					matched, err := waiting.MarkDomainVerifiedIfChallenge(ctx, domain.Domain, domain.ChallengeToken)
					if matched && err == nil {
						err = errors.New("old challenge verified reclaimed domain")
					}
					result <- err
				} else {
					result <- waiting.MarkDomainVerified(ctx, domain.Domain)
				}
			}()
			awaitDomainRemovalSessionWait(t, pool, application)
			// Emulate the previous lock holder's legacy handoff. Current
			// guarded creation shares the old owner's lock and cannot race it.
			if _, err := pool.Exec(ctx, `UPDATE custom_domains SET app_id=$2,challenge_token='new-token',verification_expires_at=clock_timestamp()+interval '7 days' WHERE domain=$1`, domain.Domain, target.ID); err != nil {
				t.Fatal(err)
			}
			if matched, err := store.MarkDomainVerifiedIfChallenge(ctx, domain.Domain, "new-token"); err != nil || !matched {
				t.Fatalf("new owner verification: matched=%v err=%v", matched, err)
			}
			before, err := store.DomainByName(ctx, domain.Domain)
			if err != nil {
				t.Fatal(err)
			}
			if released, err := queries.UnlockTrafficPolicySession(ctx, lock, key); err != nil || !released {
				t.Fatalf("test unlock: released=%v err=%v", released, err)
			}
			locked = false
			if err := <-result; challengeBound && err != nil || !challengeBound && !errors.Is(err, ErrNotFound) {
				t.Fatalf("old verification owner fence: %v", err)
			}
			after, err := store.DomainByName(ctx, domain.Domain)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("old verifier changed new owner's domain: before=%+v after=%+v err=%v", before, after, err)
			}
		})
	}
}

func TestPgTrafficDomainChallengeExpiryAfterTransactionStart(t *testing.T) {
	store, pool, _, app := trafficHostPGFixture(t)
	domain, err := store.CreateCustomDomain(t.Context(), "write-clock.example.test", app.ID, "token")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(t.Context())) }()
	if _, err := tx.Exec(t.Context(), `UPDATE custom_domains SET verification_expires_at=clock_timestamp()+interval '75 milliseconds' WHERE domain=$1`, domain.Domain); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	var expiredAfterStart bool
	if err := tx.QueryRow(t.Context(), `SELECT now()<verification_expires_at AND clock_timestamp()>verification_expires_at FROM custom_domains WHERE domain=$1`, domain.Domain).Scan(&expiredAfterStart); err != nil || !expiredAfterStart {
		t.Fatalf("expiry fixture did not cross transaction time: expired=%v err=%v", expiredAfterStart, err)
	}
	changed, err := sqlc.New().MarkTrafficDomainVerified(t.Context(), tx, sqlc.MarkTrafficDomainVerifiedParams{
		Domain: domain.Domain, AppID: uuidToPgtype(app.ID), Token: "token", ChallengeBound: true,
	})
	if err != nil || changed != 0 {
		t.Fatalf("transaction timestamp resurrected expired challenge: changed=%d err=%v", changed, err)
	}
	var verified bool
	if err := tx.QueryRow(t.Context(), `SELECT verified_at IS NOT NULL FROM custom_domains WHERE domain=$1`, domain.Domain).Scan(&verified); err != nil || verified {
		t.Fatalf("expired challenge changed verification: verified=%v err=%v", verified, err)
	}
}

func TestPgTrafficDomainMetadataAndAppPublication(t *testing.T) {
	store, pool, account, _, app, environment := trafficEnvironmentPGFixture(t)
	visibility := api.AppVisibilityInternal
	if _, err := store.UpdateApp(t.Context(), app.ID, UpdateAppParams{SetVisibility: true, Visibility: &visibility}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO custom_domains(domain,app_id,challenge_token,verified_at,environment_id) VALUES
		('public.example.test',$1,'token',now(),NULL),('*.example.test',$1,'token',now(),NULL),
		('pending.example.test',$1,'token',NULL,NULL),('scoped.example.test',$1,'token',now(),$2)`, app.ID, environment.ID); err != nil {
		t.Fatal(err)
	}
	// Escaped compiler input exercises the same new-domain gate without
	// requiring a near-maximum numeric formatting expansion for this fixture.
	if _, err := pool.Exec(t.Context(), `INSERT INTO edge_rules(id,account_id,app_id,match_host,match_path,enabled,kind,action)
		VALUES('00000000-0000-0000-0000-000000000377',$1,$2,'public.example.test','/',true,'route',
		jsonb_build_object('kind','route','route',jsonb_build_object('target_app_slug',$3::text),
		'validate',jsonb_build_object('schema',repeat('&',$4::integer))))`, account.ID, app.ID, app.Slug, api.TrafficPolicyMaxHostBytes/6+100); err != nil {
		t.Fatal(err)
	}
	visibility = api.AppVisibilityPublic
	_, err := store.UpdateApp(t.Context(), app.ID, UpdateAppParams{SetVisibility: true, Visibility: &visibility})
	var aggregate *TrafficPolicyAggregateError
	if !errors.As(err, &aggregate) || aggregate.Host != "public.example.test" || aggregate.Scope != "host_compiled_projection_estimate" {
		t.Fatalf("domain owner publication accepted overload: %v", err)
	}
	if got, err := store.AppByID(t.Context(), app.ID); err != nil || got.Visibility != api.AppVisibilityInternal {
		t.Fatalf("refused publication changed app: %+v err=%v", got, err)
	}
	if err := store.DeleteEdgeRule(t.Context(), "00000000-0000-0000-0000-000000000377"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateApp(t.Context(), app.ID, UpdateAppParams{SetVisibility: true, Visibility: &visibility}); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(t.Context())) }()
	view, err := readTrafficHostAnalysis(t.Context(), tx, uuidToPgtype(account.ID), ".apps.example.test")
	if err != nil || len(view.Domains) != 3 {
		t.Fatalf("verified domain metadata: %+v err=%v", view.Domains, err)
	}
	row, err := sqlc.New().ReadTrafficHostAnalysis(t.Context(), tx, sqlc.ReadTrafficHostAnalysisParams{AccountID: uuidToPgtype(account.ID), MaxInputs: 1, MaxBytes: 1})
	if err != nil || len(row.Data) != 0 || row.Inputs <= 1 {
		t.Fatalf("domain metadata escaped scalar bounds: %+v err=%v", row, err)
	}
}

//go:build !no_pg

// adr: 375
package state

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func seedPgDomainRemovalLegacy(t *testing.T, pool *pgxpool.Pool, rule EdgeRule) {
	t.Helper()
	action, err := json.Marshal(rule.Action)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO edge_rules(id,account_id,app_id,match_host,match_path,enabled,kind,action) VALUES($1,$2,$3,$4,'/',true,$5,$6::jsonb)`, rule.ID, rule.AccountID, rule.AppID, rule.MatchHost, string(rule.Kind), action); err != nil {
		t.Fatal(err)
	}
}

func TestPgTrafficDomainRemovalRefusalRepairAndReservationPrecedence(t *testing.T) {
	for _, mode := range trafficDomainRemovalModes {
		for _, form := range trafficDomainRemovalForms {
			t.Run(mode+"/"+form, func(t *testing.T) {
				store, pool, _, app := trafficHostPGFixture(t)
				peerAccount, peer := globalTrafficPeer(t, store, "removal-peer")
				testTrafficDomainRemoval(t, store, app, peerAccount, peer, mode, form,
					func(rule EdgeRule) { seedPgDomainRemovalLegacy(t, pool, rule) }, func() int {
						var count int
						if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM org_activity_outbox`).Scan(&count); err != nil {
							t.Fatal(err)
						}
						return count
					})
			})
		}
	}
}

func TestPgTrafficReservationProjectionMatchesAuthoritativeLookup(t *testing.T) {
	store, pool, _, app := trafficHostPGFixture(t)
	for _, domain := range []string{"*.under_score.example.test", "*.percent%.example.test", "*.spaces.example.test\u00a0", "UPPER.EXAMPLE.TEST"} {
		if _, err := pool.Exec(t.Context(), `INSERT INTO custom_domains(domain,app_id,challenge_token) VALUES($1,$2,'private-reservation-token')`, domain, app.ID); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(t.Context())) }()
	view, err := readTrafficHostAnalysis(t.Context(), tx, uuidToPgtype(""), store.trafficAppsSuffix)
	if err != nil {
		t.Fatal(err)
	}
	machine := hostAnalysisMachine{nodes: []hostAnalysisNode{newHostAnalysisNode()}, maxNodes: api.TrafficPolicyMaxAnalysisNodes}
	if err := machine.addTrafficBindingLanguages(t.Context(), 0, view); err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"api.under_score.example.test", "api.underXscore.example.test", "api.percent%.example.test", "api.percent123.example.test", "api.spaces.example.test", "under_score.example.test", "*.under_score.example.test", "upper.example.test", "unclaimed.example.test"} {
		t.Run(host, func(t *testing.T) {
			var projected bool
			positions := machine.closure([]int{0})
			for _, character := range host {
				positions = machine.step(positions, character)
			}
			for _, position := range positions {
				for _, ref := range machine.nodes[position].accepted {
					projected = projected || ref.reservation
				}
			}
			if err := store.WithPublicHostPolicySnapshot(t.Context(), func(reader PublicHostPolicyReader) error {
				reserved, err := reader.PublicHostReserved(t.Context(), "", host)
				if err != nil || projected != reserved {
					t.Fatalf("projection/lookup reservation disagreement: %q %v/%v err=%v", host, projected, reserved, err)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
	row, err := sqlc.New().ReadTrafficDomainClaims(t.Context(), tx, sqlc.ReadTrafficDomainClaimsParams{MaxInputs: api.TrafficPolicyMaxAnalysisInputs, MaxBytes: api.TrafficPolicyMaxAnalysisMetadataBytes})
	if err != nil || strings.Contains(string(row.Data), "private-reservation-token") || row.Inputs != 4 {
		t.Fatalf("domain projection leaked proof or lost claims: inputs=%d err=%v", row.Inputs, err)
	}
	row, err = sqlc.New().ReadTrafficDomainClaims(t.Context(), tx, sqlc.ReadTrafficDomainClaimsParams{MaxInputs: 1, MaxBytes: 1})
	if err != nil || len(row.Data) != 0 || row.Inputs <= 1 || row.Bytes <= 1 {
		t.Fatalf("oversized binding metadata was transferred: %+v err=%v", row, err)
	}
}

func domainRemovalWaitingStore(t *testing.T, pool *pgxpool.Pool) (*PgStore, string) {
	t.Helper()
	config := pool.Config().Copy()
	config.MaxConns = 1
	name := "domain-removal-" + uuid.NewString()
	config.ConnConfig.RuntimeParams["application_name"] = name
	waiting, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(waiting.Close)
	return NewPgStore(waiting), name
}

func awaitDomainRemovalSessionWait(t *testing.T, pool *pgxpool.Pool, application string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name=$1 AND state='idle' AND query LIKE '%pg_advisory_unlock%')`, application).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("removal did not reach a verified session-lock wait")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestPgTrafficDomainRemovalOwnerReclaimDuringLockWait(t *testing.T) {
	for _, form := range []string{"plain", "owned", "owned_activity"} {
		t.Run(form, func(t *testing.T) {
			store, pool, account, app := trafficHostPGFixture(t)
			_, peer := globalTrafficPeer(t, store, "reclaimed-domain-peer")
			const domain = "reclaimed.example.test"
			if _, err := store.CreateCustomDomain(t.Context(), domain, app.ID, "original-token"); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(t.Context(), `UPDATE custom_domains SET verification_expires_at=clock_timestamp()-interval '1 second' WHERE domain=$1`, domain); err != nil {
				t.Fatal(err)
			}
			_, release, busy, err := store.tryAcquireTrafficPolicySession(t.Context(), uuidToPgtype(account.ID), false)
			if err != nil || busy {
				t.Fatalf("owner serialization: busy=%v err=%v", busy, err)
			}
			defer release(t.Context())
			waiting, application := domainRemovalWaitingStore(t, pool)
			result := make(chan error, 1)
			go func() {
				ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
				defer cancel()
				switch form {
				case "plain":
					result <- waiting.DeleteCustomDomain(ctx, domain)
				case "owned":
					result <- waiting.DeleteCustomDomainForApp(ctx, domain, app.ID)
				default:
					_, err := waiting.DeleteCustomDomainForAppWithActivity(ctx, domain, app.ID, OrgActivity{OrgID: uuid.New(), Kind: "domain.removed", ActorType: OrgActivityActorSystem, ActorLabel: "removal-test", ResourceType: "domain", ResourceLabel: domain, SourceType: "domain.removed", SourceID: "reclaim-test"})
					result <- err
				}
			}()
			awaitDomainRemovalSessionWait(t, pool, application)
			reclaimed, err := store.CreateCustomDomainIfUnderQuota(t.Context(), domain, peer.ID, "new-private-token", 100, 500)
			if err != nil {
				t.Fatal(err)
			}
			release(t.Context())
			if err := <-result; !errors.Is(err, ErrNotFound) {
				t.Fatalf("stale authorization removed a reclaimed name: %v", err)
			}
			if after, err := store.DomainByName(t.Context(), domain); err != nil || !reflect.DeepEqual(after, reclaimed) {
				t.Fatalf("reclaimed claim changed: app=%q err=%v", after.AppID, err)
			}
			var outbox int
			if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM org_activity_outbox`).Scan(&outbox); err != nil || outbox != 0 {
				t.Fatalf("reclaim refusal wrote activity: count=%d err=%v", outbox, err)
			}
		})
	}
}

func TestPgTrafficDomainRemovalCancellationReleasesSessionLocks(t *testing.T) {
	store, pool, account, app := trafficHostPGFixture(t)
	const domain = "canceled-removal.example.test"
	if _, err := store.CreateCustomDomain(t.Context(), domain, app.ID, "private-token"); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDomainVerified(t.Context(), domain); err != nil {
		t.Fatal(err)
	}
	if err := store.SetDefaultCustomDomain(t.Context(), app.ID, domain); err != nil {
		t.Fatal(err)
	}
	before, err := store.DomainByName(t.Context(), domain)
	if err != nil {
		t.Fatal(err)
	}
	_, release, busy, err := store.tryAcquireTrafficPolicySession(t.Context(), uuidToPgtype(account.ID), false)
	if err != nil || busy {
		t.Fatalf("owner serialization: busy=%v err=%v", busy, err)
	}
	defer release(t.Context())
	waiting, application := domainRemovalWaitingStore(t, pool)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := waiting.DeleteCustomDomainForAppWithActivity(ctx, domain, app.ID, OrgActivity{OrgID: uuid.New(), Kind: "domain.removed", ActorType: OrgActivityActorSystem, ActorLabel: "removal-test", ResourceType: "domain", ResourceLabel: domain, SourceType: "domain.removed", SourceID: "canceled-removal"})
		result <- err
	}()
	awaitDomainRemovalSessionWait(t, pool, application)
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled lock wait did not refuse: %v", err)
	}
	if after, err := store.DomainByName(t.Context(), domain); err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("cancellation changed domain: %+v err=%v", after, err)
	}
	if selected, err := store.DefaultCustomDomain(t.Context(), app.ID); err != nil || selected != domain {
		t.Fatalf("cancellation changed default: %q/%v", selected, err)
	}
	var outbox int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM org_activity_outbox`).Scan(&outbox); err != nil || outbox != 0 {
		t.Fatalf("cancellation wrote activity: %d/%v", outbox, err)
	}
	release(t.Context())
	retry, stop := context.WithTimeout(t.Context(), time.Second)
	defer stop()
	if err := waiting.DeleteCustomDomainForApp(retry, domain, app.ID); err != nil {
		t.Fatalf("cancellation leaked a lock or the only connection: %v", err)
	}
}

func TestPgTrafficDomainRemovalObservesPreviousFallbackPolicyCommit(t *testing.T) {
	store, pool, _, app := trafficHostPGFixture(t)
	peerAccount, peer := globalTrafficPeer(t, store, "serialized-fallback")
	const domain, fallback = "serialized.example.test", "*.example.test"
	for name, owner := range map[string]string{domain: app.ID, fallback: peer.ID} {
		if _, err := store.CreateCustomDomain(t.Context(), name, owner, "private-token"); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDomainVerified(t.Context(), name); err != nil {
			t.Fatal(err)
		}
	}
	_, release, busy, err := store.tryAcquireTrafficPolicySession(t.Context(), uuidToPgtype(peerAccount.ID), false)
	if err != nil || busy {
		t.Fatalf("fallback serialization: busy=%v err=%v", busy, err)
	}
	defer release(t.Context())
	waiting, application := domainRemovalWaitingStore(t, pool)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- waiting.DeleteCustomDomainForApp(ctx, domain, app.ID) }()
	awaitDomainRemovalSessionWait(t, pool, application)
	// Emulate a legacy writer's commit while it owns the fallback account.
	// The bounded removal snapshot must begin after this commit and unlock.
	intent := memTrafficRule(peerAccount, peer, domain, 520)
	legacy := EdgeRule{ID: uuid.NewString(), AccountID: peerAccount.ID, AppID: peer.ID, MatchHost: domain, MatchPath: "/", Enabled: true, Kind: intent.Kind, Action: intent.Action}
	seedPgDomainRemovalLegacy(t, pool, legacy)
	release(t.Context())
	var aggregate *TrafficPolicyAggregateError
	if err := <-result; !errors.As(err, &aggregate) || aggregate.Observed <= aggregate.Limit {
		t.Fatalf("removal missed the previous holder's policy: %v", err)
	}
	if claim, err := store.DomainByName(t.Context(), domain); err != nil || claim.AppID != app.ID {
		t.Fatalf("refusal changed exact claim: %+v/%v", claim, err)
	}
	if err := store.DeleteEdgeRule(t.Context(), legacy.ID); err != nil {
		t.Fatal(err)
	}
	if err := waiting.DeleteCustomDomainForApp(t.Context(), domain, app.ID); err != nil {
		t.Fatalf("repair did not restore removal: %v", err)
	}
}

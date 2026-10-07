package state_test

// adr: 615

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/runtimeupgrade"
	"github.com/onebox-faas/faas/pkg/gateway/ingress"
	"github.com/onebox-faas/faas/pkg/state"
)

func replacePublicSession(t *testing.T, s *state.PgStore, r state.RuntimeUpgradePublicEdgeRoster) state.RuntimeUpgradePublicEdgeRoster {
	t.Helper()
	m := append([]state.RuntimeUpgradePublicEdgeMember(nil), r.Members...)
	m[0].SessionID = uuid.NewString()
	next, err := s.ReviewRuntimeUpgradePublicEdgeRoster(t.Context(), r.Revision, r.GatewayRosterRevision, r.TopologySHA256, m)
	if err != nil {
		t.Fatal(err)
	}
	return next
}

func publicWithdrawalSnapshot(tk *ingress.ActivityTracker) func(string) (state.RuntimeUpgradePublicEdgeWithdrawalSnapshot, error) {
	return func(id string) (state.RuntimeUpgradePublicEdgeWithdrawalSnapshot, error) {
		a, err := tk.Withdraw(id)
		return state.RuntimeUpgradePublicEdgeWithdrawalSnapshot{ID: a.ID, FenceID: a.FenceID, Version: a.Version, Closed: a.Closed, Known: a.Known, Active: a.Active}, err
	}
}

func observePublicCoverage(t *testing.T, s *state.PgStore, r state.RuntimeUpgradePublicEdgeRoster) state.RuntimeUpgradePublicEdgeCoverageObservation {
	t.Helper()
	out, err := (runtimeupgrade.PublicEdgeControls{Store: s}).ObserveCoverage(t.Context(), r.Revision)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func pendingPublicWithdrawals(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM runtime_upgrade_public_edge_withdrawals w WHERE NOT EXISTS(SELECT 1 FROM runtime_upgrade_public_edge_withdrawal_receipts r WHERE r.withdrawal_id=w.id)`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPgPublicWithdrawalTransitionsPreserveIdentityAndNeverReenroll(t *testing.T) {
	s, pool, _, original := publicEdgeFixture(t)
	first := replacePublicSession(t, s, original)
	second := replacePublicSession(t, s, first)
	if n := pendingPublicWithdrawals(t, pool); n != 2 {
		t.Fatal("older withdrawal lost", n)
	}
	var oldRevision, config string
	if err := pool.QueryRow(t.Context(), `SELECT roster_revision::text,config_sha256 FROM runtime_upgrade_public_edge_withdrawals WHERE public_session_id=$1`, original.Members[0].SessionID).Scan(&oldRevision, &config); err != nil || oldRevision != original.Revision || config != original.Members[0].ConfigSHA256 {
		t.Fatal(oldRevision, config, err)
	}
	topology, err := s.ReviewRuntimeUpgradePublicEdgeRoster(t.Context(), second.Revision, second.GatewayRosterRevision, strings.Repeat("d", 64), second.Members)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := s.ReviewRuntimeUpgradePublicEdgeRoster(t.Context(), topology.Revision, topology.GatewayRosterRevision, topology.TopologySHA256, topology.Members)
	if err != nil || retry.Revision != topology.Revision || pendingPublicWithdrawals(t, pool) != 2 {
		t.Fatal("retry changed withdrawal intent", retry, err)
	}
	for _, change := range []func([]state.RuntimeUpgradePublicEdgeMember){
		func(m []state.RuntimeUpgradePublicEdgeMember) { m[0] = original.Members[0] },
		func(m []state.RuntimeUpgradePublicEdgeMember) { m[0].SlotID = uuid.NewString() },
		func(m []state.RuntimeUpgradePublicEdgeMember) { m[0].ConfigSHA256 = strings.Repeat("f", 64) },
	} {
		m := append([]state.RuntimeUpgradePublicEdgeMember(nil), topology.Members...)
		change(m)
		if _, err := s.ReviewRuntimeUpgradePublicEdgeRoster(t.Context(), topology.Revision, topology.GatewayRosterRevision, topology.TopologySHA256, m); !errors.Is(err, state.ErrConflict) {
			t.Fatal("session resurrected or mutated", err)
		}
	}
	for _, query := range []string{
		`UPDATE runtime_upgrade_public_edge_roster_head SET revision=$1`,
		`DELETE FROM runtime_upgrade_public_edge_withdrawals WHERE roster_revision=$1`,
		`UPDATE runtime_upgrade_public_edge_withdrawals SET config_sha256=repeat('e',64) WHERE roster_revision=$1`,
	} {
		if _, err := pool.Exec(t.Context(), query, original.Revision); err == nil {
			t.Fatal("permanent intent bypassed", query)
		}
	}
	if _, err := pool.Exec(t.Context(), `DELETE FROM runtime_upgrade_public_edge_roster_head`); err == nil {
		t.Fatal("head deleted")
	}
	if _, err := pool.Exec(t.Context(), `UPDATE runtime_upgrade_public_edge_roster_head SET revision=NULL`); err == nil {
		t.Fatal("head erased")
	}
	// Head capture also applies to a direct database publication.
	direct := uuid.NewString()
	if _, err := pool.Exec(t.Context(), `INSERT INTO runtime_upgrade_public_edge_rosters(revision,gateway_roster_revision,topology_sha256,slot_ids,public_sessions,config_sha256s) SELECT $1,gateway_roster_revision,topology_sha256,slot_ids,ARRAY[$2::uuid,public_sessions[2]],config_sha256s FROM runtime_upgrade_public_edge_rosters WHERE revision=$3`, direct, uuid.NewString(), topology.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE runtime_upgrade_public_edge_roster_head SET revision=$1`, direct); err != nil {
		t.Fatal(err)
	}
	if n := pendingPublicWithdrawals(t, pool); n != 3 {
		t.Fatal("direct publication skipped withdrawal", n)
	}
}

func TestPgPublicWithdrawalRepairRetainsPendingAndSealsStableZero(t *testing.T) {
	s, pool, gateway, r := publicEdgeFixture(t)
	old := r.Members[0]
	tk := ingress.NewActivityTracker()
	a, _ := tk.Begin()
	if err := a.Bind(ingress.Generation{PublicRevision: r.Revision, GatewayRevision: gateway.Revision}); err != nil {
		t.Fatal(err)
	}
	b, _ := tk.Begin()
	next := replacePublicSession(t, s, r)
	for _, m := range []state.RuntimeUpgradePublicEdgeMember{next.Members[0], {SlotID: old.SlotID, SessionID: old.SessionID, ConfigSHA256: strings.Repeat("f", 64)}} {
		called := false
		found, err := s.RepairRuntimeUpgradePublicEdgeWithdrawal(t.Context(), m, func(string) (state.RuntimeUpgradePublicEdgeWithdrawalSnapshot, error) {
			called = true
			return state.RuntimeUpgradePublicEdgeWithdrawalSnapshot{}, nil
		})
		if err != nil || found || called {
			t.Fatal("unreviewed callback fenced a process", found, called, err)
		}
	}
	if found, err := s.RepairRuntimeUpgradePublicEdgeWithdrawal(t.Context(), old, publicWithdrawalSnapshot(tk)); !found || err != nil {
		t.Fatal(found, err)
	}
	if _, err := tk.Begin(); err == nil {
		t.Fatal("withdrawn process reopened")
	}
	if err := b.Bind(ingress.Generation{PublicRevision: r.Revision, GatewayRevision: gateway.Revision}); err == nil {
		t.Fatal("late bind crossed fence")
	}
	if pendingPublicWithdrawals(t, pool) != 1 {
		t.Fatal("busy process sealed")
	}
	a.Finish()
	if _, err := s.RepairRuntimeUpgradePublicEdgeWithdrawal(t.Context(), old, publicWithdrawalSnapshot(tk)); err != nil {
		t.Fatal(err)
	}
	if pendingPublicWithdrawals(t, pool) != 1 {
		t.Fatal("pending authorization vanished")
	}
	b.Finish()
	if found, err := s.RepairRuntimeUpgradePublicEdgeWithdrawal(t.Context(), old, publicWithdrawalSnapshot(tk)); !found || err != nil {
		t.Fatal(found, err)
	}
	var fence string
	var version int64
	var at time.Time
	if err := pool.QueryRow(t.Context(), `SELECT fence_id::text,activity_version,observed_at FROM runtime_upgrade_public_edge_withdrawal_receipts`).Scan(&fence, &version, &at); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RepairRuntimeUpgradePublicEdgeWithdrawal(t.Context(), old, publicWithdrawalSnapshot(tk)); err != nil {
		t.Fatal(err)
	}
	var again time.Time
	if err := pool.QueryRow(t.Context(), `SELECT observed_at FROM runtime_upgrade_public_edge_withdrawal_receipts`).Scan(&again); err != nil || !again.Equal(at) {
		t.Fatal("receipt retry rewrote time", again, at, err)
	}
	for _, edit := range []func(*state.RuntimeUpgradePublicEdgeWithdrawalSnapshot){func(a *state.RuntimeUpgradePublicEdgeWithdrawalSnapshot) { a.FenceID = uuid.NewString() }, func(a *state.RuntimeUpgradePublicEdgeWithdrawalSnapshot) { a.Version++ }} {
		_, err := s.RepairRuntimeUpgradePublicEdgeWithdrawal(t.Context(), old, func(id string) (state.RuntimeUpgradePublicEdgeWithdrawalSnapshot, error) {
			a, _ := publicWithdrawalSnapshot(tk)(id)
			edit(&a)
			return a, nil
		})
		if !errors.Is(err, state.ErrConflict) {
			t.Fatal("conflicting receipt accepted", err)
		}
	}
	for _, query := range []string{`DELETE FROM runtime_upgrade_public_edge_withdrawal_receipts`, `UPDATE runtime_upgrade_public_edge_withdrawal_receipts SET activity_version=activity_version+1`} {
		if _, err := pool.Exec(t.Context(), query); err == nil {
			t.Fatal("sealed evidence rewritten")
		}
	}
	if pendingPublicWithdrawals(t, pool) != 0 || fence == "" || version < 1 {
		t.Fatal("zero not sealed")
	}
	if _, err := s.AuthorizeRuntimeUpgradePublicEdgeIngress(t.Context(), old, gateway.Members[0].SlotID, gateway.Members[0].SessionID); !errors.Is(err, state.ErrConflict) {
		t.Fatal("sealed old process authorized", err)
	}
	m := append([]state.RuntimeUpgradePublicEdgeMember(nil), next.Members...)
	m[0] = old
	if _, err := s.ReviewRuntimeUpgradePublicEdgeRoster(t.Context(), next.Revision, gateway.Revision, next.TopologySHA256, m); !errors.Is(err, state.ErrConflict) {
		t.Fatal("sealed old process re-enrolled", err)
	}
}

func TestPgPublicWithdrawalRejectsInvalidUnknownAndBorrowedEvidence(t *testing.T) {
	s, pool, _, r := publicEdgeFixture(t)
	old := r.Members[0]
	_ = replacePublicSession(t, s, r)
	for _, edit := range []func(*state.RuntimeUpgradePublicEdgeWithdrawalSnapshot){
		func(a *state.RuntimeUpgradePublicEdgeWithdrawalSnapshot) { a.ID = uuid.NewString() }, func(a *state.RuntimeUpgradePublicEdgeWithdrawalSnapshot) { a.FenceID = "bad" }, func(a *state.RuntimeUpgradePublicEdgeWithdrawalSnapshot) { a.Version = 0 }, func(a *state.RuntimeUpgradePublicEdgeWithdrawalSnapshot) { a.Closed = false }, func(a *state.RuntimeUpgradePublicEdgeWithdrawalSnapshot) { a.Active = -1 }, func(a *state.RuntimeUpgradePublicEdgeWithdrawalSnapshot) {
			a.Active = api.RuntimeUpgradeActivityForwardLimit + 1
		},
	} {
		_, err := s.RepairRuntimeUpgradePublicEdgeWithdrawal(t.Context(), old, func(id string) (state.RuntimeUpgradePublicEdgeWithdrawalSnapshot, error) {
			a := state.RuntimeUpgradePublicEdgeWithdrawalSnapshot{ID: id, FenceID: uuid.NewString(), Version: 1, Closed: true, Known: true}
			edit(&a)
			return a, nil
		})
		if !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatal("invalid evidence admitted", err)
		}
	}
	for _, a := range []state.RuntimeUpgradePublicEdgeWithdrawalSnapshot{{Version: 1, Closed: true, Known: false}, {Version: 1, Closed: true, Known: true, Active: 1}} {
		found, err := s.RepairRuntimeUpgradePublicEdgeWithdrawal(t.Context(), old, func(id string) (state.RuntimeUpgradePublicEdgeWithdrawalSnapshot, error) {
			a.ID, a.FenceID = id, uuid.NewString()
			return a, nil
		})
		if !found || err != nil || pendingPublicWithdrawals(t, pool) != 1 {
			t.Fatal("unknown or busy zero sealed", found, err)
		}
	}
	for _, query := range []string{
		`INSERT INTO runtime_upgrade_public_edge_withdrawals(id,slot_id,public_session_id,config_sha256,roster_revision) SELECT gen_random_uuid(),slot_ids[1],public_sessions[1],config_sha256s[1],revision FROM runtime_upgrade_public_edge_rosters JOIN runtime_upgrade_public_edge_roster_head USING(revision)`,
		`INSERT INTO runtime_upgrade_public_edge_withdrawal_receipts SELECT id,gen_random_uuid(),1,true,true,1,clock_timestamp() FROM runtime_upgrade_public_edge_withdrawals`,
		`INSERT INTO runtime_upgrade_public_edge_withdrawal_receipts SELECT id,gen_random_uuid(),1,false,true,0,clock_timestamp() FROM runtime_upgrade_public_edge_withdrawals`,
		`INSERT INTO runtime_upgrade_public_edge_withdrawal_receipts SELECT id,gen_random_uuid(),1,true,false,0,clock_timestamp() FROM runtime_upgrade_public_edge_withdrawals`,
		`INSERT INTO runtime_upgrade_public_edge_withdrawal_receipts SELECT id,gen_random_uuid(),1,true,true,0,clock_timestamp()+interval '1 minute' FROM runtime_upgrade_public_edge_withdrawals`,
	} {
		if _, err := pool.Exec(t.Context(), query); err == nil {
			t.Fatal("database admitted invalid withdrawal evidence", query)
		}
	}
	if _, err := s.RepairRuntimeUpgradePublicEdgeWithdrawal(t.Context(), old, nil); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatal(err)
	}
	if _, err := (runtimeupgrade.PublicEdgeControls{}).ObserveCoverage(t.Context(), r.Revision); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatal(err)
	}
}

func TestPgPublicWithdrawalCoverageIncludesEveryHistoricalProcess(t *testing.T) {
	s, pool, _, r := publicEdgeFixture(t)
	old := r.Members[0]
	first := replacePublicSession(t, s, r)
	intermediate := first.Members[0]
	next := replacePublicSession(t, s, first)
	for _, m := range next.Members {
		recordPublicActivity(t, s, m, state.RuntimeUpgradePublicEdgeActivity{Version: 1, Known: true})
	}
	out := observePublicCoverage(t, s, next)
	if out.Status != "pending" || out.Reason != "public_edge_withdrawal_pending" || len(out.PendingWithdrawals) != 2 || out.ConfirmedEdges != 2 || out.ValidForSeconds != 0 {
		t.Fatal("fresh current edges hid historical work", out)
	}
	for i, m := range []state.RuntimeUpgradePublicEdgeMember{old, intermediate} {
		if found, err := s.RepairRuntimeUpgradePublicEdgeWithdrawal(t.Context(), m, publicWithdrawalSnapshot(ingress.NewActivityTracker())); !found || err != nil {
			t.Fatal(found, err)
		}
		out = observePublicCoverage(t, s, next)
		if len(out.PendingWithdrawals) != 1-i {
			t.Fatal("withdrawal omitted", out)
		}
		if i == 0 && out.Status != "pending" {
			t.Fatal("one receipt covered two old processes", out)
		}
	}
	if out.Status != "coverage_observed" || out.ValidForSeconds < 1 || out.ConfirmedEdges != 2 {
		t.Fatal(out)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE runtime_upgrade_public_edge_activity SET observed_at=observed_at-interval '2 minutes',expires_at=expires_at-interval '2 minutes'`); err != nil {
		t.Fatal(err)
	}
	if got := observePublicCoverage(t, s, next); got.Status != "pending" || got.ValidForSeconds != 0 {
		t.Fatal("sealed withdrawals refreshed expired current activity", got)
	}
}

func TestPgPublicWithdrawalCoverageFencesHeadsAndUsesClockAfterWithdrawalWait(t *testing.T) {
	s, pool, gateway, r := publicEdgeFixture(t)
	for _, m := range r.Members {
		recordPublicActivity(t, s, m, state.RuntimeUpgradePublicEdgeActivity{Version: 1, Known: true})
	}
	if _, err := pool.Exec(t.Context(), `WITH at AS MATERIALIZED(SELECT clock_timestamp() AS now) UPDATE runtime_upgrade_public_edge_activity SET observed_at=at.now-interval '58 seconds',expires_at=at.now+interval '2 seconds' FROM at`); err != nil {
		t.Fatal(err)
	}
	blocker, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback(t.Context()) }()
	if _, err := blocker.Exec(t.Context(), `LOCK TABLE runtime_upgrade_public_edge_withdrawals IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	type result struct {
		out state.RuntimeUpgradePublicEdgeCoverageObservation
		err error
	}
	done := make(chan result, 1)
	go func() {
		out, err := s.ObserveRuntimeUpgradePublicEdgeCoverage(t.Context(), r.Revision)
		done <- result{out, err}
	}()
	waitPublicEdgeRead(t, pool, blocker)
	for _, review := range []func(context.Context) error{
		func(ctx context.Context) error {
			_, err := s.ReviewRuntimeUpgradePublicEdgeRoster(ctx, r.Revision, gateway.Revision, strings.Repeat("e", 64), r.Members)
			return err
		},
		func(ctx context.Context) error {
			m := append([]state.RuntimeUpgradeGatewayMember(nil), gateway.Members...)
			m[0].SessionID = uuid.NewString()
			_, err := s.ReviewRuntimeUpgradeGatewayRoster(ctx, gateway.Revision, m)
			return err
		},
	} {
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		err := review(ctx)
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("review crossed coverage fence", err)
		}
	}
	time.Sleep(2100 * time.Millisecond)
	if err := blocker.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	got := <-done
	if got.err != nil || got.out.Status != "pending" || got.out.ValidForSeconds != 0 || got.out.ConfirmedEdges != 0 {
		t.Fatal("withdrawal wait extended fact lease", got)
	}
}

func TestPgPublicWithdrawalReceiptWaitKeepsLocalFenceClosedAndClockFresh(t *testing.T) {
	s, pool, _, r := publicEdgeFixture(t)
	old := r.Members[0]
	next := replacePublicSession(t, s, r)
	blocker, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback(t.Context()) }()
	if _, err := blocker.Exec(t.Context(), `LOCK TABLE runtime_upgrade_public_edge_withdrawal_receipts IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	tk := ingress.NewActivityTracker()
	done := make(chan error, 1)
	go func() {
		_, err := s.RepairRuntimeUpgradePublicEdgeWithdrawal(t.Context(), old, publicWithdrawalSnapshot(tk))
		done <- err
	}()
	waitPublicEdgeRead(t, pool, blocker)
	if _, err := tk.Begin(); err == nil {
		t.Fatal("receipt wait left admission open")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	_, err = s.ReviewRuntimeUpgradePublicEdgeRoster(ctx, next.Revision, next.GatewayRosterRevision, strings.Repeat("e", 64), next.Members)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("review crossed receipt publication", err)
	}
	var released time.Time
	if err := blocker.QueryRow(t.Context(), `SELECT clock_timestamp()`).Scan(&released); err != nil {
		t.Fatal(err)
	}
	if err := blocker.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var observed time.Time
	if err := pool.QueryRow(t.Context(), `SELECT observed_at FROM runtime_upgrade_public_edge_withdrawal_receipts`).Scan(&observed); err != nil || observed.Before(released) {
		t.Fatal("receipt clock preceded lock wait", observed, released, err)
	}
}

func TestPgPublicWithdrawalCapacityRollsBackAndRecoversOnlyAfterSeal(t *testing.T) {
	s, pool, _, r := publicEdgeFixture(t)
	old := r.Members[0]
	for range api.RuntimeUpgradePublicEdgeWithdrawalLimit {
		r = replacePublicSession(t, s, r)
	}
	m := append([]state.RuntimeUpgradePublicEdgeMember(nil), r.Members...)
	m[0].SessionID = uuid.NewString()
	if _, err := s.ReviewRuntimeUpgradePublicEdgeRoster(t.Context(), r.Revision, r.GatewayRosterRevision, r.TopologySHA256, m); !errors.Is(err, state.ErrConflict) {
		t.Fatal("capacity overflow accepted", err)
	}
	if n := pendingPublicWithdrawals(t, pool); n != api.RuntimeUpgradePublicEdgeWithdrawalLimit {
		t.Fatal("failed transition lost history", n)
	}
	current, err := s.RuntimeUpgradePublicEdgeRoster(t.Context())
	if err != nil || current.Revision != r.Revision {
		t.Fatal("failed review changed head", current, err)
	}
	if _, err := s.RepairRuntimeUpgradePublicEdgeWithdrawal(t.Context(), old, publicWithdrawalSnapshot(ingress.NewActivityTracker())); err != nil {
		t.Fatal(err)
	}
	r = replacePublicSession(t, s, r)
	if pendingPublicWithdrawals(t, pool) != api.RuntimeUpgradePublicEdgeWithdrawalLimit {
		t.Fatal("capacity did not recover after real seal")
	}
}

func TestPgPublicWithdrawalLegacyBackfillPreservesOverflowAndIsIdempotent(t *testing.T) {
	s, pool, _, r := publicEdgeFixture(t)
	if _, err := pool.Exec(t.Context(), `ALTER TABLE runtime_upgrade_public_edge_roster_head DISABLE TRIGGER runtime_upgrade_public_edge_withdrawal_capture`); err != nil {
		t.Fatal(err)
	}
	for range api.RuntimeUpgradePublicEdgeWithdrawalLimit + 1 {
		r = replacePublicSession(t, s, r)
	}
	if _, err := pool.Exec(t.Context(), `ALTER TABLE runtime_upgrade_public_edge_roster_head ENABLE TRIGGER runtime_upgrade_public_edge_withdrawal_capture`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := pool.Exec(t.Context(), `SELECT seed_runtime_upgrade_public_edge_withdrawals()`); err != nil {
			t.Fatal(err)
		}
	}
	if n := pendingPublicWithdrawals(t, pool); n != api.RuntimeUpgradePublicEdgeWithdrawalLimit+1 {
		t.Fatal("legacy history truncated", n)
	}
	for _, m := range r.Members {
		recordPublicActivity(t, s, m, state.RuntimeUpgradePublicEdgeActivity{Version: 1, Known: true})
	}
	if out := observePublicCoverage(t, s, r); out.Status != "pending" || out.Reason != "public_edge_withdrawal_capacity_exceeded" || len(out.PendingWithdrawals) != api.RuntimeUpgradePublicEdgeWithdrawalLimit+1 {
		t.Fatal(out)
	}
	topology, err := s.ReviewRuntimeUpgradePublicEdgeRoster(t.Context(), r.Revision, r.GatewayRosterRevision, strings.Repeat("e", 64), r.Members)
	if err != nil || topology.Revision == r.Revision {
		t.Fatal("legacy overflow blocked harmless refresh", err)
	}
}

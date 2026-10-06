package state_test

// adr: 613

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/runtimeupgrade"
	"github.com/onebox-faas/faas/pkg/state"
)

func publicEdgeFixture(t *testing.T) (*state.PgStore, *pgxpool.Pool, state.RuntimeUpgradeGatewayRoster, state.RuntimeUpgradePublicEdgeRoster) {
	t.Helper()
	s, pool, _ := pgStoreWithPool(t)
	gateway := seedReviewedRuntimeUpgradeGateways(t, s, []string{uuid.NewString()})
	members := []state.RuntimeUpgradePublicEdgeMember{
		{SlotID: uuid.NewString(), SessionID: uuid.NewString(), ConfigSHA256: strings.Repeat("a", 64)},
		{SlotID: uuid.NewString(), SessionID: uuid.NewString(), ConfigSHA256: strings.Repeat("b", 64)},
	}
	r, err := (runtimeupgrade.PublicEdgeControls{Store: s}).Review(t.Context(), "", gateway.Revision, strings.Repeat("c", 64), members)
	if err != nil {
		t.Fatal(err)
	}
	return s, pool, gateway, r
}

func TestPgPublicEdgeReviewCASBoundsAndImmutableIntent(t *testing.T) {
	s, pool, gateway, roster := publicEdgeFixture(t)
	controls := runtimeupgrade.PublicEdgeControls{Store: s}
	got, err := controls.Status(t.Context())
	if err != nil || !reflect.DeepEqual(got, roster) {
		t.Fatal(got, err)
	}
	got.Members[0].SessionID = uuid.NewString()
	same, err := controls.Review(t.Context(), roster.Revision, gateway.Revision, roster.TopologySHA256, roster.Members)
	if err != nil || !reflect.DeepEqual(same, roster) {
		t.Fatal("exact retry changed review", same, err)
	}
	for _, members := range [][]state.RuntimeUpgradePublicEdgeMember{nil, {roster.Members[0], roster.Members[0]}, {{SlotID: uuid.NewString(), SessionID: roster.Members[0].SessionID, ConfigSHA256: "bad"}}, make([]state.RuntimeUpgradePublicEdgeMember, api.RuntimeUpgradePublicEdgeLimit+1)} {
		if _, err := controls.Review(t.Context(), roster.Revision, gateway.Revision, roster.TopologySHA256, members); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatal("invalid review admitted", err)
		}
	}
	if _, err := controls.Review(t.Context(), "", gateway.Revision, roster.TopologySHA256, roster.Members); !errors.Is(err, state.ErrConflict) {
		t.Fatal("missing CAS accepted", err)
	}
	if _, err := controls.Review(t.Context(), roster.Revision, uuid.NewString(), roster.TopologySHA256, roster.Members); !errors.Is(err, state.ErrConflict) {
		t.Fatal("unreviewed internal binding accepted", err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, digest := range []string{strings.Repeat("d", 64), strings.Repeat("e", 64)} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := controls.Review(t.Context(), roster.Revision, gateway.Revision, digest, roster.Members)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	success, conflict := 0, 0
	for err := range errs {
		if err == nil {
			success++
		} else if errors.Is(err, state.ErrConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal(success, conflict)
	}
	for _, query := range []string{`UPDATE runtime_upgrade_public_edge_rosters SET topology_sha256=repeat('f',64) WHERE revision=$1`, `DELETE FROM runtime_upgrade_public_edge_rosters WHERE revision=$1`} {
		if _, err := pool.Exec(t.Context(), query, roster.Revision); err == nil {
			t.Fatal("immutable history modified")
		}
	}
	if _, err := (runtimeupgrade.PublicEdgeControls{}).Observe(t.Context(), roster.Revision); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatal(err)
	}
}

func TestPgPublicEdgeObservationRequiresEveryCurrentGuardAndRejectsStaleReviews(t *testing.T) {
	s, pool, gateway, roster := publicEdgeFixture(t)
	observe := func() state.RuntimeUpgradePublicEdgeObservation {
		t.Helper()
		out, err := s.ObserveRuntimeUpgradePublicEdges(t.Context(), roster.Revision)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if out := observe(); out.Status != "pending" || out.ExpectedEdges != 2 || out.ConfirmedEdges != 0 {
		t.Fatal(out)
	}
	for i, m := range roster.Members {
		if err := s.RecordRuntimeUpgradePublicEdgeGuard(t.Context(), m); err != nil {
			t.Fatal(err)
		}
		out := observe()
		if out.ConfirmedEdges != i+1 || out.ExpectedEdges != 2 {
			t.Fatal(out)
		}
	}
	if out := observe(); out.Status != "guards_observed" || out.TopologySHA256 != roster.TopologySHA256 || out.GatewayRosterRevision != gateway.Revision || out.ValidForSeconds < 1 || out.ValidForSeconds > 60 {
		t.Fatal(out)
	}
	for _, edit := range []func(*state.RuntimeUpgradePublicEdgeMember){func(m *state.RuntimeUpgradePublicEdgeMember) { m.SessionID = uuid.NewString() }, func(m *state.RuntimeUpgradePublicEdgeMember) { m.ConfigSHA256 = strings.Repeat("f", 64) }, func(m *state.RuntimeUpgradePublicEdgeMember) { m.SlotID = uuid.NewString() }} {
		m := roster.Members[0]
		edit(&m)
		if err := s.RecordRuntimeUpgradePublicEdgeGuard(t.Context(), m); !errors.Is(err, state.ErrConflict) {
			t.Fatal("unreviewed fact accepted", m, err)
		}
	}
	for _, shift := range []string{"1 minute", "-2 minutes", "-59.5 seconds"} {
		if _, err := pool.Exec(t.Context(), `WITH at AS MATERIALIZED (SELECT clock_timestamp()+$2::interval AS now) UPDATE runtime_upgrade_public_edge_guards SET observed_at=at.now,expires_at=at.now+interval '1 minute' FROM at WHERE slot_id=$1`, roster.Members[0].SlotID, shift); err != nil {
			t.Fatal(err)
		}
		if out := observe(); out.Status != "pending" || out.ConfirmedEdges != 1 || out.ExpectedEdges != 2 {
			t.Fatal("bad lease removed a desired edge", shift, out)
		}
	}
	if _, err := pool.Exec(t.Context(), `UPDATE runtime_upgrade_public_edge_guards SET guard_enabled=false WHERE slot_id=$1`, roster.Members[0].SlotID); err == nil {
		t.Fatal("unguarded fact stored")
	}
	if _, err := pool.Exec(t.Context(), `UPDATE runtime_upgrade_public_edge_guards SET public_session_id=$2 WHERE slot_id=$1`, roster.Members[0].SlotID, uuid.NewString()); err == nil {
		t.Fatal("direct SQL borrowed process authority")
	}
	next, err := s.ReviewRuntimeUpgradePublicEdgeRoster(t.Context(), roster.Revision, gateway.Revision, strings.Repeat("d", 64), roster.Members)
	if err != nil {
		t.Fatal(err)
	}
	if out := observe(); out.Reason != "public_edge_membership_changed" {
		t.Fatal(out)
	}
	roster = next
	if out := observe(); out.Status != "pending" || out.ConfirmedEdges != 0 {
		t.Fatal("topology revision reused observations", out)
	}
	for _, m := range roster.Members {
		if err := s.RecordRuntimeUpgradePublicEdgeGuard(t.Context(), m); err != nil {
			t.Fatal(err)
		}
	}
	gateway.Members[0].SessionID = uuid.NewString()
	gateway, err = s.ReviewRuntimeUpgradeGatewayRoster(t.Context(), gateway.Revision, gateway.Members)
	if err != nil {
		t.Fatal(err)
	}
	if out := observe(); out.Reason != "gateway_membership_changed" {
		t.Fatal("internal review reused public evidence", out)
	}
	if err := s.RecordRuntimeUpgradePublicEdgeGuard(t.Context(), roster.Members[0]); !errors.Is(err, state.ErrConflict) {
		t.Fatal("old edge inventory implicitly rebound", err)
	}
	roster, err = s.ReviewRuntimeUpgradePublicEdgeRoster(t.Context(), roster.Revision, gateway.Revision, roster.TopologySHA256, roster.Members)
	if err != nil {
		t.Fatal(err)
	}
	if out := observe(); out.ConfirmedEdges != 0 {
		t.Fatal(out)
	}
	old := roster.Members[0]
	roster.Members[0].SessionID = uuid.NewString()
	roster, err = s.ReviewRuntimeUpgradePublicEdgeRoster(t.Context(), roster.Revision, gateway.Revision, roster.TopologySHA256, roster.Members)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RecordRuntimeUpgradePublicEdgeGuard(t.Context(), old); !errors.Is(err, state.ErrConflict) {
		t.Fatal("old public process resurrected", err)
	}
	for _, m := range roster.Members {
		if err := s.RecordRuntimeUpgradePublicEdgeGuard(t.Context(), m); err != nil {
			t.Fatal(err)
		}
	}
	if out := observe(); out.Status != "guards_observed" {
		t.Fatal("fresh review did not recover", out)
	}
}

func waitPublicEdgeRead(t *testing.T, pool *pgxpool.Pool, blocker pgx.Tx) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		var blocked bool
		if err := pool.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))`, int32(blocker.Conn().PgConn().PID())).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("edge operation did not reach fact read")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestPgPublicEdgeObservationFencesBothReviewsAndExpiresAfterReadWait(t *testing.T) {
	s, pool, gateway, roster := publicEdgeFixture(t)
	for _, m := range roster.Members {
		if err := s.RecordRuntimeUpgradePublicEdgeGuard(t.Context(), m); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(t.Context(), `WITH at AS MATERIALIZED (SELECT clock_timestamp() AS now) UPDATE runtime_upgrade_public_edge_guards SET observed_at=at.now-interval '58 seconds',expires_at=at.now+interval '2 seconds' FROM at`); err != nil {
		t.Fatal(err)
	}
	blocker, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback(t.Context()) }()
	if _, err := blocker.Exec(t.Context(), `LOCK TABLE runtime_upgrade_public_edge_guards IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	type result struct {
		out state.RuntimeUpgradePublicEdgeObservation
		err error
	}
	done := make(chan result, 1)
	go func() { out, err := s.ObserveRuntimeUpgradePublicEdges(ctx, roster.Revision); done <- result{out, err} }()
	waitPublicEdgeRead(t, pool, blocker)
	for _, review := range []func(context.Context) error{
		func(ctx context.Context) error {
			_, err := s.ReviewRuntimeUpgradePublicEdgeRoster(ctx, roster.Revision, gateway.Revision, strings.Repeat("d", 64), roster.Members)
			return err
		},
		func(ctx context.Context) error {
			members := append([]state.RuntimeUpgradeGatewayMember(nil), gateway.Members...)
			members[0].SessionID = uuid.NewString()
			_, err := s.ReviewRuntimeUpgradeGatewayRoster(ctx, gateway.Revision, members)
			return err
		},
	} {
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		err := review(ctx)
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("review crossed observation fence", err)
		}
	}
	time.Sleep(2100 * time.Millisecond)
	if err := blocker.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	r := <-done
	if r.err != nil || r.out.Status != "pending" || r.out.ValidForSeconds != 0 || r.out.ConfirmedEdges != 0 {
		t.Fatal("read wait extended guard lease", r)
	}
}

func TestPgPublicEdgeFactPublicationFencesReviewAndUsesClockAfterWait(t *testing.T) {
	s, pool, gateway, roster := publicEdgeFixture(t)
	blocker, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback(t.Context()) }()
	if _, err := blocker.Exec(t.Context(), `LOCK TABLE runtime_upgrade_public_edge_guards IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.RecordRuntimeUpgradePublicEdgeGuard(t.Context(), roster.Members[0]) }()
	waitPublicEdgeRead(t, pool, blocker)
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	_, err = s.ReviewRuntimeUpgradePublicEdgeRoster(ctx, roster.Revision, gateway.Revision, strings.Repeat("d", 64), roster.Members)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("publication did not fence review", err)
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
	var observed, expires time.Time
	if err := pool.QueryRow(t.Context(), `SELECT observed_at,expires_at FROM runtime_upgrade_public_edge_guards WHERE slot_id=$1`, roster.Members[0].SlotID).Scan(&observed, &expires); err != nil {
		t.Fatal(err)
	}
	if observed.Before(released) || !expires.Equal(observed.Add(time.Minute)) {
		t.Fatal("publication borrowed pre-wait clock", observed, released, expires)
	}
}

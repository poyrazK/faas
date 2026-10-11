package state_test

// adr: 700

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/runtimeupgrade"
	"github.com/onebox-faas/faas/pkg/gateway/ingress"
	"github.com/onebox-faas/faas/pkg/state"
)

func recordPublicActivity(t *testing.T, s *state.PgStore, m state.RuntimeUpgradePublicEdgeMember, a state.RuntimeUpgradePublicEdgeActivity) {
	t.Helper()
	if err := s.RecordRuntimeUpgradePublicEdgeActivity(t.Context(), m, func(state.RuntimeUpgradeIngressGeneration) state.RuntimeUpgradePublicEdgeActivity { return a }); err != nil {
		t.Fatal(err)
	}
}

func trackerPublicSnapshot(tk *ingress.ActivityTracker) func(state.RuntimeUpgradeIngressGeneration) state.RuntimeUpgradePublicEdgeActivity {
	return func(g state.RuntimeUpgradeIngressGeneration) state.RuntimeUpgradePublicEdgeActivity {
		a := tk.Snapshot(ingress.Generation{PublicRevision: g.PublicRevision, GatewayRevision: g.GatewayRevision})
		return state.RuntimeUpgradePublicEdgeActivity{Version: a.Version, Known: a.Known, Pending: a.Pending, Current: a.Current, Previous: a.Previous}
	}
}

func TestPgPublicAdmissionRequiresExactBothReviewsAndLiveReceiver(t *testing.T) {
	s, pool, gateway, roster := publicEdgeFixture(t)
	m, internal := roster.Members[0], gateway.Members[0]
	b, err := s.AuthorizeRuntimeUpgradePublicEdgeIngress(t.Context(), m, internal.SlotID, internal.SessionID)
	if err != nil || b.PublicRevision != roster.Revision || b.GatewayRevision != gateway.Revision || b.GatewayRosterRevision != gateway.Revision || b.SlotID != internal.SlotID || b.SessionID != internal.SessionID || b.ValidForSeconds < 1 || b.ValidForSeconds > 60 {
		t.Fatal(b, err)
	}
	for _, edit := range []func(*state.RuntimeUpgradePublicEdgeMember){func(m *state.RuntimeUpgradePublicEdgeMember) { m.SessionID = uuid.NewString() }, func(m *state.RuntimeUpgradePublicEdgeMember) { m.ConfigSHA256 = strings.Repeat("f", 64) }, func(m *state.RuntimeUpgradePublicEdgeMember) { m.SlotID = uuid.NewString() }} {
		wrong := m
		edit(&wrong)
		if _, err := s.AuthorizeRuntimeUpgradePublicEdgeIngress(t.Context(), wrong, internal.SlotID, internal.SessionID); !errors.Is(err, state.ErrConflict) {
			t.Fatal("unreviewed public process admitted", err)
		}
	}
	if _, err := s.AuthorizeRuntimeUpgradePublicEdgeIngress(t.Context(), m, internal.SlotID, uuid.NewString()); !errors.Is(err, state.ErrConflict) {
		t.Fatal("wrong receiver admitted", err)
	}
	for _, shift := range []string{"1 minute", "-2 minutes", "-59.5 seconds"} {
		if _, err := pool.Exec(t.Context(), `WITH at AS MATERIALIZED (SELECT clock_timestamp()+$2::interval AS now) UPDATE runtime_upgrade_gateway_heartbeats SET seen_at=at.now,expires_at=at.now+interval '1 minute' FROM at WHERE slot_id=$1`, internal.SlotID, shift); err != nil {
			t.Fatal(err)
		}
		if _, err := s.AuthorizeRuntimeUpgradePublicEdgeIngress(t.Context(), m, internal.SlotID, internal.SessionID); !errors.Is(err, state.ErrConflict) {
			t.Fatal("bad receiver lease admitted", shift, err)
		}
	}
	gateway.Members[0].SessionID = uuid.NewString()
	gateway, err = s.ReviewRuntimeUpgradeGatewayRoster(t.Context(), gateway.Revision, gateway.Members)
	if err != nil {
		t.Fatal(err)
	}
	internal = gateway.Members[0]
	if err := s.HeartbeatRuntimeUpgradeGateway(t.Context(), internal.SlotID, internal.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AuthorizeRuntimeUpgradePublicEdgeIngress(t.Context(), m, internal.SlotID, internal.SessionID); !errors.Is(err, state.ErrConflict) {
		t.Fatal("public inventory implicitly rebound", err)
	}
	roster, err = s.ReviewRuntimeUpgradePublicEdgeRoster(t.Context(), roster.Revision, gateway.Revision, roster.TopologySHA256, roster.Members)
	if err != nil {
		t.Fatal(err)
	}
	if b, err := s.AuthorizeRuntimeUpgradePublicEdgeIngress(t.Context(), m, internal.SlotID, internal.SessionID); err != nil || b.PublicRevision != roster.Revision {
		t.Fatal(b, err)
	}
}

func TestPgPublicActivityRetainsPendingAndLateOlderAdmissionsAcrossReviews(t *testing.T) {
	s, _, gateway, roster := publicEdgeFixture(t)
	controls := runtimeupgrade.PublicEdgeControls{Store: s}
	tk := ingress.NewActivityTracker()
	a, _ := tk.Begin()
	m, internal := roster.Members[0], gateway.Members[0]
	old, err := s.AuthorizeRuntimeUpgradePublicEdgeIngress(t.Context(), m, internal.SlotID, internal.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RecordRuntimeUpgradePublicEdgeActivity(t.Context(), m, trackerPublicSnapshot(tk)); err != nil {
		t.Fatal(err)
	}
	if out, err := controls.ObserveActivity(t.Context(), roster.Revision); err != nil || out.Pending != 1 || out.ExpectedEdges != 2 || out.ConfirmedEdges != 0 || out.Status != "pending" {
		t.Fatal(out, err)
	}
	roster, err = s.ReviewRuntimeUpgradePublicEdgeRoster(t.Context(), roster.Revision, gateway.Revision, strings.Repeat("d", 64), roster.Members)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := controls.ObserveActivity(t.Context(), old.PublicRevision); err != nil || out.Reason != "public_edge_membership_changed" {
		t.Fatal(out, err)
	}
	// Authorization returned before review; binding arrives after review. It
	// must remain pending/previous through publication, never become current.
	if err := s.RecordRuntimeUpgradePublicEdgeActivity(t.Context(), m, trackerPublicSnapshot(tk)); err != nil {
		t.Fatal(err)
	}
	if err := a.Bind(ingress.Generation{PublicRevision: old.PublicRevision, GatewayRevision: old.GatewayRevision}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordRuntimeUpgradePublicEdgeActivity(t.Context(), m, trackerPublicSnapshot(tk)); err != nil {
		t.Fatal(err)
	}
	recordPublicActivity(t, s, roster.Members[1], state.RuntimeUpgradePublicEdgeActivity{Version: 1, Known: true})
	if out, err := controls.ObserveActivity(t.Context(), roster.Revision); err != nil || out.Previous != 1 || out.Pending != 0 || out.ConfirmedEdges != 1 || out.Status != "pending" {
		t.Fatal(out, err)
	}
	b, _ := tk.Begin()
	current, err := s.AuthorizeRuntimeUpgradePublicEdgeIngress(t.Context(), m, internal.SlotID, internal.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Bind(ingress.Generation{PublicRevision: current.PublicRevision, GatewayRevision: current.GatewayRevision}); err != nil {
		t.Fatal(err)
	}
	a.Finish()
	if err := s.RecordRuntimeUpgradePublicEdgeActivity(t.Context(), m, trackerPublicSnapshot(tk)); err != nil {
		t.Fatal(err)
	}
	if out, err := controls.ObserveActivity(t.Context(), roster.Revision); err != nil || out.Current != 1 || out.Previous != 0 || out.Status != "activity_observed" || out.ValidForSeconds < 1 || out.ValidForSeconds > 60 {
		t.Fatal(out, err)
	}
	// A second transition makes the still-open current stream previous again.
	roster, err = s.ReviewRuntimeUpgradePublicEdgeRoster(t.Context(), roster.Revision, gateway.Revision, strings.Repeat("e", 64), roster.Members)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RecordRuntimeUpgradePublicEdgeActivity(t.Context(), m, trackerPublicSnapshot(tk)); err != nil {
		t.Fatal(err)
	}
	recordPublicActivity(t, s, roster.Members[1], state.RuntimeUpgradePublicEdgeActivity{Version: 1, Known: true})
	if out, err := controls.ObserveActivity(t.Context(), roster.Revision); err != nil || out.Previous != 1 || out.Status != "pending" {
		t.Fatal(out, err)
	}
	b.Finish()
	if err := s.RecordRuntimeUpgradePublicEdgeActivity(t.Context(), m, trackerPublicSnapshot(tk)); err != nil {
		t.Fatal(err)
	}
	if out, err := controls.ObserveActivity(t.Context(), roster.Revision); err != nil || out.Status != "activity_observed" {
		t.Fatal(out, err)
	}
}

func TestPgPublicActivityBoundsVersionsAndUnknownCoverage(t *testing.T) {
	s, pool, _, roster := publicEdgeFixture(t)
	m := roster.Members[0]
	good := state.RuntimeUpgradePublicEdgeActivity{Version: 2, Known: true, Current: 1}
	recordPublicActivity(t, s, m, good)
	recordPublicActivity(t, s, m, good) // unchanged activity can renew its lease
	for _, bad := range []state.RuntimeUpgradePublicEdgeActivity{{Version: 1, Known: true}, {Version: 2, Known: true}, {Version: 3, Known: true, Pending: -1}, {Version: 3, Known: true, Current: api.RuntimeUpgradeActivityForwardLimit, Previous: 1}, {Version: 0, Known: true}} {
		if err := s.RecordRuntimeUpgradePublicEdgeActivity(t.Context(), m, func(state.RuntimeUpgradeIngressGeneration) state.RuntimeUpgradePublicEdgeActivity { return bad }); err == nil {
			t.Fatal("bad activity accepted", bad)
		}
	}
	for _, query := range []string{`UPDATE runtime_upgrade_public_edge_activity SET activity_version=1`, `UPDATE runtime_upgrade_public_edge_activity SET current_forwards=-1`, `UPDATE runtime_upgrade_public_edge_activity SET current_forwards=65536,previous_forwards=1`, `UPDATE runtime_upgrade_public_edge_activity SET public_session_id=$1`, `UPDATE runtime_upgrade_public_edge_activity SET guard_enabled=false`, `UPDATE runtime_upgrade_public_edge_activity SET expires_at=observed_at+interval '2 minutes'`} {
		args := []any{}
		if strings.Contains(query, "$1") {
			args = append(args, uuid.NewString())
		}
		if _, err := pool.Exec(t.Context(), query, args...); err == nil {
			t.Fatal("direct SQL bypassed activity contract", query)
		}
	}
	recordPublicActivity(t, s, m, state.RuntimeUpgradePublicEdgeActivity{Version: 3, Known: false})
	recordPublicActivity(t, s, roster.Members[1], state.RuntimeUpgradePublicEdgeActivity{Version: 1, Known: true})
	if out, err := s.ObserveRuntimeUpgradePublicEdgeActivity(t.Context(), roster.Revision); err != nil || out.ConfirmedEdges != 1 || out.Status != "pending" {
		t.Fatal("unknown zero confirmed", out, err)
	}
	if err := s.RecordRuntimeUpgradePublicEdgeActivity(t.Context(), m, func(state.RuntimeUpgradeIngressGeneration) state.RuntimeUpgradePublicEdgeActivity {
		return state.RuntimeUpgradePublicEdgeActivity{Version: 4, Known: true}
	}); err == nil {
		t.Fatal("unknown coverage recovered in same review")
	}
	if err := s.RecordRuntimeUpgradePublicEdgeActivity(t.Context(), m, nil); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatal(err)
	}
	if _, err := (runtimeupgrade.PublicEdgeControls{}).ObserveActivity(t.Context(), roster.Revision); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatal(err)
	}
}

func TestPgPublicActivityLeaseMembershipAndRestartInvalidation(t *testing.T) {
	s, pool, gateway, roster := publicEdgeFixture(t)
	for _, m := range roster.Members {
		recordPublicActivity(t, s, m, state.RuntimeUpgradePublicEdgeActivity{Version: 1, Known: true})
	}
	for _, shift := range []string{"1 minute", "-2 minutes", "-59.5 seconds"} {
		if _, err := pool.Exec(t.Context(), `WITH at AS MATERIALIZED (SELECT clock_timestamp()+$2::interval AS now) UPDATE runtime_upgrade_public_edge_activity SET observed_at=at.now,expires_at=at.now+interval '1 minute' FROM at WHERE slot_id=$1`, roster.Members[0].SlotID, shift); err != nil {
			t.Fatal(err)
		}
		if out, err := s.ObserveRuntimeUpgradePublicEdgeActivity(t.Context(), roster.Revision); err != nil || out.ConfirmedEdges != 1 || out.ExpectedEdges != 2 || out.Status != "pending" {
			t.Fatal(shift, out, err)
		}
	}
	old := roster.Members[0]
	roster.Members[0].SessionID = uuid.NewString()
	var err error
	roster, err = s.ReviewRuntimeUpgradePublicEdgeRoster(t.Context(), roster.Revision, gateway.Revision, roster.TopologySHA256, roster.Members)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := s.ObserveRuntimeUpgradePublicEdgeActivity(t.Context(), roster.Revision); err != nil || out.ConfirmedEdges != 0 {
		t.Fatal("replacement reused observations", out, err)
	}
	called := false
	if err := s.RecordRuntimeUpgradePublicEdgeActivity(t.Context(), old, func(state.RuntimeUpgradeIngressGeneration) state.RuntimeUpgradePublicEdgeActivity {
		called = true
		return state.RuntimeUpgradePublicEdgeActivity{Version: 1, Known: true}
	}); !errors.Is(err, state.ErrConflict) || called {
		t.Fatal("withdrawn process published", err, called)
	}
	gateway.Members[0].SessionID = uuid.NewString()
	gateway, err = s.ReviewRuntimeUpgradeGatewayRoster(t.Context(), gateway.Revision, gateway.Members)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := s.ObserveRuntimeUpgradePublicEdgeActivity(t.Context(), roster.Revision); err != nil || out.Reason != "gateway_membership_changed" {
		t.Fatal(out, err)
	}
}

func TestPgPublicActivitySnapshotsAfterWaitAndFencesBothReviews(t *testing.T) {
	s, pool, gateway, roster := publicEdgeFixture(t)
	blocker, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback(t.Context()) }()
	if _, err := blocker.Exec(t.Context(), `LOCK TABLE runtime_upgrade_public_edge_activity IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	called, done := make(chan state.RuntimeUpgradeIngressGeneration, 1), make(chan error, 1)
	go func() {
		done <- s.RecordRuntimeUpgradePublicEdgeActivity(t.Context(), roster.Members[0], func(g state.RuntimeUpgradeIngressGeneration) state.RuntimeUpgradePublicEdgeActivity {
			called <- g
			return state.RuntimeUpgradePublicEdgeActivity{Version: 1, Known: true}
		})
	}()
	waitPublicEdgeRead(t, pool, blocker)
	select {
	case <-called:
		t.Fatal("snapshot taken before fact table wait")
	default:
	}
	for _, review := range []func(context.Context) error{func(ctx context.Context) error {
		_, err := s.ReviewRuntimeUpgradePublicEdgeRoster(ctx, roster.Revision, gateway.Revision, strings.Repeat("d", 64), roster.Members)
		return err
	}, func(ctx context.Context) error {
		members := append([]state.RuntimeUpgradeGatewayMember(nil), gateway.Members...)
		members[0].SessionID = uuid.NewString()
		_, err := s.ReviewRuntimeUpgradeGatewayRoster(ctx, gateway.Revision, members)
		return err
	}} {
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		err := review(ctx)
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("review crossed snapshot fence", err)
		}
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
	if g := <-called; g.PublicRevision != roster.Revision || g.GatewayRevision != gateway.Revision {
		t.Fatal(g)
	}
	var observed time.Time
	if err := pool.QueryRow(t.Context(), `SELECT observed_at FROM runtime_upgrade_public_edge_activity WHERE slot_id=$1`, roster.Members[0].SlotID).Scan(&observed); err != nil || observed.Before(released) {
		t.Fatal("publication extended pre-wait observation", observed, released, err)
	}
}

func TestPgPublicActivityObservationExpiresDuringWait(t *testing.T) {
	s, pool, gateway, roster := publicEdgeFixture(t)
	for _, m := range roster.Members {
		recordPublicActivity(t, s, m, state.RuntimeUpgradePublicEdgeActivity{Version: 1, Known: true})
	}
	if _, err := pool.Exec(t.Context(), `WITH at AS MATERIALIZED (SELECT clock_timestamp() AS now) UPDATE runtime_upgrade_public_edge_activity SET observed_at=at.now-interval '58 seconds',expires_at=at.now+interval '2 seconds' FROM at`); err != nil {
		t.Fatal(err)
	}
	blocker, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback(t.Context()) }()
	if _, err := blocker.Exec(t.Context(), `LOCK TABLE runtime_upgrade_public_edge_activity IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	type result struct {
		out state.RuntimeUpgradePublicEdgeActivityObservation
		err error
	}
	done := make(chan result, 1)
	go func() {
		out, err := s.ObserveRuntimeUpgradePublicEdgeActivity(t.Context(), roster.Revision)
		done <- result{out, err}
	}()
	waitPublicEdgeRead(t, pool, blocker)
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	_, err = s.ReviewRuntimeUpgradePublicEdgeRoster(ctx, roster.Revision, gateway.Revision, strings.Repeat("d", 64), roster.Members)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("observation did not fence review", err)
	}
	time.Sleep(2100 * time.Millisecond)
	if err := blocker.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	r := <-done
	if r.err != nil || r.out.Status != "pending" || r.out.ConfirmedEdges != 0 || r.out.ValidForSeconds != 0 {
		t.Fatal("wait revived activity lease", r)
	}
}

func TestPgPublicAdmissionFencesReviewsAndExpiresAfterHeartbeatWait(t *testing.T) {
	s, pool, gateway, roster := publicEdgeFixture(t)
	internal := gateway.Members[0]
	if _, err := pool.Exec(t.Context(), `WITH at AS MATERIALIZED (SELECT clock_timestamp() AS now) UPDATE runtime_upgrade_gateway_heartbeats SET seen_at=at.now-interval '58 seconds',expires_at=at.now+interval '2 seconds' FROM at`); err != nil {
		t.Fatal(err)
	}
	blocker, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback(t.Context()) }()
	if _, err := blocker.Exec(t.Context(), `LOCK TABLE runtime_upgrade_gateway_heartbeats IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := s.AuthorizeRuntimeUpgradePublicEdgeIngress(t.Context(), roster.Members[0], internal.SlotID, internal.SessionID)
		done <- err
	}()
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
			t.Fatal("admission read did not fence both heads", err)
		}
	}
	time.Sleep(2100 * time.Millisecond)
	if err := blocker.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, state.ErrConflict) {
		t.Fatal("heartbeat read wait extended admission lease", err)
	}
}

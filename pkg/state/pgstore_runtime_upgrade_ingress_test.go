package state_test

// adr: 698

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgRuntimeUpgradeIngressRequiresCurrentPairAndLiveHeartbeat(t *testing.T) {
	s, pool, _ := pgStoreWithPool(t)
	member := state.RuntimeUpgradeGatewayMember{SlotID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", SessionID: uuid.NewString()}
	if _, err := s.AuthorizeRuntimeUpgradeGatewayIngress(t.Context(), member.SlotID, member.SessionID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("unreviewed ingress authorized", err)
	}
	roster, err := s.ReviewRuntimeUpgradeGatewayRoster(t.Context(), "", []state.RuntimeUpgradeGatewayMember{member})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AuthorizeRuntimeUpgradeGatewayIngress(t.Context(), member.SlotID, member.SessionID); !errors.Is(err, state.ErrConflict) {
		t.Fatal("missing heartbeat authorized", err)
	}
	if err := s.HeartbeatRuntimeUpgradeGateway(t.Context(), member.SlotID, member.SessionID); err != nil {
		t.Fatal(err)
	}
	good, err := s.AuthorizeRuntimeUpgradeGatewayIngress(t.Context(), member.SlotID, member.SessionID)
	if err != nil || good.GatewayRosterRevision != roster.Revision || good.SlotID != member.SlotID || good.SessionID != member.SessionID || good.ValidForSeconds < 1 || good.ValidForSeconds > 60 || good.CheckedAt.IsZero() {
		t.Fatal(good, err)
	}
	for _, bad := range []state.RuntimeUpgradeGatewayMember{
		{SlotID: uuid.NewString(), SessionID: member.SessionID}, {SlotID: member.SlotID, SessionID: uuid.NewString()},
	} {
		if _, err := s.AuthorizeRuntimeUpgradeGatewayIngress(t.Context(), bad.SlotID, bad.SessionID); !errors.Is(err, state.ErrConflict) {
			t.Fatal("unknown slot/process admitted", err)
		}
	}
	if _, err := s.AuthorizeRuntimeUpgradeGatewayIngress(t.Context(), strings.ToUpper(member.SlotID), member.SessionID); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatal("noncanonical identity accepted", err)
	}
	// Preserve the exact database TTL while testing future, expired and
	// subsecond liveness; these timestamps are synthetic, not native proof.
	for _, shift := range []string{"1 minute", "-2 minutes", "-59.5 seconds"} {
		if _, err := pool.Exec(t.Context(), `WITH at AS MATERIALIZED (SELECT clock_timestamp()+$2::interval AS now) UPDATE runtime_upgrade_gateway_heartbeats SET seen_at=at.now,expires_at=at.now+interval '1 minute' FROM at WHERE slot_id=$1`, member.SlotID, shift); err != nil {
			t.Fatal(err)
		}
		if _, err := s.AuthorizeRuntimeUpgradeGatewayIngress(t.Context(), member.SlotID, member.SessionID); !errors.Is(err, state.ErrConflict) {
			t.Fatal("future/expired/subsecond ingress authorized", shift, err)
		}
	}
	current, err := s.RuntimeUpgradeGatewayRoster(t.Context())
	if err != nil || !reflect.DeepEqual(current, roster) {
		t.Fatal("probing changed desired membership", current, err)
	}
	replacement := state.RuntimeUpgradeGatewayMember{SlotID: member.SlotID, SessionID: uuid.NewString()}
	next, err := s.ReviewRuntimeUpgradeGatewayRoster(t.Context(), roster.Revision, []state.RuntimeUpgradeGatewayMember{replacement})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AuthorizeRuntimeUpgradeGatewayIngress(t.Context(), member.SlotID, member.SessionID); !errors.Is(err, state.ErrConflict) {
		t.Fatal("old process reused review", err)
	}
	if _, err := s.AuthorizeRuntimeUpgradeGatewayIngress(t.Context(), replacement.SlotID, replacement.SessionID); !errors.Is(err, state.ErrConflict) {
		t.Fatal("replacement skipped heartbeat", err)
	}
	if err := s.HeartbeatRuntimeUpgradeGateway(t.Context(), replacement.SlotID, replacement.SessionID); err != nil {
		t.Fatal(err)
	}
	if fresh, err := s.AuthorizeRuntimeUpgradeGatewayIngress(t.Context(), replacement.SlotID, replacement.SessionID); err != nil || fresh.GatewayRosterRevision != next.Revision {
		t.Fatal(fresh, err)
	}
}

func TestPgRuntimeUpgradeIngressChecksExpiryAfterDatabaseWait(t *testing.T) {
	s, pool, _ := pgStoreWithPool(t)
	roster := seedReviewedRuntimeUpgradeGateways(t, s, []string{uuid.NewString()})
	member := roster.Members[0]
	if _, err := pool.Exec(t.Context(), `WITH at AS MATERIALIZED (SELECT clock_timestamp() AS now) UPDATE runtime_upgrade_gateway_heartbeats SET seen_at=at.now-interval '58 seconds',expires_at=at.now+interval '2 seconds' FROM at WHERE slot_id=$1`, member.SlotID); err != nil {
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
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := s.AuthorizeRuntimeUpgradeGatewayIngress(ctx, member.SlotID, member.SessionID)
		done <- err
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		var waiting bool
		if err := pool.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))`, int32(blocker.Conn().PgConn().PID())).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("authorization never reached heartbeat read")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// The snapshot includes the committed short lease. Its expiry passes
	// while the read waits; evaluation must use the later database clock.
	time.Sleep(2100 * time.Millisecond)
	if err := blocker.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, state.ErrConflict) {
		t.Fatal("lease expiry during read wait authorized ingress", err)
	}
}

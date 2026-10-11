// adr: 697
package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/apid/runtimeupgrade"
	"github.com/onebox-faas/faas/pkg/state"
)

func drainPgFixture(t *testing.T) (*state.PgStore, *pgxpool.Pool, state.App, state.Deployment, state.Deployment, state.RuntimeUpgradeOperationRequest, state.RuntimeUpgradeGatewayRoster, state.RuntimeUpgradeDrainSnapshot) {
	t.Helper()
	s, pool, _ := pgStoreWithPool(t)
	app, old, candidate, operation := completeVerificationFixture(t, s)
	verificationJournalAgeCutover(t, pool, candidate.ID, 6*time.Minute)
	roster := seedReviewedRuntimeUpgradeGateways(t, s, []string{uuid.NewString(), uuid.NewString()})
	for _, member := range roster.Members {
		if err := s.RecordRuntimeUpgradeGateway(t.Context(), app.ID, member.SessionID, candidate.ID); err != nil {
			t.Fatal(err)
		}
	}
	verificationJournalHealthyEvidence(t, s, app.ID, candidate.ID)
	snapshot, err := s.RuntimeUpgradeGatewayDrainSnapshot(t.Context(), app.ID)
	if err != nil || snapshot.Plan == nil {
		t.Fatal(snapshot, err)
	}
	return s, pool, app, old, candidate, operation, roster, snapshot
}

func drainPgObservation(app string, member state.RuntimeUpgradeGatewayMember, snapshot state.RuntimeUpgradeDrainSnapshot, version string) state.RuntimeUpgradeGatewayDrainObservation {
	return state.RuntimeUpgradeGatewayDrainObservation{AppID: app, SlotID: member.SlotID, SessionID: member.SessionID, FenceID: uuid.NewString(), RoutingRevision: snapshot.RoutingRevision, ActivityVersion: version, RuntimeUpgradeDrainPlan: *snapshot.Plan}
}

func drainPgSessions(roster state.RuntimeUpgradeGatewayRoster) []string {
	var sessions []string
	for _, m := range roster.Members {
		sessions = append(sessions, m.SessionID)
	}
	return sessions
}

func TestPgRuntimeUpgradeDrainRequiresWholeRosterAndInvalidatesRollbackReactivation(t *testing.T) {
	s, _, app, old, candidate, op, roster, snapshot := drainPgFixture(t)
	controls := runtimeupgrade.DrainControls{Store: s}
	sessions := drainPgSessions(roster)
	if _, err := controls.Verify(t.Context(), uuid.NewString(), op.ID, sessions); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("cross-account observation", err)
	}
	if partial, err := controls.Verify(t.Context(), app.AccountID, op.ID, sessions[:1]); err != nil || partial.Status == "forwarding_drained" {
		t.Fatal("partial roster accepted", partial, err)
	}
	first := drainPgObservation(app.ID, roster.Members[0], snapshot, "10")
	if err := s.RecordRuntimeUpgradeGatewayDrain(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	if partial, err := controls.Verify(t.Context(), app.AccountID, op.ID, sessions); err != nil || partial.Status == "forwarding_drained" || partial.ConfirmedGateways != 1 {
		t.Fatal("missing process certified", partial, err)
	}
	if err := s.RecordRuntimeUpgradeGatewayDrain(t.Context(), drainPgObservation(app.ID, roster.Members[1], snapshot, "10")); err != nil {
		t.Fatal(err)
	}
	verified, err := controls.Verify(t.Context(), app.AccountID, op.ID, sessions)
	if err != nil || verified.Status != "forwarding_drained" || verified.ValidForSeconds < 1 || verified.ConfirmedGateways != 2 {
		t.Fatal(verified, err)
	}
	if _, err := s.UpdateDeploymentTraffic(t.Context(), old.ID, 100, candidate.ID); err != nil {
		t.Fatal(err)
	}
	rollback, err := s.RuntimeUpgradeGatewayDrainSnapshot(t.Context(), app.ID)
	if err != nil || rollback.Plan != nil || rollback.RoutingRevision == snapshot.RoutingRevision {
		t.Fatal(rollback, err)
	}
	apps, err := s.ListRuntimeUpgradeGatewayDrainRepairApps(t.Context(), "")
	if err != nil || len(apps) != 1 || apps[0] != app.ID {
		t.Fatal("rollback dropped from repair", apps, err)
	}
	if _, err := s.UpdateDeploymentTraffic(t.Context(), candidate.ID, 100, old.ID); err != nil {
		t.Fatal(err)
	}
	current, err := s.RuntimeUpgradeGatewayDrainSnapshot(t.Context(), app.ID)
	if err != nil || current.Plan == nil || current.RoutingRevision == snapshot.RoutingRevision || current.RoutingRevision == rollback.RoutingRevision {
		t.Fatal("ABA revision reused", current, err)
	}
	if err := s.RecordRuntimeUpgradeGatewayDrain(t.Context(), first); !errors.Is(err, state.ErrConflict) {
		t.Fatal("stale publisher accepted after reactivation", err)
	}
	stale, err := controls.Verify(t.Context(), app.AccountID, op.ID, sessions)
	if err != nil || stale.Status == "forwarding_drained" || stale.ConfirmedGateways != 0 {
		t.Fatal("old receipts survived reactivation", stale, err)
	}
	for _, member := range roster.Members {
		if err := s.RecordRuntimeUpgradeGatewayDrain(t.Context(), drainPgObservation(app.ID, member, current, "20")); err != nil {
			t.Fatal(err)
		}
	}
	if fresh, err := controls.Verify(t.Context(), app.AccountID, op.ID, sessions); err != nil || fresh.Status != "forwarding_drained" {
		t.Fatal(fresh, err)
	}
}

func TestPgRuntimeUpgradeDrainRejectsReplayIdentityExpiredAndFutureEvidence(t *testing.T) {
	s, pool, app, _, _, op, roster, snapshot := drainPgFixture(t)
	first := drainPgObservation(app.ID, roster.Members[0], snapshot, "10")
	wrong := first
	wrong.SessionID = uuid.NewString()
	if err := s.RecordRuntimeUpgradeGatewayDrain(t.Context(), wrong); !errors.Is(err, state.ErrConflict) {
		t.Fatal("unreviewed process published", err)
	}
	wrong = first
	wrong.SlotID = roster.Members[1].SlotID
	if err := s.RecordRuntimeUpgradeGatewayDrain(t.Context(), wrong); !errors.Is(err, state.ErrConflict) {
		t.Fatal("wrong slot published", err)
	}
	wrong = first
	wrong.ActivityVersion = "18446744073709551616"
	if err := s.RecordRuntimeUpgradeGatewayDrain(t.Context(), wrong); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatal("counter overflow accepted", err)
	}
	for _, member := range roster.Members {
		if err := s.RecordRuntimeUpgradeGatewayDrain(t.Context(), drainPgObservation(app.ID, member, snapshot, "10")); err != nil {
			t.Fatal(err)
		}
	}
	wrong = first
	wrong.ActivityVersion = "9"
	if err := s.RecordRuntimeUpgradeGatewayDrain(t.Context(), wrong); !errors.Is(err, state.ErrConflict) {
		t.Fatal("older observation overwrote newer", err)
	}
	controls := runtimeupgrade.DrainControls{Store: s}
	sessions := drainPgSessions(roster)
	// Synthetic database timestamps exercise conservative expiry, never VM proof.
	for _, shift := range []string{"1 minute", "-3 minutes"} {
		if _, err := pool.Exec(t.Context(), `UPDATE runtime_upgrade_gateway_drains SET observed_at=observed_at+$2::interval,expires_at=expires_at+$2::interval WHERE app_id=$1`, app.ID, shift); err != nil {
			t.Fatal(err)
		}
		if out, err := controls.Verify(t.Context(), app.AccountID, op.ID, sessions); err != nil || out.Status == "forwarding_drained" {
			t.Fatal("future/expired receipts accepted", out, err)
		}
	}
	if err := s.PruneExpiredRuntimeUpgradeGatewayDrains(t.Context()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM runtime_upgrade_gateway_drains WHERE app_id=$1`, app.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE runtime_upgrade_gateway_heartbeats SET seen_at=seen_at-interval '2 minutes',expires_at=expires_at-interval '2 minutes' WHERE slot_id=$1`, first.SlotID); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordRuntimeUpgradeGatewayDrain(t.Context(), first); !errors.Is(err, state.ErrConflict) {
		t.Fatal("expired heartbeat permitted receipt", err)
	}
}

func TestPgRuntimeUpgradeDrainRoutingTokensCannotBeRestoredBySQL(t *testing.T) {
	s, pool, app, old, _, _, _, snapshot := drainPgFixture(t)
	var token string
	if err := pool.QueryRow(t.Context(), `SELECT runtime_upgrade_routing_token::text FROM deployments WHERE id=$1`, old.ID).Scan(&token); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE deployments SET runtime_upgrade_routing_token=$2::uuid WHERE id=$1`, old.ID, token); err != nil {
		t.Fatal(err)
	}
	current, err := s.RuntimeUpgradeGatewayDrainSnapshot(t.Context(), app.ID)
	if err != nil || current.RoutingRevision == snapshot.RoutingRevision {
		t.Fatal("direct SQL reused routing revision", current, err)
	}
	repeat, err := s.RuntimeUpgradeGatewayDrainSnapshot(t.Context(), app.ID)
	if err != nil || repeat.RoutingRevision != current.RoutingRevision {
		t.Fatal("read changed routing revision", repeat, err)
	}
}

func TestPgRuntimeUpgradeDrainPublicationRereadsRoutingAndHeartbeatAfterLockWait(t *testing.T) {
	for _, change := range []string{"rollback", "expired heartbeat"} {
		t.Run(change, func(t *testing.T) {
			s, pool, app, old, _, _, roster, snapshot := drainPgFixture(t)
			observation := drainPgObservation(app.ID, roster.Members[0], snapshot, "10")
			blocker, err := pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = blocker.Rollback(t.Context()) }()
			if _, err := blocker.Exec(t.Context(), `SELECT id FROM apps WHERE id=$1 FOR UPDATE`, app.ID); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- s.RecordRuntimeUpgradeGatewayDrain(ctx, observation) }()
			until := time.Now().Add(3 * time.Second)
			for {
				var waiting bool
				if err := pool.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))`, int32(blocker.Conn().PgConn().PID())).Scan(&waiting); err != nil {
					t.Fatal(err)
				}
				if waiting {
					break
				}
				if time.Now().After(until) {
					t.Fatal("publisher did not reach app fence")
				}
				time.Sleep(10 * time.Millisecond)
			}
			if change == "rollback" {
				if _, err := blocker.Exec(t.Context(), `UPDATE deployments SET traffic_percent=CASE WHEN id=$2::uuid THEN 100 ELSE 0 END WHERE app_id=$1 AND status='live'`, app.ID, old.ID); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := pool.Exec(t.Context(), `UPDATE runtime_upgrade_gateway_heartbeats SET seen_at=seen_at-interval '2 minutes',expires_at=expires_at-interval '2 minutes' WHERE slot_id=$1`, observation.SlotID); err != nil {
					t.Fatal(err)
				}
			}
			if err := blocker.Commit(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := <-done; !errors.Is(err, state.ErrConflict) {
				t.Fatal("post-wait changed evidence published", err)
			}
			var count int
			if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM runtime_upgrade_gateway_drains WHERE app_id=$1`, app.ID).Scan(&count); err != nil || count != 0 {
				t.Fatal(count, err)
			}
		})
	}
}

package state_test

// adr: 695

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/apid/runtimeupgrade"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgRuntimeUpgradeGatewayMembershipFencesReviewAndRecoversExpiredLiveness(t *testing.T) {
	s, pool, _ := pgStoreWithPool(t)
	app, _, candidate, r := completeVerificationFixture(t, s)
	verificationJournalAgeCutover(t, pool, candidate.ID, 6*time.Minute)
	sessions := []string{uuid.NewString(), uuid.NewString()}
	roster := seedReviewedRuntimeUpgradeGateways(t, s, sessions)
	for _, session := range sessions {
		if err := s.RecordRuntimeUpgradeGateway(t.Context(), app.ID, session, candidate.ID); err != nil {
			t.Fatal(err)
		}
	}
	verificationJournalHealthyEvidence(t, s, app.ID, candidate.ID)
	if _, err := s.StartRuntimeUpgradeVerification(t.Context(), r.AccountID, r.ID, sessions); err != nil {
		t.Fatal(err)
	}
	claim, err := s.ClaimRuntimeUpgradeVerification(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// Keep the exact database TTL constraint; expire one synthetic heartbeat
	// while the worker waits for the app fence. An absent member stays desired.
	if _, err := pool.Exec(t.Context(), `WITH at AS MATERIALIZED (SELECT clock_timestamp() AS now) UPDATE runtime_upgrade_gateway_heartbeats SET seen_at=at.now-interval '59 seconds',expires_at=at.now+interval '1 second' FROM at WHERE slot_id=$1`, roster.Members[1].SlotID); err != nil {
		t.Fatal(err)
	}
	blocker, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback(t.Context()) }()
	if _, err := blocker.Exec(t.Context(), `SELECT id FROM apps WHERE id=$1 FOR UPDATE`, app.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := s.AdvanceRuntimeUpgradeVerification(t.Context(), claim); done <- err }()
	waitUntil := time.Now().Add(3 * time.Second)
	for {
		var waiting bool
		if err := pool.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))`, int32(blocker.Conn().PgConn().PID())).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(waitUntil) {
			t.Fatal("verification never reached app fence")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Review must wait for the worker's roster fence, so a concurrent roster
	// change cannot slip between observation and verified publication.
	reviewCtx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	_, err = s.ReviewRuntimeUpgradeGatewayRoster(reviewCtx, roster.Revision, roster.Members[:1])
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("review bypassed verification fence", err)
	}
	time.Sleep(1100 * time.Millisecond)
	if err := blocker.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	pending, err := s.RuntimeUpgradeVerificationJournal(t.Context(), r.AccountID, r.ID)
	if err != nil || pending.Phase != state.RuntimeUpgradeVerificationPending || pending.Reason != "gateway_membership_pending" || len(pending.GatewaySessions) != 2 || pending.GatewayRosterRevision != roster.Revision {
		t.Fatal("offline member disappeared or expired heartbeat verified", pending, err)
	}
	current, err := s.RuntimeUpgradeGatewayRoster(t.Context())
	if err != nil || !reflect.DeepEqual(current, roster) {
		t.Fatal("cancelled review changed desired membership", current, err)
	}
	for _, member := range roster.Members {
		if err := s.HeartbeatRuntimeUpgradeGateway(t.Context(), member.SlotID, member.SessionID); err != nil {
			t.Fatal(err)
		}
	}
	verificationJournalDue(t, pool, r.ID)
	if did, err := (runtimeupgrade.VerificationExecutor{Store: state.NewPgStore(pool)}).RunOnce(t.Context()); err != nil || !did {
		t.Fatal("recovered worker failed", did, err)
	}
	verified, err := s.RuntimeUpgradeVerificationJournal(t.Context(), r.AccountID, r.ID)
	if err != nil || verified.Phase != state.RuntimeUpgradeVerificationVerified || verified.LastObservation.GatewayRosterRevision != roster.Revision {
		t.Fatal(verified, err)
	}
	next := append([]state.RuntimeUpgradeGatewayMember{}, roster.Members...)
	next = append(next, state.RuntimeUpgradeGatewayMember{SlotID: uuid.NewString(), SessionID: uuid.NewString()})
	if _, err := s.ReviewRuntimeUpgradeGatewayRoster(t.Context(), roster.Revision, next); err != nil {
		t.Fatal(err)
	}
	fresh, err := s.VerifyRuntimeUpgrade(t.Context(), r.AccountID, r.ID, sessions)
	if err != nil || fresh.Reason != "gateway_membership_changed" || fresh.Status == "verified" {
		t.Fatal("historical verification used as current membership", fresh, err)
	}
	history, err := s.RuntimeUpgradeVerificationJournal(t.Context(), r.AccountID, r.ID)
	if err != nil || !reflect.DeepEqual(history, verified) {
		t.Fatal("membership review rewrote verified history", history, err)
	}
}

func TestPgRuntimeUpgradeGatewayRosterDatabaseGuardsAndLegacyEnrollment(t *testing.T) {
	s, pool, _ := pgStoreWithPool(t)
	_, _, _, r := completeVerificationFixture(t, s)
	sessions := []string{uuid.NewString()}
	roster := seedReviewedRuntimeUpgradeGateways(t, s, sessions)
	if _, err := s.StartRuntimeUpgradeVerification(t.Context(), r.AccountID, r.ID, sessions); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{
		`UPDATE runtime_upgrade_gateway_rosters SET gateway_sessions=ARRAY[gen_random_uuid()] WHERE revision=$1`,
		`DELETE FROM runtime_upgrade_gateway_rosters WHERE revision=$1`,
		`INSERT INTO runtime_upgrade_gateway_rosters(revision,slot_ids,gateway_sessions) VALUES (gen_random_uuid(),ARRAY[gen_random_uuid(),gen_random_uuid()],ARRAY[$1::uuid,$1::uuid])`,
	} {
		if _, err := pool.Exec(t.Context(), sql, roster.Revision); err == nil {
			t.Fatal("database accepted invalid/changed roster", sql)
		}
	}
	if _, err := pool.Exec(t.Context(), `UPDATE runtime_upgrade_verifications SET gateway_roster_revision=NULL WHERE operation_id=$1`, r.ID); err == nil {
		t.Fatal("database unfroze operation roster")
	}
	// Synthetic pre-ADR-695 pending row: migration leaves its history intact,
	// but it cannot acquire membership authority merely because a roster exists.
	if _, err := pool.Exec(t.Context(), `ALTER TABLE runtime_upgrade_verifications DISABLE TRIGGER USER`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE runtime_upgrade_verifications SET gateway_roster_revision=NULL WHERE operation_id=$1`, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `ALTER TABLE runtime_upgrade_verifications ENABLE TRIGGER USER`); err != nil {
		t.Fatal(err)
	}
	if _, err := (runtimeupgrade.VerificationExecutor{Store: s}).RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	j, err := s.RuntimeUpgradeVerificationJournal(t.Context(), r.AccountID, r.ID)
	if err != nil || j.Phase != state.RuntimeUpgradeVerificationBlocked || j.Reason != "gateway_membership_unreviewed" {
		t.Fatal("legacy participant list silently acquired roster authority", j, err)
	}
}

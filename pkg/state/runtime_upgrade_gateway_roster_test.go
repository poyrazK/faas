package state_test

// adr: 695

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/runtimeupgrade"
	"github.com/onebox-faas/faas/pkg/state"
)

func seedReviewedRuntimeUpgradeGateways(t *testing.T, s state.Store, sessions []string) state.RuntimeUpgradeGatewayRoster {
	t.Helper()
	members := make([]state.RuntimeUpgradeGatewayMember, len(sessions))
	for i, session := range sessions {
		members[i] = state.RuntimeUpgradeGatewayMember{SlotID: uuid.NewString(), SessionID: session}
	}
	roster, err := s.(state.RuntimeUpgradeGatewayRosterStore).ReviewRuntimeUpgradeGatewayRoster(t.Context(), "", members)
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range roster.Members {
		if err := s.(state.RuntimeUpgradeGatewayHeartbeatStore).HeartbeatRuntimeUpgradeGateway(t.Context(), member.SlotID, member.SessionID); err != nil {
			t.Fatal(err)
		}
	}
	return roster
}

func TestRuntimeUpgradeGatewayRosterRequiresReviewAndCompareAndSwap(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var s state.Store = state.NewMemStore()
			if backend == "postgres" {
				s, _ = pgStore(t)
			}
			controls := runtimeupgrade.GatewayRosterControls{Store: s.(state.RuntimeUpgradeGatewayRosterStore)}
			heartbeats := s.(state.RuntimeUpgradeGatewayHeartbeatStore)
			if _, err := controls.Status(t.Context()); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("unreviewed roster appeared", err)
			}
			members := []state.RuntimeUpgradeGatewayMember{{SlotID: uuid.NewString(), SessionID: uuid.NewString()}, {SlotID: uuid.NewString(), SessionID: uuid.NewString()}}
			if err := heartbeats.HeartbeatRuntimeUpgradeGateway(t.Context(), members[0].SlotID, members[0].SessionID); !errors.Is(err, state.ErrConflict) {
				t.Fatal("heartbeat invented desired member", err)
			}
			tooMany := make([]state.RuntimeUpgradeGatewayMember, api.RuntimeUpgradeGatewaySessionLimit+1)
			for i := range tooMany {
				tooMany[i] = state.RuntimeUpgradeGatewayMember{SlotID: uuid.NewString(), SessionID: uuid.NewString()}
			}
			for _, bad := range [][]state.RuntimeUpgradeGatewayMember{nil, tooMany, {members[0], members[0]}, {{SlotID: members[0].SlotID, SessionID: members[1].SessionID}, members[1]}, {{SlotID: uuid.Nil.String(), SessionID: uuid.NewString()}}, {{SlotID: uuid.NewString(), SessionID: "invalid"}}} {
				if _, err := controls.Review(t.Context(), "", bad); !errors.Is(err, state.ErrInvalidArgument) {
					t.Fatal("invalid roster accepted", err)
				}
			}
			members[0].SessionID = strings.ToUpper(members[0].SessionID)
			roster, err := controls.Review(t.Context(), "", members)
			if err != nil || len(roster.Members) != 2 {
				t.Fatal(roster, err)
			}
			reversed := slices.Clone(roster.Members)
			slices.Reverse(reversed)
			replay, err := controls.Review(t.Context(), roster.Revision, reversed)
			if err != nil || !reflect.DeepEqual(roster, replay) {
				t.Fatal("same review changed revision", replay, err)
			}
			if _, err := controls.Review(t.Context(), "", reversed); !errors.Is(err, state.ErrConflict) {
				t.Fatal("stale first-review precondition accepted", err)
			}
			for _, member := range roster.Members {
				if err := heartbeats.HeartbeatRuntimeUpgradeGateway(t.Context(), member.SlotID, member.SessionID); err != nil {
					t.Fatal(err)
				}
				if err := heartbeats.HeartbeatRuntimeUpgradeGateway(t.Context(), member.SlotID, uuid.NewString()); !errors.Is(err, state.ErrConflict) {
					t.Fatal("restart substituted itself", err)
				}
			}
			// Two valid concurrent reviews of the same revision cannot both win.
			results := make(chan error, 2)
			for range 2 {
				next := slices.Clone(roster.Members)
				next[0].SessionID = uuid.NewString()
				go func() { _, err := controls.Review(t.Context(), roster.Revision, next); results <- err }()
			}
			won, conflicted := 0, 0
			for range 2 {
				err := <-results
				if err == nil {
					won++
				} else if errors.Is(err, state.ErrConflict) {
					conflicted++
				} else {
					t.Fatal(err)
				}
			}
			if won != 1 || conflicted != 1 {
				t.Fatal("reviews did not serialize", won, conflicted)
			}
			current, err := controls.Status(t.Context())
			if err != nil || current.Revision == roster.Revision {
				t.Fatal(current, err)
			}
			current.Members[0].SessionID = uuid.NewString()
			retained, err := controls.Status(t.Context())
			if err != nil || reflect.DeepEqual(current, retained) {
				t.Fatal("caller changed stored roster", retained, err)
			}
			if err := heartbeats.HeartbeatRuntimeUpgradeGateway(t.Context(), roster.Members[0].SlotID, roster.Members[0].SessionID); !errors.Is(err, state.ErrConflict) {
				t.Fatal("old process resurrected after reviewed replacement", err)
			}
		})
	}
}

func TestRuntimeUpgradeGatewayMembershipRejectsPartialAndChangedRoster(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var s state.Store = state.NewMemStore()
			if backend == "postgres" {
				s, _ = pgStore(t)
			}
			app, _, candidate, r := completeVerificationFixture(t, s)
			verify := s.(state.RuntimeUpgradeVerificationStore)
			journal := s.(state.RuntimeUpgradeVerificationJournalStore)
			sessions := []string{uuid.NewString(), uuid.NewString()}
			unreviewed, err := verify.VerifyRuntimeUpgrade(t.Context(), r.AccountID, r.ID, sessions)
			if err != nil || unreviewed.Reason != "gateway_membership_unreviewed" {
				t.Fatal(unreviewed, err)
			}
			if _, err := journal.StartRuntimeUpgradeVerification(t.Context(), r.AccountID, r.ID, sessions); !errors.Is(err, state.ErrConflict) {
				t.Fatal("enrolled without expected inventory", err)
			}
			rosterStore := s.(state.RuntimeUpgradeGatewayRosterStore)
			members := []state.RuntimeUpgradeGatewayMember{{SlotID: uuid.NewString(), SessionID: sessions[0]}, {SlotID: uuid.NewString(), SessionID: sessions[1]}}
			roster, err := rosterStore.ReviewRuntimeUpgradeGatewayRoster(t.Context(), "", members)
			if err != nil {
				t.Fatal(err)
			}
			for _, session := range sessions {
				if err := s.(state.RuntimeUpgradeGatewayStore).RecordRuntimeUpgradeGateway(t.Context(), app.ID, session, candidate.ID); err != nil {
					t.Fatal(err)
				}
			}
			pending, err := verify.VerifyRuntimeUpgrade(t.Context(), r.AccountID, r.ID, sessions)
			if err != nil || pending.Reason != "gateway_membership_pending" {
				t.Fatal("installed weights substituted for liveness", pending, err)
			}
			partial, err := verify.VerifyRuntimeUpgrade(t.Context(), r.AccountID, r.ID, sessions[:1])
			if err != nil || partial.Reason != "gateway_membership_mismatch" {
				t.Fatal("subset certified", partial, err)
			}
			if _, err := journal.StartRuntimeUpgradeVerification(t.Context(), r.AccountID, r.ID, sessions[:1]); !errors.Is(err, state.ErrConflict) {
				t.Fatal("subset enrolled", err)
			}
			for _, member := range members {
				if err := s.(state.RuntimeUpgradeGatewayHeartbeatStore).HeartbeatRuntimeUpgradeGateway(t.Context(), member.SlotID, member.SessionID); err != nil {
					t.Fatal(err)
				}
			}
			live, err := verify.VerifyRuntimeUpgrade(t.Context(), r.AccountID, r.ID, sessions)
			if err != nil || live.Reason != "health_evidence_unavailable" || live.GatewayRosterRevision != roster.Revision {
				t.Fatal(live, err)
			}
			j, err := journal.StartRuntimeUpgradeVerification(t.Context(), r.AccountID, r.ID, sessions)
			if err != nil || j.GatewayRosterRevision != roster.Revision {
				t.Fatal(j, err)
			}
			if _, err := rosterStore.ReviewRuntimeUpgradeGatewayRoster(t.Context(), roster.Revision, members[:1]); err != nil {
				t.Fatal(err)
			}
			if _, err := (runtimeupgrade.VerificationExecutor{Store: journal}).RunOnce(t.Context()); err != nil {
				t.Fatal(err)
			}
			blocked, err := journal.RuntimeUpgradeVerificationJournal(t.Context(), r.AccountID, r.ID)
			if err != nil || blocked.Phase != state.RuntimeUpgradeVerificationBlocked || blocked.Reason != "gateway_membership_changed" || blocked.GatewayRosterRevision != roster.Revision {
				t.Fatal("roster shrink weakened frozen operation", blocked, err)
			}
			replay, err := journal.StartRuntimeUpgradeVerification(t.Context(), r.AccountID, r.ID, sessions)
			if err != nil || !reflect.DeepEqual(replay, blocked) {
				t.Fatal("review replay rebound to new roster", replay, err)
			}
		})
	}
}

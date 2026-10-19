package state_test

// adr: 695

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRuntimeUpgradeGatewayMembershipKeepsMissingAndExpiredMembers(t *testing.T) {
	now := time.Now().UTC()
	roster := state.RuntimeUpgradeGatewayRoster{Revision: uuid.NewString(), Members: []state.RuntimeUpgradeGatewayMember{{SlotID: uuid.NewString(), SessionID: uuid.NewString()}, {SlotID: uuid.NewString(), SessionID: uuid.NewString()}}}
	first := state.RuntimeUpgradeGatewayHeartbeat{Revision: roster.Revision, SlotID: roster.Members[0].SlotID, SessionID: roster.Members[0].SessionID, SeenAt: now.Add(-time.Second), ExpiresAt: now.Add(59 * time.Second)}
	second := state.RuntimeUpgradeGatewayHeartbeat{Revision: roster.Revision, SlotID: roster.Members[1].SlotID, SessionID: roster.Members[1].SessionID, SeenAt: now.Add(-41 * time.Second), ExpiresAt: now.Add(19 * time.Second)}
	for _, tc := range []struct {
		name       string
		heartbeats []state.RuntimeUpgradeGatewayHeartbeat
		mutate     func(*state.RuntimeUpgradeGatewayHeartbeat)
		want       string
	}{
		{name: "missing", heartbeats: []state.RuntimeUpgradeGatewayHeartbeat{first}, want: "gateway_membership_pending"},
		{name: "fresh", heartbeats: []state.RuntimeUpgradeGatewayHeartbeat{first, second}},
		{name: "expired", heartbeats: []state.RuntimeUpgradeGatewayHeartbeat{first, second}, mutate: func(h *state.RuntimeUpgradeGatewayHeartbeat) { h.ExpiresAt = now }, want: "gateway_membership_pending"},
		{name: "subsecond", heartbeats: []state.RuntimeUpgradeGatewayHeartbeat{first, second}, mutate: func(h *state.RuntimeUpgradeGatewayHeartbeat) { h.ExpiresAt = now.Add(time.Millisecond) }, want: "gateway_membership_pending"},
		{name: "future", heartbeats: []state.RuntimeUpgradeGatewayHeartbeat{first, second}, mutate: func(h *state.RuntimeUpgradeGatewayHeartbeat) { h.SeenAt = now.Add(time.Second) }, want: "gateway_membership_pending"},
		{name: "old_revision", heartbeats: []state.RuntimeUpgradeGatewayHeartbeat{first, second}, mutate: func(h *state.RuntimeUpgradeGatewayHeartbeat) { h.Revision = uuid.NewString() }, want: "gateway_membership_pending"},
		{name: "restarted", heartbeats: []state.RuntimeUpgradeGatewayHeartbeat{first, second}, mutate: func(h *state.RuntimeUpgradeGatewayHeartbeat) { h.SessionID = uuid.NewString() }, want: "gateway_membership_pending"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.mutate != nil {
				tc.mutate(&tc.heartbeats[1])
			}
			out, err := state.NewRuntimeUpgradeVerification(uuid.NewString(), roster.Sessions(), now)
			if err != nil {
				t.Fatal(err)
			}
			out = state.EvaluateRuntimeUpgradeGatewayMembership(out, roster, roster.Revision, true, tc.heartbeats)
			if out.Reason != tc.want || len(out.GatewaySessions) != 2 || (tc.want == "" && out.ValidForSeconds != 19) {
				t.Fatal("liveness weakened participants or validity", out)
			}
		})
	}
}

package state

// adr: 609

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestRuntimeUpgradeGatewayMembershipKeepsMissingAndExpiredMembers(t *testing.T) {
	now := time.Now().UTC()
	roster := RuntimeUpgradeGatewayRoster{Revision: uuid.NewString(), Members: []RuntimeUpgradeGatewayMember{{SlotID: uuid.NewString(), SessionID: uuid.NewString()}, {SlotID: uuid.NewString(), SessionID: uuid.NewString()}}}
	first := runtimeUpgradeGatewayHeartbeat{Revision: roster.Revision, SlotID: roster.Members[0].SlotID, SessionID: roster.Members[0].SessionID, SeenAt: now.Add(-time.Second), ExpiresAt: now.Add(59 * time.Second)}
	second := runtimeUpgradeGatewayHeartbeat{Revision: roster.Revision, SlotID: roster.Members[1].SlotID, SessionID: roster.Members[1].SessionID, SeenAt: now.Add(-41 * time.Second), ExpiresAt: now.Add(19 * time.Second)}
	for _, tc := range []struct {
		name       string
		heartbeats []runtimeUpgradeGatewayHeartbeat
		mutate     func(*runtimeUpgradeGatewayHeartbeat)
		want       string
	}{
		{name: "missing", heartbeats: []runtimeUpgradeGatewayHeartbeat{first}, want: "gateway_membership_pending"},
		{name: "fresh", heartbeats: []runtimeUpgradeGatewayHeartbeat{first, second}},
		{name: "expired", heartbeats: []runtimeUpgradeGatewayHeartbeat{first, second}, mutate: func(h *runtimeUpgradeGatewayHeartbeat) { h.ExpiresAt = now }, want: "gateway_membership_pending"},
		{name: "subsecond", heartbeats: []runtimeUpgradeGatewayHeartbeat{first, second}, mutate: func(h *runtimeUpgradeGatewayHeartbeat) { h.ExpiresAt = now.Add(time.Millisecond) }, want: "gateway_membership_pending"},
		{name: "future", heartbeats: []runtimeUpgradeGatewayHeartbeat{first, second}, mutate: func(h *runtimeUpgradeGatewayHeartbeat) { h.SeenAt = now.Add(time.Second) }, want: "gateway_membership_pending"},
		{name: "old_revision", heartbeats: []runtimeUpgradeGatewayHeartbeat{first, second}, mutate: func(h *runtimeUpgradeGatewayHeartbeat) { h.Revision = uuid.NewString() }, want: "gateway_membership_pending"},
		{name: "restarted", heartbeats: []runtimeUpgradeGatewayHeartbeat{first, second}, mutate: func(h *runtimeUpgradeGatewayHeartbeat) { h.SessionID = uuid.NewString() }, want: "gateway_membership_pending"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.mutate != nil {
				tc.mutate(&tc.heartbeats[1])
			}
			out, err := newRuntimeUpgradeVerification(uuid.NewString(), roster.sessions(), now)
			if err != nil {
				t.Fatal(err)
			}
			out = evaluateRuntimeUpgradeGatewayMembership(out, roster, roster.Revision, true, tc.heartbeats)
			if out.Reason != tc.want || len(out.GatewaySessions) != 2 || (tc.want == "" && out.ValidForSeconds != 19) {
				t.Fatal("liveness weakened participants or validity", out)
			}
		})
	}
}

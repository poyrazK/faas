package state

import (
	"cmp"
	"context"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// SlotID is a privately provisioned stable ingress identity. SessionID changes
// on every process start. Only private apid review can change this desired set.
type RuntimeUpgradeGatewayMember struct {
	SlotID    string `json:"slot_id"`
	SessionID string `json:"session_id"`
}

type RuntimeUpgradeGatewayRoster struct {
	Revision  string                        `json:"revision"`
	Members   []RuntimeUpgradeGatewayMember `json:"members"`
	CreatedAt time.Time                     `json:"created_at"`
}

// This is an administrative seam, never a customer route. Empty expected
// revision means first review; subsequent reviews require the current revision.
type RuntimeUpgradeGatewayRosterStore interface {
	ReviewRuntimeUpgradeGatewayRoster(context.Context, string, []RuntimeUpgradeGatewayMember) (RuntimeUpgradeGatewayRoster, error)
	RuntimeUpgradeGatewayRoster(context.Context) (RuntimeUpgradeGatewayRoster, error)
}

// Gatewayd may publish liveness only for its exact reviewed slot/process pair.
// A heartbeat cannot add/remove members or substitute a restarted process.
type RuntimeUpgradeGatewayHeartbeatStore interface {
	HeartbeatRuntimeUpgradeGateway(context.Context, string, string) error
}

type runtimeUpgradeGatewayHeartbeat struct {
	Revision, SlotID, SessionID string
	SeenAt, ExpiresAt           time.Time
}

func canonicalRuntimeUpgradeGatewayMembers(expected string, members []RuntimeUpgradeGatewayMember) ([]RuntimeUpgradeGatewayMember, error) {
	if expected != "" && !canonicalRuntimeUpgradeGatewayUUID(expected) {
		return nil, ErrInvalidArgument
	}
	if len(members) == 0 || len(members) > api.RuntimeUpgradeGatewaySessionLimit {
		return nil, ErrInvalidArgument
	}
	out := slices.Clone(members)
	seen := make(map[string]bool, len(out))
	for i, member := range out {
		slot, slotErr := uuid.Parse(member.SlotID)
		session, sessionErr := uuid.Parse(member.SessionID)
		if slotErr != nil || sessionErr != nil || slot == uuid.Nil || session == uuid.Nil || seen[session.String()] {
			return nil, ErrInvalidArgument
		}
		out[i] = RuntimeUpgradeGatewayMember{SlotID: slot.String(), SessionID: session.String()}
		seen[session.String()] = true
	}
	slices.SortFunc(out, func(a, b RuntimeUpgradeGatewayMember) int { return cmp.Compare(a.SlotID, b.SlotID) })
	for i := 1; i < len(out); i++ {
		if out[i-1].SlotID == out[i].SlotID {
			return nil, ErrInvalidArgument
		}
	}
	return out, nil
}

func canonicalRuntimeUpgradeGatewayUUID(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed != uuid.Nil && parsed.String() == id
}

func (r RuntimeUpgradeGatewayRoster) sessions() []string {
	sessions := make([]string, len(r.Members))
	for i, member := range r.Members {
		sessions[i] = member.SessionID
	}
	slices.Sort(sessions)
	return sessions
}

func evaluateRuntimeUpgradeGatewayMembership(out RuntimeUpgradeVerification, roster RuntimeUpgradeGatewayRoster, frozen string, enrolled bool, heartbeats []runtimeUpgradeGatewayHeartbeat) RuntimeUpgradeVerification {
	if roster.Revision == "" || (enrolled && frozen == "") {
		out.Reason = "gateway_membership_unreviewed"
		return out
	}
	out.GatewayRosterRevision = roster.Revision
	if enrolled && frozen != roster.Revision {
		out.Reason = "gateway_membership_changed"
		return out
	}
	if !slices.Equal(out.GatewaySessions, roster.sessions()) {
		out.Reason = "gateway_membership_mismatch"
		return out
	}
	for _, member := range roster.Members {
		remaining := 0
		for _, h := range heartbeats {
			if h.Revision == roster.Revision && h.SlotID == member.SlotID && h.SessionID == member.SessionID && !h.SeenAt.After(out.CheckedAt) && h.ExpiresAt.After(out.CheckedAt) {
				remaining = int(h.ExpiresAt.Sub(out.CheckedAt) / time.Second)
				break
			}
		}
		if remaining < 1 {
			out.Reason = "gateway_membership_pending"
			return out
		}
		out.ValidForSeconds = min(out.ValidForSeconds, remaining)
	}
	return out
}

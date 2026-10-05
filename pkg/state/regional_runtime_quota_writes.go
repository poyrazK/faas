package state

// adr: 617
import "strings"

// A quota stream currently has no activated allowance. Hold the same mutex as
// journal mutation and reject creation/publication before changing durable work.
func (m *MemStore) regionalQuotaFrozenLocked(appID string) bool {
	_, exists := m.regionalRuntimeQuotaHeads[regionalQuotaCanonicalID(appID)]
	return exists
}

func quotaInstanceWriteNeedsAdmission(old, next Instance) bool {
	if old.ID != "" && (old.AppID != next.AppID || old.ID != next.ID) {
		return true
	}
	nextState, oldState := State(strings.ToLower(next.State)), State(strings.ToLower(old.State))
	if !nextState.CountsForRAM() {
		return false
	}
	if old.ID == "" || !oldState.CountsForRAM() {
		return true
	}
	if old.NodeID != next.NodeID || old.DeploymentID != next.DeploymentID || old.WakeID != next.WakeID || old.Mode != next.Mode || old.RAMMB != next.RAMMB || old.Netns != next.Netns || old.HostIP != next.HostIP || old.GuestUID != next.GuestUID {
		return true
	}
	if oldState == nextState {
		return false
	}
	switch nextState {
	case StateWaking, StateColdBooting, StateRunning:
		return true
	case StateWarm:
		return oldState != StateRunning
	default:
		return false // Quiesce, drain and teardown retain their existing path.
	}
}

func (m *MemStore) checkRegionalQuotaInstanceWriteLocked(old, next Instance) error {
	if quotaInstanceWriteNeedsAdmission(old, next) && (m.regionalQuotaFrozenLocked(old.AppID) || m.regionalQuotaFrozenLocked(next.AppID)) {
		return ErrRegionalRuntimeQuotaFrozen
	}
	return nil
}

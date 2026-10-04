package runtimequota

// adr: 494

// HostEpoch identifies a host journal incarnation for physical permit binding.
// Discovery is bookkeeping, never authority to execute or evidence of drain.
type HostEpoch struct {
	NodeID, HostEpochID string
}

func (h HostEpoch) Validate() error {
	if !validID(h.NodeID) || !validID(h.HostEpochID) {
		return ErrInvalid
	}
	return nil
}

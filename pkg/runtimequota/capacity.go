package runtimequota

// adr: 618

// CapacityPermit binds one physical runtime attempt to its original allowance
// and host incarnation. An issued marker is retained bookkeeping, not permission
// to execute: the host must also enforce current admission and its local fence.
type CapacityPermit struct {
	Grant                                                    Grant
	ID, InstanceID, RunID, DeploymentID, NodeID, HostEpochID string
	Kind                                                     string
}

const (
	CapacityServing = "serving"
	CapacityWarm    = "warm"
	CapacityPrime   = "prime"
	CapacityAppTask = "app_task"
)

func (p CapacityPermit) Validate() error {
	if p.Grant.validateIdentity() != nil || !validID(p.ID) || !validID(p.InstanceID) || !validID(p.RunID) || !validID(p.DeploymentID) || !validID(p.NodeID) || !validID(p.HostEpochID) {
		return ErrInvalid
	}
	switch p.Kind {
	case CapacityServing, CapacityWarm, CapacityPrime, CapacityAppTask:
	default:
		return ErrInvalid
	}
	return nil
}

// A serving seat counts concurrency independently of a retained physical guest.
// Each new seat is immutable. A pause must irreversibly fence this seat before
// its capacity returns; a warm permit can remain resident across that pause.
type ServingSeat struct {
	Permit  CapacityPermit
	ID      string
	Rollout bool
}

func (s ServingSeat) Validate() error {
	if s.Permit.Validate() != nil || !validID(s.ID) || (s.Permit.Kind != CapacityServing && s.Permit.Kind != CapacityWarm) || (s.Rollout && s.Permit.Grant.RolloutConcurrency == 0) {
		return ErrInvalid
	}
	return nil
}

// ServingSeatStop proves closure of one serving authorization only. It can
// never prove physical drain of the underlying paused or running host permit.
type ServingSeatStop struct {
	Seat            ServingSeat
	FenceID         string
	AdmissionFrozen bool
	CountedServing  int
}

func (s ServingSeatStop) Validate() error {
	if s.Seat.Validate() != nil || !validID(s.FenceID) || !s.AdmissionFrozen || s.CountedServing != 0 {
		return ErrInvalid
	}
	return nil
}

func (s Signer) SignServingSeatStop(c ServingSeatStop) (Envelope, error) {
	if c.Validate() != nil || c.Seat.Permit.Grant.Binding.CellID != s.issuerID {
		return Envelope{}, ErrInvalid
	}
	return sign(s, "serving-seat-stop", c)
}

func (t Trust) VerifyServingSeatStop(e Envelope) (ServingSeatStop, error) {
	m, err := verify[ServingSeatStop](t, e, "serving-seat-stop", Cell)
	if err != nil {
		return ServingSeatStop{}, err
	}
	if m.Claims.Validate() != nil || m.Claims.Seat.Permit.Grant.Binding.CellID != m.IssuerID {
		return ServingSeatStop{}, ErrInvalid
	}
	return m.Claims, nil
}

// CapacityStop authenticates a cell owner's exact assertion that the original
// host incarnation persisted an irreversible fence and stopped this attempt,
// including late boots and paused residents. Signing does not establish either
// operational condition; the host/cell drain owner must prove them first.
type CapacityStop struct {
	Permit          CapacityPermit
	FenceID         string
	AdmissionFrozen bool
	CountedRuntimes int
}

func (s CapacityStop) Validate() error {
	if s.Permit.Validate() != nil || !validID(s.FenceID) || !s.AdmissionFrozen || s.CountedRuntimes != 0 {
		return ErrInvalid
	}
	return nil
}

func (s Signer) SignCapacityStop(c CapacityStop) (Envelope, error) {
	if c.Validate() != nil || c.Permit.Grant.Binding.CellID != s.issuerID {
		return Envelope{}, ErrInvalid
	}
	return sign(s, "capacity-stop", c)
}

func (t Trust) VerifyCapacityStop(e Envelope) (CapacityStop, error) {
	m, err := verify[CapacityStop](t, e, "capacity-stop", Cell)
	if err != nil {
		return CapacityStop{}, err
	}
	if m.Claims.Validate() != nil || m.Claims.Permit.Grant.Binding.CellID != m.IssuerID {
		return CapacityStop{}, ErrInvalid
	}
	return m.Claims, nil
}

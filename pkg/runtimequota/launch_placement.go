package runtimequota

// adr: 497
import (
	"strings"

	"github.com/google/uuid"
)

// NewLaunchPlacement identifies an initial physical launch, not a warm resume.
// Mutable claims are excluded from IDs so a changed claim conflicts with the
// retained attempt rather than obtaining a fresh identity. These IDs are not
// authorization or evidence that any host has stopped.
func NewLaunchPlacement(g Grant, h HostEpoch, b LifecycleBinding, runID string, rollout bool) (Placement, error) {
	if g.Validate() != nil || h.Validate() != nil || !validID(b.InstanceID) || !validID(runID) || (rollout && b.Purpose != CapacityServing) {
		return Placement{}, ErrInvalid
	}
	key := strings.Join([]string{"gregale/regional-launch/v1", g.Binding.CellID, g.Binding.RegionalAppID, h.NodeID, h.HostEpochID, b.InstanceID, runID}, "/")
	p := Placement{Permit: CapacityPermit{
		Grant: g, ID: uuid.NewSHA1(uuid.Nil, []byte(key)).String(),
		InstanceID: b.InstanceID, RunID: runID, DeploymentID: b.DeploymentID,
		NodeID: h.NodeID, HostEpochID: h.HostEpochID, Kind: b.Purpose,
	}}
	if b.Purpose == CapacityServing {
		p.Seat = &ServingSeat{Permit: p.Permit, ID: uuid.NewSHA1(uuid.Nil, []byte("gregale/initial-serving-seat/v1/"+p.Permit.ID)).String(), Rollout: rollout}
	}
	if err := p.ValidateBinding(b); err != nil {
		return Placement{}, err
	}
	return p, nil
}

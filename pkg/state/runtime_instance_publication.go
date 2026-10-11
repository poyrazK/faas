package state

import (
	"context"
	"net/netip"

	"github.com/google/uuid"
)

// RuntimeInstancePublication is authored by schedd from the admitted boot or
// paused resume. The target is RUNNING by default; paused restores publish WARM.
// Even an empty secret set has an original deployment/environment fence.
type RuntimeInstancePublication struct {
	AccountID, AppID, InstanceID, NodeID, WakeID string
	ExpectedState, TargetState, Netns, HostIP    string
	GuestUID                                     int
	Fence                                        RuntimeAppSecretFence
	ConfigFence                                  RuntimeAppConfigFence
	Inputs                                       *RuntimeConfigInputs
	RuntimeUpgradeColdBoot                       *RuntimeUpgradeColdBoot
}

type RuntimeInstancePublicationStore interface {
	PublishOwnedInstanceRuntime(context.Context, RuntimeInstancePublication) (Instance, error)
	InstanceRuntimeConfigFence(context.Context, string, string, string) (RuntimeAppConfigFence, error)
}

func validateRuntimeInstancePublication(p RuntimeInstancePublication) error {
	if p.Fence.empty() || !validRuntimeAppSecretFence(p.Fence) || !validRuntimeAppConfigFence(p.ConfigFence) || p.Netns == "" || p.GuestUID <= 0 ||
		!validRuntimePublicationTransition(p.ExpectedState, p.targetState()) {
		return ErrInvalidArgument
	}
	if p.Inputs != nil {
		if err := validateRuntimeConfigInputs(*p.Inputs); err != nil {
			return err
		}
		if p.Inputs.Scope != normalizedDeploymentScope(p.Fence.Scope) {
			return ErrConflict
		}
	}
	for _, value := range []string{p.AccountID, p.AppID, p.InstanceID, p.NodeID, p.WakeID} {
		if id, err := uuid.Parse(value); err != nil || id == uuid.Nil {
			return ErrInvalidArgument
		}
	}
	if _, err := netip.ParseAddr(p.HostIP); err != nil {
		return ErrInvalidArgument
	}
	return nil
}

// Empty target preserves the RUNNING publication contract for wake and prime.
func (p RuntimeInstancePublication) targetState() string {
	if p.TargetState == "" {
		return string(StateRunning)
	}
	return p.TargetState
}

func validRuntimePublicationTransition(from, to string) bool {
	if to == string(StateWarm) {
		return from == string(StateWaking)
	}
	return to == string(StateRunning) && (from == string(StateColdBooting) || from == string(StateWaking) || from == string(StateWarm))
}

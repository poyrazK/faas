package state

import (
	"context"
	"net/netip"

	"github.com/google/uuid"
)

// RuntimeInstancePublication is authored by schedd from the admitted boot.
// Even an empty secret set has an original deployment/environment fence.
type RuntimeInstancePublication struct {
	AccountID, AppID, InstanceID, NodeID, WakeID string
	ExpectedState, Netns, HostIP                 string
	GuestUID                                     int
	Fence                                        RuntimeAppSecretFence
}

type RuntimeInstancePublicationStore interface {
	PublishOwnedInstanceRuntime(context.Context, RuntimeInstancePublication) (Instance, error)
}

func validateRuntimeInstancePublication(p RuntimeInstancePublication) error {
	if p.Fence.empty() || !validRuntimeAppSecretFence(p.Fence) || p.Netns == "" || p.GuestUID <= 0 ||
		(p.ExpectedState != string(StateColdBooting) && p.ExpectedState != string(StateWaking)) {
		return ErrInvalidArgument
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

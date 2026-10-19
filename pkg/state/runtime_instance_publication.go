package state

import (
	"context"
	"encoding/json"
	"net/netip"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
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
	AdmissionReceipt, PromotionReceipt           *runtimeadmission.Receipt
	RuntimeUpgradeColdBoot                       *RuntimeUpgradeColdBoot
	// Bookkeeping, when set, commits a wake's bookkeeping with the
	// publication instead of after it.
	Bookkeeping *RuntimePublicationBookkeeping
	// GenerationGuarded publishes a wake admitted from schedd's owner cache
	// on the app's runtime generation instead of the fence's reads
	// (ADR-1741): every change the fence checks moves the generation. ctx
	// carries the guard the admission insert checked; the publication takes
	// it after its own row locks and, when it moved, writes nothing and
	// refuses with ErrRuntimeGenerationMoved, so the caller publishes again
	// through the fence. It applies only without a standards receipt.
	GenerationGuarded bool
}

// RuntimePublicationBookkeeping is what a wake records with its publication.
// The transition event and the notification commit with the publication or
// not at all, and cost no extra round trip. The deployment's snapshot-miss
// backoff is only reported for the caller to clear after commit: the
// publication holds a share lock on the deployment row, so clearing it inside
// would deadlock concurrent publications of one deployment.
type RuntimePublicationBookkeeping struct {
	// EventKind and EventData are schedd's transition event about the
	// instance. An empty kind records no event.
	EventKind string
	EventData []byte
	// NotifyChannel and NotifyPayload go out with pg_notify when the
	// publication commits. An empty channel leaves notifying to the caller;
	// only a store on the caller's notification database can send it.
	NotifyChannel, NotifyPayload string
	// SnapshotBackoffRecorded is set by the store when the publication
	// commits.
	SnapshotBackoffRecorded bool
}

type RuntimeInstancePublicationStore interface {
	PublishOwnedInstanceRuntime(context.Context, RuntimeInstancePublication) (Instance, error)
	InstanceRuntimeConfigFence(context.Context, string, string, string) (RuntimeAppConfigFence, error)
}

func validateRuntimeInstancePublication(p RuntimeInstancePublication) error {
	if err := validateOwnedNativeReceipt(p); err != nil {
		return err
	}
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
	if !validPublicationBookkeeping(p.Bookkeeping) {
		return ErrInvalidArgument
	}
	return nil
}

// validPublicationBookkeeping refuses bookkeeping the database would refuse,
// which would abort the publication of a VM that already booted.
func validPublicationBookkeeping(b *RuntimePublicationBookkeeping) bool {
	return b == nil || (b.EventKind == "" || json.Valid(b.EventData)) && (b.NotifyChannel != "" || b.NotifyPayload == "")
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
	return to == string(StateRunning) && (from == string(StateColdBooting) || from == string(StateWaking) || from == string(StateWarm) || from == string(StateFrozen))
}

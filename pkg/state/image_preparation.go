// adr: 463 — private, durable checkpoints for builder-to-imaged recovery.
package state

import (
	"context"
	"errors"
	"strings"
	"time"
)

type ImagePreparationPhase string

const (
	ImagePreparing      ImagePreparationPhase = "preparing"
	ImageLayerPublished ImagePreparationPhase = "layer_published"
	ImageScanComplete   ImagePreparationPhase = "scan_complete"
	ImageHandedOff      ImagePreparationPhase = "handed_off"
)

var ErrImagePreparationNotOwned = errors.New("state: image preparation belongs to another node")

type ImagePreparation struct {
	DeploymentID, NodeName, InputPath, InputKey, ClaimToken string
	InputBytes                                              int64
	Phase                                                   ImagePreparationPhase
	UpdatedAt                                               time.Time
}

// DeploymentImagePreparationStore keeps internal artifact paths out of the
// public stage projection. Callers hold DeploymentActivationLocker across work;
// a new begin also fences metadata publication by a replaced worker token.
type DeploymentImagePreparationStore interface {
	TransitionImagePreparation(context.Context, string, string, DeploymentStatus) error
	BeginImagePreparation(context.Context, string, string) (ImagePreparation, error)
	PublishImagePreparationLayer(context.Context, string, string, string, string, int64) error
	AdvanceImagePreparation(context.Context, string, string, ImagePreparationPhase, ImagePreparationPhase) error
	ListResumableImagePreparations(context.Context, string, int) ([]BuildImageWork, error)
}

func imagePreparationCanBegin(status DeploymentStatus, prior *ImagePreparation, node string) error {
	if status.IsTerminal() || status == DeployLive {
		return ErrNotFound
	}
	if prior == nil {
		if status != DeployPending && status != DeployBuilding {
			return ErrNotFound
		}
		return nil
	}
	if prior.Phase == ImageHandedOff {
		return ErrNotFound
	}
	if node != "" && prior.NodeName != "" && strings.TrimSpace(prior.NodeName) != node {
		return ErrImagePreparationNotOwned
	}
	return nil
}

func imagePreparationCanAdvance(status DeploymentStatus, from, to ImagePreparationPhase) bool {
	return (from == ImageLayerPublished && to == ImageScanComplete && status == DeployImaging) ||
		(from == ImageScanComplete && to == ImageHandedOff && status == DeploySnapshotting)
}

var _ DeploymentImagePreparationStore = (*PgStore)(nil)
var _ DeploymentImagePreparationStore = (*MemStore)(nil)

func imagePreparationCanTransition(status DeploymentStatus, phase ImagePreparationPhase, next DeploymentStatus) bool {
	if next == DeployImaging {
		return (phase == ImagePreparing && (status == DeployPending || status == DeployBuilding || status == DeployImaging)) || (phase == ImageLayerPublished && status == DeployImaging)
	}
	return next == DeploySnapshotting && phase == ImageScanComplete && (status == DeployImaging || status == DeploySnapshotting)
}

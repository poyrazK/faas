package state

// adr: 393

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/imagechain"
	"github.com/onebox-faas/faas/pkg/ociref"
)

// BaseImageProducer is immutable platform producer evidence. Company approval,
// current scans and native boot still require their separate owner boundaries.
type BaseImageProducer struct {
	ID, InputHash string
	Input         BaseImageProducerInput
	PublishedAt   time.Time
}
type BaseImageProducerInput struct {
	ID                    string                            `json:"-"`
	Artifact              imagechain.BaseArtifact           `json:"artifact"`
	SourceReference       string                            `json:"source_reference"`
	SourceDigest          string                            `json:"source_digest"`
	SelectedDigest        string                            `json:"selected_digest"`
	ImageChain            *imagechain.Evidence              `json:"image_chain"`
	LayoutVersion         string                            `json:"layout_version"`
	GuestInitDigest       string                            `json:"guest_init_digest"`
	ContentBytes          int64                             `json:"content_bytes"`
	LayerStart            int                               `json:"layer_start"`
	Layers                []imagechain.LayerConsumption     `json:"layers"`
	ParentProducerID      string                            `json:"parent_producer_id,omitempty"`
	ParentInputHash       string                            `json:"parent_input_hash,omitempty"`
	ParentMaterialization *imagechain.ParentMaterialization `json:"parent_materialization,omitempty"`
}
type BaseImageProducerStore interface {
	PublishBaseImageProducer(context.Context, BaseImageProducerInput) (BaseImageProducer, error)
	GetCurrentBaseImageProducer(context.Context, string) (BaseImageProducer, error)
	GetBaseImageProducerByID(context.Context, string) (BaseImageProducer, error)
}

func cloneBaseImageProducer(value BaseImageProducer) BaseImageProducer {
	value.Input.ImageChain = value.Input.ImageChain.Clone()
	value.Input.Layers = append([]imagechain.LayerConsumption(nil), value.Input.Layers...)
	if value.Input.ParentMaterialization != nil {
		p := *value.Input.ParentMaterialization
		value.Input.ParentMaterialization = &p
	}
	return value
}
func prepareBaseImageProducer(input BaseImageProducerInput) (BaseImageProducerInput, string, error) {
	in := cloneBaseImageProducer(BaseImageProducer{Input: input}).Input
	if !validStandardResourceRead(in.ID, in.ID) || !in.Artifact.Valid() || in.ContentBytes < 0 || in.LayoutVersion != imagechain.BaseLayoutVersion || in.GuestInitDigest != "" && ociref.ValidateDigest(in.GuestInitDigest) != nil {
		return in, "", ErrInvalidArgument
	}
	ref, err := ociref.ParseReference(in.SourceReference)
	if err != nil || ref.Digest != in.SourceDigest || ref.Tag != "" || ref.String() != in.SourceReference {
		return in, "", ErrInvalidArgument
	}
	image, err := imagechain.Validate(in.ImageChain, in.SourceDigest, in.SelectedDigest)
	if err != nil || imagechain.ValidateConsumption(image, in.LayerStart, in.Layers) != nil {
		return in, "", ErrInvalidArgument
	}
	if in.ParentProducerID == "" {
		if in.LayerStart != 0 || in.ParentInputHash != "" || in.ParentMaterialization != nil {
			return in, "", ErrInvalidArgument
		}
	} else {
		if !validStandardResourceRead(in.ParentProducerID, in.ParentProducerID) || sameStandardUUID(in.ID, in.ParentProducerID) || len(in.ParentInputHash) != 64 || in.ParentMaterialization == nil || !in.ParentMaterialization.Valid() || in.LayerStart == 0 {
			return in, "", ErrInvalidArgument
		}
		in.ParentProducerID = canonicalStandardUUID(in.ParentProducerID)
	}
	in.ID = canonicalStandardUUID(in.ID)
	hash, err := standardReviewDigest(in)
	return in, hash, err
}
func validateBaseImageProducer(value BaseImageProducer) error {
	in, hash, err := prepareBaseImageProducer(value.Input)
	if err != nil {
		return err
	}
	if in.ID != value.ID || hash != value.InputHash || value.PublishedAt.IsZero() {
		return fmt.Errorf("base producer stored binding mismatch")
	}
	return nil
}
func checkBaseImageProducerParent(in BaseImageProducerInput, parent BaseImageProducer) error {
	if err := validateBaseImageProducer(parent); err != nil {
		return err
	}
	if in.ParentProducerID != parent.ID || in.ParentInputHash != parent.InputHash || in.Artifact.StorageKey == parent.Input.Artifact.StorageKey || in.ParentMaterialization == nil || in.ParentMaterialization.Artifact != parent.Input.Artifact {
		return ErrApplicationStandardRuntimeStale
	}
	child, err := imagechain.Validate(in.ImageChain, in.SourceDigest, in.SelectedDigest)
	if err != nil {
		return err
	}
	base, err := imagechain.Validate(parent.Input.ImageChain, parent.Input.SourceDigest, parent.Input.SelectedDigest)
	if err != nil || len(base.DiffIDs) != in.LayerStart || len(child.DiffIDs) < in.LayerStart {
		return ErrApplicationStandardRuntimeStale
	}
	for i, diff := range base.DiffIDs {
		if diff != child.DiffIDs[i] {
			return ErrApplicationStandardRuntimeStale
		}
	}
	return nil
}

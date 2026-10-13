package state

import (
	"errors"
	"testing"
)

func TestCheckpointCompensationCannotFinishThroughResourceLabels(t *testing.T) {
	for _, kind := range []string{"data_checkpoint", "data_checkpoint_receipt", "data_checkpoint_adoption", "data_checkpoint_release"} {
		t.Run(kind, func(t *testing.T) {
			resource := ProjectEnvironmentCloneResource{Kind: kind, Name: "production", SourceID: "original-owner", Status: "compensated"}
			op := ProjectEnvironmentCloneOperation{Status: CloneOperationCompensating, Resources: []ProjectEnvironmentCloneResource{resource}}
			if err := validateCloneResourceTransition(op, CloneOperationCompensated, op.Resources, ""); !errors.Is(err, ErrConflict) {
				t.Fatalf("unverified checkpoint cleanup became terminal: %v", err)
			}
			if err := validateCloneResourceTransition(op, CloneOperationCompensating, op.Resources, ""); err != nil {
				t.Fatalf("checkpoint cleanup recovery blocked: %v", err)
			}
		})
	}
	legacy := ProjectEnvironmentCloneOperation{Status: CloneOperationCompensating, Resources: []ProjectEnvironmentCloneResource{{Kind: "source_revision", Name: "production", SourceVersion: "original-revision", Status: "compensated"}}}
	if err := validateCloneResourceTransition(legacy, CloneOperationCompensated, legacy.Resources, ""); err != nil {
		t.Fatalf("legacy compensation blocked: %v", err)
	}
}

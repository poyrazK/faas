package state_test

import (
	"errors"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

func TestCheckpointCompensationCannotFinishThroughResourceLabels(t *testing.T) {
	for _, kind := range []string{"data_checkpoint", "data_checkpoint_receipt", "data_checkpoint_adoption", "data_checkpoint_release"} {
		t.Run(kind, func(t *testing.T) {
			resource := state.ProjectEnvironmentCloneResource{Kind: kind, Name: "production", SourceID: "original-owner", Status: "compensated"}
			op := state.ProjectEnvironmentCloneOperation{Status: state.CloneOperationCompensating, Resources: []state.ProjectEnvironmentCloneResource{resource}}
			if err := state.ValidateCloneResourceTransition(op, state.CloneOperationCompensated, op.Resources, ""); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("unverified checkpoint cleanup became terminal: %v", err)
			}
			if err := state.ValidateCloneResourceTransition(op, state.CloneOperationCompensating, op.Resources, ""); err != nil {
				t.Fatalf("checkpoint cleanup recovery blocked: %v", err)
			}
		})
	}
	legacy := state.ProjectEnvironmentCloneOperation{Status: state.CloneOperationCompensating, Resources: []state.ProjectEnvironmentCloneResource{{Kind: "source_revision", Name: "production", SourceVersion: "original-revision", Status: "compensated"}}}
	if err := state.ValidateCloneResourceTransition(legacy, state.CloneOperationCompensated, legacy.Resources, ""); err != nil {
		t.Fatalf("legacy compensation blocked: %v", err)
	}
}

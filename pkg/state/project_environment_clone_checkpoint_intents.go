package state

import "context"

// A lease-scoped private journal. Its kinds are intentionally NOT implemented
// publication strategies: a selected common checkpoint does not publish a clone.
type ProjectEnvironmentCloneCheckpointIntentStore interface {
	AdvanceProjectEnvironmentCloneCheckpointIntent(context.Context, ProjectEnvironmentCloneLease, []ProjectEnvironmentCloneResource) (ProjectEnvironmentCloneOperation, error)
}

func validateCloneCheckpointIntentTransition(op ProjectEnvironmentCloneOperation, resources []ProjectEnvironmentCloneResource) error {
	if op.Status != CloneOperationCapturing || len(resources) < 1 || len(resources) > 2 {
		return ErrConflict
	}
	if err := validateCloneResourceTransition(op, CloneOperationCapturing, resources, ""); err != nil {
		return err
	}
	intent := resources[0]
	if intent.Kind != "data_checkpoint" || intent.Name != op.SourceEnvironment || intent.SourceID != op.ID || !validCloneObjectSHA256(intent.SourceVersion) {
		return ErrConflict
	}
	switch intent.Status {
	case "planned", "capturing":
		if len(resources) != 1 || intent.TargetID != "" || intent.CapturePoint != "" {
			return ErrConflict
		}
	case "captured":
		if len(resources) != 2 || intent.TargetID == "" || intent.CapturePoint == "" {
			return ErrConflict
		}
		proof := resources[1]
		if proof.Kind != "data_checkpoint_receipt" || proof.Name != intent.Name || proof.SourceID != intent.TargetID ||
			!validCloneObjectSHA256(proof.SourceVersion) || proof.CapturePoint != intent.CapturePoint || proof.TargetID != "" || proof.Status != "captured" {
			return ErrConflict
		}
	default:
		return ErrConflict
	}
	if len(op.Resources) == 0 {
		if intent.Status != "planned" {
			return ErrConflict
		}
		return nil
	}
	if len(op.Resources) != 1 || op.Resources[0].Kind != "data_checkpoint" {
		return ErrConflict
	}
	switch op.Resources[0].Status {
	case "planned":
		if intent.Status != "capturing" {
			return ErrConflict
		}
	case "capturing":
		if intent.Status != "captured" {
			return ErrConflict
		}
	default:
		return ErrConflict
	}
	return nil
}

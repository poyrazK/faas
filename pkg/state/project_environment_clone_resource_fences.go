package state

import "strings"

func cloneResourceKey(resource ProjectEnvironmentCloneResource) string {
	return resource.Kind + "\x00" + resource.Name
}

func validateCloneResourceTransition(op ProjectEnvironmentCloneOperation, nextStatus string, resources []ProjectEnvironmentCloneResource, errorCode string) error {
	byKey := make(map[string]ProjectEnvironmentCloneResource, len(resources))
	for _, resource := range resources {
		if resource.Kind == "" || resource.Name == "" || len(resource.Kind) > 128 || len(resource.Name) > 1024 ||
			strings.ContainsRune(resource.Kind+resource.Name, '\x00') || len(resource.SourceID) > 4096 || len(resource.SourceVersion) > 4096 ||
			len(resource.TargetID) > 4096 || len(resource.CapturePoint) > 4096 || !validCloneResourceStatus(resource.Status) {
			return ErrInvalidProjectEnvironmentCloneOperation
		}
		key := cloneResourceKey(resource)
		if _, ok := byKey[key]; ok {
			return ErrInvalidProjectEnvironmentCloneOperation
		}
		byKey[key] = resource
	}
	for _, previous := range op.Resources {
		next, ok := byKey[cloneResourceKey(previous)]
		if !ok || previous.SourceID != "" && previous.SourceID != next.SourceID ||
			previous.SourceVersion != "" && previous.SourceVersion != next.SourceVersion ||
			previous.TargetID != "" && previous.TargetID != next.TargetID ||
			previous.CapturePoint != "" && previous.CapturePoint != next.CapturePoint {
			return ErrConflict
		}
		if op.Status != CloneOperationPending && op.Status != CloneOperationCapturing &&
			(previous.SourceID != next.SourceID || previous.SourceVersion != next.SourceVersion || previous.CapturePoint != next.CapturePoint) {
			return ErrConflict
		}
	}
	if op.Status != CloneOperationPending && op.Status != CloneOperationCapturing && len(resources) != len(op.Resources) {
		return ErrConflict
	}
	if nextStatus == CloneOperationCopying {
		for _, resource := range resources {
			if !cloneResourceHasImplementedStrategy(resource.Kind) || resource.Status != "captured" && resource.Status != "ready" {
				return ErrConflict
			}
		}
	}
	if nextStatus == CloneOperationPublishing || nextStatus == CloneOperationReady {
		return validateCloneReadyResources(op, resources, errorCode)
	}
	if nextStatus == CloneOperationCompensated {
		for _, resource := range resources {
			if resource.Status != "compensated" {
				return ErrConflict
			}
		}
	}
	return nil
}

func validCloneResourceStatus(status string) bool {
	switch status {
	case "planned", "capturing", "captured", "copying", "verifying", "ready", "failed", "unsupported", "compensating", "compensated":
		return true
	default:
		return false
	}
}

// Every complete inventory includes its captured root revision, even for an
// empty project. This is a store gate; the capture worker must still prove that
// the inventory covers the actual effective environment.
func validateCloneReadyResources(op ProjectEnvironmentCloneOperation, resources []ProjectEnvironmentCloneResource, errorCode string) error {
	if len(resources) == 0 || errorCode != "" {
		return ErrConflict
	}
	root := false
	for _, resource := range resources {
		if resource.Status != "ready" || !cloneResourceHasImplementedStrategy(resource.Kind) {
			return ErrConflict
		}
		if resource.Kind == "source_revision" {
			if root || resource.Name != op.SourceEnvironment || resource.SourceVersion != op.SourceRevisionHash {
				return ErrConflict
			}
			root = true
		}
		if (resource.Kind == "object_storage" || resource.Kind == "managed_postgres" || resource.Kind == "postgres" || resource.Kind == "workload") &&
			(resource.SourceID == "" || resource.TargetID == "" || resource.SourceID == resource.TargetID ||
				(resource.SourceVersion == "" && resource.CapturePoint == "")) {
			return ErrConflict
		}
	}
	if !root {
		return ErrConflict
	}
	return nil
}

// New dependency kinds require an explicit strategy before they can enter a
// successful complete-clone receipt. Unsupported inventory remains visible.
func cloneResourceHasImplementedStrategy(kind string) bool {
	switch kind {
	case "source_revision", "project_config", "workload", "workload_settings", "variables", "secrets", "route_policy", "edge_policy", "managed_postgres", "postgres", "object_storage":
		return true
	default:
		return false
	}
}

type projectCloneObjectCopyProof struct {
	sourceID, targetID, hash, capture      string
	objectCount, entryCount, verifiedCount int
}

func validateCloneObjectCopyProofs(resources []ProjectEnvironmentCloneResource, proofs []projectCloneObjectCopyProof) error {
	buckets := make(map[string]ProjectEnvironmentCloneResource)
	for _, resource := range resources {
		if resource.Kind == "object_storage" {
			if _, ok := buckets[resource.SourceID]; ok {
				return ErrConflict
			}
			buckets[resource.SourceID] = resource
		}
	}
	if len(buckets) != len(proofs) {
		return ErrConflict
	}
	for _, proof := range proofs {
		resource, ok := buckets[proof.sourceID]
		if !ok || resource.TargetID != proof.targetID || resource.SourceVersion != proof.hash || resource.CapturePoint != proof.capture ||
			proof.entryCount != proof.objectCount || proof.verifiedCount != proof.objectCount {
			return ErrConflict
		}
	}
	return nil
}

package bindings

import (
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

// ValidateReleasePolicyRoute protects explicit requirements from older clients
// and makes unsupported servers reject new policy writes before mutation.
func ValidateReleasePolicyRoute(request api.SetBindingReleasePolicyRequest, current api.BindingReleasePolicy, route string) error {
	if (route == "queues") != (request.QueueRequirements != nil) {
		return fmt.Errorf("use release-policy-with-queues with explicit queue_requirements (an empty object clears them)")
	}
	if route != "queues" && (route == "storage") != (request.ObjectStorageRequirements != nil) {
		return fmt.Errorf("use release-policy-with-object-storage with explicit object_storage_requirements (an empty object clears them)")
	}
	if len(current.QueueRequirements) > 0 && request.QueueRequirements == nil {
		return fmt.Errorf("this policy has queue requirements; use release-policy-with-queues to retain or explicitly clear them")
	}
	if len(current.ObjectStorageRequirements) > 0 && request.ObjectStorageRequirements == nil {
		return fmt.Errorf("this policy has object-storage requirements; use release-policy-with-object-storage or release-policy-with-queues to retain or explicitly clear them")
	}
	return nil
}

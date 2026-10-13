package bindingcheck

import (
	"fmt"
	"maps"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

func cloneStorageRequirements(requirements map[string]string) map[string]string {
	return maps.Clone(requirements)
}

func objectStorageEvidenceMode(v *api.BindingVerification) string {
	if v.ObjectStorageMode == "" {
		return "read" // Old servers only produced the five-stage listing canary.
	}
	return v.ObjectStorageMode
}

func storageModeCovers(actual, required string) bool {
	return actual == required || actual == "read_write"
}

func (r *Report) checkObjectStorageRequirements(inventory api.AppBindingInventory, policy Policy) {
	for prefix := range policy.ObjectStorageRequirements {
		count := 0
		for _, item := range inventory.Bindings {
			if item.Type == api.BindingTypeObjectStorage && item.Scope == r.Scope && item.Binding == prefix {
				count++
			}
		}
		if count != 1 {
			code, message := "object_storage_binding_missing", "Required object-storage prefix is not bound in the selected deployment scope."
			if count > 1 {
				code, message = "object_storage_binding_ambiguous", "Required object-storage prefix is not unique in the selected deployment scope."
			}
			r.Blockers = append(r.Blockers, Finding{Code: code, Type: api.BindingTypeObjectStorage, Binding: prefix, Scope: r.Scope, Message: message})
		}
	}
}

func (r *Report) checkObjectStorageOperations(item api.AppBindingInventoryItem, required string) {
	if required == "" {
		return
	}
	if !storageModeCovers(item.Access, required) {
		r.bindingBlock(item, "object_storage_permission_insufficient", fmt.Sprintf("Binding permission does not cover required %s access; update this managed binding's permission.", required))
	}
	if item.Verification == nil || !storageModeCovers(objectStorageEvidenceMode(item.Verification), required) {
		hint := "Run bindings verify with --object-storage " + item.Binding + " for the exact deployment."
		if required != "read" {
			hint = "Run bindings verify with --object-storage " + item.Binding + " --object-storage-write for the exact deployment; successful upload and cleanup"
			if required == "read_write" {
				hint += " and byte-for-byte download"
			}
			hint += " evidence is required."
		}
		r.bindingBlock(item, "object_storage_operations_missing", "Recorded probe operations do not cover required "+required+" access. "+hint)
		return
	}
	stages := []string{"bucket_access"}
	if required == "write" || required == "read_write" {
		stages = []string{"upload", "cleanup"}
		if required == "read_write" {
			stages = append(stages, "download")
		}
	}
	var incomplete []string
	for _, stage := range stages {
		count, passed := 0, false
		for _, check := range item.Verification.Checks {
			if check.Name == stage {
				count++
				passed = check.Status == "passed"
			}
		}
		if count != 1 || !passed {
			incomplete = append(incomplete, stage)
		}
	}
	if len(incomplete) > 0 {
		r.bindingBlock(item, "object_storage_operations_incomplete", "Required storage stages are missing or unsuccessful: "+strings.Join(incomplete, ", ")+". Verify this exact deployment again; writes require explicit --object-storage-write.")
	}
}

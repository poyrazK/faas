package bindings

import (
	"maps"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/bindingcheck"
)

func compareDiagnosticMetadata(out *api.BindingVerificationRunDiagnostics, r api.BindingVerificationRun, i api.AppBindingInventory, p api.BindingReleasePolicy) {
	change := func(field, old, current, code string, item api.AppBindingInventoryItem) {
		if old == current {
			return
		}
		out.Changes = append(out.Changes, api.BindingVerificationDifference{App: r.App, Type: item.Type, Name: item.Name, Binding: item.Binding, Scope: r.Scope, Field: field, Reviewed: old, Current: current})
		out.Blockers = append(out.Blockers, api.BindingVerificationDiagnosticBlocker{App: r.App, Type: item.Type, Name: item.Name, Binding: item.Binding, Scope: r.Scope, Code: code, Message: "The reviewed public " + strings.ReplaceAll(field, "_", " ") + " changed.", Recovery: "review_new_run"})
	}
	empty := api.AppBindingInventoryItem{}
	change("policy_revision", strconv.FormatInt(r.Policy.Revision, 10), strconv.FormatInt(p.Revision, 10), "policy_changed", empty)
	change("policy_mode", diagnosticEnum(r.Policy.Mode), diagnosticEnum(p.Mode), "policy_changed", empty)
	change("maximum_evidence_age", diagnosticDuration(r.Policy.MaxVerificationAge), diagnosticDuration(p.MaxVerificationAge), "policy_changed", empty)
	change("require_application_ack", strconv.FormatBool(r.Policy.RequireApplicationAck), strconv.FormatBool(p.RequireApplicationAck), "policy_changed", empty)
	for _, name := range diagnosticMapKeys(r.Policy.ObjectStorageRequirements, p.ObjectStorageRequirements) {
		change("object_storage_requirement", diagnosticEnum(r.Policy.ObjectStorageRequirements[name]), diagnosticEnum(p.ObjectStorageRequirements[name]), "policy_changed", api.AppBindingInventoryItem{Type: api.BindingTypeObjectStorage, Binding: name})
	}
	for _, name := range diagnosticMapKeys(r.Policy.QueueRequirements, p.QueueRequirements) {
		change("queue_requirement", diagnosticEnum(r.Policy.QueueRequirements[name]), diagnosticEnum(p.QueueRequirements[name]), "policy_changed", api.AppBindingInventoryItem{Type: api.BindingTypeQueue, Name: name, Binding: name})
	}
	if i.RuntimeFreshness == nil || i.RuntimeFreshness.ConfigChangedAt == nil || i.RuntimeFreshness.ConfigChangedAt.IsZero() || i.RuntimeFreshness.ConfigChangedAt.After(time.Now()) || i.RuntimeFreshness.Source != "instance_started_at" {
		out.Complete = false
		out.Blockers = append(out.Blockers, diagnosticBlocker("configuration_stamp_unavailable", "The current configuration timestamp is unavailable or contradictory.", "retry_diagnostics"))
	} else if r.Inventory.RuntimeFreshness != nil && r.Inventory.RuntimeFreshness.ConfigChangedAt != nil {
		change("configuration_changed_at", r.Inventory.RuntimeFreshness.ConfigChangedAt.UTC().Format(time.RFC3339Nano), i.RuntimeFreshness.ConfigChangedAt.UTC().Format(time.RFC3339Nano), "configuration_changed", empty)
	}
	for _, name := range diagnosticMapKeys(r.Inventory.ServiceTargets, i.ServiceTargets) {
		change("service_target_deployment", diagnosticUUID(r.Inventory.ServiceTargets[name]), diagnosticUUID(i.ServiceTargets[name]), "service_target_changed", api.AppBindingInventoryItem{Type: api.BindingTypeService, Name: name, Binding: name})
	}
	old, current := diagnosticBindings(r.Inventory, r.Scope), diagnosticBindings(i, r.Scope)
	keys := make([]string, 0)
	for key := range old {
		keys = append(keys, key)
	}
	for key := range current {
		if _, exists := old[key]; !exists {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		a, had := old[key]
		b, has := current[key]
		item := a
		if !had {
			item = b
		}
		change("binding_present", strconv.FormatBool(had), strconv.FormatBool(has), "binding_selection_changed", item)
		if !had || !has {
			continue
		}
		change("access", diagnosticEnum(a.Access), diagnosticEnum(b.Access), "binding_metadata_changed", item)
		change("state", diagnosticEnum(a.State), diagnosticEnum(b.State), "binding_metadata_changed", item)
		change("credential_generation", diagnosticGeneration(a.CredentialGeneration), diagnosticGeneration(b.CredentialGeneration), "credential_generation_changed", item)
		change("credential_configured", diagnosticBool(a.CredentialConfigured), diagnosticBool(b.CredentialConfigured), "binding_metadata_changed", item)
		change("rotation_pending", diagnosticBool(a.RotationPending), diagnosticBool(b.RotationPending), "binding_rotation_changed", item)
		change("queue_canary_supported", strconv.FormatBool(a.QueueCanarySupported), strconv.FormatBool(b.QueueCanarySupported), "queue_capability_changed", item)
		change("service_target_deployment", diagnosticUUID(a.ServiceTargetDeploymentID), diagnosticUUID(b.ServiceTargetDeploymentID), "service_target_changed", item)
	}
	// The public digest can change on metadata that intentionally has no value
	// comparison (for example a protected origin/pin). Do not guess its contents.
	if len(out.Changes) == 0 && bindingcheck.VerificationReviewDigest(i, p) != r.ReviewDigest {
		out.Blockers = append(out.Blockers, diagnosticBlocker("binding_metadata_changed", "The public review identity changed; protected metadata has no value comparison.", "review_new_run"))
	}
}
func diagnosticBindings(i api.AppBindingInventory, scope string) map[string]api.AppBindingInventoryItem {
	out := map[string]api.AppBindingInventoryItem{}
	for _, item := range i.Bindings {
		if item.Type == api.BindingTypeQueue {
			if !api.QueueBindingInScope(item, scope) {
				continue
			}
		} else if (item.Type == api.BindingTypePostgres || item.Type == api.BindingTypeObjectStorage) && (item.Scope != scope || item.Type == api.BindingTypePostgres && item.Access == "migration") {
			continue
		}
		out[item.Type+"\x00"+item.Name+"\x00"+item.Binding+"\x00"+item.Scope] = item
	}
	return out
}
func diagnosticMapKeys(a, b map[string]string) []string {
	all := maps.Clone(a)
	if all == nil {
		all = map[string]string{}
	}
	for key, value := range b {
		all[key] = value
	}
	keys := make([]string, 0, len(all))
	for key := range all {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
func diagnosticGeneration(v *int64) string {
	if v == nil {
		return "unknown"
	}
	return strconv.FormatInt(*v, 10)
}
func diagnosticBool(v *bool) string {
	if v == nil {
		return "unknown"
	}
	return strconv.FormatBool(*v)
}
func diagnosticDuration(s string) string {
	d, err := time.ParseDuration(s)
	if err != nil {
		return "invalid"
	}
	return d.String()
}
func diagnosticUUID(s string) string {
	if s == "" {
		return "unset"
	}
	if len(s) == 32 {
		s = s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:]
	}
	if len(s) != 36 {
		return "unknown"
	}
	for index, ch := range s {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			if ch != '-' {
				return "unknown"
			}
			continue
		}
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return "unknown"
		}
	}
	return s
}
func diagnosticEnum(s string) string {
	switch s {
	case "":
		return "unset"
	case "off", "warn", "enforce", "invoke", "declared", "revoked", "available", "configured", "active", "ready", "enabled", "disabled", "pending", "creating", "provisioning", "deleting", "deleted", "failed", "blocked", "unknown", "unavailable", "not_configured", "read", "write", "read_write", "migration", "consumer_readiness", "handler_completion":
		return s
	default:
		return "unknown"
	}
}

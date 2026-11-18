package mcpmanagement

import (
	"context"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/tasktoolbinding"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

func (m *management) getUploadedTaskBindings(ctx context.Context, a getUploadedTaskIntentArgs) (any, error) {
	if err := m.hostedTaskSourceAccess(); err != nil {
		return nil, err
	}
	if !toolpublication.ValidID(a.IntentID) {
		return nil, invalid("invalid_argument", "intent_id must be a lowercase SHA256")
	}
	account, err := m.uploadedTaskAccount(ctx)
	if err != nil {
		return nil, err
	}
	record, err := m.client.GetHostedTaskBindings(ctx, a.IntentID)
	if err != nil {
		return nil, err
	}
	if tasktoolbinding.ValidateReceipt(record, account, a.IntentID) != nil {
		return nil, invalid("invalid_task_binding_receipt", "native Task binding receipt is invalid")
	}
	return tasktoolbinding.View(record, time.Now()), nil
}
func uploadedTaskBindingOutputSchema() map[string]any {
	props := map[string]any{}
	for _, n := range []string{"intent_id", "intent_sha256", "review_sha256", "binding_sha256"} {
		props[n] = map[string]any{"type": "string", "pattern": "^[0-9a-f]{64}$"}
	}
	props["account_id"] = map[string]any{"type": "string", "format": "uuid"}
	for _, n := range []string{"created_at", "expires_at", "checked_at", "valid_until"} {
		props[n] = map[string]any{"type": "string", "format": "date-time"}
	}
	props["version"] = map[string]any{"type": "integer", "const": 1}
	props["status"] = map[string]any{"type": "string", "enum": []string{"selected", "verifying", "passed", "failed", "stale", "revoked", "expired"}}
	props["ready_to_apply"] = map[string]any{"type": "boolean", "const": false}
	props["bindings_verified"] = map[string]any{"type": "boolean", "const": false}
	props["owner_authorization_required"] = map[string]any{"type": "boolean", "const": true}
	props["checks"] = map[string]any{"type": "array", "minItems": 7, "maxItems": 7, "items": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"name", "status"}, "properties": map[string]any{"name": map[string]any{"type": "string", "enum": tasktoolbinding.ResultNames()}, "status": map[string]any{"type": "string", "enum": []string{"not_checked", "passed", "failed"}}}}}
	required := []string{}
	for name := range props {
		if name != "checked_at" && name != "valid_until" {
			required = append(required, name)
		}
	}
	slices.Sort(required)
	return map[string]any{"type": "object", "additionalProperties": false, "properties": props, "required": required}
}

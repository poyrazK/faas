package mcpmanagement

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

// A deploy applies the directory's gregale.yaml deployment settings
// (execution mode, replicas, scaling policy) with PATCH /v1/apps/{slug},
// which the agent budget prices (ADR-20261111114059). A local deploy plan
// previews them, so the agent sees the cost before it deploys.

// deploySettingFields are the gregale.yaml settings a deploy applies; the
// CLI's deploySettingGroups name the same fields.
var deploySettingFields = []string{
	"execution_mode", "restart_policy", "startup_deadline_s", "max_retries",
	"request_timeout_s", "stop_grace_period_s", "stop_signal", "service_replicas",
	"worker_replicas", "after_restore", "before_checkpoint", "scaling_policy", "retry_policy",
}

// budgeted reports whether the account's credential pays from an agent
// budget: agent keys, and hosted sign-ins (mcp-login keys). A person's own
// key never does, so it is never asked to preview.
func (m *management) budgeted(account api.AccountResponse) bool {
	return slices.Contains(account.KeyScopes, api.ScopeAgent) || m.remote()
}

// previewDeploySettings prices the deployment settings of dir's
// gregale.yaml for app. A new app is priced as created with its execution
// mode and replicas; a scaling policy's floor is priced when the deploy
// applies it. Nil when nothing is priced or the preview fails: the deploy is
// priced and gated all the same.
func (m *management) previewDeploySettings(ctx context.Context, dir, app string, newApp bool) *api.AgentBudgetPreview {
	manifest, found, err := gregalemanifest.Load(dir)
	if err != nil || !found || manifest == nil || manifest.Validate() != nil {
		return nil
	}
	desired, _ := manifest.LifecycleRequest()
	desired.ScalingPolicy = manifest.ScalingPolicyRequest()
	data, err := json.Marshal(desired)
	if err != nil {
		return nil
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil {
		return nil
	}
	for key := range fields {
		if !slices.Contains(deploySettingFields, key) {
			delete(fields, key)
		}
	}
	if len(fields) == 0 {
		return nil
	}
	var out api.AgentBudgetPreview
	if newApp {
		create := api.CreateAppRequest{Slug: app, ServiceReplicas: desired.ServiceReplicas, WorkerReplicas: desired.WorkerReplicas}
		if desired.ExecutionMode != nil {
			create.ExecutionMode = *desired.ExecutionMode
		}
		out, err = m.client.PreviewCreateApp(ctx, create)
	} else {
		var update api.UpdateAppRequest
		if data, err = json.Marshal(fields); err != nil || json.Unmarshal(data, &update) != nil {
			return nil
		}
		out, err = m.client.PreviewUpdateApp(ctx, app, update)
	}
	if err != nil || !out.Charged {
		return nil
	}
	return &out
}

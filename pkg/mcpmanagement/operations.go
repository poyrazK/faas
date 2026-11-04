package mcpmanagement

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretscan"
)

// Requests are private journal inputs, not agent-facing plan contents. The
// encrypted storedPlan explicitly saves them; plans expose only affected keys.
type operationPlan struct {
	Changes          json.RawMessage `json:"changes,omitempty"`
	Kind             string          `json:"kind"`
	Request          json.RawMessage `json:"-"`
	Keys             []string        `json:"keys,omitempty"`
	ResourceID       string          `json:"resource_id,omitempty"`
	ExpectedRevision int64           `json:"expected_revision,omitempty"`
	AppHash          string          `json:"app_configuration_hash"`
	ResourceHash     string          `json:"resource_hash,omitempty"`
}

var operationKinds = map[string]bool{"restart": true, "scale": true, "env": true, "config_rollback": true, "cron_update": true, "cron_delete": true, "domain_delete": true}

type operationArgs struct {
	App         string `json:"app" jsonschema:"Existing app slug"`
	Project     string `json:"project,omitempty" jsonschema:"Project slug for a named environment"`
	Environment string `json:"environment,omitempty" jsonschema:"Named environment; omit for default"`
	Reason      string `json:"reason" jsonschema:"Reviewable reason; at most 280 characters"`
}

type scalePlanArgs struct {
	App            string `json:"app"`
	Project        string `json:"project,omitempty"`
	Environment    string `json:"environment,omitempty"`
	Reason         string `json:"reason"`
	RAMMB          *int   `json:"ram_mb,omitempty"`
	CPUMillicores  *int   `json:"cpu_millicores,omitempty"`
	MaxConcurrency *int   `json:"max_concurrency,omitempty"`
	MinInstances   *int   `json:"min_instances,omitempty"`
}

type configHistoryArgs struct {
	App         string `json:"app"`
	Project     string `json:"project,omitempty"`
	Environment string `json:"environment,omitempty"`
	Limit       int    `json:"limit,omitempty" jsonschema:"1 to 100; default 20"`
}

type configRollbackArgs struct {
	App         string `json:"app"`
	Project     string `json:"project,omitempty"`
	Environment string `json:"environment,omitempty"`
	Revision    int64  `json:"revision" jsonschema:"Exact revision from gregale_list_config_history"`
	Reason      string `json:"reason"`
}

type restartStatusArgs struct {
	App    string `json:"app"`
	WakeID string `json:"wake_id" jsonschema:"wake_id in the restart receipt"`
}

func (m *management) hasCapability(scope string) bool {
	return !m.remote() || slices.Contains(m.opts.Capabilities, scope)
}

func registerOperations(s *mcp.Server, m *management) {
	register(s, m, "gregale_get_config", "Inspect variable names, latest configuration revision and guard status in the selected scope. No variable or secret values are returned.", m.getConfig)
	register(s, m, "gregale_list_config_history", "List bounded configuration history and guard decisions without values.", m.listConfigHistory)
	register(s, m, "gregale_plan_restart", "Review a fresh cold restart of the default app scope so current variables and secrets reach running processes. Native restart is app-wide; named-environment-only connections cannot use it.", m.planRestart)
	register(s, m, "gregale_get_restart", "Inspect native durable restart processing by wake_id. Completion is separate from application health.", m.getRestart)
	register(s, m, "gregale_plan_scale", "Review RAM, CPU, concurrency or warm-instance changes. Pins configuration and named workload revision; native plan and residency limits apply.", m.planScale)
	if m.hasCapability(api.ScopeEnvWrite) {
		register(s, m, "gregale_plan_config_rollback", "Review restoring non-secret variables to an exact configuration revision. Secret values cannot be restored. Protected environments require their approved workflow.", m.planConfigRollback)
		register(s, m, "gregale_plan_env_change", "Review setting non-secret variables in a consented default or named scope. Credential-like inputs are rejected. No secret-write scope is required; apply with gregale_apply_env_change.", m.planNativeEnvChange)
		if m.opts.AllowWrites {
			registerTool(s, m, "gregale_apply_env_change", "Apply a reviewed non-secret configuration plan. All variables and one configuration revision commit atomically. Retry the same plan ID after an uncertain response to recover its original receipt.", false, m.applyOperation)
		}
	}
	if m.opts.AllowWrites {
		registerTool(s, m, "gregale_apply_operation", "Apply an unexpired restart, scaling, configuration or resource lifecycle plan. Reuse the plan ID after errors; completed receipts are durable when the connection has a journal.", false, m.applyOperation)
	}
}

func (m *management) operationBase(ctx context.Context, a operationArgs, kind string, request any) (*releasePlan, error) {
	if err := validateReason(a.App, a.Reason); err != nil {
		return nil, err
	}
	app, env, err := m.appEnvironment(ctx, a.App, a.Project, a.Environment, false)
	if err != nil {
		return nil, err
	}
	if env.Protected {
		return nil, invalid("approval_required", "protected environments require their native approved configuration or release workflow")
	}
	account, err := m.client.Whoami(ctx)
	if err != nil {
		return nil, err
	}
	hash, err := appConfigurationHash(app)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	p := &releasePlan{Action: "operation_" + kind, App: a.App, AppID: app.ID, AccountID: account.ID, APIBase: m.opts.APIBase, Profile: m.opts.Profile, Scope: normalizedScope(a.Environment), Project: a.Project, ProjectID: env.ProjectID, EnvironmentID: env.ID, Reason: a.Reason, ExpiresAt: time.Now().Add(releasePlanTTL), Operation: &operationPlan{Kind: kind, Request: data, AppHash: hex.EncodeToString(hash[:])}}
	if a.Environment != "" {
		_, p.WorkloadRevision, err = m.client.GetAppInEnvironmentRevision(ctx, a.App, a.Environment)
	}
	return p, err
}

func (m *management) planRestart(ctx context.Context, a operationArgs) (any, error) {
	if a.Project != "" || a.Environment != "" {
		return nil, invalid("unsupported_scope", "the native restart is app-wide; use an approved environment rollout for a named target")
	}
	p, err := m.operationBase(ctx, a, "restart", struct{}{})
	if err != nil {
		return nil, err
	}
	p.TrafficEffect = "Cold restart with current configuration. Active requests may be interrupted; follow wake_id with gregale_get_restart, then check health."
	return m.savePlan(p)
}

func (m *management) getRestart(ctx context.Context, a restartStatusArgs) (any, error) {
	if _, err := uuid.Parse(a.WakeID); err != nil {
		return nil, invalid("invalid_argument", "wake_id must be a native restart UUID")
	}
	if _, _, err := m.appEnvironment(ctx, a.App, "", "", false); err != nil {
		return nil, err
	}
	out, err := m.client.GetRuntimeConfigRestartStatus(ctx, a.App, a.WakeID)
	if err == nil && out.WakeID != a.WakeID {
		return nil, invalid("invalid_response", "restart identity differs from the requested wake_id")
	}
	return out, err
}

func (m *management) planScale(ctx context.Context, a scalePlanArgs) (any, error) {
	if a.RAMMB == nil && a.CPUMillicores == nil && a.MaxConcurrency == nil && a.MinInstances == nil {
		return nil, invalid("invalid_argument", "provide at least one scaling setting")
	}
	for _, n := range []*int{a.RAMMB, a.CPUMillicores, a.MaxConcurrency} {
		if n != nil && *n <= 0 {
			return nil, invalid("invalid_argument", "RAM, CPU and concurrency must be positive")
		}
	}
	if a.MinInstances != nil && *a.MinInstances < 0 {
		return nil, invalid("invalid_argument", "min_instances must be non-negative")
	}
	req := api.UpdateAppRequest{RAMMB: a.RAMMB, CPUMillicores: a.CPUMillicores, MaxConcurrency: a.MaxConcurrency, MinInstances: a.MinInstances}
	p, err := m.operationBase(ctx, operationArgs{a.App, a.Project, a.Environment, a.Reason}, "scale", req)
	if err != nil {
		return nil, err
	}
	p.TrafficEffect = "Change selected app resource settings. Warm instances and increased resource sizes can increase usage charges; native quotas and residency rules remain enforced."
	p.Operation.Changes = p.Operation.Request
	return m.savePlan(p)
}

func (m *management) getConfig(ctx context.Context, a appArgs) (any, error) {
	app, _, err := m.appEnvironment(ctx, a.App, a.Project, a.Environment, false)
	if err != nil {
		return nil, err
	}
	vars, err := m.client.GetAppsSlugEnvWithScope(ctx, a.App, normalizedScope(a.Environment))
	if err != nil {
		return nil, err
	}
	history, err := m.client.ListConfigRevisions(ctx, a.App, normalizedScope(a.Environment), 1)
	if err != nil {
		return nil, err
	}
	return map[string]any{"app_id": app.ID, "scope": normalizedScope(a.Environment), "variables": vars.Env, "revisions": history.Revisions, "guard": history.Guard}, nil
}

func (m *management) listConfigHistory(ctx context.Context, a configHistoryArgs) (any, error) {
	if _, _, err := m.appEnvironment(ctx, a.App, a.Project, a.Environment, false); err != nil {
		return nil, err
	}
	limit, err := pageLimit(a.Limit)
	if err != nil {
		return nil, err
	}
	return m.client.ListConfigRevisions(ctx, a.App, normalizedScope(a.Environment), limit)
}

func (m *management) configRevision(ctx context.Context, app, scope string) (int64, error) {
	r, err := m.client.ListConfigRevisions(ctx, app, normalizedScope(scope), 1)
	if err != nil || len(r.Revisions) == 0 {
		return 0, err
	}
	return r.Revisions[0].Revision, nil
}

func scanVariables(values map[string]string, app string) error {
	if _, _, err := envChangeInput(values); err != nil {
		return err
	}
	pairs := make([]secretscan.Pair, 0, len(values))
	for k, v := range values {
		if api.SensitiveConfigurationKey(k) {
			return secretRefusal(nil, app)
		}
		pairs = append(pairs, secretscan.Pair{Key: k, Value: v})
	}
	if len(secretscan.ScanEnvPairs(pairs, "MCP configuration")) > 0 {
		return secretRefusal(nil, app)
	}
	return nil
}

func (m *management) planNativeEnvChange(ctx context.Context, a planEnvChangeArgs) (any, error) {
	if !m.hasCapability(api.ScopeEnvWrite) {
		return nil, invalid("capability_required", "configuration changes require env:write consent")
	}
	if err := scanVariables(a.Values, a.App); err != nil {
		return nil, err
	}
	if a.Restart && a.Environment != "" {
		return nil, invalid("unsupported_scope", "restart is app-wide; omit restart for a named scope")
	}
	p, err := m.operationBase(ctx, operationArgs{a.App, a.Project, a.Environment, a.Reason}, "env", a)
	if err != nil {
		return nil, err
	}
	p.Operation.Keys = slices.Sorted(maps.Keys(a.Values))
	p.Operation.ExpectedRevision, err = m.configRevision(ctx, a.App, a.Environment)
	if err != nil {
		return nil, err
	}
	p.TrafficEffect = fmt.Sprintf("Atomically set %d non-secret variable(s) in %s with one revision and native config guards; existing instances retain their process environment until a cold restart.", len(a.Values), p.Scope)
	return m.savePlan(p)
}

func (m *management) planConfigRollback(ctx context.Context, a configRollbackArgs) (any, error) {
	if a.Revision < 1 {
		return nil, invalid("invalid_argument", "revision must be positive")
	}
	p, err := m.operationBase(ctx, operationArgs{a.App, a.Project, a.Environment, a.Reason}, "config_rollback", a.Revision)
	if err != nil {
		return nil, err
	}
	target, err := m.client.GetConfigRevision(ctx, a.App, p.Scope, a.Revision)
	if err != nil {
		return nil, err
	}
	if target.Revision != a.Revision || target.Scope != p.Scope {
		return nil, invalid("invalid_response", "configuration revision identity differs from the selection")
	}
	p.Operation.Keys = slices.Sorted(maps.Keys(target.Variables))
	p.Operation.ExpectedRevision, err = m.configRevision(ctx, a.App, p.Scope)
	if err != nil {
		return nil, err
	}
	p.TrafficEffect = "Restore variables from the reviewed revision; secret values are not restored. Inspect the returned differences and native config guard."
	return m.savePlan(p)
}

func (m *management) applyOperation(ctx context.Context, a applyArgs) (any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.plans[a.PlanID]
	if p == nil || p.Operation == nil {
		return nil, invalid("invalid_plan", "unknown operation plan; inspect state before planning again")
	}
	if _, err := m.checkedPlan(ctx, p.ID, p.Action); err != nil {
		return nil, err
	}
	if p.localReceipt != nil {
		return p.localReceipt, nil
	}
	if p.failure != nil && p.failure.Status == "refused" {
		problem := p.failure.Problem
		return nil, &api.APIError{Problem: api.Problem{Status: problem.Status, Code: problem.Code, Detail: problem.Message, Hint: problem.Hint}}
	}
	if (p.Operation.Kind == "env" || p.Operation.Kind == "config_rollback") && !m.hasCapability(api.ScopeEnvWrite) {
		return nil, invalid("capability_required", "configuration changes require env:write consent")
	}
	app, env, err := m.appEnvironment(ctx, p.App, p.Project, namedScope(p.Scope), true)
	if err != nil {
		return nil, err
	}
	if env.Protected {
		return nil, invalid("approval_required", "protected environments require native approval")
	}
	if !p.attempted {
		h, err := appConfigurationHash(app)
		if err != nil {
			return nil, err
		}
		if hex.EncodeToString(h[:]) != p.Operation.AppHash {
			return nil, invalid("plan_stale", "app configuration changed; review a new plan")
		}
	}
	wasAttempted := p.attempted
	p.attempted = true
	if err := m.persistPlans(); err != nil {
		return nil, err
	}
	ctx = api.ContextWithIdempotencyKey(ctx, "mcp-operation-"+p.ID)
	result, err := m.runOperation(ctx, p, wasAttempted)
	if err != nil {
		return nil, m.operationFailure(p, err, wasAttempted)
	}
	p.failure = nil
	p.localReceipt = map[string]any{"plan_id": p.ID, "app": p.App, "scope": p.Scope, "kind": p.Operation.Kind, "result": result}
	if err := m.persistPlans(); err != nil {
		return nil, err
	}
	return p.localReceipt, nil
}

func (m *management) runOperation(ctx context.Context, p *releasePlan, retry bool) (any, error) {
	switch p.Operation.Kind {
	case "restart":
		return m.client.RestartAppFresh(ctx, p.App)
	case "scale":
		var req api.UpdateAppRequest
		if err := json.Unmarshal(p.Operation.Request, &req); err != nil {
			return nil, err
		}
		if retry {
			app, _, err := m.appEnvironment(ctx, p.App, p.Project, namedScope(p.Scope), true)
			if err != nil {
				return nil, err
			}
			matches := (req.RAMMB == nil || app.RAMMB == *req.RAMMB) && (req.CPUMillicores == nil || app.CPUMillicores == *req.CPUMillicores) && (req.MaxConcurrency == nil || app.MaxConcurrency == *req.MaxConcurrency) && (req.MinInstances == nil || app.MinInstances == *req.MinInstances)
			if !matches {
				return nil, invalid("outcome_unknown", "inspect scaling settings before replanning; an ambiguous update will not overwrite another writer")
			}
			return projectApp(app), nil
		}
		var app api.AppResponse
		var err error
		if p.EnvironmentID != "" {
			app, err = m.client.UpdateAppInEnvironmentAtRevision(ctx, p.App, p.Scope, p.WorkloadRevision, req)
		} else {
			app, err = m.client.UpdateApp(ctx, p.App, req)
		}
		return projectApp(app), err
	case "env":
		return m.applyNativeEnv(ctx, p, retry)
	case "config_rollback":
		if err := m.checkOperationConfigRevision(ctx, p); err != nil {
			return nil, err
		}
		var revision int64
		if err := json.Unmarshal(p.Operation.Request, &revision); err != nil {
			return nil, err
		}
		return m.client.RollbackConfig(ctx, p.App, p.Scope, revision)
	case "cron_update", "cron_delete", "domain_delete":
		return m.applyResourceOperation(ctx, p)
	}
	return nil, invalid("invalid_plan", "unsupported operation kind")
}

func (m *management) checkOperationConfigRevision(ctx context.Context, p *releasePlan) error {
	revision, err := m.configRevision(ctx, p.App, p.Scope)
	if err != nil {
		return err
	}
	if revision != p.Operation.ExpectedRevision {
		return invalid("plan_stale", "configuration changed or a previous write has an unknown outcome; inspect configuration history before planning again")
	}
	return nil
}

func (m *management) applyNativeEnv(ctx context.Context, p *releasePlan, retry bool) (any, error) {
	var a planEnvChangeArgs
	if err := json.Unmarshal(p.Operation.Request, &a); err != nil {
		return nil, err
	}
	if err := scanVariables(a.Values, p.App); err != nil {
		return nil, err
	}
	requestID := envRequestID(p)
	result, err := m.client.ApplyEnvBatch(ctx, p.App, p.Scope, api.EnvBatchRequest{RequestID: requestID, ExpectedRevision: &p.Operation.ExpectedRevision, Values: a.Values})
	if err != nil {
		return nil, err
	}
	if !validConfigCheckpoint(p, &result) {
		return nil, invalid("invalid_response", "configuration receipt identity differs from this plan")
	}
	if p.configReceipt == nil {
		p.configReceipt = &result
		// A confirmed commit supersedes an earlier uncertain attempt. A crash
		// before the restart or final receipt restores this checkpoint as partial.
		p.failure = nil
		if err := m.persistPlans(); err != nil {
			return nil, err
		}
	} else if p.configReceipt.ConfigChange.Revision != result.ConfigChange.Revision {
		return nil, invalid("invalid_response", "configuration checkpoint changed across retries")
	}
	out := map[string]any{"updated_keys": result.UpdatedKeys, "revision": result.ConfigChange.Revision, "config_change": result.ConfigChange}
	if a.Restart {
		r, err := m.client.RestartAppFresh(ctx, p.App)
		if err != nil {
			return nil, err
		}
		out["wake_id"] = r.WakeID
	}
	return out, nil
}

func envRequestID(p *releasePlan) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("gregale-mcp-env:"+p.ID)).String()
}

func validConfigCheckpoint(p *releasePlan, r *api.EnvBatchResponse) bool {
	return p.Operation != nil && p.Operation.Kind == "env" && r.AppID == p.AppID && r.Scope == p.Scope && r.RequestID == envRequestID(p) && r.ConfigChange.Revision > p.Operation.ExpectedRevision && slices.Equal(r.UpdatedKeys, p.Operation.Keys) && len(r.DeletedKeys) == 0
}

func resourceFingerprint(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

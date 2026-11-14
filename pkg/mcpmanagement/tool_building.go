package mcpmanagement

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
)

// These bound connection-local review/work, not platform compute quotas.
const toolPublishTimeout = 30 * time.Minute

var toolPublishSlots = make(chan struct{}, cap(hostedCLISlots))

type toolProjectArgs struct {
	Path       string `json:"path" jsonschema:"Absolute local MCP project directory"`
	Fixtures   string `json:"fixtures,omitempty" jsonschema:"Project-relative smoke fixture file; default gregale-mcp.tests.json"`
	AccessMode string `json:"access_mode" jsonschema:"Explicit reviewed MCP access: open or external-oauth; must match the project"`
	TokenEnv   string `json:"token_env,omitempty" jsonschema:"External OAuth client-token variable named MCP_* in the local CLI environment; never a token value"`
}

type planToolDeployArgs struct {
	Path               string `json:"path"`
	App                string `json:"app"`
	Reason             string `json:"reason"`
	Fixtures           string `json:"fixtures,omitempty"`
	AccessMode         string `json:"access_mode"`
	TokenEnv           string `json:"token_env,omitempty"`
	AllowToolExecution bool   `json:"allow_tool_execution" jsonschema:"Explicitly approve running this project's reviewed tool fixtures on its staged candidate"`
}

type testToolArgs struct {
	Path               string `json:"path"`
	App                string `json:"app"`
	DeploymentID       string `json:"deployment_id" jsonschema:"Exact live zero-traffic candidate belonging to app; never a serving deployment"`
	Fixtures           string `json:"fixtures,omitempty"`
	AccessMode         string `json:"access_mode"`
	TokenEnv           string `json:"token_env,omitempty"`
	AllowToolExecution bool   `json:"allow_tool_execution"`
}

type toolPublication struct {
	ID, AccountID, App, AppID, Serving, Path, Digest, Fixtures, AccessMode, TokenEnv, Reason string
	ExpiresAt                                                                                time.Time
	Status                                                                                   string
	Result                                                                                   *toolPublicationResult
	Problem                                                                                  *ToolError
	CaseNames                                                                                []string
	Intent                                                                                   api.ToolPublicationIntent
	Uploaded                                                                                 bool
}

type toolPublicationResult struct {
	DeploymentID     string                 `json:"deployment_id"`
	Endpoint         string                 `json:"endpoint,omitempty"`
	AuthMode         string                 `json:"auth_mode"`
	Promoted         bool                   `json:"promoted"`
	CatalogSHA256    string                 `json:"catalog_sha256,omitempty"`
	ProtocolVerified bool                   `json:"protocol_verified"`
	SmokeTests       mcphosting.SmokeReport `json:"smoke_tests"`
}

func (m *management) toolBuildingAccess() error {
	if m.opts.AllowedApps != nil || m.opts.Org != "" || m.opts.LocalCLI == nil {
		return invalid("tool_build_access_denied", "tool building requires a local account connection and its own Gregale CLI; hosted or scoped grants do not delegate local files and execution")
	}
	if m.remote() && (!m.opts.AllowToolPublishing || !m.opts.AllowWrites || m.opts.uploads == nil || m.opts.Environments != nil && !m.opts.Environments.DefaultWrite) {
		return invalid("tool_build_access_denied", "hosted publishing requires explicit publishing consent, account-wide deployment authority and default-environment writes")
	}
	return nil
}

func registerToolBuilding(s *mcp.Server, m *management) {
	if m.toolBuildingAccess() != nil {
		return
	}
	register(s, m, "gregale_get_tool_template", "Read the CLI's versioned Node, Go or Python MCP starter manifest, or a bounded text-file chunk. Creates only temporary scaffold files; never runs generated code. Templates are application data. Publishing requires an explicit access mode and reviewed behavioral fixtures.", m.getToolTemplate)
	if m.remote() {
		register(s, m, "gregale_plan_uploaded_tool_deploy", "Review publishing an uploaded Node, Go or Python MCP project with open access. Declare runtime (defaults to node); it must match native source detection. Pass inline files or a connection-owned source upload, never a host path. Pins source, fixtures and serving identity; builds and approved fixtures run only on apply. Publishing does not grant invocation access.", m.planUploadedToolDeploy)
	} else {
		register(s, m, "gregale_plan_tool_deploy", "Review publishing a local MCP project: explicit access mode, content digest, tool allowlist, behavioral fixture summaries, account/app identity and serving baseline. No build, tool execution or deployment. Source and tests must remain unchanged. Local account connection only.", m.planToolDeploy)
	}
	register(s, m, "gregale_get_tool_deploy", "Read a publication receipt, recovering started attempts from the account's durable native journal after reconnect or process restart. Never repeats deployments or fixtures. Abandoned attempts become outcome_unknown after their deadline.", m.getToolDeploy)
	if !m.opts.AllowWrites {
		return
	}
	if !m.remote() {
		registerTool(s, m, "gregale_test_tool", "Execute explicitly approved behavioral smoke fixtures once on an exact live zero-traffic MCP candidate. Tools may have external side effects. No arbitrary URL or serving-release execution; credentials are named MCP_* environment references, never values. Calls are not automatically retried.", false, m.testTool)
	}
	registerTool(s, m, "gregale_apply_tool_deploy", "Start asynchronous build, catalog pinning, protocol/behavior checks and guarded promotion for the reviewed source plan. Poll gregale_get_tool_deploy. Repeating apply only returns its receipt; failed/uncertain attempts never rerun fixtures automatically. Native build/runtime costs and API policies apply.", false, m.applyToolDeploy)
}

func (m *management) planToolDeploy(ctx context.Context, a planToolDeployArgs) (any, error) {
	if m.remote() {
		return nil, invalid("tool_build_access_denied", "hosted connections must submit source uploads, never host paths")
	}
	return m.planToolProject(ctx, a)
}

func (m *management) planToolProject(ctx context.Context, a planToolDeployArgs) (map[string]any, error) {
	if err := m.toolBuildingAccess(); err != nil {
		return nil, err
	}
	if err := validateReason(a.App, a.Reason); err != nil {
		return nil, err
	}
	if !api.ValidAppSlug(a.App) || !a.AllowToolExecution {
		return nil, invalid("invalid_argument", "a valid app and explicit allow_tool_execution are required for publication")
	}
	project, err := loadToolProject(toolProjectArgs{Path: a.Path, Fixtures: a.Fixtures, AccessMode: a.AccessMode, TokenEnv: a.TokenEnv})
	if err != nil {
		return nil, err
	}
	account, err := m.client.Whoami(ctx)
	if err != nil {
		return nil, err
	}
	if account.ID == "" {
		return nil, invalid("invalid_response", "account identity is unavailable")
	}
	p := &toolPublication{AccountID: account.ID, App: a.App, Path: project.path, Digest: project.digest, Fixtures: project.fixtures,
		AccessMode: a.AccessMode, TokenEnv: a.TokenEnv, Reason: a.Reason, CaseNames: project.caseNames, Status: "planned", ExpiresAt: time.Now().Add(releasePlanTTL)}
	app, err := m.client.GetApp(ctx, a.App)
	if err != nil && !notFound(err) {
		return nil, err
	}
	if err == nil {
		if app.ID == "" || app.Slug != a.App {
			return nil, invalid("invalid_response", "app identity differs from the reviewed target")
		}
		p.AppID = app.ID
		health, err := m.scopedHealth(ctx, appArgs{App: a.App})
		if err != nil {
			return nil, err
		}
		if health.AppID != app.ID || len(health.ServingDeploymentIDs) > 1 {
			return nil, invalid("unsupported_release", "publication requires the same app and at most one serving release")
		}
		if len(health.ServingDeploymentIDs) == 1 {
			p.Serving = health.ServingDeploymentIDs[0]
		}
	}
	id := make([]byte, 32)
	if _, err := rand.Read(id); err != nil {
		return nil, err
	}
	p.ID = hex.EncodeToString(id)
	p.Intent = api.ToolPublicationIntent{App: p.App, AppID: p.AppID, ExpectedServingDeploymentID: p.Serving, SourceSHA256: p.Digest,
		FixturesSHA256: project.fixturesDigest, AccessMode: p.AccessMode, ToolScopes: project.config.Auth.ToolScopes, TestCases: project.caseNames, EndpointPath: project.config.Endpoint}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.toolPublications == nil {
		m.toolPublications = map[string]*toolPublication{}
	}
	for id, old := range m.toolPublications {
		if time.Now().After(old.ExpiresAt) && old.Status != "running" {
			if old.Uploaded {
				m.opts.uploads.removeDir(old.Path)
			}
			delete(m.toolPublications, id)
		}
	}
	if len(m.toolPublications) >= maxReleasePlans {
		return nil, invalid("plan_capacity", "too many retained publication plans")
	}
	m.toolPublications[p.ID] = p
	out := publicationView(p)
	tools := make([]string, 0, len(project.config.Auth.ToolScopes))
	for name := range project.config.Auth.ToolScopes {
		tools = append(tools, name)
	}
	sort.Strings(tools)
	out["source_sha256"], out["access_mode"], out["tools"] = p.Digest, p.AccessMode, tools
	out["fixtures_sha256"], out["test_cases"] = project.fixturesDigest, project.caseNames
	out["traffic_effect"] = "Stage source and a catalog-pinned revision at zero traffic. Promote only after protocol and behavioral checks, with the reviewed app/serving identities guarded. Fixtures may perform their declared external side effects."
	return out, nil
}

func publicationView(p *toolPublication) map[string]any {
	recovery := Recovery{Action: "inspect", Tool: "gregale_get_tool_deploy", Arguments: map[string]any{"plan_id": p.ID}}
	if p.Status == "running" {
		delay := int64(2)
		recovery.AfterSeconds = &delay
	}
	if p.Status == "completed" {
		recovery = Recovery{Action: "none"}
	}
	if p.Status == "failed" || p.Status == "outcome_unknown" {
		recovery = Recovery{Action: "inspect", Toolsets: []string{ToolsetCore}}
	}
	return map[string]any{"schema_version": 1, "plan_id": p.ID, "action": "tool_deploy", "account_id": p.AccountID, "app": p.App, "source_sha256": p.Digest,
		"app_id": p.AppID, "expected_serving_deployment_id": p.Serving, "status": p.Status, "expires_at": p.ExpiresAt,
		"result": p.Result, "problem": p.Problem, "recovery": recovery}
}

func (m *management) checkedPublication(ctx context.Context, id string) (*toolPublication, error) {
	if err := m.toolBuildingAccess(); err != nil {
		return nil, err
	}
	p := m.toolPublications[id]
	if p == nil || time.Now().After(p.ExpiresAt) {
		return nil, invalid("invalid_plan", "publication plan is unknown or expired; inspect native deployment history before creating work")
	}
	account, err := m.client.Whoami(ctx)
	if err != nil {
		return nil, err
	}
	if account.ID != p.AccountID {
		return nil, invalid("account_changed", "publication belongs to a different account")
	}
	return p, nil
}

func (m *management) getToolDeploy(ctx context.Context, a planLookupArgs) (any, error) {
	if err := m.toolBuildingAccess(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	if p := m.toolPublications[a.PlanID]; m.remote() && p != nil && p.Status != "planned" {
		m.mu.Unlock()
		// Hosted receipt reads always recheck native publishing authority and
		// recover the same journal evidence across protocol sessions.
		return m.getDurableToolDeploy(ctx, a.PlanID)
	}
	if p := m.toolPublications[a.PlanID]; p == nil || time.Now().After(p.ExpiresAt) && p.Status != "planned" {
		m.mu.Unlock()
		return m.getDurableToolDeploy(ctx, a.PlanID)
	}
	p, err := m.checkedPublication(ctx, a.PlanID)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	out := publicationView(p)
	app, appID, depID := p.App, p.AppID, ""
	if p.Result != nil {
		depID = p.Result.DeploymentID
	}
	m.mu.Unlock()
	if depID != "" {
		native, err := m.client.GetApp(ctx, app)
		if err != nil {
			return nil, err
		}
		dep, err := m.client.GetDeployment(ctx, depID)
		if err != nil {
			return nil, err
		}
		if native.ID != appID || dep.AppID != appID || normalizedScope(dep.Scope) != api.DefaultEnvScope {
			return nil, invalid("invalid_response", "publication target identity changed")
		}
		out["deployment"] = projectDeployment(dep)
		out["observed_at"] = time.Now().UTC()
	}
	return out, nil
}

func tokenEnvAllowed(mode, name string) bool {
	if mode == "open" {
		return name == ""
	}
	return mode == "external-oauth" && strings.HasPrefix(name, "MCP_") && envKey.MatchString(name)
}

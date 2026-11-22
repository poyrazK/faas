package mcpmanagement

import (
	"context"
	"encoding/json"
	"os"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
	"github.com/onebox-faas/faas/pkg/tasktoolrelease"
	"github.com/onebox-faas/faas/pkg/tasktoolsource"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

type uploadedTaskSource struct {
	App            string       `json:"app" jsonschema:"Existing personal-account app slug for this stack role"`
	UploadID       string       `json:"upload_id,omitempty" jsonschema:"One-time source upload bound to this connection and app; exclusive with files"`
	Files          []inlineFile `json:"files,omitempty" jsonschema:"Or bounded inline Node source files; no local paths"`
	ExpectedSHA256 string       `json:"expected_source_sha256,omitempty" jsonschema:"Optional source digest from an earlier hosted review; changed content or modes are refused"`
}

type planUploadedTaskArgs struct {
	Web                uploadedTaskSource `json:"web"`
	Worker             uploadedTaskSource `json:"worker"`
	ObserverApp        string             `json:"observer_app"`
	ObserverMetricApp  string             `json:"observer_metric_app"`
	PreviousWorkerApps []string           `json:"previous_worker_apps,omitempty"`
	TimeoutSeconds     int                `json:"release_timeout_seconds,omitempty" jsonschema:"Native release deadline, default 300; review executes no release"`
}

type uploadedTaskReview = tasktoolsource.Review

func (m *management) planUploadedTaskToolDeploy(ctx context.Context, a planUploadedTaskArgs) (any, error) {
	return m.withUploadedTaskReview(ctx, a, func(out uploadedTaskReview, _, _ string) (any, error) { return out, nil })
}

func (m *management) withUploadedTaskReview(ctx context.Context, a planUploadedTaskArgs, done func(uploadedTaskReview, string, string) (any, error)) (any, error) {
	if err := m.toolBuildingAccess(); err != nil {
		return nil, err
	}
	if !m.remote() {
		return nil, invalid("tool_build_access_denied", "uploaded Task review requires an unscoped hosted publishing connection")
	}
	if err := validateUploadedTaskArgs(&a); err != nil {
		return nil, err
	}
	accountID, err := m.uploadedTaskAccount(ctx)
	if err != nil {
		return nil, err
	}
	webDir, err := m.stageUploadedTaskSource(a.Web)
	if err != nil {
		return nil, err
	}
	defer m.opts.uploads.removeDir(webDir)
	workerDir, err := m.stageUploadedTaskSource(a.Worker)
	if err != nil {
		return nil, err
	}
	defer m.opts.uploads.removeDir(workerDir)
	out, err := reviewUploadedTaskSources(a, webDir, workerDir)
	if err != nil {
		return nil, err
	}
	value, err := m.finishUploadedTaskReview(ctx, a, out, accountID, webDir, workerDir)
	if err != nil {
		return nil, err
	}
	return done(value.(uploadedTaskReview), webDir, workerDir)
}

func (m *management) uploadedTaskAccount(ctx context.Context) (string, error) {
	account, err := m.client.Whoami(ctx)
	if err != nil {
		return "", err
	}
	if uuid.Validate(account.ID) != nil || m.opts.accountID != account.ID || account.KeyApp != nil || account.KeyOrg != nil || len(account.KeyProjects) != 0 {
		return "", invalid("tool_build_access_denied", "Task review requires the authenticated unscoped personal account")
	}
	return account.ID, nil
}

func (m *management) finishUploadedTaskReview(ctx context.Context, a planUploadedTaskArgs, out uploadedTaskReview, accountID, webDir, workerDir string) (any, error) {
	var err error
	out.AccountID = accountID
	out.Kind = "hosted_task_source_review"
	out.Apps, err = m.uploadedTaskAppReviews(ctx, a)
	if err != nil {
		return nil, err
	}
	// Account/app/source/policy identity is independent of temporary upload paths,
	// archive identities, ambient API/namespace variables and observation time.
	body, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	out.Fingerprint = toolpublication.Digest(append([]byte("hosted-task-source-review:v1:"), body...))
	intent := api.TaskToolReleaseIntent{ReleaseFingerprint: out.Fingerprint, WorkerRuntime: out.WorkerRuntime, Protocol: out.Protocol, ObserverMetricApp: out.ObserverMetricApp, Endpoint: out.Endpoint, AccessMode: out.AccessMode, ToolScopes: out.ToolScopes, Apps: out.Apps, RequiredSetup: out.RequiredSetup, Reason: "Review uploaded Task release"}
	if tasktoolrelease.Validate(intent) != nil {
		return nil, invalid("unsupported_task_review", "Task review requires distinct existing default-environment app identities and compatible native release reviews")
	}
	if err := verifyUploadedTaskSources(out.Sources, webDir, workerDir); err != nil {
		return nil, err
	}
	return out, nil
}

func validateUploadedTaskArgs(a *planUploadedTaskArgs) error {
	if a.TimeoutSeconds == 0 {
		a.TimeoutSeconds = 300
	}
	// Match the existing native release plan's 1–600s deadline and role bound.
	if a.TimeoutSeconds < 1 || a.TimeoutSeconds > 600 || len(a.PreviousWorkerApps) > api.TaskToolReleaseAppsMax-3 {
		return invalid("invalid_argument", "Task review exceeds native release deadline or role bounds")
	}
	a.PreviousWorkerApps = append([]string{}, a.PreviousWorkerApps...)
	seen := map[string]bool{}
	slugs := append([]string{a.Web.App, a.Worker.App, a.ObserverApp}, a.PreviousWorkerApps...)
	for _, slug := range slugs {
		if !api.ValidAppSlug(slug) || seen[slug] {
			return invalid("invalid_argument", "Task stack roles must name distinct valid existing apps")
		}
		seen[slug] = true
	}
	if !seen[a.ObserverMetricApp] {
		return invalid("invalid_argument", "observer_metric_app must name a reviewed stack app")
	}
	for _, source := range []uploadedTaskSource{a.Web, a.Worker} {
		if (source.UploadID == "") == (len(source.Files) == 0) || source.ExpectedSHA256 != "" && !toolpublication.ValidID(source.ExpectedSHA256) {
			return invalid("invalid_argument", "each source needs either upload_id or files and an optional lowercase SHA256")
		}
	}
	return nil
}

func (m *management) stageUploadedTaskSource(a uploadedTaskSource) (string, error) {
	dir, err := m.uploadedProject(a.App, a.UploadID, a.Files)
	if err != nil {
		return "", invalid("invalid_upload", "Task source is invalid, unavailable, expired, consumed or belongs to another app/connection; provide fresh matching source")
	}
	return dir, nil
}

func reviewUploadedTaskSources(a planUploadedTaskArgs, webDir, workerDir string) (uploadedTaskReview, error) {
	var out uploadedTaskReview
	web, webHash, err := reviewUploadedTaskSource(webDir, a.Web.ExpectedSHA256, false)
	if err != nil {
		return out, err
	}
	worker, workerHash, err := reviewUploadedTaskSource(workerDir, a.Worker.ExpectedSHA256, true)
	if err != nil {
		return out, err
	}
	if len(web.Auth.ToolScopes) == 0 || len(web.Auth.ToolScopes) > api.ToolPublicationMaxTools {
		return out, invalid("unsupported_task_source", "Task web source needs an explicit bounded tool permission policy")
	}
	out.TaskReleaseReview = mcphosting.TaskReleaseReview{Version: 1, Status: "review_only", Protocol: api.MCPProtocolVersion, WebApp: a.Web.App, WorkerApp: a.Worker.App, WorkerRuntime: "node", ObserverApp: a.ObserverApp, ObserverMetricApp: a.ObserverMetricApp, PreviousWorkerApps: a.PreviousWorkerApps, Endpoint: web.Endpoint, AccessMode: web.Auth.Mode, TimeoutSeconds: a.TimeoutSeconds, WebLimits: mcphosting.ReviewTaskLimits(*web.Tasks), WorkerLimits: mcphosting.ReviewTaskLimits(*worker.Tasks), ToolScopes: web.Auth.ToolScopes, ReadinessRoles: []string{}, RequiredSetup: tasktoolrelease.RequiredSetup(), MigrationsRequired: true}
	out.Sources = map[string]string{"web": webHash, "worker": workerHash}
	return out, nil
}

func reviewUploadedTaskSource(dir, expected string, worker bool) (mcphosting.Config, string, error) {
	var cfg mcphosting.Config
	digest, err := toolSourceDigest(dir)
	if err != nil {
		return cfg, "", invalid("unsupported_task_source", "Task source requires bounded regular files")
	}
	if expected != "" && expected != digest {
		return cfg, "", invalid("source_changed", "Task source bytes or modes differ from the earlier hosted review")
	}
	if validateHostedToolProject(dir, "node") != nil {
		return cfg, "", invalid("unsupported_task_source", "Task review requires Node web and worker projects with native build markers and start scripts")
	}
	cfg, err = mcphosting.Load(dir)
	if err != nil || cfg.Tasks == nil || cfg.Tasks.Enabled == nil || !*cfg.Tasks.Enabled {
		return cfg, "", invalid("unsupported_task_source", "both Task sources need valid hosting contracts with Tasks enabled")
	}
	if worker {
		root, err := os.OpenRoot(dir)
		if err != nil {
			return cfg, "", invalid("unsupported_task_source", "Task worker source is unavailable")
		}
		defer func() { _ = root.Close() }()
		file, err := root.Lstat("tasks-release.js")
		if err != nil || !file.Mode().IsRegular() {
			return cfg, "", invalid("unsupported_task_source", "Node Task worker must include its regular tasks-release.js adapter; start from the mcp-node-tasks starter")
		}
	}
	return cfg, digest, nil
}

func (m *management) uploadedTaskAppReviews(ctx context.Context, a planUploadedTaskArgs) ([]api.TaskToolReleaseApp, error) {
	apps := []api.TaskToolReleaseApp{{Role: "web", App: a.Web.App}, {Role: "worker", App: a.Worker.App}, {Role: "observer", App: a.ObserverApp}}
	for _, slug := range a.PreviousWorkerApps {
		apps = append(apps, api.TaskToolReleaseApp{Role: "previous_worker", App: slug})
	}
	for i := range apps {
		pin, err := m.client.GetAppReleaseReview(ctx, apps[i].App)
		if err != nil {
			return nil, err
		}
		apps[i].Review = pin
	}
	return apps, nil
}

func verifyUploadedTaskSources(expected map[string]string, webDir, workerDir string) error {
	for role, dir := range map[string]string{"web": webDir, "worker": workerDir} {
		actual, err := toolSourceDigest(dir)
		if err != nil || actual != expected[role] {
			return invalid("source_changed", "Task source changed while its review was being prepared")
		}
	}
	return nil
}

func uploadedTaskReviewOutputSchema() map[string]any {
	schema := taskToolReviewOutputSchema()
	props := schema["properties"].(map[string]any)
	props["kind"] = map[string]any{"type": "string", "const": "hosted_task_source_review"}
	props["account_id"] = map[string]any{"type": "string", "format": "uuid"}
	props["source_sha256"] = map[string]any{"type": "object", "additionalProperties": false, "required": []string{"web", "worker"}, "properties": map[string]any{"web": map[string]any{"type": "string", "pattern": "^[0-9a-f]{64}$"}, "worker": map[string]any{"type": "string", "pattern": "^[0-9a-f]{64}$"}}}
	props["apps"] = map[string]any{"type": "array", "minItems": 3, "maxItems": api.TaskToolReleaseAppsMax, "items": map[string]any{"type": "object", "required": []string{"role", "app", "review"}}}
	schema["required"] = append(schema["required"].([]string), "kind", "account_id", "source_sha256", "apps")
	return schema
}

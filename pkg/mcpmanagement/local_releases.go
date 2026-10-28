package mcpmanagement

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

// LocalCLI runs the serving gregale binary itself with --non-interactive
// --json, this connection's credential and API base, and args
// (ADR-20261026141841). It returns the finished command; err reports only a
// command that could not run or was cancelled.
type LocalCLI func(ctx context.Context, args []string, stdin []byte) (LocalCLIResult, error)

// LocalCLIResult is one finished command. Runners bound both streams.
type LocalCLIResult struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// Local tool bounds, not platform quotas (those live in pkg/api/limits.go).
const (
	localDeployTimeout  = 10 * time.Minute
	maxLocalSourceFiles = 200_000
	maxLocalShapeBytes  = 32 << 10
	maxEnvChangeKeys    = 50
)

var envKey = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

type localSourcePlan struct {
	Path        string          `json:"path"`
	Fingerprint string          `json:"fingerprint"`
	Files       int             `json:"files"`
	Bytes       int64           `json:"bytes"`
	NewApp      bool            `json:"new_app"`
	CreateOnly  bool            `json:"create_only,omitempty"`
	Shape       json.RawMessage `json:"shape,omitempty"`
}

type envChangePlan struct {
	Keys             []string        `json:"keys"`
	Changes          json.RawMessage `json:"changes"`
	ApplyMode        string          `json:"apply_mode"`
	Restart          bool            `json:"restart,omitempty"`
	ExpectedRevision int64           `json:"expected_revision"`
	values           map[string]string
}

type planLocalDeployArgs struct {
	Path       string `json:"path" jsonschema:"Absolute project directory"`
	App        string `json:"app" jsonschema:"App slug; created on apply if missing"`
	Reason     string `json:"reason" jsonschema:"Reviewable release reason; at most 280 characters"`
	CreateOnly bool   `json:"create_only,omitempty" jsonschema:"Create the app without deploying"`
}

type planEnvChangeArgs struct {
	App     string            `json:"app" jsonschema:"Existing Gregale app slug"`
	Values  map[string]string `json:"values" jsonschema:"1 to 50 KEY: value pairs; keys match ^[A-Z][A-Z0-9_]*$, values hold no newlines"`
	Reason  string            `json:"reason" jsonschema:"Reviewable change reason; at most 280 characters"`
	Restart bool              `json:"restart,omitempty" jsonschema:"Restart so values apply now; otherwise on the next cold wake"`
}

func registerLocalReleases(s *mcp.Server, m *management) {
	// Remote grants consent to existing app identities, and the bridge host's
	// files and credentials are not the caller's: local connections only.
	if m.opts.LocalCLI == nil || m.opts.AllowedApps != nil || m.opts.Environments != nil {
		return
	}
	register(s, m, "gregale_plan_local_deploy", "Review deploying a local directory as one app through this machine's gregale CLI, without uploading; pins the files and the app's release state for 15 minutes. Apply makes a new or non-serving app live; a serving app is staged at 0% for gregale_promote_deployment. Functions, multi-service projects and linked project directories stay in the CLI.", m.planLocalDeploy)
	register(s, m, "gregale_plan_env_change", "Review setting non-secret environment variables on an app's default scope through the gregale CLI; pins the config revision for 15 minutes. Credential-like values are refused: ask the user to run `gregale secrets set`, never for the value.", m.planEnvChange)
	if !m.opts.AllowWrites {
		return
	}
	registerTool(s, m, "gregale_apply_local_deploy", "Apply a reviewed local deploy plan: upload the unchanged directory with a stable idempotency key, creating the app if planned. This can incur build/runtime costs. Retry with the same plan ID after ambiguous errors; follow with gregale_wait_deployment.", false, m.applyLocalDeploy)
	registerTool(s, m, "gregale_apply_env_change", "Apply a reviewed environment change if no other config change landed since planning. The config guard judges the change and can restore the previous values.", false, m.applyEnvChange)
}

func (m *management) planLocalDeploy(ctx context.Context, args planLocalDeployArgs) (any, error) {
	if err := validateReason(args.App, args.Reason); err != nil {
		return nil, err
	}
	if !api.ValidAppSlug(args.App) {
		return nil, invalid("invalid_argument", "app must be a valid Gregale app slug")
	}
	dir, err := localProjectDir(args.Path)
	if err != nil {
		return nil, err
	}
	return m.planDirDeploy(ctx, dir, args)
}

// planDirDeploy plans deploying dir as one app: a local project, or the
// hosted bridge's sanitized copy of an upload (ADR-20261026195952).
func (m *management) planDirDeploy(ctx context.Context, dir string, args planLocalDeployArgs) (any, error) {
	fingerprint, files, size, err := fingerprintTree(dir)
	if err != nil {
		return nil, err
	}
	shape, err := m.runCLI(ctx, []string{"deploy", "--path", dir, "--name", args.App, "--app", "--plan"}, nil)
	if err != nil {
		return nil, err
	}
	if len(shape) > maxLocalShapeBytes || !json.Valid(shape) || !bytes.HasPrefix(bytes.TrimSpace(shape), []byte("{")) {
		return nil, invalid("invalid_response", "the CLI returned no app plan; run `gregale deploy --plan` in the directory")
	}
	source := &localSourcePlan{Path: dir, Fingerprint: fingerprint, Files: files, Bytes: size, CreateOnly: args.CreateOnly, Shape: json.RawMessage(bytes.TrimSpace(shape))}
	account, err := m.client.Whoami(ctx)
	if err != nil {
		return nil, err
	}
	app, err := m.client.GetApp(ctx, args.App)
	switch {
	case notFound(err):
		source.NewApp = true
		effect := "Create app " + args.App + " and serve its first release."
		if args.CreateOnly {
			effect = "Create app " + args.App + " without deploying."
		}
		return m.savePlan(&releasePlan{Action: "local_deploy", AccountID: account.ID, APIBase: m.opts.APIBase, Profile: m.opts.Profile, App: args.App, Scope: api.DefaultEnvScope, Reason: args.Reason, TrafficEffect: effect, ExpiresAt: time.Now().Add(releasePlanTTL), LocalSource: source})
	case err != nil:
		return nil, err
	case args.CreateOnly:
		return nil, invalid("app_exists", "app already exists; plan a deploy without create_only")
	}
	health, err := m.scopedHealth(ctx, appArgs{App: args.App})
	if err != nil {
		return nil, err
	}
	if health.AppID != app.ID {
		return nil, invalid("invalid_response", "API returned inconsistent app or health identity")
	}
	if len(health.ServingDeploymentIDs) == 0 {
		return m.savePlan(&releasePlan{Action: "local_deploy", AccountID: account.ID, APIBase: m.opts.APIBase, Profile: m.opts.Profile, AppID: app.ID, App: args.App, Scope: api.DefaultEnvScope, Reason: args.Reason, TrafficEffect: "Deploy the first serving release of " + args.App + ".", ExpiresAt: time.Now().Add(releasePlanTTL), LocalSource: source, latestID: health.LatestDeploymentID})
	}
	// A serving app gets the image releases' plan, so wait and atomic
	// promotion work unchanged; staging never moves traffic.
	p, err := m.capturePlan(ctx, args.App, args.Reason, "deploy")
	if err != nil {
		return nil, err
	}
	p.LocalSource, p.TrafficEffect = source, "Upload the directory and stage a new revision at 0% traffic. Explicit promotion sends 100% to it only if the original serving release is unchanged."
	return m.savePlan(p)
}

func (m *management) applyLocalDeploy(ctx context.Context, args applyArgs) (any, error) {
	m.mu.Lock()
	p, err := m.checkedLocalDeploy(ctx, args.PlanID)
	if err != nil || p.localReceipt != nil {
		m.mu.Unlock()
		if err != nil {
			return nil, err
		}
		return p.localReceipt, nil
	}
	if p.localInFlight {
		m.mu.Unlock()
		return nil, invalid("in_progress", "this plan is already being applied; retry the same plan ID when that call returns")
	}
	fingerprint, _, _, err := fingerprintTree(p.LocalSource.Path)
	if err == nil && fingerprint != p.LocalSource.Fingerprint {
		err = invalid("source_changed", "the directory changed since planning; plan again")
		if p.attempted {
			err = invalid("source_changed", "the directory changed after an attempt whose outcome is unknown; inspect the app's deployments before planning again")
		}
	}
	if err == nil && !p.attempted && p.Action == "deploy" {
		err = m.verifyBaseline(ctx, p)
	}
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	// Ambiguous retries must reuse the same key and the same files.
	p.attempted, p.localInFlight = true, true
	m.mu.Unlock()

	receipt, err := m.runLocalDeploy(ctx, p)

	m.mu.Lock()
	defer m.mu.Unlock()
	p.localInFlight = false
	if err != nil {
		return nil, err
	}
	p.localReceipt = receipt
	return receipt, nil
}

// checkedLocalDeploy is checkedPlan for local deploy plans. Plans for an app
// that does not serve yet cannot use checkedPlan's existing-app capture.
func (m *management) checkedLocalDeploy(ctx context.Context, id string) (*releasePlan, error) {
	p := m.plans[id]
	if p != nil && p.Action == "deploy" && p.LocalSource != nil {
		return m.checkedPlan(ctx, id, "deploy")
	}
	if !m.opts.AllowWrites {
		return nil, invalid("writes_disabled", "restart the connection with --allow-writes to enable reviewed release actions")
	}
	if p == nil || p.Action != "local_deploy" || p.LocalSource == nil {
		return nil, invalid("invalid_plan", "plan ID is unknown or belongs to a different action or connection")
	}
	if time.Now().After(p.ExpiresAt) {
		delete(m.plans, id)
		return nil, invalid("plan_expired", "plan expired; inspect the app's deployments before creating a new plan")
	}
	account, err := m.client.Whoami(ctx)
	if err != nil {
		return nil, err
	}
	if account.ID != p.AccountID {
		return nil, invalid("account_changed", "account identity changed; create a new connection and plan")
	}
	if p.localReceipt != nil || p.attempted {
		return p, nil
	}
	app, err := m.client.GetApp(ctx, p.App)
	if p.LocalSource.NewApp {
		if err == nil {
			return nil, invalid("plan_stale", "the app was created since planning; plan again")
		}
		if !notFound(err) {
			return nil, err
		}
		return p, nil
	}
	if err != nil {
		return nil, err
	}
	health, err := m.scopedHealth(ctx, appArgs{App: p.App})
	if err != nil {
		return nil, err
	}
	if app.ID != p.AppID || health.AppID != app.ID || len(health.ServingDeploymentIDs) != 0 || health.LatestDeploymentID != p.latestID {
		return nil, invalid("plan_stale", "the app changed or started serving since planning; plan again to stage at zero traffic")
	}
	return p, nil
}

func (m *management) runLocalDeploy(ctx context.Context, p *releasePlan) (any, error) {
	source := p.LocalSource
	args := []string{"deploy", "--path", source.Path, "--name", p.App, "--app"}
	if source.CreateOnly {
		// An ambiguous first attempt may already have created the app.
		if app, err := m.client.GetApp(ctx, p.App); err == nil {
			return m.createdApp(p, app)
		} else if !notFound(err) {
			return nil, err
		}
		if _, err := m.runCLI(ctx, append(args, "--create-only"), nil); err != nil {
			return nil, err
		}
		app, err := m.client.GetApp(ctx, p.App)
		if err != nil {
			return nil, err
		}
		return m.createdApp(p, app)
	}
	// The reviewed plan is the confirmation; --create-only refuses --yes.
	args = append(args, "--yes", "--no-wait", "--idempotency-key", "mcp-local-"+p.ID)
	if p.Action == "deploy" {
		args = append(args, "--no-traffic")
	}
	out, err := m.runCLI(ctx, args, nil)
	if err != nil {
		return nil, err
	}
	var receipt struct {
		ID            string `json:"id"`
		AppURL        string `json:"app_url"`
		TimedOut      bool   `json:"timed_out"`
		ResumeCommand string `json:"resume_command"`
	}
	if err := json.Unmarshal(out, &receipt); err != nil || validateSegment(receipt.ID, "deployment_id") != nil {
		return nil, invalid("invalid_response", "the CLI returned no deployment receipt; inspect the app's deployments before retrying this plan")
	}
	app, err := m.client.GetApp(ctx, p.App)
	if err != nil {
		return nil, err
	}
	if p.AppID == "" {
		p.AppID = app.ID
	}
	d, err := m.client.GetDeployment(ctx, receipt.ID)
	if err != nil {
		return nil, err
	}
	if app.ID != p.AppID || d.AppID != p.AppID || normalizedScope(d.Scope) != api.DefaultEnvScope || (p.Action == "deploy" && d.TrafficPercent != 0) {
		return nil, invalid("invalid_response", "the deployment belongs to another app or scope, or carries traffic; inspect deployments before retrying")
	}
	result := map[string]any{"plan_id": p.ID, "account_id": p.AccountID, "app": p.App, "deployment": projectDeployment(d)}
	if receipt.AppURL != "" {
		result["app_url"] = receipt.AppURL
	}
	if receipt.TimedOut {
		result["resume_command"] = receipt.ResumeCommand
	}
	if p.Action == "deploy" {
		// The CLI may reconcile gregale.yaml settings while staging. Promotion
		// guards against changes after this point; its traffic switch stays
		// atomic on the planned serving release.
		hash, err := appConfigurationHash(app)
		if err != nil {
			return nil, err
		}
		p.appHash, p.result = hash, &d
		result["next"] = "Wait with gregale_wait_deployment, check the staged revision, then promote with gregale_promote_deployment and this plan ID."
	} else {
		result["next"] = "Wait with gregale_wait_deployment until the deployment is live, then request app_url."
	}
	return result, nil
}

func (m *management) createdApp(p *releasePlan, app api.AppResponse) (any, error) {
	if app.ID == "" || app.Slug != p.App {
		return nil, invalid("invalid_response", "API returned an inconsistent app identity")
	}
	p.AppID = app.ID
	return map[string]any{"plan_id": p.ID, "account_id": p.AccountID, "app": projectApp(app), "next": "Set variables with gregale_plan_env_change, then plan a local deploy without create_only."}, nil
}

func (m *management) planEnvChange(ctx context.Context, args planEnvChangeArgs) (any, error) {
	if err := validateReason(args.App, args.Reason); err != nil {
		return nil, err
	}
	keys, stdin, err := envChangeInput(args.Values)
	if err != nil {
		return nil, err
	}
	account, err := m.client.Whoami(ctx)
	if err != nil {
		return nil, err
	}
	app, err := m.client.GetApp(ctx, args.App)
	if err != nil {
		return nil, err
	}
	revision, err := m.latestConfigRevision(ctx, args.App)
	if err != nil {
		return nil, err
	}
	out, err := m.runCLI(ctx, envPushArgs(args.App, args.Restart, true), stdin)
	if err != nil {
		return nil, secretRefusal(err, args.App)
	}
	var preview struct {
		App       string            `json:"app_slug"`
		Scope     string            `json:"scope"`
		CanPush   bool              `json:"can_push"`
		Changes   json.RawMessage   `json:"changes"`
		Findings  []json.RawMessage `json:"scan_findings"`
		Blockers  []string          `json:"blockers"`
		ApplyMode string            `json:"apply_mode"`
	}
	if err := json.Unmarshal(out, &preview); err != nil || preview.App != args.App || normalizedScope(preview.Scope) != api.DefaultEnvScope {
		return nil, invalid("invalid_response", "the CLI returned an inconsistent environment preview")
	}
	if len(preview.Findings) > 0 {
		return nil, secretRefusal(nil, args.App)
	}
	if !preview.CanPush {
		return nil, invalid("env_change_blocked", "the change cannot be applied: "+strings.Join(preview.Blockers, "; "))
	}
	values := make(map[string]string, len(args.Values))
	for key, value := range args.Values {
		values[key] = value
	}
	effect := fmt.Sprintf("Set %d variable(s) on the default scope; running instances keep their values until the next cold wake. The config guard can restore the previous values.", len(keys))
	if args.Restart {
		effect = fmt.Sprintf("Set %d variable(s) on the default scope and restart the app. The config guard can restore the previous values.", len(keys))
	}
	return m.savePlan(&releasePlan{Action: "env_change", AccountID: account.ID, APIBase: m.opts.APIBase, Profile: m.opts.Profile, AppID: app.ID, App: args.App, Scope: api.DefaultEnvScope, Reason: args.Reason, TrafficEffect: effect, ExpiresAt: time.Now().Add(releasePlanTTL),
		EnvChange: &envChangePlan{Keys: keys, Changes: preview.Changes, ApplyMode: preview.ApplyMode, Restart: args.Restart, ExpectedRevision: revision, values: values}})
}

func (m *management) applyEnvChange(ctx context.Context, args applyArgs) (any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.opts.AllowWrites {
		return nil, invalid("writes_disabled", "restart the connection with --allow-writes to enable reviewed release actions")
	}
	p := m.plans[args.PlanID]
	if p == nil || p.Action != "env_change" || p.EnvChange == nil {
		return nil, invalid("invalid_plan", "plan ID is unknown or belongs to a different action or connection")
	}
	if p.localReceipt != nil {
		return p.localReceipt, nil
	}
	if time.Now().After(p.ExpiresAt) {
		delete(m.plans, args.PlanID)
		return nil, invalid("plan_expired", "plan expired; inspect the app's config history before creating a new plan")
	}
	account, err := m.client.Whoami(ctx)
	if err != nil {
		return nil, err
	}
	app, err := m.client.GetApp(ctx, p.App)
	if err != nil {
		return nil, err
	}
	if account.ID != p.AccountID || app.ID != p.AppID {
		return nil, invalid("plan_stale", "account or app identity changed; create a new plan")
	}
	if !p.attempted {
		revision, err := m.latestConfigRevision(ctx, p.App)
		if err != nil {
			return nil, err
		}
		if revision != p.EnvChange.ExpectedRevision {
			return nil, invalid("plan_stale", "another config change landed since planning; review the config history and plan again")
		}
	}
	// Setting the same values again is safe, so an ambiguous attempt retries.
	p.attempted = true
	_, stdin, err := envChangeInput(p.EnvChange.values)
	if err != nil {
		return nil, err
	}
	out, err := m.runCLI(ctx, envPushArgs(p.App, p.EnvChange.Restart, false), stdin)
	if err != nil {
		return nil, secretRefusal(err, p.App)
	}
	var receipt struct {
		App         string   `json:"app_slug"`
		Scope       string   `json:"scope"`
		UpdatedKeys []string `json:"updated_keys"`
		ApplyMode   string   `json:"apply_mode"`
		WakeID      string   `json:"wake_id"`
		Warnings    []string `json:"warnings"`
	}
	if err := json.Unmarshal(out, &receipt); err != nil || receipt.App != p.App || normalizedScope(receipt.Scope) != api.DefaultEnvScope {
		return nil, invalid("invalid_response", "the CLI returned an inconsistent environment receipt; inspect the config history before retrying")
	}
	result := map[string]any{"plan_id": p.ID, "account_id": p.AccountID, "app": p.App, "scope": api.DefaultEnvScope, "updated_keys": receipt.UpdatedKeys, "apply_mode": receipt.ApplyMode}
	if receipt.WakeID != "" {
		result["wake_id"] = receipt.WakeID
	}
	if len(receipt.Warnings) > 0 {
		result["warnings"] = receipt.Warnings
	}
	p.localReceipt, p.EnvChange.values = result, nil
	return result, nil
}

func envPushArgs(app string, restart, dryRun bool) []string {
	// An explicit app and scope keep a linked project context from choosing
	// another target; strict scanning refuses credential-shaped values.
	args := []string{"env", "push", "--app", app, "--scope", api.DefaultEnvScope, "--from-stdin", "--secret-scan", "strict"}
	if restart {
		args = append(args, "--restart")
	}
	if dryRun {
		args = append(args, "--dry-run")
	}
	return args
}

func envChangeInput(values map[string]string) ([]string, []byte, error) {
	if len(values) == 0 || len(values) > maxEnvChangeKeys {
		return nil, nil, invalid("invalid_argument", fmt.Sprintf("values must hold between 1 and %d variables", maxEnvChangeKeys))
	}
	keys := make([]string, 0, len(values))
	for key, value := range values {
		if !envKey.MatchString(key) {
			return nil, nil, invalid("invalid_argument", "variable names must match ^[A-Z][A-Z0-9_]*$")
		}
		if !utf8.ValidString(value) || strings.ContainsAny(value, "\r\n\x00") {
			return nil, nil, invalid("invalid_argument", "values must be valid UTF-8 without newlines or NUL bytes")
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var stdin bytes.Buffer
	for _, key := range keys {
		stdin.WriteString(key + "=" + values[key] + "\n")
	}
	return keys, stdin.Bytes(), nil
}

// secretRefusal turns a strict-scan rejection into guidance that keeps the
// secret out of the conversation.
func secretRefusal(err error, app string) error {
	var apiErr *api.APIError
	if err == nil || errors.As(err, &apiErr) && apiErr.Problem.Code == "secret_scan_strict" {
		return invalid("secret_detected", "a value looks like a credential and was not set. Call gregale_request_secret so the user enters it on Gregale's page, or ask them to run `gregale secrets set "+app+" KEY` themselves; never ask for or pass a secret value in a tool call")
	}
	return err
}

func (m *management) latestConfigRevision(ctx context.Context, app string) (int64, error) {
	revisions, err := m.client.ListConfigRevisions(ctx, app, api.DefaultEnvScope, 1)
	if err != nil || len(revisions.Revisions) == 0 {
		return 0, err
	}
	return revisions.Revisions[0].Revision, nil
}

// runCLI returns stdout of a successful command. A failed command returns the
// Problem object the CLI wrote to stderr, so codes and hints reach the agent.
func (m *management) runCLI(ctx context.Context, args []string, stdin []byte) ([]byte, error) {
	result, err := m.opts.LocalCLI(ctx, args, stdin)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	if err != nil {
		return nil, invalid("cli_unavailable", "could not run the local gregale CLI: "+err.Error())
	}
	if result.ExitCode == 0 {
		return result.Stdout, nil
	}
	return nil, cliProblem(result)
}

// cliProblem is the error of a failed CLI command: the Problem object it wrote
// to stderr when there is one, so codes and hints reach the agent.
func cliProblem(result LocalCLIResult) error {
	var problem api.Problem
	stderr := bytes.TrimSpace(result.Stderr)
	candidates := [][]byte{stderr}
	lines := bytes.Split(stderr, []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		candidates = append(candidates, lines[i])
	}
	for _, candidate := range candidates {
		if json.Unmarshal(candidate, &problem) == nil && (problem.Code != "" || problem.Title != "") {
			return &api.APIError{Problem: problem}
		}
		problem = api.Problem{}
	}
	first, _, _ := bytes.Cut(stderr, []byte("\n"))
	return invalid("cli_failed", fmt.Sprintf("gregale exited with code %d: %s", result.ExitCode, first))
}

func notFound(err error) bool {
	var apiErr *api.APIError
	return errors.As(err, &apiErr) && apiErr.Problem.Status == 404
}

// localProjectDir accepts an existing, absolute project directory. The
// filesystem root, the home directory and its ancestors are refused, as is a
// directory under a linked project context, whose deploys target project
// environments.
func localProjectDir(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", invalid("invalid_path", "path must be an absolute directory path")
	}
	dir := filepath.Clean(path)
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() {
		return "", invalid("invalid_path", "path must be an existing directory, not a file or symbolic link")
	}
	home, _ := os.UserHomeDir()
	if dir == filepath.Dir(dir) || home != "" && (dir == home || strings.HasPrefix(home, dir+string(filepath.Separator))) {
		return "", invalid("invalid_path", "refusing to deploy the filesystem root or the home directory; choose the project directory")
	}
	for at := dir; ; at = filepath.Dir(at) {
		if _, err := os.Stat(filepath.Join(at, ".gregale", "project.json")); err == nil {
			return "", invalid("linked_project", "this directory is linked to a Gregale project; deploy it with `gregale deploy` or the project release tools")
		}
		if at == filepath.Dir(at) {
			return dir, nil
		}
	}
}

// fingerprintTree hashes the path, size, mode and modification time of every
// regular file outside .git, in lexical order. Apply refuses a changed tree.
func fingerprintTree(dir string) (string, int, int64, error) {
	h := sha256.New()
	files := 0
	var size int64
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" && path != dir {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if files++; files > maxLocalSourceFiles {
			return invalid("source_too_large", fmt.Sprintf("the directory holds more than %d files; deploy it with `gregale deploy`", maxLocalSourceFiles))
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		size += info.Size()
		_, err = fmt.Fprintf(h, "%s\x00%d\x00%o\x00%d\n", filepath.ToSlash(rel), info.Size(), info.Mode().Perm(), info.ModTime().UnixNano())
		return err
	})
	var local *inputError
	if errors.As(err, &local) {
		return "", 0, 0, err
	}
	if err != nil {
		return "", 0, 0, invalid("invalid_path", "could not read the project directory: "+err.Error())
	}
	return hex.EncodeToString(h.Sum(nil)), files, size, nil
}

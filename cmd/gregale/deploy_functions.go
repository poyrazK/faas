package main

// deploy_functions.go — `functions:` fan-out for `gregale deploy` (ADR-985).
//
// A manifest that lists several functions deploys each one as its own
// function app through the ordinary single-function deploy path. A
// function's RAM, request timeout, concurrency, env and secrets are put on
// its app before its deployment is built; its idle timeout, minimum
// instances, cron triggers, bucket events and inbound webhooks are
// reconciled after (ADR-1342). Once every function is live, the entry
// function's hosts get one kind=route edge rule per declared route.
// Functions deploy in manifest order and the fan-out stops at the first
// failure; re-running the deploy converges.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"gopkg.in/yaml.v3"
)

// functionSetClient is the API surface the post-deploy reconcile needs.
type functionSetClient interface {
	manifestCronClient
	manifestQueueBindingClient
	PostTriggersBatchCreate(ctx context.Context, req api.CreateTriggerBatchRequest) (map[string]any, error)
	GetApp(ctx context.Context, slug string) (api.AppResponse, error)
	UpdateApp(ctx context.Context, slug string, req api.UpdateAppRequest) (api.AppResponse, error)
	ListApps(ctx context.Context) ([]api.AppResponse, error)
	ListObjectBuckets(ctx context.Context, slug string) (api.ObjectBucketList, error)
	GetObjectBucketNotifications(ctx context.Context, slug, bucket string) (api.ObjectBucketNotifications, error)
	PutObjectBucketNotifications(ctx context.Context, slug, bucket string, in api.ObjectBucketNotificationsRequest) (api.ObjectBucketNotifications, error)
	ListInboundWebhookEndpoints(ctx context.Context, slug string) ([]api.InboundWebhookEndpointResponse, error)
	CreateInboundWebhookEndpoint(ctx context.Context, slug string, req api.CreateInboundWebhookEndpointRequest) (api.InboundWebhookEndpointResponse, error)
	UpdateInboundWebhookEndpoint(ctx context.Context, slug, id string, req api.UpdateInboundWebhookEndpointRequest) (api.InboundWebhookEndpointResponse, error)
	PutAppsSlugEnvKey(ctx context.Context, slug, key, value string) error
	GetAppsSlugEnv(ctx context.Context, slug string) (api.AppEnvListResponse, error)
	DeleteAppsSlugEnvKey(ctx context.Context, slug, key string) error
	ListSecrets(ctx context.Context, slug string) (api.AppSecretListResponse, error)
	SetSecret(ctx context.Context, slug, key, value string) error
	ListDomains(ctx context.Context) ([]api.CustomDomainResponse, error)
	ListEdgeRulesForApp(ctx context.Context, slug string) ([]api.EdgeRuleResponse, error)
	CreateEdgeRule(ctx context.Context, slug string, req api.CreateEdgeRuleRequest) (api.EdgeRuleResponse, error)
	DeleteEdgeRule(ctx context.Context, id string) error
}

// functionSetDeploy carries the parent deploy's resolved inputs.
type functionSetDeploy struct {
	args        []string // the parent `gregale deploy` arguments
	cwd         string
	projectDir  string
	projectSlug string
	manifest    *gregalemanifest.Manifest
	// fromCode records settings read from handler files
	// (ADR-20261107193000), for the plan and deploy output.
	fromCode   []gregalemanifest.InlineConfigSource
	planOnly   bool // --dry-run, --diff, --server-diff or --create-only
	noTriggers bool
	out        io.Writer
	// deployOne runs the single-function deploy; tests replace it.
	deployOne func(args []string) int
	getenv    func(string) string
}

type plannedFunction struct {
	spec    gregalemanifest.FunctionSpec
	slug    string
	root    string // relative to cwd, as passed to --path
	runtime string
	handler string
	// calls is what the function may call through its typed client
	// (ADR-20261111150000).
	calls gregalemanifest.FunctionCalls
}

// functionSetManifest returns the manifest's functions when this deploy is a
// plain local-source deploy of a functions manifest.
func functionSetManifest(sourceDir string) (*gregalemanifest.Manifest, bool, error) {
	m, _, ok, err := functionSetManifestFromCode(sourceDir)
	return m, ok, err
}

// functionSetManifestFromCode also applies config declared in handler files
// and discovers functions/<name>/ directories that declare one
// (ADR-20261107193000); gregale.yaml wins over code.
func functionSetManifestFromCode(sourceDir string) (*gregalemanifest.Manifest, []gregalemanifest.InlineConfigSource, bool, error) {
	if sourceDir == "" {
		return nil, nil, false, nil
	}
	m, found, err := gregalemanifest.Load(sourceDir)
	if err != nil {
		return nil, nil, false, err
	}
	if !found {
		m = nil
	}
	m, sources, err := gregalemanifest.ApplyInlineConfig(m, sourceDir)
	if err != nil {
		return nil, nil, false, err
	}
	if m == nil || len(m.Functions) == 0 {
		return nil, nil, false, nil
	}
	return m, sources, true, nil
}

// plan validates every function before anything is deployed. The shared
// plan (ADR-20261108160000) resolves each function; root is then made
// relative to the working directory, as --path takes it.
func (d functionSetDeploy) plan() ([]plannedFunction, error) {
	resolved, err := d.manifest.PlanFunctions(d.projectDir, d.projectSlug)
	if err != nil {
		return nil, err
	}
	specs := make([]gregalemanifest.FunctionSpec, 0, len(resolved))
	for _, p := range resolved {
		specs = append(specs, p.Spec)
	}
	graph, err := gregalemanifest.FunctionCallGraph(specs, func(name string) string {
		return gregalemanifest.FunctionSlug(d.projectSlug, name)
	})
	if err != nil {
		return nil, err
	}
	planned := make([]plannedFunction, 0, len(resolved))
	for _, p := range resolved {
		absRoot := filepath.Join(d.projectDir, filepath.FromSlash(p.Root))
		root, err := filepath.Rel(d.cwd, absRoot)
		if err != nil {
			root = absRoot
		}
		planned = append(planned, plannedFunction{spec: p.Spec, slug: p.Slug, root: root, runtime: p.Runtime, handler: p.Handler, calls: graph[p.Spec.Name]})
	}
	return planned, nil
}

// shared is the function as the shared plan describes it.
func (fn plannedFunction) shared() gregalemanifest.FunctionPlan {
	return gregalemanifest.FunctionPlan{Spec: fn.spec, Slug: fn.slug, Runtime: fn.runtime, Handler: fn.handler}
}

// functionDeployArgs rewrites the parent deploy arguments for one function:
// its root and slug replace --path and --name, and the function shape is
// explicit so the child never re-reads the project manifest as an app. The
// fan-out applies triggers itself, so the child never stages them.
func functionDeployArgs(args []string, fn plannedFunction) []string {
	out := stripDeployFlags(args, "path", "name", "runtime", "handler")
	return append(out, "--path", fn.root, "--name", fn.slug, "--function", "--runtime", fn.runtime, "--handler", fn.handler, "--no-triggers")
}

// functionCreateArgs reserves a function's app without a deployment, so its
// settings are in place before its first build.
func functionCreateArgs(fn plannedFunction) []string {
	return []string{"--create-only", "--name", fn.slug, "--function", "--runtime", fn.runtime, "--handler", fn.handler}
}

// stripDeployFlags removes value flags (both -x v and -x=v forms).
func stripDeployFlags(args []string, names ...string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		key, _, hasValue := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		if strings.HasPrefix(arg, "-") && slices.Contains(names, key) {
			if !hasValue && i+1 < len(args) {
				i++
			}
			continue
		}
		out = append(out, arg)
	}
	return out
}

// run deploys every function and returns the process exit code.
func (d functionSetDeploy) run(ctx context.Context, client functionSetClient) int {
	planned, err := d.plan()
	if err != nil {
		return printErr("Invalid functions manifest", err)
	}
	if client != nil {
		if acct, whoErr := client.Whoami(ctx); whoErr == nil {
			plan := api.Plan(acct.Plan)
			if err := d.manifest.ValidateForPlan(plan); err != nil {
				return printErr("Invalid functions manifest", err)
			}
			if l, ok := api.LimitsFor(plan); ok && len(planned) > l.DeployedApps {
				return printErr("Too many functions", fmt.Errorf("%d functions need %d apps; the %s plan allows %d deployed apps", len(planned), len(planned), plan, l.DeployedApps))
			}
		}
	}
	_, _ = fmt.Fprintf(d.out, "Deploying %d functions from %s\n", len(planned), displayPath(d.cwd, d.projectDir))
	d.printConfigFromCode()
	done := make([]string, 0, len(planned))
	for i, fn := range planned {
		_, _ = fmt.Fprintf(d.out, "\n[%d/%d] %s → %s (%s, %s)\n", i+1, len(planned), fn.spec.Name, fn.slug, fn.runtime, fn.root)
		if !d.planOnly && client != nil {
			if err := d.applySettings(ctx, client, fn); err != nil {
				_, _ = fmt.Fprintf(d.out, "function %s: its settings did not apply, so it was not deployed: %v\n", fn.spec.Name, err)
				d.summarize(done, fn, planned[i+1:])
				return 1
			}
		}
		if code := d.deployOne(functionDeployArgs(d.args, fn)); code != 0 {
			d.summarize(done, fn, planned[i+1:])
			return code
		}
		if !d.planOnly && client != nil {
			if err := d.reconcile(ctx, client, fn); err != nil {
				_, _ = fmt.Fprintf(d.out, "function %s deployed, but its configuration did not apply: %v\n", fn.spec.Name, err)
				d.summarize(done, fn, planned[i+1:])
				return 1
			}
		}
		done = append(done, fn.slug)
	}
	if !d.planOnly && client != nil {
		if err := d.reconcileRoutes(ctx, client, planned); err != nil {
			d.summarize(done, plannedFunction{}, nil)
			_, _ = fmt.Fprintf(d.out, "Every function is deployed, but the routes did not apply: %v\n", err)
			return 1
		}
	}
	d.summarize(done, plannedFunction{}, nil)
	if !d.planOnly && client != nil {
		d.reportUnlisted(ctx, client, done)
	}
	return 0
}

// functionSetPlan is `deploy --plan` for a functions manifest: what each
// function deploys as, read from local files only.
type functionSetPlan struct {
	Inference     string                `json:"inference"`
	Functions     []functionSetPlanItem `json:"functions"`
	DeployCommand string                `json:"deploy_command"`
	// ConfigFromCode lists settings read from handler files.
	ConfigFromCode []functionConfigFromCode `json:"config_from_code,omitempty"`
}

type functionConfigFromCode struct {
	Function   string   `json:"function"`
	File       string   `json:"file"`
	Keys       []string `json:"keys"`
	Discovered bool     `json:"discovered"`
}

type functionSetPlanItem struct {
	Name    string `json:"name"`
	App     string `json:"app"`
	Runtime string `json:"runtime"`
	Handler string `json:"handler"`
	Root    string `json:"root"`
	// Hooks are the snapshot hooks the handler declares (ADR-20261110190000).
	Hooks []string `json:"hooks,omitempty"`
}

// printPlan shows the function set without login, upload or remote state.
func (d functionSetDeploy) printPlan() int {
	planned, err := d.plan()
	if err != nil {
		return printErr("Invalid functions manifest", err)
	}
	plan := functionSetPlan{Inference: "local", DeployCommand: "gregale deploy", Functions: make([]functionSetPlanItem, 0, len(planned))}
	for _, fn := range planned {
		plan.Functions = append(plan.Functions, functionSetPlanItem{Name: fn.spec.Name, App: fn.slug, Runtime: fn.runtime, Handler: fn.handler, Root: fn.root, Hooks: fn.spec.Hooks})
	}
	for _, src := range d.fromCode {
		plan.ConfigFromCode = append(plan.ConfigFromCode, functionConfigFromCode{Function: src.Function, File: src.File, Keys: src.Keys, Discovered: src.Discovered})
	}
	if jsonOutput {
		return jsonOut(writeJSON(plan))
	}
	_, _ = fmt.Fprintf(d.out, "Plan (local inference; no login, upload or remote state)\n\nFunctions (%d) from %s:\n", len(plan.Functions), displayPath(d.cwd, d.projectDir))
	for _, fn := range plan.Functions {
		_, _ = fmt.Fprintf(d.out, "  %-20s %-28s %-10s %s\n", fn.Name, fn.App, fn.Runtime, fn.Root)
	}
	d.printConfigFromCode()
	printSnapshotHooks(d.out, planned)
	_, _ = fmt.Fprintf(d.out, "\nDeploy them: %s\nCompare with remote state: gregale deploy --dry-run\n", plan.DeployCommand)
	return 0
}

// printSnapshotHooks lists the snapshot hooks each function declares in code
// (ADR-20261110190000): the deploy wires beforeSnapshot and afterRestore to
// the runner, and init runs before each worker is ready.
func printSnapshotHooks(out io.Writer, planned []plannedFunction) {
	header := false
	for _, fn := range planned {
		if len(fn.spec.Hooks) == 0 {
			continue
		}
		if !header {
			_, _ = fmt.Fprintln(out, "\nSnapshot hooks in code:")
			header = true
		}
		_, _ = fmt.Fprintf(out, "  %-20s %s\n", fn.spec.Name, strings.Join(fn.spec.Hooks, ", "))
	}
}

// printConfigFromCode names each setting read from a handler file, so a
// value that differs from gregale.yaml is never a mystery.
func (d functionSetDeploy) printConfigFromCode() {
	if len(d.fromCode) == 0 {
		return
	}
	_, _ = fmt.Fprintln(d.out, "\nConfig from code (gregale.yaml wins where both set a value):")
	for _, src := range d.fromCode {
		note := ""
		if src.Discovered {
			note = " (discovered; not in gregale.yaml)"
		}
		keys := strings.Join(src.Keys, ", ")
		if keys == "" {
			keys = "no settings"
		}
		_, _ = fmt.Fprintf(d.out, "  %-20s %s: %s%s\n", src.Function, src.File, keys, note)
	}
}

func (d functionSetDeploy) summarize(done []string, failed plannedFunction, skipped []plannedFunction) {
	verb := "Deployed"
	if d.planOnly {
		verb = "Planned"
	}
	_, _ = fmt.Fprintf(d.out, "\n%s %d function(s)", verb, len(done))
	if len(done) > 0 {
		_, _ = fmt.Fprintf(d.out, ": %s", strings.Join(done, ", "))
	}
	_, _ = fmt.Fprintln(d.out)
	if failed.slug != "" {
		_, _ = fmt.Fprintf(d.out, "Failed: %s\n", failed.slug)
	}
	if len(skipped) > 0 {
		names := make([]string, len(skipped))
		for i, fn := range skipped {
			names[i] = fn.slug
		}
		_, _ = fmt.Fprintf(d.out, "Not attempted: %s\n", strings.Join(names, ", "))
	}
}

// reportUnlisted names function apps of this project that the manifest no
// longer lists. They are kept: deleting an app is always explicit.
func (d functionSetDeploy) reportUnlisted(ctx context.Context, client functionSetClient, deployed []string) {
	apps, err := client.ListApps(ctx)
	if err != nil {
		return
	}
	var unlisted []string
	for _, app := range apps {
		if app.Type == "function" && strings.HasPrefix(app.Slug, d.projectSlug+"-") && !slices.Contains(deployed, app.Slug) {
			unlisted = append(unlisted, app.Slug)
		}
	}
	if len(unlisted) == 0 {
		return
	}
	sort.Strings(unlisted)
	_, _ = fmt.Fprintf(d.out, "Not in gregale.yaml (kept): %s — delete with `gregale app delete <slug> --yes`\n", strings.Join(unlisted, ", "))
}

// reconcile applies one deployed function's manifest configuration.
func (d functionSetDeploy) reconcile(ctx context.Context, client functionSetClient, fn plannedFunction) error {
	app, err := client.GetApp(ctx, fn.slug)
	if err != nil {
		return fmt.Errorf("read app %s: %w", fn.slug, err)
	}
	var update api.UpdateAppRequest
	if fn.spec.IdleTimeoutS != 0 && app.IdleTimeoutS != fn.spec.IdleTimeoutS {
		idle := fn.spec.IdleTimeoutS
		update.IdleTimeoutS = &idle
	}
	// The floor applies once a deployment exists to keep warm.
	if fn.spec.MinInstances != nil && app.MinInstances != *fn.spec.MinInstances {
		floor := *fn.spec.MinInstances
		update.MinInstances = &floor
	}
	if update.IdleTimeoutS != nil || update.MinInstances != nil {
		if _, err := client.UpdateApp(ctx, fn.slug, update); err != nil {
			return fmt.Errorf("set idle_timeout_s / min_instances: %w", err)
		}
	}
	if d.noTriggers {
		return nil
	}
	crons := d.manifest.FunctionCronTriggers(fn.shared())
	txn, err := reconcileManifestCronsWithRollback(ctx, client, fn.slug, crons)
	if err != nil {
		return fmt.Errorf("cron triggers: %w", err)
	}
	if err := reconcileManifestRealtime(ctx, client, fn.slug, d.manifest.FunctionRealtimeTriggers(fn.shared()), txn); err != nil {
		if rollbackErr := txn.rollback(ctx); rollbackErr != nil {
			err = errors.Join(err, rollbackErr)
		}
		return fmt.Errorf("realtime triggers: %w", err)
	}
	txn.commit()
	acct, err := client.Whoami(ctx)
	if err != nil {
		return fmt.Errorf("read account: %w", err)
	}
	if err := reconcileFunctionBucketEvents(ctx, client, fn.slug, app.ID, acct.ID, fn.spec.BucketEvents); err != nil {
		return fmt.Errorf("bucket events: %w", err)
	}
	if err := reconcileFunctionWebhooks(ctx, client, d.out, fn.slug, fn.spec.Webhooks, d.lookupEnv()); err != nil {
		return fmt.Errorf("webhooks: %w", err)
	}
	if fn.spec.QueueBindings != nil {
		if err := reconcileQueueBindings(ctx, client, fn.slug, fn.spec.QueueBindings, "push", "http"); err != nil {
			return fmt.Errorf("queue bindings: %w", err)
		}
	}
	if err := applyFunctionEventTriggers(ctx, client, fn.slug, app.ID, fn.spec.EventTriggers); err != nil {
		return fmt.Errorf("event triggers: %w", err)
	}
	return nil
}

// applyFunctionEventTriggers subscribes a function to its declared events
// through the trigger batch endpoint, which applies them the way a deploy
// applies a manifest's event_triggers (ADR-1342).
func applyFunctionEventTriggers(ctx context.Context, client functionSetClient, slug, appID string, triggers []gregalemanifest.EventTrigger) error {
	if len(triggers) == 0 {
		return nil
	}
	scoped := make([]gregalemanifest.EventTrigger, len(triggers))
	for i, trigger := range triggers {
		trigger.App = slug
		scoped[i] = trigger
	}
	doc, err := yaml.Marshal(struct {
		EventTriggers []gregalemanifest.EventTrigger `yaml:"event_triggers"`
	}{scoped})
	if err != nil {
		return fmt.Errorf("encode: %w", err)
	}
	out, err := client.PostTriggersBatchCreate(ctx, api.CreateTriggerBatchRequest{AppID: appID, ManifestYAML: string(doc)})
	if err != nil {
		return err
	}
	failures, _ := out["errors"].([]any)
	if len(failures) == 0 {
		return nil
	}
	messages := make([]string, 0, len(failures))
	for _, failure := range failures {
		if row, ok := failure.(map[string]any); ok {
			messages = append(messages, fmt.Sprint(row["message"]))
		}
	}
	return errors.New(strings.Join(messages, "; "))
}

func (d functionSetDeploy) lookupEnv() func(string) string {
	if d.getenv == nil {
		return os.Getenv
	}
	return d.getenv
}

// applySettings puts a function's pre-deploy settings on its app before its
// deployment is built (ADR-1342): RAM and concurrency shape the build, and
// env and secrets reach its first wake. A new function's app is created
// first, without a deployment.
func (d functionSetDeploy) applySettings(ctx context.Context, client functionSetClient, fn plannedFunction) error {
	callable := len(fn.calls.Names) > 0
	if !fn.spec.HasPreDeploySettings() && !callable {
		return nil
	}
	app, err := client.GetApp(ctx, fn.slug)
	if isNotFound(err) {
		if code := d.deployOne(functionCreateArgs(fn)); code != 0 {
			return fmt.Errorf("create app %s: exit status %d", fn.slug, code)
		}
		app, err = client.GetApp(ctx, fn.slug)
	}
	if err != nil {
		return fmt.Errorf("read app %s: %w", fn.slug, err)
	}
	update, changed := fn.spec.AppSettingsUpdate(app)
	// Snapshot hooks declared in code point the app's lifecycle at the
	// runner (ADR-20261110190000 §5); the deployment is built with them.
	after, before, hooksChanged, err := fn.spec.HookLifecycleUpdate(app.Manifest.AfterRestore, app.Manifest.BeforeCheckpoint)
	if err != nil {
		return err
	}
	if hooksChanged {
		update.AfterRestore, update.BeforeCheckpoint, changed = after, before, true
	}
	// Private bindings to the apps the function may call, and the name map
	// its typed client reads (ADR-20261111150000 §2).
	if callable {
		if bindings, bound := fn.calls.BindingUpdate(app); bound {
			update.ServiceBindingTargets, update.ServiceBindingPolicy, changed = bindings.ServiceBindingTargets, bindings.ServiceBindingPolicy, true
		}
		if !maps.Equal(app.FunctionCallables, fn.calls.Names) {
			names := maps.Clone(fn.calls.Names)
			update.FunctionCallables, changed = &names, true
		}
	}
	if changed {
		if _, err := client.UpdateApp(ctx, fn.slug, update); err != nil {
			return fmt.Errorf("update app settings: %w", err)
		}
	}
	for _, key := range slices.Sorted(maps.Keys(fn.spec.Env)) {
		if err := client.PutAppsSlugEnvKey(ctx, fn.slug, key, fn.spec.Env[key]); err != nil {
			return fmt.Errorf("set env %s: %w", key, err)
		}
	}
	if fn.spec.PruneEnv {
		if err := pruneFunctionEnv(ctx, client, d.out, fn.slug, fn.spec.Env); err != nil {
			return err
		}
	}
	return sealFunctionSecrets(ctx, client, fn.slug, fn.spec.SecretsFromEnv, d.lookupEnv())
}

// pruneFunctionEnv removes the default-scope env keys a prune_env function
// does not declare. Secrets and scoped env are separate and never touched.
func pruneFunctionEnv(ctx context.Context, client functionSetClient, out io.Writer, slug string, keep map[string]string) error {
	current, err := client.GetAppsSlugEnv(ctx, slug)
	if err != nil {
		return fmt.Errorf("list env: %w", err)
	}
	for _, row := range current.Env {
		if _, declared := keep[row.Key]; declared {
			continue
		}
		if err := client.DeleteAppsSlugEnvKey(ctx, slug, row.Key); err != nil {
			return fmt.Errorf("remove env %s: %w", row.Key, err)
		}
		_, _ = fmt.Fprintf(out, "removed env %s from %s (prune_env)\n", row.Key, slug)
	}
	return nil
}

// sealFunctionSecrets seals each named secret from the deployer's
// environment. A name unset there keeps the value the app already has; a
// secret the app has never had fails the deploy, since the function needs it.
func sealFunctionSecrets(ctx context.Context, client functionSetClient, slug string, names []string, getenv func(string) string) error {
	var existing map[string]bool
	for _, name := range names {
		if value := getenv(name); value != "" {
			if err := client.SetSecret(ctx, slug, name, value); err != nil {
				return fmt.Errorf("seal secret %s: %w", name, err)
			}
			continue
		}
		if existing == nil {
			list, err := client.ListSecrets(ctx, slug)
			if err != nil {
				return fmt.Errorf("list secrets: %w", err)
			}
			existing = map[string]bool{}
			for _, secret := range list.Secrets {
				existing[secret.Key] = true
			}
		}
		if !existing[name] {
			return fmt.Errorf("secret %s is not set on %s yet; export %s and deploy again", name, slug, name)
		}
	}
	return nil
}

// functionRouteRulePrefix names the edge rules a functions manifest owns
// (ADR-1342). Rules with any other name are never touched.
const functionRouteRulePrefix = "functions:"

// functionRoutePriority places managed routes after the default priority of
// rules added by hand; the most specific route comes first.
const functionRoutePriority = 500

// reconcileRoutes gives the entry function's hosts one kind=route edge rule
// per declared route and removes the managed rules the manifest no longer
// declares from every function app.
func (d functionSetDeploy) reconcileRoutes(ctx context.Context, client functionSetClient, planned []plannedFunction) error {
	desired, entry, err := desiredFunctionRoutes(ctx, client, planned)
	if err != nil {
		return err
	}
	for _, fn := range planned {
		rules, err := client.ListEdgeRulesForApp(ctx, fn.slug)
		if err != nil {
			if fn.slug != entry {
				continue
			}
			return fmt.Errorf("list edge rules of %s: %w", fn.slug, err)
		}
		var want []api.CreateEdgeRuleRequest
		if fn.slug == entry {
			want = desired
		}
		if err := reconcileRouteRules(ctx, client, fn.slug, rules, want); err != nil {
			return err
		}
	}
	return nil
}

// desiredFunctionRoutes lists the route rules for the entry function's
// hosts, most specific route first.
func desiredFunctionRoutes(ctx context.Context, client functionSetClient, planned []plannedFunction) ([]api.CreateEdgeRuleRequest, string, error) {
	type route struct{ glob, function, target string }
	var routes []route
	entry := ""
	for _, fn := range planned {
		if fn.spec.Entry {
			entry = fn.slug
		}
		for _, glob := range fn.spec.Routes {
			routes = append(routes, route{glob, fn.spec.Name, fn.slug})
		}
	}
	if entry == "" || len(routes) == 0 {
		return nil, entry, nil
	}
	hosts, err := functionEntryHosts(ctx, client, entry)
	if err != nil {
		return nil, "", err
	}
	sort.SliceStable(routes, func(i, j int) bool {
		a, b := routeSpecificity(routes[i].glob), routeSpecificity(routes[j].glob)
		if a != b {
			return a > b
		}
		return routes[i].glob < routes[j].glob
	})
	var out []api.CreateEdgeRuleRequest
	for i, r := range routes {
		action, err := json.Marshal(api.EdgeRuleRouteAction{TargetAppSlug: r.target})
		if err != nil {
			return nil, "", err
		}
		priority := functionRoutePriority + i
		for _, host := range hosts {
			out = append(out, api.CreateEdgeRuleRequest{
				MatchHost: host, MatchPath: r.glob, Kind: "route", Action: action,
				Priority: &priority, Name: functionRouteRulePrefix + r.function,
			})
		}
	}
	return out, entry, nil
}

// routeSpecificity ranks a path glob by its literal prefix: /users/admin/*
// is more specific than /users/*.
func routeSpecificity(glob string) int {
	if i := strings.IndexAny(glob, "*?["); i >= 0 {
		return i
	}
	return len(glob) + 1
}

// functionEntryHosts returns the entry app's platform host and its verified
// custom domains.
func functionEntryHosts(ctx context.Context, client functionSetClient, slug string) ([]string, error) {
	app, err := client.GetApp(ctx, slug)
	if err != nil {
		return nil, fmt.Errorf("read entry app %s: %w", slug, err)
	}
	var hosts []string
	if u, err := url.Parse(app.URL); err == nil && u.Hostname() != "" {
		hosts = append(hosts, strings.ToLower(u.Hostname()))
	}
	domains, err := client.ListDomains(ctx)
	if err != nil {
		return nil, fmt.Errorf("list custom domains: %w", err)
	}
	for _, domain := range domains {
		if domain.AppID == app.ID && domain.Verified && domain.Environment == "" {
			hosts = append(hosts, strings.ToLower(domain.Domain))
		}
	}
	if len(hosts) == 0 {
		return nil, fmt.Errorf("entry app %s has no address", slug)
	}
	sort.Strings(hosts)
	return slices.Compact(hosts), nil
}

// reconcileRouteRules creates the wanted route rules an app lacks, then
// deletes its managed route rules that are no longer wanted.
func reconcileRouteRules(ctx context.Context, client functionSetClient, slug string, current []api.EdgeRuleResponse, want []api.CreateEdgeRuleRequest) error {
	key := func(host, matchPath, name string, priority int, action []byte) string {
		var route api.EdgeRuleRouteAction
		_ = json.Unmarshal(action, &route)
		return strings.Join([]string{strings.ToLower(host), matchPath, name, fmt.Sprint(priority), route.TargetAppSlug}, "\x00")
	}
	have := map[string]api.EdgeRuleResponse{}
	for _, rule := range current {
		if rule.Kind == "route" && strings.HasPrefix(rule.Name, functionRouteRulePrefix) {
			have[key(rule.MatchHost, rule.MatchPath, rule.Name, rule.Priority, rule.Action)] = rule
		}
	}
	wanted := map[string]bool{}
	for _, req := range want {
		k := key(req.MatchHost, req.MatchPath, req.Name, *req.Priority, req.Action)
		wanted[k] = true
		if _, ok := have[k]; ok {
			continue
		}
		if _, err := client.CreateEdgeRule(ctx, slug, req); err != nil {
			return fmt.Errorf("route %s on %s: %w", req.MatchPath, req.MatchHost, err)
		}
	}
	for _, k := range slices.Sorted(maps.Keys(have)) {
		if wanted[k] {
			continue
		}
		if err := client.DeleteEdgeRule(ctx, have[k].ID); err != nil {
			return fmt.Errorf("remove route %s on %s: %w", have[k].MatchPath, have[k].MatchHost, err)
		}
	}
	return nil
}

// functionARN is the object-notification destination for a function app.
func functionARN(region, accountID, appID string) string {
	return "arn:gregale:lambda:" + region + ":" + accountID + ":function:" + appID
}

// resolveFunctionBucket finds a bucket by ID or name in an app's catalog.
func resolveFunctionBucket(items []api.ObjectBucket, reference string) (api.ObjectBucket, error) {
	var matches []api.ObjectBucket
	for _, bucket := range items {
		if bucket.State != "deleted" && (bucket.ID == reference || bucket.Name == reference) {
			matches = append(matches, bucket)
		}
	}
	switch len(matches) {
	case 0:
		return api.ObjectBucket{}, fmt.Errorf("no bucket named or identified %q is visible to this app; create it with `gregale add bucket` or grant it", reference)
	case 1:
		return matches[0], nil
	default:
		return api.ObjectBucket{}, fmt.Errorf("bucket name %q matches %d buckets; use its ID", reference, len(matches))
	}
}

// reconcileFunctionBucketEvents replaces the function's managed notification
// rules on every bucket it declares, and removes them from buckets in its own
// catalog that it no longer declares. Rules owned by anything else are kept.
func reconcileFunctionBucketEvents(ctx context.Context, client functionSetClient, slug, appID, accountID string, events []gregalemanifest.FunctionBucketEvent) error {
	type target struct{ owner, bucketID string }
	desired := map[target][]api.ObjectNotificationRule{}
	declaredBuckets := map[string]bool{}
	order := []target{}
	catalogs := map[string]api.ObjectBucketList{}
	catalog := func(owner string) (api.ObjectBucketList, error) {
		if list, ok := catalogs[owner]; ok {
			return list, nil
		}
		list, err := client.ListObjectBuckets(ctx, owner)
		if err != nil {
			return list, fmt.Errorf("list buckets for %s: %w", owner, err)
		}
		catalogs[owner] = list
		return list, nil
	}
	for i, event := range events {
		owner := event.App
		if owner == "" {
			owner = slug
		}
		list, err := catalog(owner)
		if err != nil {
			return err
		}
		bucket, err := resolveFunctionBucket(list.Items, event.Bucket)
		if err != nil {
			return fmt.Errorf("bucket_events[%d]: %w", i, err)
		}
		key := target{owner, bucket.ID}
		if _, seen := desired[key]; !seen {
			order = append(order, key)
		}
		declaredBuckets[bucket.ID] = true
		desired[key] = append(desired[key], api.ObjectNotificationRule{
			ID:          gregalemanifest.FunctionBucketRuleID(slug, i, event),
			Destination: functionARN(bucket.Region, accountID, appID),
			Events:      append([]string(nil), event.Events...),
			Prefix:      event.Prefix,
			Suffix:      event.Suffix,
		})
	}
	// Buckets the function can see but no longer declares may still hold its
	// rules from an earlier deploy. A read the API refuses is not ours to fix.
	own, err := catalog(slug)
	if err == nil {
		for _, bucket := range own.Items {
			if declaredBuckets[bucket.ID] || bucket.State == "deleted" {
				continue
			}
			key := target{slug, bucket.ID}
			desired[key] = nil
			order = append(order, key)
		}
	}
	prefix := gregalemanifest.FunctionBucketRulePrefix(slug)
	for _, key := range order {
		current, err := client.GetObjectBucketNotifications(ctx, key.owner, key.bucketID)
		if err != nil {
			if desired[key] == nil {
				continue
			}
			return fmt.Errorf("read notifications for bucket %s: %w", key.bucketID, err)
		}
		next := make([]api.ObjectNotificationRule, 0, len(current.Rules)+len(desired[key]))
		for _, rule := range current.Rules {
			if !strings.HasPrefix(rule.ID, prefix) {
				next = append(next, rule)
			}
		}
		next = append(next, desired[key]...)
		if sameNotificationRules(current.Rules, next) {
			continue
		}
		if _, err := client.PutObjectBucketNotifications(ctx, key.owner, key.bucketID, api.ObjectBucketNotificationsRequest{Rules: next}); err != nil {
			return fmt.Errorf("write notifications for bucket %s: %w", key.bucketID, err)
		}
	}
	return nil
}

func sameNotificationRules(a, b []api.ObjectNotificationRule) bool {
	if len(a) != len(b) {
		return false
	}
	canonical := func(rules []api.ObjectNotificationRule) []string {
		out := make([]string, len(rules))
		for i, r := range rules {
			events := append([]string(nil), r.Events...)
			sort.Strings(events)
			out[i] = strings.Join([]string{r.ID, r.Destination, strings.Join(events, ","), r.Prefix, r.Suffix}, "\x00")
		}
		sort.Strings(out)
		return out
	}
	return slices.Equal(canonical(a), canonical(b))
}

// reconcileFunctionWebhooks creates missing endpoints and updates delivery
// paths. Endpoints the manifest does not list are reported and kept: deleting
// one would break the URL configured at the provider.
func reconcileFunctionWebhooks(ctx context.Context, client functionSetClient, out io.Writer, slug string, hooks []gregalemanifest.FunctionWebhook, getenv func(string) string) error {
	existing, err := client.ListInboundWebhookEndpoints(ctx, slug)
	if err != nil {
		if len(hooks) == 0 {
			return nil
		}
		return fmt.Errorf("list endpoints: %w", err)
	}
	byName := make(map[string]api.InboundWebhookEndpointResponse, len(existing))
	for _, endpoint := range existing {
		byName[endpoint.Name] = endpoint
	}
	declared := make(map[string]bool, len(hooks))
	for _, hook := range hooks {
		declared[hook.Name] = true
		current, ok := byName[hook.Name]
		if ok {
			if current.Provider != hook.Provider {
				return fmt.Errorf("endpoint %q uses provider %q, manifest says %q; a provider cannot change, so delete the endpoint first", hook.Name, current.Provider, hook.Provider)
			}
			if hook.DeliveryPath != "" && current.DeliveryPath != hook.DeliveryPath {
				deliveryPath := hook.DeliveryPath
				if _, err := client.UpdateInboundWebhookEndpoint(ctx, slug, current.ID, api.UpdateInboundWebhookEndpointRequest{DeliveryPath: &deliveryPath}); err != nil {
					return fmt.Errorf("update endpoint %q: %w", hook.Name, err)
				}
			}
			continue
		}
		secret := strings.TrimSpace(getenv(hook.SigningSecretEnv))
		if secret == "" {
			return fmt.Errorf("endpoint %q does not exist yet; set %s to its signing secret and deploy again", hook.Name, hook.SigningSecretEnv)
		}
		created, err := client.CreateInboundWebhookEndpoint(ctx, slug, api.CreateInboundWebhookEndpointRequest{
			Name: hook.Name, Provider: hook.Provider, SigningSecret: secret, DeliveryPath: hook.DeliveryPath,
		})
		if err != nil {
			return fmt.Errorf("create endpoint %q: %w", hook.Name, err)
		}
		_, _ = fmt.Fprintf(out, "Webhook %s for %s: %s\n  Save this URL now and register it with %s; it is shown only once.\n", hook.Name, slug, created.EndpointURL, hook.Provider)
	}
	var extra []string
	for _, endpoint := range existing {
		if !declared[endpoint.Name] {
			extra = append(extra, endpoint.Name)
		}
	}
	if len(extra) > 0 {
		sort.Strings(extra)
		_, _ = fmt.Fprintf(out, "Webhook endpoints on %s not in gregale.yaml (kept): %s\n", slug, strings.Join(extra, ", "))
	}
	return nil
}

// displayPath renders dir relative to cwd when possible.
func displayPath(cwd, dir string) string {
	if rel, err := filepath.Rel(cwd, dir); err == nil && !strings.HasPrefix(rel, "..") {
		if rel == "." {
			return "the current directory"
		}
		return rel
	}
	return dir
}

// deployFunctionSet is the entry point from cmdDeployTarballToExisting.
func deployFunctionSet(ctx context.Context, d functionSetDeploy) int {
	client, err := authedClientWithDeployTimeout(5 * time.Minute)
	if err != nil {
		return printErr("Not logged in", err)
	}
	if d.deployOne == nil {
		member := withFunctionSetMember(ctx)
		d.deployOne = func(args []string) int { return cmdDeployTarballToExisting(member, args, false) }
	}
	if d.out == nil {
		d.out = osStdout
		if jsonOutput {
			d.out = os.Stderr
		}
	}
	return d.run(ctx, client)
}

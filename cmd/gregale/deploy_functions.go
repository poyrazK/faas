package main

// deploy_functions.go — `functions:` fan-out for `gregale deploy` (ADR-976).
//
// A manifest that lists several functions deploys each one as its own
// function app through the ordinary single-function deploy path, then
// reconciles that function's idle timeout, cron triggers, bucket events and
// inbound webhooks. Functions deploy in manifest order and the fan-out stops
// at the first failure; re-running the deploy converges.

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

// functionSetClient is the API surface the post-deploy reconcile needs.
type functionSetClient interface {
	manifestCronClient
	GetApp(ctx context.Context, slug string) (api.AppResponse, error)
	UpdateApp(ctx context.Context, slug string, req api.UpdateAppRequest) (api.AppResponse, error)
	ListApps(ctx context.Context) ([]api.AppResponse, error)
	ListObjectBuckets(ctx context.Context, slug string) (api.ObjectBucketList, error)
	GetObjectBucketNotifications(ctx context.Context, slug, bucket string) (api.ObjectBucketNotifications, error)
	PutObjectBucketNotifications(ctx context.Context, slug, bucket string, in api.ObjectBucketNotificationsRequest) (api.ObjectBucketNotifications, error)
	ListInboundWebhookEndpoints(ctx context.Context, slug string) ([]api.InboundWebhookEndpointResponse, error)
	CreateInboundWebhookEndpoint(ctx context.Context, slug string, req api.CreateInboundWebhookEndpointRequest) (api.InboundWebhookEndpointResponse, error)
	UpdateInboundWebhookEndpoint(ctx context.Context, slug, id string, req api.UpdateInboundWebhookEndpointRequest) (api.InboundWebhookEndpointResponse, error)
}

// functionSetDeploy carries the parent deploy's resolved inputs.
type functionSetDeploy struct {
	args        []string // the parent `gregale deploy` arguments
	cwd         string
	projectDir  string
	projectSlug string
	manifest    *gregalemanifest.Manifest
	planOnly    bool // --dry-run, --diff, --server-diff or --create-only
	noTriggers  bool
	out         io.Writer
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
}

// functionSetManifest returns the manifest's functions when this deploy is a
// plain local-source deploy of a functions manifest.
func functionSetManifest(sourceDir string) (*gregalemanifest.Manifest, bool, error) {
	if sourceDir == "" {
		return nil, false, nil
	}
	m, found, err := gregalemanifest.Load(sourceDir)
	if err != nil || !found || len(m.Functions) == 0 {
		return nil, false, err
	}
	return m, true, nil
}

// functionSlug is the app slug of one function in a project.
func functionSlug(projectSlug, name string) string {
	return projectSlug + "-" + name
}

// plan validates every function before anything is deployed.
func (d functionSetDeploy) plan() ([]plannedFunction, error) {
	if err := d.manifest.Validate(); err != nil {
		return nil, err
	}
	planned := make([]plannedFunction, 0, len(d.manifest.Functions))
	for _, fn := range d.manifest.Functions {
		slug := functionSlug(d.projectSlug, fn.Name)
		if !api.ValidAppSlug(slug) {
			return nil, fmt.Errorf("function %q: app slug %q must be 3-40 lowercase letters, digits and hyphens; shorten the function name or pass a shorter --name", fn.Name, slug)
		}
		absRoot := filepath.Join(d.projectDir, filepath.FromSlash(fn.FunctionRoot()))
		info, err := os.Stat(absRoot)
		if err != nil || !info.IsDir() {
			return nil, fmt.Errorf("function %q: root %q is not a directory", fn.Name, fn.Root)
		}
		runtime, handler, err := functionRuntimeAndHandler(fn, absRoot)
		if err != nil {
			return nil, fmt.Errorf("function %q: %w", fn.Name, err)
		}
		root, err := filepath.Rel(d.cwd, absRoot)
		if err != nil {
			root = absRoot
		}
		planned = append(planned, plannedFunction{spec: fn, slug: slug, root: root, runtime: runtime, handler: handler})
	}
	return planned, nil
}

// functionRuntimeAndHandler resolves the runtime and wire handler for one
// function. A handler file in a shared tree must exist; without one, the
// root's conventional handler.* file decides the runtime.
func functionRuntimeAndHandler(fn gregalemanifest.FunctionSpec, absRoot string) (string, string, error) {
	if fn.Handler != "" {
		info, err := os.Stat(filepath.Join(absRoot, filepath.FromSlash(fn.Handler)))
		if err != nil || info.IsDir() {
			return "", "", fmt.Errorf("handler %q is not a file in %s", fn.Handler, fn.FunctionRoot())
		}
		runtime := fn.Runtime
		if runtime == "" {
			switch gregalemanifest.HandlerRuntimeFamily(fn.Handler) {
			case "node":
				runtime = runtimeNode22
			case "python":
				runtime = runtimePython312
			}
		}
		return runtime, path.Clean(fn.Handler), nil
	}
	if fn.Runtime != "" {
		return fn.Runtime, defaultTemplateHandler, nil
	}
	runtime, handler, ok := inferFunctionRuntime(absRoot)
	if !ok {
		return "", "", fmt.Errorf("could not infer a runtime from %s; set runtime: in gregale.yaml", fn.FunctionRoot())
	}
	return runtime, handler, nil
}

// functionDeployArgs rewrites the parent deploy arguments for one function:
// its root and slug replace --path and --name, and the function shape is
// explicit so the child never re-reads the project manifest as an app. The
// fan-out applies triggers itself, so the child never stages them.
func functionDeployArgs(args []string, fn plannedFunction) []string {
	out := stripDeployFlags(args, "path", "name", "runtime", "handler")
	return append(out, "--path", fn.root, "--name", fn.slug, "--function", "--runtime", fn.runtime, "--handler", fn.handler, "--no-triggers")
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
	done := make([]string, 0, len(planned))
	for i, fn := range planned {
		_, _ = fmt.Fprintf(d.out, "\n[%d/%d] %s → %s (%s, %s)\n", i+1, len(planned), fn.spec.Name, fn.slug, fn.runtime, fn.root)
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
	d.summarize(done, plannedFunction{}, nil)
	if !d.planOnly && client != nil {
		d.reportUnlisted(ctx, client, done)
	}
	return 0
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
	_, _ = fmt.Fprintf(d.out, "Not in gregale.yaml (kept): %s — delete with `gregale apps --yes <slug>`\n", strings.Join(unlisted, ", "))
}

// reconcile applies one deployed function's manifest configuration.
func (d functionSetDeploy) reconcile(ctx context.Context, client functionSetClient, fn plannedFunction) error {
	app, err := client.GetApp(ctx, fn.slug)
	if err != nil {
		return fmt.Errorf("read app %s: %w", fn.slug, err)
	}
	if fn.spec.IdleTimeoutS != 0 && app.IdleTimeoutS != fn.spec.IdleTimeoutS {
		idle := fn.spec.IdleTimeoutS
		if _, err := client.UpdateApp(ctx, fn.slug, api.UpdateAppRequest{IdleTimeoutS: &idle}); err != nil {
			return fmt.Errorf("set idle_timeout_s: %w", err)
		}
	}
	if d.noTriggers {
		return nil
	}
	crons := &gregalemanifest.Manifest{Triggers: []gregalemanifest.Trigger{}}
	for _, trigger := range d.manifest.Triggers {
		if trigger.App == fn.spec.Name {
			trigger.App = fn.slug
			crons.Triggers = append(crons.Triggers, trigger)
		}
	}
	txn, err := reconcileManifestCronsWithRollback(ctx, client, fn.slug, crons)
	if err != nil {
		return fmt.Errorf("cron triggers: %w", err)
	}
	txn.commit()
	acct, err := client.Whoami(ctx)
	if err != nil {
		return fmt.Errorf("read account: %w", err)
	}
	if err := reconcileFunctionBucketEvents(ctx, client, fn.slug, app.ID, acct.ID, fn.spec.BucketEvents); err != nil {
		return fmt.Errorf("bucket events: %w", err)
	}
	getenv := d.getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	if err := reconcileFunctionWebhooks(ctx, client, d.out, fn.slug, fn.spec.Webhooks, getenv); err != nil {
		return fmt.Errorf("webhooks: %w", err)
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
		d.deployOne = func(args []string) int { return cmdDeployTarballToExisting(ctx, args, false) }
	}
	if d.out == nil {
		d.out = osStdout
		if jsonOutput {
			d.out = os.Stderr
		}
	}
	return d.run(ctx, client)
}

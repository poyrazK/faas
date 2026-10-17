package gregalemanifest

import (
	"errors"
	"fmt"
	"maps"
	"path"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// FunctionSpec is one entry of `functions:` (ADR-985). Each entry deploys as
// its own function app; the CLI fans `gregale deploy` out over the list.
// Root is the uploaded source directory. Handler, when set, names the
// function's file inside Root, so several Node or Python functions can share
// one source tree; Root then defaults to the project directory.
type FunctionSpec struct {
	Name         string                `yaml:"name"`
	Root         string                `yaml:"root,omitempty"`
	Handler      string                `yaml:"handler,omitempty"`
	Runtime      string                `yaml:"runtime,omitempty"`
	IdleTimeoutS int                   `yaml:"idle_timeout_s,omitempty"`
	BucketEvents []FunctionBucketEvent `yaml:"bucket_events,omitempty"`
	Webhooks     []FunctionWebhook     `yaml:"webhooks,omitempty"`

	// Per-function app settings (ADR-1342). An omitted key leaves the
	// app's current setting alone. Concurrency is the function's
	// function_concurrency: requests one Node or Python worker serves at
	// once.
	RAMMB           int               `yaml:"ram_mb,omitempty"`
	RequestTimeoutS int               `yaml:"request_timeout_s,omitempty"`
	MinInstances    *int              `yaml:"min_instances,omitempty"`
	Concurrency     int               `yaml:"concurrency,omitempty"`
	Env             map[string]string `yaml:"env,omitempty"`
	// PruneEnv makes Env the function's whole default-scope env: a deploy
	// removes every other key. Off, keys the manifest drops stay on the app.
	PruneEnv bool `yaml:"prune_env,omitempty"`
	// SecretsFromEnv names secrets whose values the deploy reads from the
	// deployer's environment and seals; the values never appear in the
	// manifest.
	SecretsFromEnv []string `yaml:"secrets_from_env,omitempty"`
	// Routes are the request path globs the entry function's hosts send to
	// this function. Entry marks that front-door function, which serves
	// every path no route claims.
	Routes []string `yaml:"routes,omitempty"`
	Entry  bool     `yaml:"entry,omitempty"`
	// QueueBindings push queue messages to the function and EventTriggers
	// subscribe it to events (ADR-1342). An omitted queue_bindings leaves
	// the app's bindings alone and an empty list removes them; event
	// triggers are added and updated, never removed, as on any deploy.
	QueueBindings []QueueBinding `yaml:"queue_bindings,omitempty"`
	EventTriggers []EventTrigger `yaml:"event_triggers,omitempty"`
}

// HasPreDeploySettings reports whether the function sets anything that must
// be on its app before the deployment is built: RAM and concurrency shape
// the build, and env and secrets reach the first wake.
func (fn FunctionSpec) HasPreDeploySettings() bool {
	return fn.RAMMB != 0 || fn.RequestTimeoutS != 0 || fn.Concurrency != 0 || len(fn.Env) > 0 || fn.PruneEnv || len(fn.SecretsFromEnv) > 0
}

// FunctionBucketEvent routes object-storage notifications for one bucket to
// the function. App names the app whose bucket grant manages the bucket's
// notification configuration; empty means the function itself.
type FunctionBucketEvent struct {
	ID     string   `yaml:"id,omitempty"`
	Bucket string   `yaml:"bucket"`
	App    string   `yaml:"app,omitempty"`
	Events []string `yaml:"events"`
	Prefix string   `yaml:"prefix,omitempty"`
	Suffix string   `yaml:"suffix,omitempty"`
}

// FunctionWebhook declares an inbound webhook endpoint delivering to the
// function. The signing secret is read from SigningSecretEnv when the
// endpoint is created; it never appears in the manifest.
type FunctionWebhook struct {
	Name             string `yaml:"name"`
	Provider         string `yaml:"provider"`
	SigningSecretEnv string `yaml:"signing_secret_env"`
	DeliveryPath     string `yaml:"delivery_path,omitempty"`
}

var (
	functionNameRE   = regexp.MustCompile(`^[a-z]([a-z0-9-]{0,28}[a-z0-9])?$`)
	bucketEventIDRE  = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$`)
	envVarNameRE     = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
	functionsAllowed = map[string]bool{"schema_version": true, "functions": true, "triggers": true}
)

// placeholderFunctionARN lets manifest validation reuse the notification
// rule normalizer before the function's real app and account IDs exist.
const placeholderFunctionARN = "arn:gregale:lambda:placeholder:00000000-0000-4000-8000-000000000000:function:00000000-0000-4000-8000-000000000001"

// FunctionBucketRuleID is the notification rule ID a manifest-managed bucket
// event gets. The function slug prefix lets a deploy replace only the rules
// it owns and keep every other rule on the bucket.
func FunctionBucketRuleID(functionSlug string, index int, event FunctionBucketEvent) string {
	suffix := event.ID
	if suffix == "" {
		suffix = fmt.Sprintf("%d", index+1)
	}
	return FunctionBucketRulePrefix(functionSlug) + suffix
}

// FunctionBucketRulePrefix is the rule-ID prefix owned by one function.
func FunctionBucketRulePrefix(functionSlug string) string {
	return "fn-" + functionSlug + "-"
}

func (m *Manifest) validateFunctions(plan api.Plan) error {
	if m.Functions == nil {
		return nil
	}
	if len(m.Functions) == 0 {
		return errors.New("functions: list at least one function, or remove the key")
	}
	if err := m.rejectSectionsBesideFunctions(); err != nil {
		return err
	}
	limits := api.MustLimitsFor(plan)
	_, ceiling := limits.IdleTimeoutBounds()
	names := make(map[string]struct{}, len(m.Functions))
	routes := map[string]string{}
	var entries []string
	routed := false
	roots := make(map[string]string, len(m.Functions))
	byBucket := map[string][]api.ObjectNotificationRule{}
	for i, fn := range m.Functions {
		field := fmt.Sprintf("functions[%d]", i)
		if !functionNameRE.MatchString(fn.Name) {
			return fmt.Errorf("%s.name %q must be 1-30 lowercase letters, digits or hyphens, starting with a letter", field, fn.Name)
		}
		if _, dup := names[fn.Name]; dup {
			return fmt.Errorf("%s: duplicate function name %q", field, fn.Name)
		}
		names[fn.Name] = struct{}{}
		if fn.Runtime != "" && !api.ValidFunctionRuntime(fn.Runtime) {
			return fmt.Errorf("%s.runtime %q is not a function runtime (want one of %s)", field, fn.Runtime, strings.Join(api.FunctionRuntimes, ", "))
		}
		root, err := cleanFunctionRoot(fn.Root, fn.Handler != "")
		if err != nil {
			return fmt.Errorf("%s.root: %w", field, err)
		}
		handler := ""
		if fn.Handler != "" {
			if handler, err = validateFunctionHandler(fn); err != nil {
				return fmt.Errorf("%s.handler: %w", field, err)
			}
		}
		entry := root + "\x00" + handler
		if other, dup := roots[entry]; dup {
			if handler == "" {
				return fmt.Errorf("%s.root %q is already the root of function %q; give one of them a handler", field, root, other)
			}
			return fmt.Errorf("%s.handler %q in %q is already the handler of function %q", field, handler, root, other)
		}
		roots[entry] = fn.Name
		if fn.IdleTimeoutS != 0 && (fn.IdleTimeoutS < api.IdleTimeoutFloorSeconds || fn.IdleTimeoutS > ceiling) {
			return fmt.Errorf("%s.idle_timeout_s %d must be between %d and %d on this plan", field, fn.IdleTimeoutS, api.IdleTimeoutFloorSeconds, ceiling)
		}
		if err := validateFunctionSettings(field, fn, limits); err != nil {
			return err
		}
		if err := validateFunctionDeliveries(field, fn); err != nil {
			return err
		}
		for j, route := range fn.Routes {
			if other, dup := routes[route]; dup {
				return fmt.Errorf("%s.routes[%d] %q is already a route of function %q", field, j, route, other)
			}
			routes[route] = fn.Name
			routed = true
		}
		if fn.Entry {
			entries = append(entries, fn.Name)
		}
		ids := map[string]struct{}{}
		for j, event := range fn.BucketEvents {
			eventField := fmt.Sprintf("%s.bucket_events[%d]", field, j)
			if strings.TrimSpace(event.Bucket) == "" {
				return fmt.Errorf("%s.bucket is required", eventField)
			}
			if event.App != "" && !isDNSSafeSlug(event.App) {
				return fmt.Errorf("%s.app %q must match [a-z0-9-]+", eventField, event.App)
			}
			if event.ID != "" && !bucketEventIDRE.MatchString(event.ID) {
				return fmt.Errorf("%s.id %q must be lowercase letters, digits or hyphens", eventField, event.ID)
			}
			if len(event.Events) == 0 {
				return fmt.Errorf("%s.events: list at least one event, e.g. s3:ObjectCreated:*", eventField)
			}
			id := FunctionBucketRuleID(fn.Name, j, event)
			if _, dup := ids[id]; dup {
				return fmt.Errorf("%s: duplicate id %q", eventField, event.ID)
			}
			ids[id] = struct{}{}
			rule := api.ObjectNotificationRule{ID: id, Destination: placeholderFunctionARN, Events: event.Events, Prefix: event.Prefix, Suffix: event.Suffix}
			if _, err := api.NormalizeObjectNotificationRules([]api.ObjectNotificationRule{rule}); err != nil {
				return fmt.Errorf("%s: %w", eventField, err)
			}
			key := event.App + "\x00" + event.Bucket
			byBucket[key] = append(byBucket[key], rule)
		}
		hooks := map[string]struct{}{}
		for j, hook := range fn.Webhooks {
			hookField := fmt.Sprintf("%s.webhooks[%d]", field, j)
			if !isDNSSafeSlug(hook.Name) {
				return fmt.Errorf("%s.name %q must match [a-z][a-z0-9-]{0,62}", hookField, hook.Name)
			}
			if _, dup := hooks[hook.Name]; dup {
				return fmt.Errorf("%s: duplicate webhook name %q", hookField, hook.Name)
			}
			hooks[hook.Name] = struct{}{}
			if !validInboundWebhookProvider(hook.Provider) {
				return fmt.Errorf("%s.provider %q must be one of %s", hookField, hook.Provider, strings.Join(api.AllowedInboundWebhookProviders, ", "))
			}
			if !envVarNameRE.MatchString(hook.SigningSecretEnv) {
				return fmt.Errorf("%s.signing_secret_env %q must name an environment variable like STRIPE_WEBHOOK_SECRET", hookField, hook.SigningSecretEnv)
			}
			if hook.DeliveryPath != "" && !strings.HasPrefix(hook.DeliveryPath, "/") {
				return fmt.Errorf("%s.delivery_path %q must start with /", hookField, hook.DeliveryPath)
			}
		}
	}
	if len(entries) > 1 {
		return fmt.Errorf("functions: only one function can be the entry; %s all set entry: true", strings.Join(entries, ", "))
	}
	if routed && len(entries) == 0 {
		return errors.New("functions: routes need a front door; set entry: true on the function whose address serves them")
	}
	for key, rules := range byBucket {
		if _, err := api.NormalizeObjectNotificationRules(rules); err != nil {
			_, bucket, _ := strings.Cut(key, "\x00")
			return fmt.Errorf("functions: bucket_events on bucket %q: %w", bucket, err)
		}
	}
	for i, trigger := range m.Triggers {
		if trigger.Kind != TriggerKindCron {
			return fmt.Errorf("trigger[%d]: functions support cron triggers only; declare %q triggers with `gregale triggers add` after deploy", i, trigger.Kind)
		}
		if _, ok := names[trigger.App]; !ok {
			return fmt.Errorf("trigger[%d].app %q must name one of the functions", i, trigger.App)
		}
	}
	return nil
}

// validateFunctionSettings checks one function's app settings (ADR-1342)
// against the plan. apid stays authoritative; this catches mistakes before
// any function deploys.
func validateFunctionSettings(field string, fn FunctionSpec, limits api.Limits) error {
	if fn.RAMMB < 0 || fn.RAMMB > limits.RAMMB {
		return fmt.Errorf("%s.ram_mb %d must be between 1 and %d on the %s plan", field, fn.RAMMB, limits.RAMMB, limits.Plan)
	}
	if maxTimeout := int(limits.RequestBudgetMaxDuration() / time.Second); fn.RequestTimeoutS < 0 || fn.RequestTimeoutS > maxTimeout {
		return fmt.Errorf("%s.request_timeout_s %d must be between 1 and %d on the %s plan", field, fn.RequestTimeoutS, maxTimeout, limits.Plan)
	}
	if fn.MinInstances != nil && (*fn.MinInstances < 0 || *fn.MinInstances > limits.MaxMinInstances) {
		return fmt.Errorf("%s.min_instances %d must be between 0 and %d on the %s plan", field, *fn.MinInstances, limits.MaxMinInstances, limits.Plan)
	}
	if fn.Concurrency < 0 || fn.Concurrency > limits.ConcurrencyPerVMBound {
		return fmt.Errorf("%s.concurrency %d must be between 1 and %d on the %s plan", field, fn.Concurrency, limits.ConcurrencyPerVMBound, limits.Plan)
	}
	for _, key := range slices.Sorted(maps.Keys(fn.Env)) {
		if problem := api.ValidateEnvKey(key); problem != nil {
			return fmt.Errorf("%s.env %q: %s", field, key, problem.Detail)
		}
	}
	seen := map[string]bool{}
	for j, name := range fn.SecretsFromEnv {
		if problem := api.ValidateEnvKey(name); problem != nil {
			return fmt.Errorf("%s.secrets_from_env[%d] %q: %s", field, j, name, problem.Detail)
		}
		if seen[name] {
			return fmt.Errorf("%s.secrets_from_env[%d]: %q is listed twice", field, j, name)
		}
		seen[name] = true
		if _, plain := fn.Env[name]; plain {
			return fmt.Errorf("%s: %q is both a plain env value and a secret; keep one", field, name)
		}
	}
	if fn.Entry && len(fn.Routes) > 0 {
		return fmt.Errorf("%s: the entry function serves every path no route claims; remove its routes", field)
	}
	for j, route := range fn.Routes {
		if !strings.HasPrefix(route, "/") {
			return fmt.Errorf("%s.routes[%d] %q must start with /", field, j, route)
		}
		if _, err := api.MatchEdgeRulePath(route, "/"); err != nil {
			return fmt.Errorf("%s.routes[%d] %q is not a path glob: %w", field, j, route, err)
		}
		if _, templated := api.EdgeRuleTemplatedPath(route); templated {
			return fmt.Errorf("%s.routes[%d] %q: routes are globs such as /users/*, not {param} templates", field, j, route)
		}
	}
	return nil
}

// validateFunctionDeliveries checks a function's queue bindings and event
// triggers. A function consumes queues by push, and its event triggers
// deliver to it, so neither names another mode or app.
func validateFunctionDeliveries(field string, fn FunctionSpec) error {
	names := map[string]bool{}
	for j, binding := range fn.QueueBindings {
		bindingField := fmt.Sprintf("%s.queue_bindings[%d]", field, j)
		if binding.Mode == "pull" {
			return fmt.Errorf("%s: a function consumes queues by push; remove mode: pull", bindingField)
		}
		pushed := binding
		pushed.Mode = "push"
		if pushed.WorkloadClass == "" {
			pushed.WorkloadClass = "http"
		}
		if err := pushed.Validate(); err != nil {
			return fmt.Errorf("%s: %w", bindingField, err)
		}
		if names[binding.Name] {
			return fmt.Errorf("%s: duplicate queue binding name %q", bindingField, binding.Name)
		}
		names[binding.Name] = true
	}
	for j, trigger := range fn.EventTriggers {
		if trigger.App != "" {
			return fmt.Errorf("%s.event_triggers[%d].app: remove app; a function's event triggers deliver to that function", field, j)
		}
		if err := trigger.Validate(j); err != nil {
			return fmt.Errorf("%s: %w", field, err)
		}
	}
	return nil
}

// rejectSectionsBesideFunctions keeps a functions manifest to the sections
// the per-function fan-out applies, so no declaration is silently dropped.
func (m *Manifest) rejectSectionsBesideFunctions() error {
	v := reflect.ValueOf(*m)
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		key, _, _ := strings.Cut(t.Field(i).Tag.Get("yaml"), ",")
		if key == "" || key == "-" || functionsAllowed[key] {
			continue
		}
		if !v.Field(i).IsZero() {
			return fmt.Errorf("functions: cannot be combined with %s: in the same manifest; configure each function after deploy, or use separate manifests", key)
		}
	}
	return nil
}

// RepositoryDeployFunctions reports how a single-app repository deploy
// treats a `functions:` manifest (ADR-985). The CLI fans such a manifest out
// into one function app per entry and applies each function's settings
// itself; a function app's upload carries the project manifest whenever the
// function shares the project root, so a function app applies nothing from
// it (skip). Any other app would silently deploy the repository root, so it
// is refused.
func (m *Manifest) RepositoryDeployFunctions(functionApp bool) (skip bool, err error) {
	if len(m.Functions) == 0 {
		return false, nil
	}
	if functionApp {
		return true, nil
	}
	return false, errors.New("functions: is deployed with `gregale deploy` from a checkout, one app per function; point this app at a single function's root instead")
}

// FunctionRoot is the function's source directory relative to the project:
// Root, or the project itself when only a handler is given.
func (fn FunctionSpec) FunctionRoot() string {
	root, err := cleanFunctionRoot(fn.Root, fn.Handler != "")
	if err != nil {
		return path.Clean(strings.TrimSpace(fn.Root))
	}
	return root
}

// HandlerRuntimeFamily infers a function's runtime family from its handler
// file when no runtime is set: "node" for .js/.mjs, "python" for .py.
func HandlerRuntimeFamily(handler string) string {
	switch path.Ext(handler) {
	case ".js", ".mjs":
		return "node"
	case ".py":
		return "python"
	}
	return ""
}

// validateFunctionHandler checks a shared-tree handler file and returns its
// clean path relative to the function's root.
func validateFunctionHandler(fn FunctionSpec) (string, error) {
	runtime := fn.Runtime
	if strings.HasPrefix(runtime, "go") {
		return "", errors.New("a Go function is built from its own root; remove handler and give the function its own root")
	}
	if runtime == "" {
		switch HandlerRuntimeFamily(fn.Handler) {
		case "node":
			runtime = "node24"
		case "python":
			runtime = "python313"
		default:
			return "", fmt.Errorf("%q is not a .js, .mjs or .py file; set runtime to choose the function's runtime", fn.Handler)
		}
	}
	file, ok, err := api.FunctionHandlerFile(runtime, fn.Handler)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("%q is not a handler file for runtime %s", fn.Handler, runtime)
	}
	return file, nil
}

func cleanFunctionRoot(root string, hasHandler bool) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		if hasHandler {
			return ".", nil
		}
		return "", errors.New("is required (the function's source directory), or set handler to use a file in the project")
	}
	if strings.HasPrefix(root, "/") || strings.Contains(root, "\\") {
		return "", fmt.Errorf("%q must be a relative directory", root)
	}
	clean := path.Clean(root)
	if clean == "." && !hasHandler {
		return "", fmt.Errorf("%q is the project root; set handler to name the function's file in it", root)
	}
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("%q must stay inside the project", root)
	}
	return clean, nil
}

func validInboundWebhookProvider(provider string) bool {
	for _, allowed := range api.AllowedInboundWebhookProviders {
		if provider == allowed {
			return true
		}
	}
	return false
}

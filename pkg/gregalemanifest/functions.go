package gregalemanifest

import (
	"errors"
	"fmt"
	"path"
	"reflect"
	"regexp"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// FunctionSpec is one entry of `functions:` (ADR-976). Each entry deploys as
// its own function app from its own source root; the CLI fans
// `gregale deploy` out over the list.
type FunctionSpec struct {
	Name         string                `yaml:"name"`
	Root         string                `yaml:"root"`
	Runtime      string                `yaml:"runtime,omitempty"`
	IdleTimeoutS int                   `yaml:"idle_timeout_s,omitempty"`
	BucketEvents []FunctionBucketEvent `yaml:"bucket_events,omitempty"`
	Webhooks     []FunctionWebhook     `yaml:"webhooks,omitempty"`
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
	_, ceiling := api.MustLimitsFor(plan).IdleTimeoutBounds()
	names := make(map[string]struct{}, len(m.Functions))
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
		root, err := cleanFunctionRoot(fn.Root)
		if err != nil {
			return fmt.Errorf("%s.root: %w", field, err)
		}
		if other, dup := roots[root]; dup {
			return fmt.Errorf("%s.root %q is already the root of function %q", field, fn.Root, other)
		}
		roots[root] = fn.Name
		if fn.Runtime != "" && !api.ValidFunctionRuntime(fn.Runtime) {
			return fmt.Errorf("%s.runtime %q is not a function runtime (want one of %s)", field, fn.Runtime, strings.Join(api.FunctionRuntimes, ", "))
		}
		if fn.IdleTimeoutS != 0 && (fn.IdleTimeoutS < api.IdleTimeoutFloorSeconds || fn.IdleTimeoutS > ceiling) {
			return fmt.Errorf("%s.idle_timeout_s %d must be between %d and %d on this plan", field, fn.IdleTimeoutS, api.IdleTimeoutFloorSeconds, ceiling)
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

func cleanFunctionRoot(root string) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", errors.New("is required (the function's source directory)")
	}
	if strings.HasPrefix(root, "/") || strings.Contains(root, "\\") {
		return "", fmt.Errorf("%q must be a relative directory", root)
	}
	clean := path.Clean(root)
	if clean == "." {
		return "", fmt.Errorf("%q must be a subdirectory, not the project root", root)
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

package gregalemanifest

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// The function settings plan shared by `gregale deploy` and repository
// pushes (ADR-20261108160000): which app each function deploys as, with
// which runtime and handler, and which of its settings map onto that app.
// Keeping one implementation means a push and a CLI deploy cannot drift.

const (
	functionRuntimeNode22    = "node22"
	functionRuntimePython312 = "python312"
	functionRuntimeGo124     = "go124"
	// DefaultFunctionHandler is the wire handler of a function whose root
	// holds a conventional handler.* file.
	DefaultFunctionHandler = "handler.handler"
)

// FunctionPlan is one function resolved against its source tree.
type FunctionPlan struct {
	Spec FunctionSpec
	// Slug is the function's app: <project>-<name>.
	Slug string
	// Root is the function's source directory, slash-separated and relative
	// to the project directory ("." for the project itself).
	Root    string
	Runtime string
	Handler string
}

// FunctionSlug is the app slug of one function in a project.
func FunctionSlug(projectSlug, name string) string {
	return projectSlug + "-" + name
}

// PlanFunctions validates the manifest and resolves every function against
// the source tree at projectDir before anything is deployed.
func (m *Manifest) PlanFunctions(projectDir, projectSlug string) ([]FunctionPlan, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	planned := make([]FunctionPlan, 0, len(m.Functions))
	for _, fn := range m.Functions {
		slug := FunctionSlug(projectSlug, fn.Name)
		if !api.ValidAppSlug(slug) {
			return nil, fmt.Errorf("function %q: app slug %q must be 3-40 lowercase letters, digits and hyphens; shorten the function or project name", fn.Name, slug)
		}
		root := fn.FunctionRoot()
		absRoot := filepath.Join(projectDir, filepath.FromSlash(root))
		info, err := os.Stat(absRoot)
		if err != nil || !info.IsDir() {
			return nil, fmt.Errorf("function %q: root %q is not a directory", fn.Name, fn.Root)
		}
		runtime, handler, err := ResolveFunctionRuntime(fn, absRoot)
		if err != nil {
			return nil, fmt.Errorf("function %q: %w", fn.Name, err)
		}
		planned = append(planned, FunctionPlan{Spec: fn, Slug: slug, Root: root, Runtime: runtime, Handler: handler})
	}
	return planned, nil
}

// ResolveFunctionRuntime resolves the runtime and wire handler of one
// function. A handler file in a shared tree must exist; without one, the
// root's conventional handler.* file decides the runtime.
func ResolveFunctionRuntime(fn FunctionSpec, absRoot string) (runtime, handler string, err error) {
	if fn.Handler != "" {
		info, err := os.Stat(filepath.Join(absRoot, filepath.FromSlash(fn.Handler)))
		if err != nil || info.IsDir() {
			return "", "", fmt.Errorf("handler %q is not a file in %s", fn.Handler, fn.FunctionRoot())
		}
		runtime := fn.Runtime
		if runtime == "" {
			switch HandlerRuntimeFamily(fn.Handler) {
			case "node":
				runtime = functionRuntimeNode22
			case "python":
				runtime = functionRuntimePython312
			}
		}
		return runtime, path.Clean(fn.Handler), nil
	}
	if fn.Runtime != "" {
		return fn.Runtime, DefaultFunctionHandler, nil
	}
	runtime, handler, ok := InferFunctionRuntime(absRoot)
	if !ok {
		return "", "", fmt.Errorf("could not infer a runtime from %s; set runtime: in gregale.yaml", fn.FunctionRoot())
	}
	return runtime, handler, nil
}

// InferFunctionRuntime reads a single function's root: a gregale.yaml
// `function:` block wins, else exactly one conventional handler file.
func InferFunctionRuntime(srcDir string) (runtime, handler string, ok bool) {
	if manifest, found, err := Load(srcDir); err == nil && found && manifest.Function != nil {
		if err := manifest.Validate(); err == nil {
			return strings.TrimSpace(manifest.Function.Runtime), strings.TrimSpace(manifest.Function.Handler), true
		}
	}
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return "", "", false
	}
	var picked string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		switch name := strings.ToLower(e.Name()); name {
		case "handler.js", "handler.ts", "handler.py", "handler.go":
			if picked != "" {
				return "", "", false // ambiguous: several handlers
			}
			picked = name
		}
	}
	switch picked {
	case "handler.js", "handler.ts":
		return functionRuntimeNode22, DefaultFunctionHandler, true
	case "handler.py":
		return functionRuntimePython312, DefaultFunctionHandler, true
	case "handler.go":
		return functionRuntimeGo124, DefaultFunctionHandler, true
	}
	return "", "", false
}

// FunctionCronTriggers returns the manifest's triggers for one function,
// addressed to its app. Triggers stays a non-nil empty list when the
// manifest declares triggers, so a reconcile removes stale ones.
func (m *Manifest) FunctionCronTriggers(p FunctionPlan) *Manifest {
	out := &Manifest{Triggers: []Trigger{}}
	for _, trigger := range m.Triggers {
		if trigger.App == p.Spec.Name {
			trigger.App = p.Slug
			out.Triggers = append(out.Triggers, trigger)
		}
	}
	return out
}

// FunctionRealtimeTriggers returns the manifest's realtime triggers and
// endpoints for one function, addressed to its app.
func (m *Manifest) FunctionRealtimeTriggers(p FunctionPlan) *Manifest {
	out := &Manifest{}
	if m.Triggers != nil && len(m.Triggers) == 0 {
		out.Triggers = []Trigger{}
	}
	for _, trigger := range m.Triggers {
		if trigger.Kind == TriggerKindRealtime && trigger.App == p.Spec.Name {
			trigger.App = p.Slug
			out.Triggers = append(out.Triggers, trigger)
		}
	}
	if m.Realtime != nil {
		out.Realtime = &RealtimeConfig{Endpoints: []RealtimeEndpoint{}}
		for _, endpoint := range m.Realtime.Endpoints {
			if endpoint.App == p.Spec.Name {
				endpoint.App = p.Slug
				out.Realtime.Endpoints = append(out.Realtime.Endpoints, endpoint)
			}
		}
	}
	return out
}

// AppSettingsUpdate is the app PATCH that brings an app to the function's
// declared RAM, request timeout and concurrency. An omitted key leaves the
// app's setting alone (ADR-1342).
func (fn FunctionSpec) AppSettingsUpdate(app api.AppResponse) (api.UpdateAppRequest, bool) {
	var update api.UpdateAppRequest
	changed := false
	if fn.RAMMB != 0 && app.RAMMB != fn.RAMMB {
		ram := fn.RAMMB
		update.RAMMB, changed = &ram, true
	}
	if fn.RequestTimeoutS != 0 && app.RequestTimeoutS != fn.RequestTimeoutS {
		timeout := fn.RequestTimeoutS
		update.RequestTimeoutS, changed = &timeout, true
	}
	if fn.Concurrency != 0 && max(app.FunctionConcurrency, 1) != fn.Concurrency {
		concurrency := fn.Concurrency
		update.FunctionConcurrency, changed = &concurrency, true
	}
	return update, changed
}

// RepositoryDeferredStep is a setting an unattended repository push does not
// apply because it needs a person (ADR-20261108160000 §5).
type RepositoryDeferredStep struct {
	// Kind is webhook, bucket_events or secrets_from_env.
	Kind string `json:"kind"`
	// Detail names the webhook, bucket or secret.
	Detail string `json:"detail"`
}

// RepositoryDeferredSteps lists the function's settings a push leaves to
// `gregale deploy`: new inbound webhooks (their URL is shown once), bucket
// notifications (storage manage needs MFA) and secrets read from the
// deployer's environment.
func (fn FunctionSpec) RepositoryDeferredSteps() []RepositoryDeferredStep {
	var steps []RepositoryDeferredStep
	for _, hook := range fn.Webhooks {
		steps = append(steps, RepositoryDeferredStep{Kind: "webhook", Detail: hook.Name})
	}
	for _, event := range fn.BucketEvents {
		steps = append(steps, RepositoryDeferredStep{Kind: "bucket_events", Detail: event.Bucket})
	}
	for _, name := range fn.SecretsFromEnv {
		steps = append(steps, RepositoryDeferredStep{Kind: "secrets_from_env", Detail: name})
	}
	return steps
}

package gregalemanifest

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	// FunctionsEnv is the env variable a deploy sets on each function app:
	// a JSON object from the names it may call to their app slugs
	// (ADR-20261111150000 §2). The SDK clients read it.
	FunctionsEnv = "GREGALE_FUNCTIONS"
	// FunctionSchemaMaxBytes caps one input or output schema.
	FunctionSchemaMaxBytes = 64 << 10
)

// resolveSchemas reads the function's input and output schema, inline or
// from a .json file inside its root, into InputSchema and OutputSchema.
func (fn *FunctionSpec) resolveSchemas(fsys fs.FS, root string) error {
	var err error
	if fn.InputSchema, err = resolveFunctionSchema(fsys, root, fn.Name, "input", fn.Input); err != nil {
		return err
	}
	fn.OutputSchema, err = resolveFunctionSchema(fsys, root, fn.Name, "output", fn.Output)
	return err
}

func resolveFunctionSchema(fsys fs.FS, root, name, field string, v any) (json.RawMessage, error) {
	if v == nil {
		return nil, nil
	}
	var raw []byte
	switch t := v.(type) {
	case string:
		rel := path.Clean(strings.TrimSpace(t))
		if path.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, "../") || path.Ext(rel) != ".json" {
			return nil, fmt.Errorf("function %q: %s %q must be a .json file inside the function's root", name, field, t)
		}
		b, err := fs.ReadFile(fsys, path.Join(root, rel))
		if err != nil {
			return nil, fmt.Errorf("function %q: read %s schema %q: %w", name, field, t, err)
		}
		raw = b
	case map[string]any:
		b, err := json.Marshal(t)
		if err != nil {
			return nil, fmt.Errorf("function %q: %s schema: %w", name, field, err)
		}
		raw = b
	default:
		return nil, fmt.Errorf("function %q: %s must be a JSON Schema object or a path to a .json file", name, field)
	}
	if len(raw) > FunctionSchemaMaxBytes {
		return nil, fmt.Errorf("function %q: %s schema is %d bytes; the limit is %d", name, field, len(raw), FunctionSchemaMaxBytes)
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, fmt.Errorf("function %q: %s schema is not a JSON object: %w", name, field, err)
	}
	if len(schema) == 0 {
		return nil, fmt.Errorf("function %q: %s schema is empty; omit %s instead", name, field, field)
	}
	return json.RawMessage(raw), nil
}

// FunctionCalls is what one function may call through its typed client.
type FunctionCalls struct {
	// Targets are the app slugs its service bindings name, sorted.
	Targets []string
	// Names maps each callable name to its app slug: siblings by function
	// name, `calls:` entries by their slug. It is the FunctionsEnv value.
	Names map[string]string
}

// Env is the FunctionsEnv value.
func (c FunctionCalls) Env() string {
	b, _ := json.Marshal(c.Names) // a string map always marshals; keys sort
	return string(b)
}

// FunctionCallGraph returns, for each function by name, the siblings and
// `calls:` apps it may call (ADR-20261111150000 §4). slugOf names each
// function's app.
func FunctionCallGraph(functions []FunctionSpec, slugOf func(name string) string) (map[string]FunctionCalls, error) {
	graph := make(map[string]FunctionCalls, len(functions))
	for _, fn := range functions {
		self := slugOf(fn.Name)
		calls := FunctionCalls{Names: map[string]string{}}
		for _, sibling := range functions {
			if sibling.Name != fn.Name {
				calls.Names[sibling.Name] = slugOf(sibling.Name)
			}
		}
		for _, target := range fn.Calls {
			if target == self {
				return nil, fmt.Errorf("function %q: calls cannot name the function's own app %q", fn.Name, target)
			}
			_, taken := calls.Names[target]
			if !taken && !slices.Contains(slices.Collect(maps.Values(calls.Names)), target) {
				calls.Names[target] = target
			}
		}
		for _, slug := range calls.Names {
			if !slices.Contains(calls.Targets, slug) {
				calls.Targets = append(calls.Targets, slug)
			}
		}
		slices.Sort(calls.Targets)
		if len(calls.Targets) > api.ServiceBindingTargetsMax {
			return nil, fmt.Errorf("function %q may call %d apps; the limit is %d", fn.Name, len(calls.Targets), api.ServiceBindingTargetsMax)
		}
		graph[fn.Name] = calls
	}
	return graph, nil
}

// BindingUpdate is the app PATCH that gives a function exactly the bindings
// it may call, under the declared policy; changed is false when the app
// already has them.
func (c FunctionCalls) BindingUpdate(app api.AppResponse) (api.UpdateAppRequest, bool) {
	current := make([]string, 0, len(app.ServiceBindings))
	for _, b := range app.ServiceBindings {
		current = append(current, b.Service)
	}
	slices.Sort(current)
	var update api.UpdateAppRequest
	changed := false
	if !slices.Equal(current, c.Targets) {
		targets := slices.Clone(c.Targets)
		if targets == nil {
			targets = []string{}
		}
		update.ServiceBindingTargets, changed = &targets, true
	}
	if app.ServiceBindingPolicy != api.ServiceBindingPolicyDeclared {
		policy := api.ServiceBindingPolicyDeclared
		update.ServiceBindingPolicy, changed = &policy, true
	}
	return update, changed
}

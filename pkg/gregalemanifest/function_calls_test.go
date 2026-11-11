package gregalemanifest

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/onebox-faas/faas/pkg/api"
)

// ADR-20261111150000: input/output schemas resolve inline, from a file in the
// function's root, or from config in code without renaming schema keywords.
func TestFunctionSchemasResolve(t *testing.T) {
	fsys := fstest.MapFS{
		"functions/users/handler.js":   {Data: []byte("export const config = { output: { type: 'object', additionalProperties: false } };\nexport async function handler() { return {}; }\n")},
		"functions/users/input.json":   {Data: []byte(`{"type":"object","properties":{"id":{"type":"string","minLength":1}},"required":["id"]}`)},
		"functions/orders/handler.py":  {Data: []byte("def handler(event, ctx):\n    return {}\n")},
		"functions/orders/schema.json": {Data: []byte(`not json`)},
	}
	m, _, err := ApplyInlineConfigFS(&Manifest{Functions: []FunctionSpec{
		{Name: "users", Root: "functions/users", Input: "input.json"},
		{Name: "orders", Root: "functions/orders"},
	}}, fsys)
	if err != nil {
		t.Fatal(err)
	}
	plans, err := m.PlanFunctionsFS(fsys, "shop")
	if err != nil {
		t.Fatal(err)
	}
	users := plans[0].Spec
	if !strings.Contains(string(users.InputSchema), `"minLength":1`) {
		t.Errorf("input schema = %s, want the file's schema", users.InputSchema)
	}
	var out map[string]any
	if err := json.Unmarshal(users.OutputSchema, &out); err != nil || out["additionalProperties"] != false {
		t.Errorf("output schema from code = %s (%v), want additionalProperties kept as written", users.OutputSchema, err)
	}
	if plans[1].Spec.InputSchema != nil {
		t.Errorf("a function without a schema has %s", plans[1].Spec.InputSchema)
	}

	for name, spec := range map[string]FunctionSpec{
		"not json":       {Name: "orders", Root: "functions/orders", Input: "schema.json"},
		"outside root":   {Name: "orders", Root: "functions/orders", Input: "../users/input.json"},
		"not .json":      {Name: "orders", Root: "functions/orders", Input: "handler.py"},
		"missing file":   {Name: "orders", Root: "functions/orders", Output: "nope.json"},
		"empty":          {Name: "orders", Root: "functions/orders", Input: map[string]any{}},
		"not an object":  {Name: "orders", Root: "functions/orders", Input: []any{"x"}},
		"calls self app": {Name: "orders", Root: "functions/orders", Calls: []string{"bad slug!"}},
	} {
		if _, err := (&Manifest{Functions: []FunctionSpec{spec}}).PlanFunctionsFS(fsys, "shop"); err == nil {
			t.Errorf("%s: plan accepted %+v", name, spec)
		}
	}
}

// Each function may call its siblings by name and its `calls:` apps by slug,
// through declared bindings to exactly those apps.
func TestFunctionCallGraph(t *testing.T) {
	functions := []FunctionSpec{
		{Name: "web", Calls: []string{"billing-api"}},
		{Name: "users"},
		{Name: "orders", Calls: []string{"shop-users"}},
	}
	slugOf := func(name string) string { return FunctionSlug("shop", name) }
	graph, err := FunctionCallGraph(functions, slugOf)
	if err != nil {
		t.Fatal(err)
	}
	web := graph["web"]
	if want := []string{"billing-api", "shop-orders", "shop-users"}; !slices.Equal(web.Targets, want) {
		t.Errorf("web targets = %v, want %v", web.Targets, want)
	}
	if web.Env() != `{"billing-api":"billing-api","orders":"shop-orders","users":"shop-users"}` {
		t.Errorf("web env = %s", web.Env())
	}
	// A calls entry that is already a sibling adds nothing.
	if orders := graph["orders"]; !slices.Equal(orders.Targets, []string{"shop-users", "shop-web"}) || len(orders.Names) != 2 {
		t.Errorf("orders = %+v, want its two siblings only", orders)
	}
	if _, err := FunctionCallGraph([]FunctionSpec{{Name: "web", Calls: []string{"shop-web"}}}, slugOf); err == nil {
		t.Error("a function calling its own app must be refused")
	}

	// The binding PATCH sets the targets and the declared policy, then nothing.
	update, changed := web.BindingUpdate(api.AppResponse{ServiceBindingPolicy: api.ServiceBindingPolicyAccount})
	if !changed || update.ServiceBindingTargets == nil || !slices.Equal(*update.ServiceBindingTargets, web.Targets) ||
		update.ServiceBindingPolicy == nil || *update.ServiceBindingPolicy != api.ServiceBindingPolicyDeclared {
		t.Fatalf("first update = %+v changed %v", update, changed)
	}
	bound := api.AppResponse{ServiceBindingPolicy: api.ServiceBindingPolicyDeclared, ServiceBindings: api.ServiceBindingsForTargets(web.Targets)}
	if _, changed := web.BindingUpdate(bound); changed {
		t.Error("an app with the right bindings must not change")
	}
	// A lone function binds nothing, and clears what it had.
	lone, _ := FunctionCallGraph([]FunctionSpec{{Name: "web"}}, slugOf)
	update, changed = lone["web"].BindingUpdate(bound)
	if !changed || update.ServiceBindingTargets == nil || len(*update.ServiceBindingTargets) != 0 {
		t.Errorf("lone update = %+v changed %v, want bindings cleared", update, changed)
	}
}

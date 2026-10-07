package gregalemanifest

import (
	"fmt"
	"path"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/operations"
)

// Operation schemas are JSON files relative to the selected source manifest.
// Deploy bundles their contents; serving never reads source files or URLs.
type Operation struct {
	Job                 string   `yaml:"job,omitempty" json:"job,omitempty"`
	App                 string   `yaml:"app,omitempty" toml:"app"`
	Name                string   `yaml:"name" toml:"name"`
	Workflow            string   `yaml:"workflow,omitempty" toml:"workflow"`
	TransactionReceipt  string   `yaml:"transaction_receipt,omitempty" toml:"transaction_receipt"`
	Method              string   `yaml:"method" toml:"method"`
	Path                string   `yaml:"path" toml:"path"`
	Owner               string   `yaml:"owner" toml:"owner"`
	InputSchema         string   `yaml:"input_schema" toml:"input_schema"`
	OutputSchema        string   `yaml:"output_schema" toml:"output_schema"`
	ProgressStages      []string `yaml:"progress_stages" toml:"progress_stages"`
	CompletionWebhookID string   `yaml:"completion_webhook_id,omitempty" toml:"completion_webhook_id"`
	Recovery            string   `yaml:"recovery,omitempty" toml:"recovery"`
}

func (o Operation) specification() api.OperationDefinitionSpec {
	return api.OperationDefinitionSpec{Name: o.Name, Job: o.Job, Workflow: o.Workflow, TransactionReceipt: o.TransactionReceipt, Method: o.Method, Path: o.Path, Owner: o.Owner,
		ProgressStages: append([]string(nil), o.ProgressStages...), CompletionWebhookID: o.CompletionWebhookID, Recovery: o.Recovery}
}

func (m *Manifest) validateOperations(plan api.Plan) error {
	limits := api.MustLimitsFor(plan).Operations
	if len(m.Operations) == 0 {
		return nil
	}
	if !limits.Allowed {
		return fmt.Errorf("operations exceed the %s plan definition limit", plan)
	}
	names, routes := map[string]bool{}, map[string]bool{}
	counts := map[string]int{}
	for i, o := range m.Operations {
		if o.App != "" && !isDNSSafeSlug(o.App) {
			return fmt.Errorf("operations[%d].app must be an app slug", i)
		}
		for _, file := range []string{o.InputSchema, o.OutputSchema} {
			if file == "" || len(file) > api.OperationPathMaxBytes || path.IsAbs(file) || path.Clean(file) != file || file == "." || strings.ContainsAny(file, "\\:\x00\r\n") || strings.HasPrefix(file, "../") {
				return fmt.Errorf("operations[%d]: schemas must be source-local relative JSON files", i)
			}
		}
		spec := o.specification()
		spec.InputSchema, spec.OutputSchema = []byte(`true`), []byte(`true`)
		contract, err := operations.Compile(spec, limits)
		if err != nil {
			return fmt.Errorf("operations[%d]: %w", i, err)
		}
		if o.Workflow != "" {
			found := false
			for _, workflow := range m.Workflows {
				if workflow.Name == o.Workflow {
					if err := operations.ValidateWorkflow(contract.Spec, workflow, plan); err != nil {
						return fmt.Errorf("operations[%d]: %w", i, err)
					}
					found = true
				}
			}
			if !found {
				return fmt.Errorf("operations[%d]: named workflow is missing from the manifest", i)
			}
		}
		name, route := o.App+"/"+o.Name, o.App+"/"+contract.Spec.Method+"/"+contract.Spec.Path
		if names[name] || routes[route] {
			return fmt.Errorf("operations[%d]: names and HTTP targets must be unique within an app", i)
		}
		names[name] = true
		routes[route] = true
		counts[o.App]++
	}
	for app, count := range counts {
		if app != "" {
			count += counts[""]
		}
		if count > limits.DefinitionsPerApp {
			return fmt.Errorf("operations exceed the %s plan definition limit for app %q", plan, app)
		}
	}
	return nil
}

// ResolveOperations uses an archive-owned reader. It resolves only declarations
// for this app, so another workload cannot supply its handler's schema bundle.
func (m *Manifest) ResolveOperations(slug string, plan api.Plan, read func(string, int) ([]byte, error)) error {
	m.ResolvedOperations = nil
	if err := m.validateOperations(plan); err != nil {
		return err
	}
	limits := api.MustLimitsFor(plan).Operations
	var resolved []api.OperationDefinitionSpec
	names, routes := map[string]bool{}, map[string]bool{}
	for _, o := range m.Operations {
		if o.App != "" && o.App != slug {
			continue
		}
		spec := o.specification()
		var err error
		spec.InputSchema, err = read(o.InputSchema, limits.SchemaBytes)
		if err != nil {
			return fmt.Errorf("operation %q input schema: %w", o.Name, err)
		}
		spec.OutputSchema, err = read(o.OutputSchema, limits.SchemaBytes)
		if err != nil {
			return fmt.Errorf("operation %q output schema: %w", o.Name, err)
		}
		contract, err := operations.Compile(spec, limits)
		if err != nil {
			return fmt.Errorf("operation %q: %w", o.Name, err)
		}
		route := contract.Spec.Method + " " + contract.Spec.Path
		if names[contract.Spec.Name] || routes[route] {
			return fmt.Errorf("operation names and HTTP targets must be unique in app %q", slug)
		}
		names[contract.Spec.Name] = true
		routes[route] = true
		resolved = append(resolved, contract.Spec)
	}
	m.ResolvedOperations = resolved
	return nil
}

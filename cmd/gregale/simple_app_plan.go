package main

import (
	"fmt"
	"io"
	"os"

	"github.com/onebox-faas/faas/pkg/hostingconfig"
	"github.com/onebox-faas/faas/pkg/simpleapp"
)

// resolveSimpleAppPlan is the local, side-effect-free planner behind
// `gregale deploy --plan`. It intentionally shares the same source detector
// and hosting override file as the real deploy path, while keeping the
// resulting contract independent of authentication and remote state.
func resolveSimpleAppPlan(sourceDir, slug, profile string, source simpleapp.SourceKind, explicitApp, explicitFunction bool) (simpleapp.Plan, error) {
	frameworkName := ""
	if source == simpleapp.SourceDirectory {
		resolved, _, _, err := resolveDeployShape(sourceDir, explicitFunction, explicitApp, true)
		if err != nil {
			return simpleapp.Plan{}, err
		}
		if resolved == shapeFunction {
			return simpleapp.Plan{}, fmt.Errorf("simple app deploys are HTTP applications; use the normal function path for handler-only source")
		}
		frameworkName = string(detectFramework(sourceDir))
	}
	port, health := 0, ""
	if sourceDir != "" {
		cfg, present, err := hostingconfig.Load(os.DirFS(sourceDir))
		if err != nil {
			return simpleapp.Plan{}, fmt.Errorf("read hosting defaults: %w", err)
		}
		if present {
			port, health = cfg.Port, cfg.Health
		}
	}
	return simpleapp.Resolve(simpleapp.Spec{
		Slug:       slug,
		Source:     source,
		Framework:  frameworkName,
		Profile:    profile,
		Port:       port,
		HealthPath: health,
	})
}

func renderSimpleAppPlan(w io.Writer, plan simpleapp.Plan, jsonMode bool) int {
	if jsonMode {
		return jsonOut(writeJSONTo(w, plan))
	}
	fmt.Fprintf(w, "Simple app plan for %s\n", plan.Slug)
	fmt.Fprintf(w, "  source:             %s\n", plan.Source)
	if plan.Framework != "" {
		fmt.Fprintf(w, "  framework:          %s\n", plan.Framework)
	}
	fmt.Fprintf(w, "  resources:          %s\n", plan.ResourceProfile)
	if plan.MemoryMB > 0 {
		fmt.Fprintf(w, "  memory/cpu:         %d MB / %d millicores\n", plan.MemoryMB, plan.CPUMillicores)
	}
	fmt.Fprintf(w, "  listener:           :%d %s\n", plan.Port, plan.HealthPath)
	fmt.Fprintf(w, "  execution:          %s\n", plan.ExecutionMode)
	fmt.Fprintf(w, "  scaling:            scale to zero\n")
	fmt.Fprintf(w, "  local storage:      %s\n", plan.LocalStorage)
	fmt.Fprintf(w, "  durable state:      %s (bind a database/object store)\n", plan.DurableState)
	fmt.Fprintln(w, "  defaults applied:")
	for _, applied := range plan.DefaultsApplied {
		fmt.Fprintf(w, "    - %s\n", applied)
	}
	fmt.Fprintln(w, "No remote state changed. Run `gregale deploy` to apply this plan.")
	return 0
}

package main

import (
	"fmt"
	"io"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
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

// applySimpleAppPlanToCreateRequest keeps the real deploy path aligned with
// `gregale deploy --plan`. The plan owns the customer-facing defaults; the
// deploy command only adds flags that are outside the simple stateless path.
func applySimpleAppPlanToCreateRequest(req *api.CreateAppRequest, plan simpleapp.Plan) {
	if req == nil {
		return
	}
	planned := plan.CreateRequest()
	req.Type = planned.Type
	req.ExecutionMode = planned.ExecutionMode
	req.HealthPath = planned.HealthPath
	if planned.ResourceProfile != "" {
		req.ResourceProfile = planned.ResourceProfile
	}
}

func renderSimpleAppPlan(w io.Writer, plan simpleapp.Plan, jsonMode bool) int {
	if jsonMode {
		return jsonOut(writeJSONTo(w, plan))
	}
	_, _ = fmt.Fprintf(w, "Simple app plan for %s\n", plan.Slug)
	_, _ = fmt.Fprintf(w, "  source:             %s\n", plan.Source)
	if plan.Framework != "" {
		_, _ = fmt.Fprintf(w, "  framework:          %s\n", plan.Framework)
	}
	_, _ = fmt.Fprintf(w, "  resources:          %s\n", plan.ResourceProfile)
	if plan.MemoryMB > 0 {
		_, _ = fmt.Fprintf(w, "  memory/cpu:         %d MB / %d millicores\n", plan.MemoryMB, plan.CPUMillicores)
	}
	_, _ = fmt.Fprintf(w, "  listener:           :%d %s\n", plan.Port, plan.HealthPath)
	_, _ = fmt.Fprintf(w, "  execution:          %s\n", plan.ExecutionMode)
	_, _ = fmt.Fprintf(w, "  scaling:            scale to zero\n")
	_, _ = fmt.Fprintf(w, "  local storage:      %s\n", plan.LocalStorage)
	_, _ = fmt.Fprintf(w, "  durable state:      %s (bind a database/object store)\n", plan.DurableState)
	_, _ = fmt.Fprintln(w, "  defaults applied:")
	for _, applied := range plan.DefaultsApplied {
		_, _ = fmt.Fprintf(w, "    - %s\n", applied)
	}
	_, _ = fmt.Fprintln(w, "No remote state changed. Run `gregale deploy` to apply this plan.")
	return 0
}

// renderSimpleAppDeploySummary keeps the human deploy path explicit about the
// effective stateless contract without printing any source or secret values.
func renderSimpleAppDeploySummary(w io.Writer, plan *simpleapp.Plan) {
	if plan == nil {
		return
	}
	_, _ = fmt.Fprintf(w, "Effective simple app plan: resources=%s, listener=:%d %s, execution=%s, scale=to-zero, local-storage=%s, durable-state=%s\n",
		plan.ResourceProfile, plan.Port, plan.HealthPath, plan.ExecutionMode, plan.LocalStorage, plan.DurableState)
}

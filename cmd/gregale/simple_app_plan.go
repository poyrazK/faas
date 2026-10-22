package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/frameworkprofile"
	"github.com/onebox-faas/faas/pkg/simpleapp"
)

// resolveSimpleAppPlan is the local, side-effect-free planner behind
// `gregale deploy --plan`. It intentionally shares the same source detector
// and hosting override file as the real deploy path, while keeping the
// resulting contract independent of authentication and remote state.
func resolveSimpleAppPlan(sourceDir, slug, profile string, source simpleapp.SourceKind, explicitApp, explicitFunction bool) (simpleapp.Plan, error) {
	frameworkName := ""
	port, health := 0, ""
	if source == simpleapp.SourceDirectory {
		resolved, _, _, err := resolveDeployShape(sourceDir, explicitFunction, explicitApp, true)
		if err != nil {
			return simpleapp.Plan{}, err
		}
		if resolved == shapeFunction {
			// production-us hunt #4: the old text ("use the normal function
			// path") named no command, for four of the built-in templates.
			return simpleapp.Plan{}, fmt.Errorf("--plan previews HTTP apps; this source is a function. Preview it with `gregale deploy --diff` or `gregale deploy --dry-run`, then deploy with `gregale deploy`")
		}
		inferred, err := frameworkprofile.AnalyzeDir(sourceDir)
		if err != nil {
			return simpleapp.Plan{}, fmt.Errorf("analyze deploy source: %w", err)
		}
		frameworkName = inferred.Framework
		port = inferred.Port
		health = inferred.HealthPath
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

func renderSimpleAppPlan(w io.Writer, plan simpleapp.Plan, jsonMode bool, previews ...*cliPreviewSummary) int {
	var preview *cliPreviewSummary
	if len(previews) > 0 {
		preview = previews[0]
	}
	if jsonMode {
		return jsonOut(writeJSONTo(w, struct {
			simpleapp.Plan
			Preview *cliPreviewSummary `json:"preview,omitempty"`
		}{plan, preview}))
	}
	renderCLIPreview(w, preview)
	// Mark each row that came from a default instead of repeating the
	// values in a separate list; JSON keeps defaults_applied.
	defaulted := map[string]bool{}
	for _, applied := range plan.DefaultsApplied {
		key, _, _ := strings.Cut(applied, "=")
		defaulted[key] = true
	}
	marked := map[string]bool{}
	mark := func(keys ...string) string {
		suffix := ""
		for _, key := range keys {
			marked[key] = true
			if defaulted[key] {
				suffix = " (default)"
			}
		}
		return suffix
	}
	_, _ = fmt.Fprintf(w, "Simple app plan for %s\n", plan.Slug)
	_, _ = fmt.Fprintf(w, "  source:             %s\n", plan.Source)
	if plan.Framework != "" {
		_, _ = fmt.Fprintf(w, "  framework:          %s\n", plan.Framework)
	}
	_, _ = fmt.Fprintf(w, "  resources:          %s%s\n", plan.ResourceProfile, mark("resource_profile"))
	if plan.MemoryMB > 0 {
		_, _ = fmt.Fprintf(w, "  memory/cpu:         %d MB / %d millicores\n", plan.MemoryMB, plan.CPUMillicores)
	}
	if plan.HealthPath == "" {
		_, _ = fmt.Fprintf(w, "  listener:           :%d · TCP listener readiness%s\n", plan.Port, mark("port", "readiness"))
	} else {
		_, _ = fmt.Fprintf(w, "  listener:           :%d %s%s\n", plan.Port, plan.HealthPath, mark("port", "health_path"))
	}
	_, _ = fmt.Fprintf(w, "  execution:          %s%s\n", plan.ExecutionMode, mark("execution_mode"))
	_, _ = fmt.Fprintf(w, "  scaling:            scale to zero%s\n", mark("scale_to_zero"))
	_, _ = fmt.Fprintf(w, "  local storage:      %s%s\n", plan.LocalStorage, mark("local_storage"))
	_, _ = fmt.Fprintf(w, "  durable state:      %s (bind a database/object store)%s\n", plan.DurableState, mark("durable_state"))
	for _, applied := range plan.DefaultsApplied {
		if key, _, _ := strings.Cut(applied, "="); !marked[key] {
			_, _ = fmt.Fprintf(w, "  default:            %s\n", applied)
		}
	}
	_, _ = fmt.Fprintln(w, "No remote state changed. This is local inference, not a saved apply plan; deploy rechecks the source and remote state.")
	return 0
}

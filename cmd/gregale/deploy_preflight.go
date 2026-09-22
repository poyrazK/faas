package main

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/simpleapp"
)

// deployPreflightSummary is the human-facing deploy intent rendered after
// local validation and source resolution, but before Gregale creates an app or
// uploads source. It deliberately contains no environment values, credentials,
// source contents, or local absolute paths.
type deployPreflightSummary struct {
	Slug            string
	Source          string
	LocalChanges    string
	Environment     string
	BuildPlan       *api.BuildPlan
	SimpleAppPlan   *simpleapp.Plan
	ResourceProfile string
	ExecutionMode   string
	Release         string
}

// deployPreflightSource describes the exact local source selection without
// exposing an absolute workstation path. A dirty Git checkout is called out
// separately because the default committed-HEAD behavior is otherwise easy to
// mistake for a working-tree deploy.
func deployPreflightSource(
	prov *zeroConfigProvenance,
	includeWorkingTree bool,
	dirtyFiles int,
	imageRef, templateName, archiveName, sourcePath string,
) (source, localChanges string) {
	switch {
	case imageRef != "":
		return "OCI image " + imageRef, ""
	case templateName != "":
		return "template " + templateName, ""
	case archiveName != "":
		return "archive " + filepath.Base(archiveName), ""
	case prov != nil:
		sha := shortDeploySHA(prov.SHA)
		if includeWorkingTree {
			source = "working tree"
			if sha != "" {
				source += " at " + sha
			}
			if dirtyFiles > 0 {
				localChanges = fmt.Sprintf("%d local %s included", dirtyFiles, pluralizeDeployChange(dirtyFiles))
			}
			return source, localChanges
		}
		source = "commit " + sha
		if dirtyFiles > 0 {
			localChanges = fmt.Sprintf("%d local %s excluded; use --worktree to include them", dirtyFiles, pluralizeDeployChange(dirtyFiles))
		}
		return source, localChanges
	case sourcePath != "":
		path := filepath.Clean(sourcePath)
		if filepath.IsAbs(path) {
			path = filepath.Base(path)
		}
		return "working tree from --path " + path, ""
	default:
		return "working tree", ""
	}
}

func shortDeploySHA(sha string) string {
	sha = strings.TrimSpace(sha)
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func pluralizeDeployChange(n int) string {
	if n == 1 {
		return "change"
	}
	return "changes"
}

func deployPreflightRelease(safe bool, canaryPreset string, trafficPercent int, rollbackOn5xx *bool) string {
	var release string
	switch {
	case safe:
		release = "safe · balanced 1% -> 10% -> 50% -> 100% · automatic rollback"
	case canaryPreset != "" && canaryPreset != "none":
		release = "canary · " + canaryPreset
	case trafficPercent >= 0:
		release = fmt.Sprintf("traffic split · %d%%", trafficPercent)
	default:
		release = "standard · 100% after readiness"
	}
	if rollbackOn5xx != nil && *rollbackOn5xx && !strings.Contains(release, "automatic rollback") {
		release += " · first-wake 5xx rollback"
	}
	return release
}

func renderDeployPreflight(w io.Writer, summary deployPreflightSummary) {
	_, _ = fmt.Fprintln(w, "Deployment plan:")
	_, _ = fmt.Fprintf(w, "  %-14s %s\n", "app:", summary.Slug)
	_, _ = fmt.Fprintf(w, "  %-14s %s\n", "source:", summary.Source)
	if summary.LocalChanges != "" {
		_, _ = fmt.Fprintf(w, "  %-14s %s\n", "local changes:", summary.LocalChanges)
	}

	if runtime := deployPreflightRuntime(summary.BuildPlan); runtime != "" {
		_, _ = fmt.Fprintf(w, "  %-14s %s\n", "runtime:", runtime)
	}
	if summary.BuildPlan != nil && summary.BuildPlan.Entrypoint != "" {
		_, _ = fmt.Fprintf(w, "  %-14s %s\n", "start:", summary.BuildPlan.Entrypoint)
	}
	if summary.BuildPlan != nil && summary.BuildPlan.Handler != "" {
		_, _ = fmt.Fprintf(w, "  %-14s %s\n", "handler:", summary.BuildPlan.Handler)
	}
	if listener := deployPreflightListener(summary.BuildPlan, summary.SimpleAppPlan); listener != "" {
		_, _ = fmt.Fprintf(w, "  %-14s %s\n", "listener:", listener)
	}
	if resources := deployPreflightResources(summary); resources != "" {
		_, _ = fmt.Fprintf(w, "  %-14s %s\n", "resources:", resources)
	}
	environment := summary.Environment
	if environment == "" {
		environment = "default"
	}
	_, _ = fmt.Fprintf(w, "  %-14s %s\n", "environment:", environment)
	if summary.Release != "" {
		_, _ = fmt.Fprintf(w, "  %-14s %s\n", "release:", summary.Release)
	}
	_, _ = fmt.Fprintln(w)
}

func deployPreflightRuntime(plan *api.BuildPlan) string {
	if plan == nil {
		return ""
	}
	class := plan.Class
	if class == "" {
		class = "app"
	}
	parts := []string{class}
	if plan.Framework != "" && plan.Framework != "unknown" {
		parts = append(parts, plan.Framework)
	}
	if plan.Version != "" {
		parts = append(parts, plan.Version)
	}
	if plan.Runtime != "" {
		parts = append(parts, plan.Runtime)
	}
	return strings.Join(parts, " · ")
}

func deployPreflightListener(build *api.BuildPlan, simple *simpleapp.Plan) string {
	port, health := 0, ""
	if build != nil {
		port, health = build.Port, build.HealthPath
	}
	if simple != nil {
		if port == 0 {
			port = simple.Port
		}
		if health == "" {
			health = simple.HealthPath
		}
	}
	if port == 0 && health == "" {
		return ""
	}
	if port == 0 {
		return "health GET " + health
	}
	if health == "" {
		return fmt.Sprintf(":%d", port)
	}
	return fmt.Sprintf(":%d · health GET %s", port, health)
}

func deployPreflightResources(summary deployPreflightSummary) string {
	profile := strings.TrimSpace(summary.ResourceProfile)
	execution := strings.TrimSpace(summary.ExecutionMode)
	if summary.SimpleAppPlan != nil {
		if profile == "" {
			profile = summary.SimpleAppPlan.ResourceProfile
		}
		if execution == "" {
			execution = summary.SimpleAppPlan.ExecutionMode
		}
	}
	if profile == "" {
		profile = "plan-default"
	}
	parts := []string{profile}
	if execution != "" {
		parts = append(parts, execution)
	}
	if summary.SimpleAppPlan != nil && summary.SimpleAppPlan.ScaleToZero {
		parts = append(parts, "scale-to-zero")
	}
	return strings.Join(parts, " · ")
}

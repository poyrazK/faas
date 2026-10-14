package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"regexp"

	"github.com/onebox-faas/faas/pkg/api"
)

// Configure only known non-secret task settings. Credential values continue to
// use sealed secrets; per-key writes preserve unrelated env and are retryable.
func cmdMCPTasksConfigure(args []string) int {
	fs := newFlagSet("mcp-tasks-configure", flag.ContinueOnError)
	app := fs.String("app", "", "reserved task application slug")
	role := fs.String("role", "", "web, worker or observer")
	namespace := fs.String("namespace", "", "shared stable queue namespace")
	worker := fs.String("worker-app", "", "metrics destination (required for worker/observer)")
	apply := fs.Bool("apply", false, "write the previewed non-secret task settings")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 || !api.ValidAppSlug(*app) || !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,254}$`).MatchString(*namespace) {
		return printErr("Invalid MCP task configuration", errors.New("--app and a 1-255 character --namespace (letters, digits, dot, underscore or hyphen) are required"))
	}
	if *role != "web" && *role != "worker" && *role != "observer" {
		return printErr("Invalid MCP task role", errors.New("--role must be web, worker or observer"))
	}
	if *role == "web" && *worker != "" || *role != "web" && (!api.ValidAppSlug(*worker) || (*role == "worker" && *worker != *app) || (*role == "observer" && *worker == *app)) {
		return printErr("Invalid MCP task metrics destination", errors.New("web does not publish metrics; worker must target itself and observer must target a separate --worker-app"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	current, err := client.GetApp(ctx, *app)
	if err != nil {
		return printErr("Could not get MCP task app", err)
	}
	if *role == "web" && isWorkerApp(current) || *role != "web" && !isWorkerApp(current) {
		return printErr("Invalid MCP task app mode", errors.New("web requires an HTTP app; worker and observer require worker-mode apps"))
	}
	type setting struct {
		Key     string `json:"key"`
		Value   string `json:"value"`
		Changed bool   `json:"changed"`
	}
	settings := []setting{{"MCP_TASKS_ROLE", *role, false}, {"MCP_TASK_NAMESPACE", *namespace, false}}
	if *role != "web" {
		settings = append(settings, setting{"MCP_TASKS_SCALING_APP_SLUG", *worker, false})
	}
	for i := range settings {
		if current.Manifest.EnvSecrets[settings[i].Key] != "" {
			return printErr("Conflicting MCP task secret binding", fmt.Errorf("%s must use a non-secret setting; remove its sealed binding before configuring", settings[i].Key))
		}
		settings[i].Changed = current.Manifest.Env[settings[i].Key] != settings[i].Value
		if settings[i].Changed && current.DeploymentAvailability == api.AppDeploymentAvailabilityLive {
			return printErr("MCP task configuration requires a reviewed rollout", errors.New("configure role, namespace and metric destination before the first deployment; this bootstrap command does not change live applications"))
		}
	}
	result := struct {
		AppSlug     string    `json:"app_slug"`
		Applied     bool      `json:"applied"`
		Settings    []setting `json:"settings"`
		WrittenKeys []string  `json:"written_keys"`
	}{*app, false, settings, []string{}}
	if *apply {
		for _, item := range settings {
			if !item.Changed {
				continue
			}
			if err := client.PutAppsSlugEnvKey(ctx, *app, item.Key, item.Value); err != nil {
				if jsonOutput {
					if code := jsonOut(writeJSON(result)); code != 0 {
						return code
					}
				} else {
					_, _ = fmt.Fprintf(osStdout, "Written keys: %v. Re-run the same command to resume.\n", result.WrittenKeys)
				}
				return printErr("Could not finish MCP task configuration", err)
			}
			result.WrittenKeys = append(result.WrittenKeys, item.Key)
		}
		result.Applied = true
	}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	_, _ = fmt.Fprintf(osStdout, "MCP task settings for %s (applied=%t)\n", *app, *apply)
	for _, item := range settings {
		_, _ = fmt.Fprintf(osStdout, "  %s=%s (changed=%t)\n", item.Key, item.Value, item.Changed)
	}
	if !*apply {
		_, _ = fmt.Fprintln(osStdout, "Re-run with --apply to write these non-secret settings before deploying.")
	}
	return 0
}

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/onebox-faas/faas/cmd/gregale/templates"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
)

type mcpInitOptions struct {
	Path, Runtime, AuthMode, Issuer, JWKSURL, Resource, Scopes, Name string
	Tasks                                                            bool
}

// Validate the complete access contract before creating customer files. This
// uses the same configuration validator as deploy; no network or credentials
// are needed to scaffold a private resource server.
func mcpInitConfig(options mcpInitOptions) (mcphosting.Config, error) {
	body, err := templates.FS.ReadFile("mcp-" + options.Runtime + "/" + mcphosting.ConfigFile)
	if err != nil {
		return mcphosting.Config{}, err
	}
	config, err := mcphosting.Decode(bytes.NewReader(body))
	if err != nil {
		return config, err
	}
	if options.Tasks {
		if !api.ValidAppSlug(options.Name) || !api.ValidAppSlug(options.Name+"-observer") {
			return config, errors.New("--tasks requires --name with a valid app slug prefix of at most 54 characters")
		}
		*config.Tasks.Enabled = true
	} else if options.Name != "" {
		return config, errors.New("--name requires --tasks")
	}
	config.Auth.Mode = options.AuthMode
	config.Auth.Issuer, config.Auth.JWKSURL, config.Auth.Resource = options.Issuer, options.JWKSURL, options.Resource
	if options.Scopes != "" {
		for _, scope := range strings.Split(options.Scopes, ",") {
			config.Auth.Scopes = append(config.Auth.Scopes, strings.TrimSpace(scope))
		}
	}
	if err := config.Validate(); err != nil {
		return config, err
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		return config, err
	}
	return mcphosting.Decode(bytes.NewReader(encoded))
}

func runMCPInit(options mcpInitOptions) int {
	config, err := mcpInitConfig(options)
	if err != nil {
		return printErr("Invalid MCP onboarding configuration", err)
	}
	dest, err := filepath.Abs(options.Path)
	if err != nil {
		return printErr("Could not resolve --path", err)
	}
	if info, err := os.Lstat(dest); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return printErr("Refusing MCP destination", errors.New("destination must not be a symlink"))
	}
	if err := checkDestEmpty(dest); err != nil {
		return printErr("Refusing MCP destination", err)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return printErr("Could not create parent directory", err)
	}
	stage, err := os.MkdirTemp(filepath.Dir(dest), ".gregale-mcp-init-")
	if err != nil {
		return printErr("Could not stage MCP starter", err)
	}
	defer func() { _ = os.RemoveAll(stage) }()
	roles := []string{""}
	if options.Tasks {
		roles = []string{"web", "worker", "observer"}
	}
	for _, role := range roles {
		root := filepath.Join(stage, role)
		if err := os.MkdirAll(root, 0o755); err != nil {
			return printErr("Could not stage MCP project", err)
		}
		if err := templates.Materialize("mcp-"+options.Runtime, root); err != nil {
			return printErr("Could not materialize MCP project", err)
		}
		body, err := json.MarshalIndent(config, "", "  ")
		if err != nil {
			return printErr("Could not encode MCP configuration", err)
		}
		if err := os.WriteFile(filepath.Join(root, mcphosting.ConfigFile), append(body, '\n'), 0o644); err != nil {
			return printErr("Could not write MCP configuration", err)
		}
		if role != "" {
			if err := writeMCPStackRole(root, role, options); err != nil {
				return printErr("Could not write MCP task role", err)
			}
		}
	}
	if options.Tasks {
		if err := os.WriteFile(filepath.Join(stage, "README.md"), []byte(mcpStackReadme(options)), 0o644); err != nil {
			return printErr("Could not write task setup guide", err)
		}
	}
	if err := os.Chmod(stage, 0o755); err != nil {
		return printErr("Could not publish MCP starter", err)
	}
	if err := os.Rename(stage, dest); err != nil {
		return printErr("Could not publish MCP starter", err)
	}
	next := []string{"Read README.md for dependency installation and local startup."}
	if options.Tasks {
		next = append(next, "Follow the root README.md to bind credentials, deploy the three roles and verify the observer before enabling scale-to-zero.")
	} else if options.AuthMode == "external-oauth" {
		next = append(next, "Obtain an access token from the configured provider with the resource audience and required scopes.", "Deploy with gregale mcp deploy --token-env MCP_ACCESS_TOKEN; the provider owns login, consent and refresh.")
	} else {
		next = append(next, "This starter uses public access. Configure external-oauth before exposing private tools or data.")
	}
	if jsonOutput {
		return jsonOut(writeJSON(struct {
			Template  string   `json:"template"`
			Path      string   `json:"path"`
			Status    string   `json:"status"`
			Runtime   string   `json:"runtime"`
			AuthMode  string   `json:"auth_mode"`
			Tasks     bool     `json:"tasks"`
			NextSteps []string `json:"next_steps"`
		}{"mcp-" + options.Runtime, dest, "created", options.Runtime, options.AuthMode, options.Tasks, next}))
	}
	PrintOK(osStdout, "Created %s MCP project at %s (access: %s)", options.Runtime, dest, options.AuthMode)
	for _, step := range next {
		_, _ = fmt.Fprintln(osStdout, step)
	}
	return 0
}

func writeMCPStackRole(root, role string, options mcpInitOptions) error {
	command := map[string]string{"node": "npm start", "go": "go run .", "python": "python server.py"}[options.Runtime]
	if options.Runtime == "node" && role != "web" {
		command = "npm run start:tasks-" + role
	}
	manifest := "hosting:\n  start: " + command + "\n"
	if role == "web" {
		manifest += "  port: 8080\n  health: /healthz\n"
	} else {
		manifest += "worker:\n  drain_timeout: 45s\n  stop_signal: SIGTERM\n  scale:\n    min: 1\n"
		if role == "worker" {
			manifest += "    max: 10\n    metric: custom\n    name: mcp_tasks_outstanding\n    target: 1\n"
		} else {
			manifest += "    max: 1\n"
		}
	}
	if err := os.WriteFile(filepath.Join(root, "gregale.yaml"), []byte(manifest), 0o644); err != nil {
		return err
	}
	env := "MCP_TASKS_ROLE=" + role + "\nMCP_TASK_NAMESPACE=" + options.Name + "\n"
	if role != "web" {
		env += "MCP_TASKS_SCALING_APP_SLUG=" + options.Name + "-worker\n"
	}
	return os.WriteFile(filepath.Join(root, "runtime.env"), []byte(env), 0o644)
}

func mcpStackReadme(options mcpInitOptions) string {
	var guide strings.Builder
	fmt.Fprintf(&guide, "# %s MCP task stack\n\nThis scaffold contains separate `web`, `worker` and `observer` projects in %s.\nAccess mode: `%s`. Each role has Tasks enabled and shares namespace `%s`.\nThe worker starts with minimum one replica; the observer always keeps one.\n\n", options.Name, options.Runtime, options.AuthMode, options.Name)
	guide.WriteString("Install dependencies in each project using its README. Install the same application handlers in web and worker before releasing changes. For local startup, export the settings in each role's `runtime.env` in that process.\n\nReserve the applications, then configure their non-secret runtime settings from this directory. Omit `--apply` to preview each configuration:\n\n```sh\n")
	for _, role := range []string{"web", "worker", "observer"} {
		fmt.Fprintf(&guide, "gregale deploy --create-only --path ./%s --name %s-%s", role, options.Name, role)
		if role != "web" {
			guide.WriteString(" --execution-mode worker")
		}
		fmt.Fprintf(&guide, "\ngregale mcp tasks configure --app %s-%s --role %s --namespace %s", options.Name, role, role, options.Name)
		if role != "web" {
			fmt.Fprintf(&guide, " --worker-app %s-worker", options.Name)
		}
		guide.WriteString(" --apply\n")
	}
	guide.WriteString("```\n\nPrepare PostgreSQL and its schema before deploying. Follow the runtime README for schema initialization: Node uses a separate migration binding; Go/Python currently initialize schema from the application runtime credential. Do not give the observer a write or migration credential.\n\nBind credentials through Gregale secrets, without putting values in `runtime.env` or source:\n\n| Role | Required secrets |\n| --- | --- |\n| web | DATABASE_URL, MCP_TASK_OWNER_KEY |\n| worker | DATABASE_URL, the same MCP_TASK_OWNER_KEY, MCP_TASKS_SCALING_TOKEN |\n| observer | DATABASE_URL with SELECT-only grants, MCP_TASKS_SCALING_TOKEN |\n\nWeb and worker use the same queue database and stable owner key of at least 32 bytes. The observer reads that database through a separate read-only role and does not need an owner key. Both metric tokens must be restricted to `metrics:write` for the worker application. Each starter README describes the credential and schema contracts.\n\nDeploy the worker and observer, then the web application:\n\n```sh\n")
	fmt.Fprintf(&guide, "gregale deploy --path ./worker --name %s-worker\ngregale deploy --path ./observer --name %s-observer\ngregale mcp deploy --path ./web --name %s-web", options.Name, options.Name, options.Name)
	if options.AuthMode == "external-oauth" {
		guide.WriteString(" --token-env MCP_ACCESS_TOKEN")
	}
	guide.WriteString("\n```\n\n")
	if options.AuthMode == "external-oauth" {
		guide.WriteString("Obtain `MCP_ACCESS_TOKEN` from your provider with the canonical resource audience and configured scopes before deploying web. Gregale validates bearer tokens; the provider owns login, consent, issuance and refresh.\n\n")
	} else {
		guide.WriteString("The web endpoint is public. Configure external OAuth before exposing private business data.\n\n")
	}
	guide.WriteString("Verify the observer and queue before requesting scale-to-zero:\n\n```sh\n")
	fmt.Fprintf(&guide, "gregale mcp tasks observer setup --app %s-observer --worker-app %s-worker --config ./observer/gregale-mcp.json\ngregale mcp tasks setup --app %s-worker --observer-app %s-observer --min 0\n# Apply only after the preview reports a ready observer and fresh heartbeat:\ngregale mcp tasks setup --app %s-worker --observer-app %s-observer --min 0 --apply\n", options.Name, options.Name, options.Name, options.Name, options.Name, options.Name)
	guide.WriteString("```\n\nThe observer and database remain running when workers reach zero. For later worker replacements, use `gregale mcp tasks release` with a separate candidate worker application and the previous worker apps in the release plan. Keep its journal outside the source directories. Handlers with external side effects must be idempotent by Task ID.\n\nMCP hosting remains preview until native lifecycle and real provider/client qualification evidence is recorded.\n")
	return guide.String()
}

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
// mcpInitTemplate is the starter a runtime uses. A Tasks stack starts from
// the runtime's -tasks starter; a plain server from the smaller one.
func mcpInitTemplate(options mcpInitOptions) string {
	if options.Tasks {
		return "mcp-" + options.Runtime + "-tasks"
	}
	return "mcp-" + options.Runtime
}

func mcpInitConfig(options mcpInitOptions) (mcphosting.Config, error) {
	body, err := templates.FS.ReadFile(mcpInitTemplate(options) + "/" + mcphosting.ConfigFile)
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
		if err := templates.Materialize(mcpInitTemplate(options), root); err != nil {
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
		next = append(next, "Obtain an access token from the configured provider with the resource audience and required scopes.", "Deploy with gregale mcp server deploy --token-env MCP_ACCESS_TOKEN; the provider owns login, consent and refresh.")
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
		}{mcpInitTemplate(options), dest, "created", options.Runtime, options.AuthMode, options.Tasks, next}))
	}
	PrintOK(osStdout, "Created %s MCP project at %s (access: %s)", options.Runtime, dest, options.AuthMode)
	for _, step := range next {
		_, _ = fmt.Fprintln(osStdout, step)
	}
	return 0
}

func writeMCPStackRole(root, role string, options mcpInitOptions) error {
	// A Go build runs its compiled binary; the runtime image has no Go
	// toolchain, so a Go role declares no start command (Hunt 32 H32-75).
	// MCP_TASKS_ROLE selects the role inside the one binary.
	command := map[string]string{"node": "npm start", "python": "python server.py"}[options.Runtime]
	if options.Runtime == "node" && role != "web" {
		command = "npm run start:tasks-" + role
	}
	hosting := ""
	if command != "" {
		hosting += "  start: " + command + "\n"
	}
	if role == "web" {
		hosting += "  port: 8080\n  health: /healthz\n"
	}
	manifest := ""
	if hosting != "" {
		manifest = "hosting:\n" + hosting
	}
	if migrate := mcpStackMigrationCommand(options.Runtime); role == "web" && migrate != "" {
		// The web release applies the Task schema with the migration
		// binding before any role serves; nothing else can reach the
		// database before the first deploy (MCP Tasks prod probe
		// 2026-11-22). It calls node/python or the compiled Go binary
		// directly: the shared runner bases ship no npm or Go toolchain.
		manifest += "release:\n  command: " + migrate + "\n"
	}
	if role != "web" {
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

// mcpStackMigrationCommand is the web role's release command that applies the
// Task schema, or "" for an unknown runtime. A Go source build ships only its
// compiled binary, which Railpack writes to /app/out (the path pkg/imaged
// reads); the binary answers `tasks-migrate apply` itself.
func mcpStackMigrationCommand(runtime string) string {
	return map[string]string{"node": "node tasks-migrate.js apply", "python": "python task_schema.py apply", "go": "/app/out tasks-migrate apply"}[runtime]
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
	guide.WriteString("```\n\n")
	mcpStackDatabaseGuide(&guide, options)
	guide.WriteString("Bind the other credentials through Gregale secrets, without putting values in `runtime.env` or source:\n\n| Role | Required secrets |\n| --- | --- |\n| web | MCP_TASK_OWNER_KEY |\n| worker | the same MCP_TASK_OWNER_KEY, MCP_TASKS_SCALING_TOKEN |\n| observer | MCP_TASKS_SCALING_TOKEN |\n\n")
	fmt.Fprintf(&guide, "```sh\ngregale secrets set --app %s-web MCP_TASK_OWNER_KEY=<32+ random bytes>\ngregale secrets set --app %s-worker MCP_TASK_OWNER_KEY=<the same value>\ngregale secrets set --app %s-worker MCP_TASKS_SCALING_TOKEN=<metrics:write key>\ngregale secrets set --app %s-observer MCP_TASKS_SCALING_TOKEN=<metrics:write key>\n```\n\n", options.Name, options.Name, options.Name, options.Name)
	guide.WriteString("Web and worker use the same stable owner key of at least 32 bytes. The observer does not need an owner key. Both metric tokens must be restricted to `metrics:write` for the worker application. Each starter README describes the credential and schema contracts.\n\n")
	if mcpStackMigrationCommand(options.Runtime) != "" {
		guide.WriteString("Deploy the web application first: its release command applies the Task schema before anything serves. Then deploy the worker and observer:\n\n```sh\n")
		fmt.Fprintf(&guide, "gregale mcp server deploy --path ./web --name %s-web", options.Name)
		if options.AuthMode == "external-oauth" {
			guide.WriteString(" --token-env MCP_ACCESS_TOKEN")
		}
		fmt.Fprintf(&guide, "\ngregale deploy --path ./worker --name %s-worker\ngregale deploy --path ./observer --name %s-observer", options.Name, options.Name)
	} else {
		guide.WriteString("Apply the Task schema as the runtime README describes, then deploy the worker and observer, then the web application:\n\n```sh\n")
		fmt.Fprintf(&guide, "gregale deploy --path ./worker --name %s-worker\ngregale deploy --path ./observer --name %s-observer\ngregale mcp deploy --path ./web --name %s-web", options.Name, options.Name, options.Name)
		if options.AuthMode == "external-oauth" {
			guide.WriteString(" --token-env MCP_ACCESS_TOKEN")
		}
	}
	guide.WriteString("\n```\n\n")
	if options.AuthMode == "external-oauth" {
		guide.WriteString("Obtain `MCP_ACCESS_TOKEN` from your provider with the canonical resource audience and configured scopes before deploying web. Gregale validates bearer tokens; the provider owns login, consent, issuance and refresh.\n\n")
	} else {
		guide.WriteString("The web endpoint is public. Configure external OAuth before exposing private business data.\n\n")
	}
	guide.WriteString("Verify the observer and queue before requesting scale-to-zero:\n\n```sh\n")
	fmt.Fprintf(&guide, "gregale mcp observer setup --app %s-observer --worker-app %s-worker --config ./observer/gregale-mcp.json\ngregale mcp tasks setup --app %s-worker --observer-app %s-observer --min 0\n# Apply only after the preview reports a ready observer and fresh heartbeat:\ngregale mcp tasks setup --app %s-worker --observer-app %s-observer --min 0 --apply\n", options.Name, options.Name, options.Name, options.Name, options.Name, options.Name)
	guide.WriteString("```\n\nThe observer and database remain running when workers reach zero. ")
	guide.WriteString("For later worker replacements, use `gregale mcp server tasks deploy` with a separate candidate worker application and the previous worker apps in the release plan. Keep its journal outside the source directories. ")
	if options.Runtime != "node" {
		// ADR-20261123160000: the CLI gates Go/Python releases itself.
		guide.WriteString("For Go/Python workers the CLI runs the release gate itself: it drains previous workers only after each replacement registers every handler version that unfinished Tasks need. Recovery, quarantine and retirement of a failed Go/Python release stay manual; see the runtime README. ")
	}
	guide.WriteString("Handlers with external side effects must be idempotent by Task ID.\n\nMCP hosting remains preview until native lifecycle and real provider/client qualification evidence is recorded.\n")
	return guide.String()
}

// mcpStackDatabaseGuide writes the managed PostgreSQL steps: one database, a
// read-write binding for web and worker, a schema-owner binding only on web
// for its release migration, and a read-only binding for the observer.
func mcpStackDatabaseGuide(guide *strings.Builder, options mcpInitOptions) {
	guide.WriteString("Create the queue database and bind it to each role. Web and worker share a read-write binding; only web gets the schema-owner binding, which its release command uses; the observer gets read-only access:\n\n```sh\n")
	fmt.Fprintf(guide, "gregale postgres create %s-db\n", options.Name)
	fmt.Fprintf(guide, "gregale postgres attach %s-db %s-web --env DATABASE_URL --access read_write\n", options.Name, options.Name)
	fmt.Fprintf(guide, "gregale postgres attach %s-db %s-web --env MCP_TASK_MIGRATION_DATABASE_URL --access migration\n", options.Name, options.Name)
	fmt.Fprintf(guide, "gregale postgres attach %s-db %s-worker --env DATABASE_URL --access read_write\n", options.Name, options.Name)
	fmt.Fprintf(guide, "gregale postgres attach %s-db %s-observer --env DATABASE_URL --access read_only\n```\n\n", options.Name, options.Name)
}

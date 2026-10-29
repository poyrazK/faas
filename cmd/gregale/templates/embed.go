// Package templates ships the built-in Gregale
// starter projects as an embed.FS so the CLI is a single static
// binary. Precedent: migrations/embed.go:13 — `//go:embed` pulls in
// the sibling subdirectories at compile time.
//
// Each template is a self-contained tar.gz-able directory with the
// minimum surface for a happy-path first deploy: a handler on :8080
// (matches guest-init's expected port, guest/init/main.go), a manifest
// file when needed, and a README that points at the next gregale CLI
// command the customer will run.
//
// Note on Go templates: the templates ship WITHOUT a go.mod because Go's
// //go:embed refuses to descend into a directory that contains one —
// it treats the file as a module boundary. Materialize writes one for HTTP
// apps (hello-go and mcp-go). function-go must remain marker-free so a later
// zero-config deploy sees handler.go as a function; the CLI adds its module
// file only to the upload archive.
package templates

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

	"github.com/onebox-faas/faas/pkg/api"
)

// FS holds the embedded starter projects. The root is the directory
// this file lives in, so subdirs are accessed by their template name.
//
// Include private Python module filenames, which directory embedding omits.
//
//go:embed hello-node hello-python hello-go cron-example function-node function-python function-go function-node24 function-python313 function-rust event-worker queue-worker s3-uploader object-storage-node slack-bot rest-api-postgres rest-api-postgres-python cron-worker webhook-receiver ai-chat secret-reload-node customer-platform mcp-node mcp-go mcp-python customer-operation-export customer-operation-job-export customer-operation-workflow-export data-api data-api-starter durable-entity-node data-api-teams data-api-support-console auth
//go:embed rest-api-postgres-python/runtime/*.py object-storage-python object-storage-python/runtime/*.py
var FS embed.FS

// agentsGuide is the AGENTS.md every materialized project receives, so a
// coding agent finds the deploy, inspect and rollback contract without
// reading the docs first. Its only field is the template name; README.md
// stays the source for each template's own setup.
//
//go:embed agents.md.tmpl
var agentsGuide string

var agentsGuideTemplate = template.Must(template.New("AGENTS.md").Parse(agentsGuide))

// GoToolchainVersion is the patched toolchain selected by Gregale's built-in
// Go starters. Unlike a customer-owned go.mod, these generated modules are
// platform-owned defaults and must not knowingly create images with HIGH
// stdlib findings, so they name the release Go function builds use.
const GoToolchainVersion = api.GoBuildToolchain

// Names is the canonical template list, kept here so the CLI can
// validate --template before touching the embed FS. The hello/function and
// stateless-contract scaffolds ship with `gregale deploy`; `gregale init`
// (commands_init.go) can materialize every template. Names must stay in
// lockstep with the //go:embed directive above — adding a template
// means a new entry in BOTH places.
var Names = []string{
	"hello-node",
	"hello-python",
	"hello-go",
	"cron-example",
	"function-node",
	"function-python",
	"function-go",
	"function-node24",
	"function-python313",
	"function-rust",
	"event-worker",
	"queue-worker",
	"s3-uploader",
	"object-storage-node",
	"object-storage-python",
	"slack-bot",
	"rest-api-postgres",
	"rest-api-postgres-python",
	"cron-worker",
	"webhook-receiver",
	"ai-chat",
	"secret-reload-node",
	"customer-platform",
	"mcp-node",
	"mcp-go",
	"mcp-python",
	"customer-operation-export",
	"customer-operation-job-export",
	"customer-operation-workflow-export",
	"data-api",
	"data-api-starter",
	"data-api-teams",
	"data-api-support-console",
	"durable-entity-node",
	"auth",
}

// unlisted templates stay embedded and materializable by name but are left
// out of customer discovery: `gregale init --list`, completions,
// unknown-template hints and the dashboard catalog. durable-entity-node
// scaffolds the operator-only durable entity preview (ADR-712); auth is
// application Auth, a dark launch (ADR-1190) that `gregale auth create`
// materializes.
var unlisted = map[string]bool{"durable-entity-node": true, "auth": true}

// ListedNames returns Names without unlisted templates, in catalog order.
func ListedNames() []string {
	names := make([]string, 0, len(Names))
	for _, name := range Names {
		if !unlisted[name] {
			names = append(names, name)
		}
	}
	return names
}

// generatedDotfiles are files a template needs whose names start with '.'.
// //go:embed omits such names from a directory pattern, so Materialize
// writes them instead.
var generatedDotfiles = map[string]map[string]string{
	"object-storage-python": {
		".gitignore":     "__pycache__/\n*.pyc\n.venv/\n.gregale/\n.env\n.env.*\n*.secrets\n",
		".gregaleignore": "/tests/\n**/__pycache__/\n*.pyc\n/.venv/\n/.gregale/\n.env\n.env.*\n*.secrets\n",
	},
	"rest-api-postgres-python": {
		".gregale/env.json": "{\"optional\":[\"MIGRATION_DATABASE_URL\"]}\n",
		".gitignore":        "__pycache__/\n*.pyc\n.venv/\n.gregale/\n.env\n.env.*\n*.secrets\n",
		".gregaleignore":    "/tests/\n**/__pycache__/\n*.pyc\n/.venv/\n/.gregale/\n.env\n.env.*\n*.secrets\n",
	},
	"data-api-support-console": {".gitignore": "node_modules/\n.gregale/\n.env\n.env.*\n", ".gregaleignore": "/test/\n/demo.mjs\n/.env\n/.env.*\n"},
	"data-api-teams": {
		".gregale/env.json": "{\"optional\":[\"MIGRATION_DATABASE_URL\"]}\n",
		".gitignore":        "node_modules/\nclient/dist/\nclient/client.js\nclient/vendor/\n.gregale/\n.gregale-tools/\n.env\n.env.*\n",
		".gregaleignore":    "/client/\n/tools/\n/workers/\n/ci/\n/.github/\n/.gregale/\n/.gregale-tools/\n/seed.sql\n/data-api.dev.json\n/data-api.requests.json\n/data-api-artifacts.json\n",
	},
	"object-storage-node": {
		".gitignore":     "node_modules/\n.gregale/\n.env\n.env.*\n*.secrets\n",
		".gregaleignore": "/test/\n/node_modules/\n/.gregale/\n.env\n.env.*\n*.secrets\n",
	},
	"durable-entity-node": {
		".gitignore":     "node_modules/\n.env\n.env.*\nlocal-checkpoint.json\n.session-frame.json\n",
		".gregaleignore": "/dev.mjs\n/tools/\n/test/\n/tsconfig.json\n/local-checkpoint.json\n/.session-frame.json\n/.env\n/.env.*\n",
	},
	"data-api-starter": {
		".gregale/env.json": "{\"optional\":[\"MIGRATION_DATABASE_URL\"]}\n",
		".gitignore":        "node_modules/\nclient/dist/\nclient/browser/client.js\n.gregale-tools/\n.gregale/\n.env\n.env.*\n",
		".gregaleignore":    "/client/\n/tools/\n/test/\n/ci/\n/.github/\n/.gregale-tools/\n/.gregale/\n/data-api-artifacts.json\n/data-api.dev.json\n/data-api.requests.json\n/data-api.requests.json.*.tmp\n",
	},
	// production-us hunt #4: tools/ holds owner-machine scripts that need an
	// account-owner FAAS_TOKEN. Without this file `gregale doctor` scanned
	// them and told users to store FAAS_TOKEN as an app secret.
	"customer-platform": {
		".gregale/env.json": "{\"optional\":[\"MIGRATION_DATABASE_URL\"]}\n",
		".gregaleignore": "# Owner-machine tools mint and rotate customer keys with an account-owner\n" +
			"# credential. They never run in the app (the Dockerfile copies app/\n" +
			"# only), so keep them out of the upload and out of doctor's env checks.\n" +
			"/tools/\n",
	},
	"mcp-node": {
		".gregale/env.json": "{\"optional\":[\"MCP_CATALOG_EXPECTED_SHA256\",\"MCP_TASK_MIGRATION_DATABASE_URL\",\"MCP_TASK_RELEASE_INPUT\",\"MCP_TELEMETRY_LOSS_ALERT\"]}\n",
	},
	"mcp-go": {
		".gregale/env.json": "{\"optional\":[\"GREGALE_API_URL\",\"MCP_BIND_ADDRESS\",\"MCP_CATALOG_EXPECTED_SHA256\",\"MCP_CONFIG_PATH\",\"MCP_DRAIN_TIMEOUT_SECONDS\",\"MCP_TASKS_ROLE\",\"MCP_TASKS_SCALING_APP_SLUG\",\"MCP_TASKS_SCALING_TOKEN\",\"MCP_TASK_MIGRATION_DATABASE_URL\",\"MCP_TASK_NAMESPACE\",\"MCP_TELEMETRY_LOSS_ALERT\"]}\n",
	},
	"mcp-python": {
		".gregale/env.json": "{\"optional\":[\"MCP_BIND_ADDRESS\",\"MCP_TELEMETRY_LOSS_ALERT\"]}\n",
	},
	"rest-api-postgres": {
		".gregale/env.json": "{\"optional\":[\"MIGRATION_DATABASE_URL\"]}\n",
	},
	"data-api": {
		".gregale/env.json": "{\"optional\":[\"DOCKER_CONTEXT\",\"MIGRATION_DATABASE_URL\"]}\n",
	},
}

// generatedTemplateFiles maps source files kept under non-Go suffixes in the
// repository to their output names. The MCP Go server itself is a real Go
// package after materialization, but its source must not be compiled as part
// of Gregale's repository module.
var generatedTemplateFiles = map[string]map[string]string{
	"mcp-go": {"main_test.go.tmpl": "main_test.go"}}

// mcpGoModuleRequirements mirrors the Go SDK's pruned module graph so a fresh
// mcp-go starter can run without a preliminary `go mod tidy`.
const mcpGoModuleRequirements = `
require github.com/modelcontextprotocol/go-sdk v1.8.0
require (
	github.com/jackc/pgx/v5 v5.11.0
	github.com/golang-jwt/jwt/v5 v5.3.1
	github.com/yosida95/uritemplate/v3 v3.0.2
)

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/google/jsonschema-go v0.4.3 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/segmentio/asm v1.1.3 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/stretchr/testify v1.12.1 // indirect
	golang.org/x/oauth2 v0.35.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/time v0.15.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
`

func goModuleContent(name string) []byte {
	content := fmt.Sprintf("module %s\n\ngo %s\n", name, GoToolchainVersion)
	if name == "mcp-go" {
		content += mcpGoModuleRequirements
	}
	return []byte(content)
}

// Workflow sources remain visible to go:embed. Scaffold their conventional
// hidden destinations when init materializes the project.
var generatedTemplateCopies = map[string]map[string]string{
	"data-api-teams": {".github/workflows/data-api-client.yml": "ci/client.yml"},
	"data-api-starter": {
		".github/workflows/data-api-client.yml":  "ci/client.yml",
		".github/workflows/data-api-preview.yml": "ci/preview.yml",
	},
}

// Share the runtime catalog implementation with the owner-side permission tool.
var sharedTemplateCopies = map[string]map[string]string{
	"data-api-support-console": {"public/csv.mjs": "data-api/csv-export.mjs"},
	"data-api-teams": {
		"Dockerfile":                             "data-api-starter/Dockerfile",
		"Procfile":                               "data-api-starter/Procfile",
		"gregale.yaml":                           "data-api-starter/gregale.yaml",
		"server.mjs":                             "data-api-starter/server.mjs",
		"migrations/migrate.mjs":                 "data-api-starter/migrations/migrate.mjs",
		"migrations/release.mjs":                 "data-api-starter/migrations/release.mjs",
		"migrations/rpc-permissions.mjs":         "data-api-starter/migrations/rpc-permissions.mjs",
		"migrations/rpc-runtime/types.mjs":       "data-api/types.mjs",
		"migrations/rpc-runtime/type-render.mjs": "data-api/type-render.mjs",
		"migrations/rpc-runtime/config.mjs":      "data-api/config.mjs",
		"tools/artifacts.mjs":                    "data-api-starter/tools/artifacts.mjs",
		"tools/artifact-lib.mjs":                 "data-api-starter/tools/artifact-lib.mjs",
	},
	"data-api-starter": {
		"migrations/rpc-runtime/types.mjs":       "data-api/types.mjs",
		"migrations/rpc-runtime/type-render.mjs": "data-api/type-render.mjs",
		"migrations/rpc-runtime/config.mjs":      "data-api/config.mjs",
	},
}

// Exists reports whether name is a known template.
func Exists(name string) bool {
	if !NameIsValid(name) {
		return false
	}
	for _, n := range Names {
		if n == name {
			return true
		}
	}
	return false
}

// sub returns an fs.FS rooted at the named template. Returns an error
// if the name isn't a known template (so callers don't have to
// re-validate). Internal — public callers use Materialize/TarGz.
func sub(name string) (fs.FS, error) {
	if !Exists(name) {
		return nil, fmt.Errorf("unknown template %q", name)
	}
	return fs.Sub(FS, name)
}

// Materialize copies the template named name into dest. dest should be
// an empty directory (the CLI uses os.MkdirTemp; tests use
// MaterializeForTest). Delegates to os.CopyFS — no dotfile filtering,
// no header munging. TarGz is the path that wraps the result for the
// accept-time tarball scan; TarGz is the one that skips dotfiles.
func Materialize(name, dest string) error {
	subFS, err := sub(name)
	if err != nil {
		return err
	}
	if err := os.CopyFS(dest, subFS); err != nil {
		return err
	}
	if err := withholdMonetization(name, dest); err != nil {
		return err
	}
	for source, output := range generatedTemplateFiles[name] {
		if err := os.Rename(filepath.Join(dest, source), filepath.Join(dest, output)); err != nil {
			return err
		}
	}
	for file, content := range generatedDotfiles[name] {
		target := filepath.Join(dest, file)
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		if _, err := os.Stat(target); os.IsNotExist(err) {
			if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
				return err
			}
		}
	}
	for target, source := range generatedTemplateCopies[name] {
		content, err := fs.ReadFile(subFS, source)
		if err != nil {
			return err
		}
		path := filepath.Join(dest, target)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, content, 0o644); err != nil {
			return err
		}
	}
	for target, source := range sharedTemplateCopies[name] {
		content, err := fs.ReadFile(FS, source)
		if err != nil {
			return err
		}
		targetPath := filepath.Join(dest, target)
		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(targetPath, content, 0o644); err != nil {
			return err
		}
	}
	// HTTP Go apps need a module marker. function-go stays marker-free so a
	// later zero-config deploy detects handler.go as a function; the packer
	// adds its build module to the upload archive.
	if name == "hello-go" || name == "mcp-go" {
		modPath := filepath.Join(dest, "go.mod")
		if _, err := os.Stat(modPath); os.IsNotExist(err) {
			if err := os.WriteFile(modPath, goModuleContent(name), 0o644); err != nil {
				return err
			}
		}
	}
	return writeAgentsGuide(name, dest)
}

// writeAgentsGuide adds AGENTS.md unless the destination already has one.
func writeAgentsGuide(name, dest string) error {
	target := filepath.Join(dest, "AGENTS.md")
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		return err
	}
	var guide bytes.Buffer
	if err := agentsGuideTemplate.Execute(&guide, struct{ Template string }{name}); err != nil {
		return err
	}
	return os.WriteFile(target, guide.Bytes(), 0o644)
}

// TarGz materializes the template and writes a tar.gz to dest. The
// top-level directory in the archive is `name/` so `tar -xzf` produces
// a single `name/` folder instead of dumping files into cwd.
func TarGz(name, dest string) error {
	if !Exists(name) {
		return fmt.Errorf("unknown template %q", name)
	}
	rootFS, err := sub(name)
	if err != nil {
		return err
	}
	f, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("could not create %s: %w", dest, err)
	}
	defer func() { _ = f.Close() }()
	gz := gzip.NewWriter(f)
	defer func() { _ = gz.Close() }()
	tw := tar.NewWriter(gz)
	defer func() { _ = tw.Close() }()

	var entries []string
	if err := fs.WalkDir(rootFS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == "." || strings.HasPrefix(path.Base(p), ".") {
			if d.IsDir() && p != "." {
				return fs.SkipDir
			}
			return nil
		}
		entries = append(entries, p)
		return nil
	}); err != nil {
		return err
	}
	sort.Strings(entries)
	for _, p := range entries {
		info, err := fs.Stat(rootFS, p)
		if err != nil {
			return err
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		archivePath := p
		if output, ok := generatedTemplateFiles[name][p]; ok {
			archivePath = output
		}
		hdr.Name = name + "/" + filepath.ToSlash(archivePath)
		if !info.Mode().IsRegular() {
			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}
			continue
		}
		raw, err := fs.ReadFile(rootFS, p)
		if err != nil {
			return err
		}
		content, ok := shipped(name, p, raw)
		if !ok {
			continue
		}
		hdr.Size = int64(len(content))
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if _, err := tw.Write(content); err != nil {
			return err
		}
	}
	var shared []string
	for target := range sharedTemplateCopies[name] {
		shared = append(shared, target)
	}
	sort.Strings(shared)
	for _, target := range shared {
		content, err := fs.ReadFile(FS, sharedTemplateCopies[name][target])
		if err != nil {
			return err
		}
		hdr := &tar.Header{Name: name + "/" + target, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if _, err := tw.Write(content); err != nil {
			return err
		}
	}
	if name == "hello-go" || name == "function-go" || name == "mcp-go" {
		modContent := goModuleContent(name)
		hdr := &tar.Header{
			Name:     name + "/go.mod",
			Mode:     0o644,
			Size:     int64(len(modContent)),
			Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if _, err := tw.Write(modContent); err != nil {
			return err
		}
	}
	return nil
}

// MaterializeForTest copies a single template into a fresh tempdir and
// returns the dir + a cleanup func. Tests use this to assert the embed
// FS round-trips through `tar -xzf`.
func MaterializeForTest(name string) (string, func(), error) {
	dir, err := os.MkdirTemp("", "gregale-tpl-test-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	if err := Materialize(name, dir); err != nil {
		cleanup()
		return "", nil, err
	}
	return dir, cleanup, nil
}

// NameIsValid returns true if name contains only characters safe for
// a tar header prefix. Defensive — Names is hard-coded today but the
// CLI's --template is user input.
func NameIsValid(name string) bool {
	if name == "" || strings.ContainsAny(name, "/\\") || name == "." || name == ".." {
		return false
	}
	return true
}

// CategoryFor returns the customer-facing group label for a template
// name. Used by `gregale init --list` (commands_init.go) to bucket the
// templates by what the customer is trying to do, not by alphabetical
// order. Recognised categories:
//
//	"hello"              — first-touch smoke tests (3)
//	"function"           — generic runtimes the customer customises (7)
//	"stateless-contract" — managed-service scaffolds that BYO credentials (7)
//	"event-driven"       — internal event and queue worker starters (2)
//	"ai"                 — LLM-facing scaffolds that BYO keys (1)
//	"mcp"                — stateless MCP server starters (3)
//	""                   — unknown / not in Names
//
// The grouping mirrors the template sections in the public docs site:
// hello/function are the "just run it" tier, stateless-contract
// scaffolds are the "BYO managed service" tier, ai is the LLM tier,
// and mcp contains the protocol-specific language starters.
// Adding a template means a new entry in BOTH Names AND this switch.
func CategoryFor(name string) string {
	switch name {
	case "hello-node", "hello-python", "hello-go":
		return "hello"
	case "function-node", "function-python", "function-go", "function-node24", "function-python313", "function-rust", "cron-example":
		return "function"
	case "event-worker", "queue-worker":
		return "event-driven"
	case "s3-uploader", "object-storage-node", "object-storage-python", "slack-bot", "rest-api-postgres", "rest-api-postgres-python", "cron-worker", "webhook-receiver", "secret-reload-node", "customer-platform", "data-api", "data-api-starter", "data-api-teams", "data-api-support-console", "auth":
		return "stateless-contract"
	case "ai-chat":
		return "ai"
	case "mcp-node", "mcp-go", "mcp-python":
		return "mcp"
	case "durable-entity-node":
		return "durable-entities"
	case "customer-operation-export", "customer-operation-job-export", "customer-operation-workflow-export":
		return "operations"
	}
	return ""
}

// summaries are the one-line descriptions shown by `gregale init --list` and
// the interactive template picker. Every listed template needs one.
var summaries = map[string]string{
	"hello-node":                         "Express HTTP app with a JSON greeting and health check",
	"hello-python":                       "Flask HTTP app served by gunicorn",
	"hello-go":                           "Go HTTP app using only the standard library",
	"cron-example":                       "Handler for a scheduled request",
	"function-node":                      "Node.js 22 function handler",
	"function-python":                    "Python 3.12 function handler",
	"function-go":                        "Go function handler",
	"function-node24":                    "Node.js 24 function handler",
	"function-python313":                 "Python 3.13 function handler",
	"function-rust":                      "Rust function on the binary runtime",
	"event-worker":                       "Node.js worker for Gregale's event router",
	"queue-worker":                       "Node.js consumer for a durable queue",
	"s3-uploader":                        "Node.js upload endpoint for S3-compatible storage",
	"object-storage-node":                "Node.js file service on a managed bucket",
	"object-storage-python":              "FastAPI file service on a managed bucket",
	"slack-bot":                          "Node.js Slack Events API handler",
	"rest-api-postgres":                  "Node.js REST API on PostgreSQL with migrations",
	"rest-api-postgres-python":           "FastAPI REST API on PostgreSQL with migrations",
	"cron-worker":                        "Node.js function run on a schedule by Upstash QStash",
	"webhook-receiver":                   "Node.js endpoint for inbound webhooks",
	"ai-chat":                            "Node.js chat endpoint for OpenAI or Anthropic",
	"secret-reload-node":                 "Node.js and PostgreSQL app that reloads rotated secrets",
	"customer-platform":                  "Node.js and PostgreSQL starter for multi-tenant apps",
	"mcp-node":                           "Node.js MCP server with sample tools and prompts",
	"mcp-go":                             "Go MCP server with sample tools",
	"mcp-python":                         "Python MCP server with sample tools",
	"customer-operation-export":          "HTTP operation with a CSV export and progress page",
	"customer-operation-job-export":      "Job that exports a CSV with progress and download",
	"customer-operation-workflow-export": "Workflow export with progress and cancellation",
	"data-api":                           "Schema-generated PostgreSQL API with JWT auth",
	"data-api-starter":                   "Data API app with migrations and row-level security",
	"data-api-teams":                     "Data API app with team membership and roles",
	"data-api-support-console":           "Read-only support console on the Data API",
}

// Summary returns a template's one-line description, or "" when it has none.
func Summary(name string) string {
	return summaries[name]
}

// CategoryLabels name each category in the interactive template picker.
var CategoryLabels = map[string]string{
	"hello":              "HTTP app",
	"function":           "Function",
	"event-driven":       "Event or queue worker",
	"stateless-contract": "App with a database or external service",
	"ai":                 "AI chat endpoint",
	"mcp":                "MCP server",
	"operations":         "Long-running customer operation",
	"durable-entities":   "Durable entities",
}

// CategoryOrder is the canonical order in which `gregale init --list`
// prints categories. Pins against accidental reorders in CategoryFor.
var CategoryOrder = []string{"hello", "function", "event-driven", "stateless-contract", "ai", "mcp", "operations", "durable-entities"}

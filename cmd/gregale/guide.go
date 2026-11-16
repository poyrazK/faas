// adr: 20261112223000

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// guidePage is one task an agent or a person looks up with `gregale guide
// <task>` (ADR-20261112223000 §4). Every command in Steps is resolved
// against the CLI manifest by TestEveryGuideCommandIsARealCommand, and a
// rendered page stays under guidePageMaxBytes, so a page never names a
// command that does not exist and never costs an agent a help dump.
type guidePage struct {
	Task    string      `json:"task"`
	Title   string      `json:"title"`
	Summary string      `json:"summary"`
	Steps   []guideStep `json:"steps"`
	Notes   []string    `json:"notes,omitempty"`
}

// guideStep is one thing to do and the command that does it.
type guideStep struct {
	Do      string `json:"do"`
	Command string `json:"command,omitempty"`
}

const guidePageMaxBytes = 4096

var guidePages = []guidePage{
	{
		Task: "deploy", Title: "Deploy an app",
		Summary: "Preview first: the dry run says what Gregale will do with this source and which features apply.",
		Steps: []guideStep{
			{Do: "Check the source locally (listener, env, Dockerfile)", Command: "gregale doctor"},
			{Do: "Preview the deploy; read its \"For this repo\" section", Command: "gregale deploy --dry-run"},
			{Do: "Deploy", Command: "gregale deploy --yes"},
			{Do: "Deploy a published image instead of source", Command: "gregale deploy --image <image>@sha256:<digest> --name <app>"},
			{Do: "Verify a response from the accepted deployment (including private apps)", Command: "gregale release verify <deployment> --app <app> --path /"},
			{Do: "Optionally allow anonymous public requests", Command: "gregale app update <app> --no-require-authn"},
			{Do: "Check serving health", Command: "gregale app health <app>"},
		},
		Notes: []string{
			"A Compose file or several services: see `gregale guide compose-project`.",
			"A private URL can return 401 without credentials after a successful deploy. release verify checks the exact deployment using owner access; it does not verify anonymous public access.",
			"A source build must answer HTTP GET /healthz on its port; see `gregale guide health-checks`.",
		},
	},
	{
		Task: "compose-project", Title: "Deploy a Compose project",
		Summary: "Each Compose service becomes an app in one project. Gregale does not run Docker's network: service names do not resolve.",
		Steps: []guideStep{
			{Do: "Preview every service, its URL, health check and managed databases", Command: "gregale deploy --project --dry-run"},
			{Do: "Deploy the project", Command: "gregale deploy --project --yes"},
			{Do: "Deploy some services only", Command: "gregale deploy --project --only <service>"},
			{Do: "Open a public service to everyone", Command: "gregale app update <app> --no-require-authn"},
			{Do: "Restart, health-check or change every app of the project at once", Command: "gregale app restart --project <project> --fresh --wait"},
			{Do: "Follow every service's logs", Command: "gregale logs --project <project> --follow"},
			{Do: "Run a command in a fresh instance of a service", Command: "gregale app exec <app> -- <command>"},
		},
		Notes: []string{
			"Plain environment: values are imported only with `x-gregale-env-import: true` at the top of the Compose file; a URL that names a sibling (http://api:8080) is rewritten to its Gregale address without it. Do not copy them into Dockerfiles.",
			"A gRPC service declares `x-gregale-app-protocol: grpc` on its Compose service.",
			"Too many files: each service's .dockerignore applies inside its build context; list directories no service builds from (k8s, docs) in .gregaleignore.",
			"Code that dials a Compose name (redis, db) must read $GREGALE_SERVICE_<NAME>_HOST (raw TCP) or $GREGALE_SERVICE_<NAME>_URL (HTTP); the caller lists the service in depends_on. The dry run names each place.",
			"A postgres service becomes a managed database bound as DATABASE_URL. Gregale does not run other datastores (redis, mysql, mongo): an app parks to a snapshot and keeps no data. Point the code at a hosted one (Upstash, PlanetScale, Atlas) with an env URL.",
			"A build target or command for development (target: dev, nodemon, --reload) is deployed as written.",
			"A gRPC address (PRODUCT_SERVICE_ADDR: products:3550) is rewritten to products.svc.gregale:10081. Set x-gregale-streaming and x-gregale-wake-ahead on a service instead of later app commands.",
		},
	},
	{
		Task: "health-checks", Title: "Health checks and readiness",
		Summary: "An instance takes traffic once it passes readiness, and is restarted when it keeps failing liveness.",
		Steps: []guideStep{
			{Do: "Check an app's serving health and readiness", Command: "gregale app health <app>"},
			{Do: "Use another health path (or set hosting.health in gregale.yaml)", Command: "gregale app <app> --health-path <path>"},
			{Do: "Deploy a stateless TCP server (a broker, memcached) from its image", Command: "gregale deploy --image <image>@sha256:<digest> --name <app>"},
			{Do: "Read why a deploy did not become ready", Command: "gregale logs <app> --deployment <deployment>"},
		},
		Notes: []string{
			"A Compose healthcheck: runs inside the instance and replaces the default probe.",
			"A source build without one is probed with HTTP GET /healthz on its serving port; a 2xx is ready.",
			"A Dockerfile HEALTHCHECK adds a command check; it does not replace the HTTP probe.",
			"A directly deployed image is ready once its port accepts connections.",
			"A message broker or memcached never answers HTTP: built from source it fails with app_startup_timeout; deploy its image.",
			"Datastores (Redis, PostgreSQL, MySQL, MongoDB) are not run at all; see `gregale guide compose-project`.",
		},
	},
	{
		Task: "private-services", Title: "Private services and TCP ports",
		Summary: "Apps in your account reach each other privately; a port declared :internal never gets a public listener.",
		Steps: []guideStep{
			{Do: "Give an app no public URL", Command: "gregale app <app> --visibility internal"},
			{Do: "Declare a private TCP port (a broker, memcached)", Command: "gregale app <app> --ports <name>=tcp:<port>:internal"},
			{Do: "Declare a public raw TCP port, then give it a listener", Command: "gregale app <app> --ports <name>=tcp:<port>"},
			{Do: "Create the public listener for that port", Command: "gregale app <app> tcp add --name <name> --guest-port <port>"},
			{Do: "Reach a private port from your machine", Command: "gregale app <app> port-forward <local-port>:<host>:<port>"},
		},
		Notes: []string{
			"A caller that lists the service in depends_on gets $GREGALE_SERVICE_<NAME>_HOST (raw TCP, any declared port) and $GREGALE_SERVICE_<NAME>_URL (HTTP).",
			"http://<name>.internal:10081 resolves only for a service the caller lists in depends_on, or a sibling in its project under the project policy; apps deployed one by one call each other at http://<app>.svc.gregale:10081.",
			"A private service parks when idle and wakes on a connection; keep one running with `gregale app <app> --min 1`.",
		},
	},
	{
		Task: "secrets-and-env", Title: "Environment variables and secrets",
		Summary: "Set values before the first request; the dry run lists keys the code reads that will have no value.",
		Steps: []guideStep{
			{Do: "Set a value (stored sealed; never shown again)", Command: "gregale env set --app <app> KEY=<value>"},
			{Do: "Push a local .env file", Command: "gregale env push --app <app> -f .env"},
			{Do: "List an app's key names", Command: "gregale env list --app <app>"},
			{Do: "Set one value for every service of a project environment", Command: "gregale environment secrets set <project> <environment> KEY=<value>"},
		},
		Notes: []string{"Never write secret values into source, Dockerfiles or gregale.yaml."},
	},
	{
		Task: "rollouts", Title: "Releases, rollouts and rollback",
		Summary: "Each deploy is a release; its app's release policy decides how it reaches traffic.",
		Steps: []guideStep{
			{Do: "Release new deploys at once (test environments)", Command: "gregale app update <app> --release-policy immediate"},
			{Do: "See the current traffic split and canary step", Command: "gregale release traffic status"},
			{Do: "Abort a canary: all traffic back to the earlier release", Command: "gregale release traffic rollback <app>"},
			{Do: "List an app's releases", Command: "gregale release list --app <app>"},
		},
		Notes: []string{"The default policy is a health-gated canary when a release is already live."},
	},
	{
		Task: "debug-a-failure", Title: "Debug a failed deploy or a failing app",
		Summary: "Every failure names its code and a fix; start from the fix.",
		Steps: []guideStep{
			{Do: "Read an app's logs", Command: "gregale logs <app>"},
			{Do: "Read a failed deployment's build and startup logs", Command: "gregale logs <app> --deployment <deployment>"},
			{Do: "See an app's state, park reason and recommendations", Command: "gregale inspect <app>"},
			{Do: "Run a command against the live deployment", Command: "gregale app exec <app> -- <command>"},
			{Do: "Check the source locally", Command: "gregale doctor"},
		},
		Notes: []string{
			"app_startup_timeout: the app did not answer readiness in 30 s; see `gregale guide health-checks`.",
			"liveness_exhausted: the app kept failing its health check while serving and was parked.",
			"app_runtime_oom: raise memory with `gregale app <app> --ram <MB>`.",
		},
	},
}

func lookupGuidePage(task string) (guidePage, bool) {
	for _, page := range guidePages {
		if page.Task == task {
			return page, true
		}
	}
	return guidePage{}, false
}

// renderGuidePage is the text form of one page, also written to
// docs/agent-guide/<task>.md.
func renderGuidePage(page guidePage) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n%s\n\n", page.Title, page.Summary)
	for _, step := range page.Steps {
		fmt.Fprintf(&b, "- %s:\n", step.Do)
		if step.Command != "" {
			fmt.Fprintf(&b, "    %s\n", step.Command)
		}
	}
	if len(page.Notes) > 0 {
		b.WriteString("\nNotes:\n")
		for _, note := range page.Notes {
			fmt.Fprintf(&b, "- %s\n", note)
		}
	}
	return b.String()
}

func renderGuideIndex() string {
	var b strings.Builder
	b.WriteString("Gregale task guide. Run `gregale guide <task>` for one page.\n\n")
	for _, page := range guidePages {
		fmt.Fprintf(&b, "  %-18s %s\n", page.Task, page.Title)
	}
	return b.String()
}

// cmdGuide prints the task index or one task page. It needs no login.
func cmdGuide(args []string) int {
	fs := newFlagSet("guide", flag.ContinueOnError, osStderr)
	asJSON := fs.Bool("json", false, "print the pages as JSON")
	if err := parseInterspersed(fs, args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	if *asJSON {
		jsonOutput = true
	}
	if fs.NArg() > 1 {
		PrintUsage(osStderr, "usage: gregale guide [<task>] [--json]", "guide")
		return 1
	}
	if fs.NArg() == 0 {
		if jsonOutput {
			return jsonOut(writeJSON(map[string]any{"tasks": guidePages}))
		}
		_, _ = io.WriteString(osStdout, renderGuideIndex())
		return 0
	}
	page, ok := lookupGuidePage(fs.Arg(0))
	if !ok {
		tasks := make([]string, 0, len(guidePages))
		for _, p := range guidePages {
			tasks = append(tasks, p.Task)
		}
		sort.Strings(tasks)
		return printErr("Unknown guide task", fmt.Errorf("no guide task %q; tasks: %s", fs.Arg(0), strings.Join(tasks, ", ")))
	}
	if jsonOutput {
		return jsonOut(writeJSON(page))
	}
	_, _ = io.WriteString(osStdout, renderGuidePage(page))
	return 0
}

// agentMarkers are the environment variables coding agents set in the
// shells they run, confirmed against each agent's release (2026-11-14:
// Claude Code, Gemini CLI, Codex CLI, Cursor agent).
var agentMarkers = []string{"CLAUDECODE", "GEMINI_CLI", "CODEX_THREAD_ID", "CODEX_SANDBOX", "CURSOR_AGENT"}

const agentHint = "Agent: run `gregale guide` for a task index; `gregale deploy --dry-run` lists the features this repo uses."

// runByAgent reports a known agent marker, unless GREGALE_NO_AGENT_HINT=1.
// It only adds a pointer on stderr (ADR-20261112223000 §5): no default,
// prompt or successful command's stdout changes.
func runByAgent() bool {
	if os.Getenv("GREGALE_NO_AGENT_HINT") == "1" {
		return false
	}
	for _, marker := range agentMarkers {
		if os.Getenv(marker) != "" {
			return true
		}
	}
	return false
}

// printAgentHint writes the pointer once per process.
func printAgentHint(w io.Writer) {
	if agentHintPrinted || jsonOutput || !runByAgent() {
		return
	}
	agentHintPrinted = true
	_, _ = fmt.Fprintln(w, agentHint)
}

var agentHintPrinted bool

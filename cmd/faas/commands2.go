package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/onebox-faas/faas/cmd/faas/templates"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/browser"
)

// Subcommand names — lifted to constants so goconst stops flagging the
// repeated "list"/"add"/"rm" string literals in the dispatch tables below.
const (
	subList    = "list"
	subAdd     = "add"
	subUpdate  = "update"
	subRm      = "rm"
	subSummary = "summary"

	statusPending  = "pending"
	statusVerified = "verified"

	// service names reused across cmdConnect + the usage hint
	// (commands2.go) so goconst stops flagging them.
	svcGithub = "github"

	// appSlugFallback is the placeholder slug sanitizeSlugForURL
	// returns when the input is entirely garbage (all stripped).
	// Lifted out of the literal so goconst stops flagging the
	// repeated "app" string across cmd/faas (main.go dispatch,
	// subcommand FlagSet names, fallback slug).
	appSlugFallback = "app"

	// Lifted out so goconst stops flagging the repeated "status"
	// string across the run() dispatch (main.go), account
	// subcommand dispatch (commands4.go), the FlagSet name
	// (commands5.go), and the SSE stream-decoder struct tag.
	statusLiteral = "status"

	// Lifted out so goconst stops flagging the repeated "live"
	// string across the SSE decoder, the recovery poll, and the
	// terminalExitForDeployment branch.
	statusLive = "live"

	// cmdNames reused across the run() dispatch table (main.go) so
	// goconst stops flagging the repeated "apps" / "status" / etc.
	// literals. Tests intentionally keep the literal form.
	dispatchApps = "apps"

	// Plural deployments list (mirrors dispatchApps shape). User runs
	// `faas deployments` to list; pagination flags live on the handler.
	dispatchDeployments = "deployments"

	// Singular deployment-get. Lifted so the dispatch literal stays
	// constant-named (goconst); the constant does NOT route through
	// appSlugFallback — the dispatch table places it before the
	// "app" case so `faas deployment <id>` is never read as an app slug.
	dispatchDeployment = "deployment"
)

// cmdApp implements `faas app <slug>` (GET /v1/apps/{slug}), `faas app <slug>
// --ram N`, and `faas apps -q <slug>` (DELETE) — UX §2.4.
//
// `--min N` (Pro/Scale only) sets the per-app cold-wake floor
// (ux_spec §6.5): N instances stay RUNNING regardless of idle
// timeout. 0 = scale to zero (default).
//
// UpdateAppRequest uses *int pointers on the wire so callers can distinguish
// "unset" from "explicit zero." We use fs.Visit to detect which flags the
// user actually passed — comparing flag values to sentinels (0 / -1) would
// silently drop valid inputs like `--ram 0` or `--idle -1`.
func cmdApp(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: faas app <slug> [--ram N] [--max-concurrency N] [--idle SEC] [--min N] [--autoscale-target-rps N] [--autoscale-target-cpu-pct N]", "apps")
		return 1
	}
	slug := args[0]
	fs := flag.NewFlagSet("app", flag.ContinueOnError)
	ram := fs.Int("ram", 0, "update RAM (MB)")
	conc := fs.Int("max-concurrency", 0, "update max concurrent requests")
	idle := fs.Int("idle", 0, "update idle timeout (seconds)")
	// --min sets the per-app cold-wake floor (ux_spec §6.5).
	// Pro/Scale only — the API rejects Hobby/Free with 403
	// plan_min_instances_not_allowed, which surfaces here as an
	// "Update failed" error with the API's problem code.
	min := fs.Int("min", 0, "min instances kept warm (Pro/Scale only; 0 = scale to zero)")
	// Issue #169 / #172: per-app reactive scale-up trigger targets.
	// --autoscale-target-rps sets the per-instance RPS target
	// (Hobby/Pro/Scale; Free rejects with 403). --autoscale-target-cpu-pct
	// sets the per-instance CPU% target in [1,100] (Pro/Scale only).
	// Both use Visit-flag detection so the explicit "0 = disable" form
	// round-trips correctly (a sentinel compare would swallow a valid
	// --autoscale-target-rps=0).
	rps := fs.Int("autoscale-target-rps", 0, "per-instance RPS target for reactive scale-up (Hobby+/0 = disable)")
	cpu := fs.Int("autoscale-target-cpu-pct", 0, "per-instance CPU%% target for reactive scale-up (Pro+ only; 1-100; 0 = disable)")
	if err := fs.Parse(args[1:]); err != nil {
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()

	// Build the partial-update payload from explicit flags only.
	explicit := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { explicit[f.Name] = true })
	var req api.UpdateAppRequest
	if explicit["ram"] {
		v := *ram
		req.RAMMB = &v
	}
	if explicit["max-concurrency"] {
		v := *conc
		req.MaxConcurrency = &v
	}
	if explicit["idle"] {
		v := *idle
		req.IdleTimeoutS = &v
	}
	if explicit["min"] {
		v := *min
		req.MinInstances = &v
	}
	if explicit["autoscale-target-rps"] {
		v := *rps
		req.AutoscaleTargetRPS = &v
	}
	if explicit["autoscale-target-cpu-pct"] {
		v := *cpu
		req.AutoscaleTargetCPUPct = &v
	}

	if req.RAMMB == nil && req.MaxConcurrency == nil && req.IdleTimeoutS == nil && req.MinInstances == nil &&
		req.AutoscaleTargetRPS == nil && req.AutoscaleTargetCPUPct == nil {
		a, err := client.GetApp(ctx, slug)
		if err != nil {
			return printErr("Could not fetch app", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(a))
		}
		fmt.Printf("%-30s %s\n", "slug:", a.Slug)
		fmt.Printf("%-30s %s\n", "url:", a.URL)
		fmt.Printf("%-30s %d MB\n", "ram:", a.RAMMB)
		fmt.Printf("%-30s %d\n", "max concurrency:", a.MaxConcurrency)
		fmt.Printf("%-30s %ds\n", "idle timeout:", a.IdleTimeoutS)
		// ux_spec §6.5: show the cold-wake floor alongside the
		// other knobs so the customer sees why an instance is
		// always resident. "scale to zero" rendering for 0 is
		// more legible than a bare "0".
		if a.MinInstances == 0 {
			fmt.Printf("%-30s %s\n", "min instances:", "scale to zero")
		} else {
			fmt.Printf("%-30s %d\n", "min instances:", a.MinInstances)
		}
		// ADR-031 + ADR-032: surface the per-app outbound CIDR
		// allowlist in the text-mode `faas app <slug>` output so a
		// customer can verify their PATCH round-tripped without
		// dropping into --json. Print only when non-empty — empty
		// is the Free/Hobby default and "no row" output is
		// misleading.
		if len(a.EgressAllowlist) > 0 {
			fmt.Printf("%-30s %s\n", "egress allowlist:",
				strings.Join(a.EgressAllowlist, ", "))
		}
		// Issue #169 / #172: surface the per-app autoscale targets
		// so a customer can verify their PATCH round-tripped. 0
		// renders as "disabled" — same UX rule as min instances.
		if a.AutoscaleTargetRPS > 0 {
			fmt.Printf("%-30s %d\n", "autoscale target rps:", a.AutoscaleTargetRPS)
		} else {
			fmt.Printf("%-30s %s\n", "autoscale target rps:", "disabled")
		}
		if a.AutoscaleTargetCPUPct > 0 {
			fmt.Printf("%-30s %d%%\n", "autoscale target cpu:", a.AutoscaleTargetCPUPct)
		} else {
			fmt.Printf("%-30s %s\n", "autoscale target cpu:", "disabled")
		}
		fmt.Printf("%-30s %s\n", "status:", a.Status)
		return 0
	}

	updated, err := client.UpdateApp(ctx, slug, req)
	if err != nil {
		return printErr("Update failed", err)
	}
	PrintOK(osStdout, "Updated")
	if explicit["min"] && *min > 0 {
		// Silent on Whoami failure: the customer just updated an app
		// successfully, don't surface an unrelated auth/network blip
		// (e.g. mid-rotation token) as a missing cost line. The echo
		// is a transparency affordance, not a guarantee.
		if acct, err := client.Whoami(ctx); err == nil {
			printResidentCostEcho(api.Plan(acct.Plan), updated.RAMMB, *min)
		}
	}
	return 0
}

// cmdAppsRm implements `faas apps -q <slug>` (DELETE /v1/apps/{slug}).
func cmdAppsRm(args []string) int {
	fs := flag.NewFlagSet("apps-rm", flag.ContinueOnError)
	quiet := fs.Bool("q", false, "suppress confirmation prompt")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: faas apps -q <slug>", "apps")
		return 1
	}
	slug := fs.Arg(0)
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if !*quiet {
		fmt.Fprintf(os.Stderr, "Delete %q and all its deployments? [y/N] ", slug)
		var ans string
		_, _ = fmt.Scanln(&ans)
		if strings.ToLower(strings.TrimSpace(ans)) != "y" {
			fmt.Println("aborted")
			return 1
		}
	}
	if err := client.DeleteApp(context.Background(), slug); err != nil {
		return printErr("Delete failed", err)
	}
	PrintOK(osStdout, "Deleted %s", slug)
	return 0
}

// cmdDeployTarball extends cmdDeploy with `--tarball`, `--runtime`, `--handler`,
// `--dockerfile`. Image digest stays as the default input. `--repo owner/name`
// opens the dashboard's repo-picker page (slice 8) where the customer binds
// the repo + branch; subsequent pushes auto-deploy via the webhook path.
//
// `--template NAME` materializes one of the six embedded starter
// projects (cmd/faas/templates/embed.go) into a tempdir, tars+gzip it,
// and proceeds down the --tarball path. For the function templates
// (function-node, function-python) we force --runtime / --handler so
// the runner wires up correctly without the customer having to know
// those flags.
func cmdDeployTarball(args []string) int {
	fs := flag.NewFlagSet("deploy", flag.ContinueOnError)
	image := fs.String("image", "", "digest-pinned image reference")
	tarball := fs.String("tarball", "", "path to source archive (tar.gz)")
	repo := fs.String("repo", "", "GitHub repo to bind and deploy (owner/name)")
	templateName := fs.String("template", "", "start from an embedded template (hello-node|hello-python|hello-go|cron-example|function-node|function-python|function-go)")
	dockerfile := fs.Bool("dockerfile", false, "build with the supplied Dockerfile inside --tarball")
	runtime := fs.String("runtime", "", "function runtime (node22|python312|go124|go124-alpine)")
	handler := fs.String("handler", "", "function handler (e.g. handler.handler)")
	name := fs.String("name", "", "app name (default: current directory)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	slug := *name
	if slug == "" {
		slug = deriveName()
	}

	// --repo is the M7.5 git-deploy path. It opens the dashboard's
	// repo-picker page where the customer finishes the bind (the
	// picker needs the GitHub install token, which only the
	// dashboard can use). Once bound, pushes auto-deploy.
	if *repo != "" {
		if err := validateRepoSlug(*repo); err != nil {
			return printErr("Invalid --repo", err)
		}
		return cmdDeployRepo(slug, *repo)
	}

	// --template materializes an embedded starter project. For function
	// templates we force the runtime + handler so the customer doesn't
	// need to know the convention; for app templates we leave them
	// unset so imaged auto-detects.
	if *templateName != "" {
		if !templates.Exists(*templateName) {
			PrintFail(os.Stderr, "unknown --template %q (known: %s)",
				*templateName, strings.Join(templates.Names, ", "))
			return 1
		}
		switch *templateName {
		case "function-node":
			*runtime = "node22"
			*handler = "handler.handler"
		case "function-python":
			*runtime = "python312"
			*handler = "handler.handler"
		case "function-go":
			// The customer's handler is a static Go binary; the
			// --handler wire field is vestigial for go124 (the imaged
			// manifest locks the entrypoint to /app/handler). We set
			// a non-empty value so the multipart writer doesn't skip
			// the field, but the value is never read by the runtime.
			*runtime = "go124"
			*handler = "handler.go"
		}
		f, err := os.CreateTemp("", "faas-template-*.tar.gz")
		if err != nil {
			return printErr("Could not create temp file", err)
		}
		tmpPath := f.Name()
		_ = f.Close()
		defer func() { _ = os.Remove(tmpPath) }()
		if err := templates.TarGz(*templateName, tmpPath); err != nil {
			return printErr("Could not materialize template", err)
		}
		*tarball = tmpPath
		// --image would have precedence over --template by accident;
		// reject it explicitly so the customer isn't surprised by
		// which one wins.
		if *image != "" {
			PrintFail(os.Stderr, "--template and --image are mutually exclusive")
			return 1
		}
	}

	// Zero-config (issue #313): no source flag → pack the current directory
	// and deploy it, mirroring the --template branch above (temp tarball →
	// set *tarball → fall through to the shared upload path). This is an
	// App-type deploy (runtime/handler stay unset) so the server's builder
	// detects the framework; we only detect locally for the UX line and to
	// set --dockerfile when a Dockerfile is at the root. --repo returned
	// earlier and --template already set *tarball, so reaching here with both
	// *image and *tarball empty means the customer gave no source at all.
	if *image == "" && *tarball == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return printErr("Could not read current directory", err)
		}
		path, fw, n, err := autoPackCwd(cwd)
		if err != nil {
			return printErr("Could not pack current directory", err)
		}
		defer func() { _ = os.Remove(path) }()
		if fw == fwUnknown {
			return printErr("No deployable source found in "+filepath.Base(cwd),
				errors.New("expected package.json, requirements.txt / pyproject.toml / "+
					"Pipfile / setup.py, go.mod, or Dockerfile at the project root — "+
					"or pass --image, --tarball, --template, or --repo"))
		}
		if fw == fwDocker {
			*dockerfile = true
		}
		PrintProgress(os.Stderr, "detected %s project in %s, packing %d file(s)",
			fw, filepath.Base(cwd), n)
		*tarball = path
	}

	client, err := authedClientWithDeployTimeout(5 * time.Minute)
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()

	if _, err := client.CreateApp(ctx, api.CreateAppRequest{Slug: slug}); err != nil {
		var ae *APIError
		if !errors.As(err, &ae) || ae.Problem.Status != 409 {
			return printErr("Could not create app", err)
		}
	}

	if *tarball != "" {
		dep, err := DeployTarball(client, ctx, slug, *tarball, *runtime, *handler, *dockerfile)
		if err != nil {
			return printErr("Bad --tarball", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(dep))
		}
		return streamDeployLogs(client, dep)
	}
	dep, err := client.Deploy(ctx, slug, api.CreateDeploymentRequest{Image: *image})
	if err != nil {
		return printErr("Deploy failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(dep))
	}
	return streamDeployLogs(client, dep)
}

// cmdDeployRepo binds (app, repo) via the dashboard and opens the
// browser to the repo-picker page. The actual binding write goes
// through the dashboard's /dashboard/account/connect-github flow
// (slice 8) because that's where the OAuth install token lives.
func cmdDeployRepo(slug, repoFullName string) int {
	if _, err := authedClient(); err != nil {
		return printErr("Not logged in", err)
	}
	target := dashboardRepoPickerURL(apiBase(), slug, repoFullName)
	// Mid-string `→` here is semantic (binding repo X to app Y), not the
	// §3.2 "in-progress" symbol. The leading glyph still routes through
	// the gate so the prefix `→ ` strips under NO_COLOR / non-TTY.
	PrintProgress(osStdout, "Opening %s to bind %s → %s", target, repoFullName, slug)
	if err := browser.Open(target); err != nil {
		// Fall back to a clickable copy if the opener is missing
		// (sandboxed CI, no DISPLAY, etc.).
		PrintFail(os.Stderr, "Could not open browser: %v", err)
		fmt.Fprintf(os.Stderr, "  Open this URL manually:\n  %s\n", target)
		return 0
	}
	return 0
}

// cmdRollback, cmdPark, cmdWake implement their eponymous routes.
func cmdRollback(args []string) int {
	if len(args) != 1 {
		PrintUsage(os.Stderr, "usage: faas rollback <slug>", "rollback")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	dep, err := client.Rollback(context.Background(), args[0])
	if err != nil {
		return printErr("Rollback failed", err)
	}
	PrintOK(osStdout, "Rolled back to %s (%s)", dep.ID, dep.Status)
	return 0
}

func cmdPark(args []string) int {
	if len(args) != 1 {
		PrintUsage(os.Stderr, "usage: faas park <slug>", "park-wake")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if err := client.Park(context.Background(), args[0]); err != nil {
		return printErr("Park failed", err)
	}
	PrintOK(osStdout, "Parked (cold)")
	return 0
}

func cmdWake(args []string) int {
	if len(args) != 1 {
		PrintUsage(os.Stderr, "usage: faas wake <slug>", "park-wake")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if err := client.Wake(context.Background(), args[0]); err != nil {
		return printErr("Wake failed", err)
	}
	PrintOK(osStdout, "Waking…")
	return 0
}

// cmdDomains dispatches list/add/rm. Adding prints the TXT record the
// customer must publish for verification (spec §7).
func cmdDomains(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: faas domains <list|add|rm> [args]", "domains")
		return 1
	}
	switch args[0] {
	case subList:
		client, err := authedClient()
		if err != nil {
			return printErr("Not logged in", err)
		}
		out, err := client.ListDomains(context.Background())
		if err != nil {
			return printErr("Request failed", err)
		}
		if jsonOutput {
			return jsonOut(writeNDJSON(out))
		}
		for _, d := range out {
			verified := statusPending
			if d.Verified {
				verified = statusVerified
			}
			fmt.Printf("%-40s %-12s %s\n", d.Domain, verified, d.AppID)
		}
		return 0
	case subAdd:
		fs := flag.NewFlagSet("domains-add", flag.ContinueOnError)
		domain := fs.String("domain", "", "domain to attach (required)")
		slug := fs.String("app", "", "app slug to attach to (required)")
		if err := fs.Parse(args[1:]); err != nil {
			return 1
		}
		if *domain == "" || *slug == "" {
			PrintUsage(os.Stderr, "usage: faas domains add --domain <d> --app <slug>", "domains")
			return 1
		}
		client, err := authedClient()
		if err != nil {
			return printErr("Not logged in", err)
		}
		d, err := client.CreateDomain(context.Background(), api.CreateCustomDomainRequest{Domain: *domain, AppID: *slug})
		if err != nil {
			return printErr("Could not add domain", err)
		}
		fmt.Printf("Add this TXT record to your DNS:\n\n")
		fmt.Printf("  _faas-verify.%s  TXT  %s\n\n", d.Domain, d.ChallengeToken)
		fmt.Printf("Then run 'faas domains list' to see when verification completes.\n")
		return 0
	case subRm:
		if len(args) != 2 {
			PrintUsage(os.Stderr, "usage: faas domains rm <domain>", "domains")
			return 1
		}
		client, err := authedClient()
		if err != nil {
			return printErr("Not logged in", err)
		}
		if err := client.DeleteDomain(context.Background(), args[1]); err != nil {
			return printErr("Delete failed", err)
		}
		PrintOK(osStdout, "Removed")
		return 0
	}
	fmt.Fprintf(os.Stderr, "unknown domains subcommand %q\n", args[0])
	return 1
}

// cmdCrons: list/add/update/rm.
func cmdCrons(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: faas crons <list|add|update|rm> [args]", "crons")
		return 1
	}
	switch args[0] {
	case subList:
		fs := flag.NewFlagSet("crons-list", flag.ContinueOnError)
		slug := fs.String("app", "", "app slug (required)")
		if err := fs.Parse(args[1:]); err != nil {
			return 1
		}
		if *slug == "" {
			PrintUsage(os.Stderr, "usage: faas crons list --app <slug>", "crons")
			return 1
		}
		client, err := authedClient()
		if err != nil {
			return printErr("Not logged in", err)
		}
		out, err := client.ListCrons(context.Background(), *slug)
		if err != nil {
			return printErr("Request failed", err)
		}
		if jsonOutput {
			return jsonOut(writeNDJSON(out))
		}
		for _, c := range out {
			state := "enabled"
			if !c.Enabled {
				state = "disabled"
			}
			fmt.Printf("%-30s %-15s %s\n", c.Schedule, state, c.Path)
		}
		return 0
	case subAdd:
		fs := flag.NewFlagSet("crons-add", flag.ContinueOnError)
		slug := fs.String("app", "", "app slug (required)")
		schedule := fs.String("schedule", "", "cron expression (required)")
		path := fs.String("path", "/", "request path")
		if err := fs.Parse(args[1:]); err != nil {
			return 1
		}
		if *slug == "" || *schedule == "" {
			PrintUsage(os.Stderr, "usage: faas crons add --app <slug> --schedule '*/5 * * * *' [--path /]", "crons")
			return 1
		}
		client, err := authedClient()
		if err != nil {
			return printErr("Not logged in", err)
		}
		c, err := client.CreateCron(context.Background(), *slug, api.CreateCronRequest{
			AppID: *slug, Schedule: *schedule, Path: *path, Enabled: boolPtr(true),
		})
		if err != nil {
			return printErr("Create failed", err)
		}
		PrintOK(osStdout, "Cron scheduled: %s %s", c.Schedule, c.Path)
		return 0
	case subUpdate:
		return cmdCronsUpdate(args[1:])
	case subRm:
		if len(args) != 2 {
			PrintUsage(os.Stderr, "usage: faas crons rm <id>", "crons")
			return 1
		}
		client, err := authedClient()
		if err != nil {
			return printErr("Not logged in", err)
		}
		if err := client.DeleteCron(context.Background(), args[1]); err != nil {
			return printErr("Delete failed", err)
		}
		PrintOK(osStdout, "Removed")
		return 0
	}
	fmt.Fprintf(os.Stderr, "unknown crons subcommand %q\n", args[0])
	return 1
}

// cronIDPattern is the 32-hex shape used by the API for cron ids
// (CronResponse.ID, the path segment of /v1/crons/{id}). Mirrors
// deploymentIDPattern — same 32-hex convention across the platform.
var cronIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{32}$`)

// renderCronState writes the human multi-line state block for one
// cron. Routes through io.Writer so tests can capture the body via
// the osStdout seam (same pattern as renderDeploymentRow in
// commands_deployments.go). Widths assume short schedule / path /
// boolean fields; no id-style left-pad because cron ids aren't
// shown on the update block — the "Updated cron <id>" line above
// already names the row.
func renderCronState(w io.Writer, c api.CronResponse) {
	_, _ = fmt.Fprintf(w, "  %-10s %s\n", "schedule:", c.Schedule)
	_, _ = fmt.Fprintf(w, "  %-10s %s\n", "path:", c.Path)
	_, _ = fmt.Fprintf(w, "  %-10s %s\n", "enabled:", strconv.FormatBool(c.Enabled))
}

// cmdCronsUpdate implements `faas crons update <id> [--schedule EXPR]
// [--path PATH] [--enable|--disable]`. Partial-update semantics:
// every flag is optional, but at least one patch field must be set
// (the server happily no-ops an empty body and emits a cron-changed
// notification — a footgun we'd rather catch at the CLI). Uses
// fs.Visit to distinguish "unset" from explicit-zero so a customer
// can pass `--path ""` to clear the path without being re-defaulted
// to `/`. The `--enable|--disable` pair is mutually exclusive;
// --schedule is locally shape-checked (5 whitespace tokens) to match
// the server's validCron so a bad expression fails fast.
func cmdCronsUpdate(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: faas crons update <id> [--schedule EXPR] [--path PATH] [--enable|--disable]", "crons")
		return 1
	}
	id := args[0]
	if !cronIDPattern.MatchString(id) {
		PrintUsage(os.Stderr, "usage: faas crons update <id>   (id is 32 hex chars)", "crons")
		return 1
	}
	fs := flag.NewFlagSet("crons-update", flag.ContinueOnError)
	schedule := fs.String("schedule", "", "cron expression (5 fields)")
	path := fs.String("path", "", "request path")
	enable := fs.Bool("enable", false, "enable the cron")
	disable := fs.Bool("disable", false, "disable the cron")
	if err := fs.Parse(args[1:]); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		PrintUsage(os.Stderr, "usage: faas crons update <id> [--schedule EXPR] [--path PATH] [--enable|--disable]", "crons")
		return 1
	}
	if *enable && *disable {
		PrintUsage(os.Stderr, "usage: faas crons update --enable | --disable (mutually exclusive)", "crons")
		return 1
	}
	// Reject no-fields-set early; the server otherwise no-ops and
	// emits a cron-changed notification — a footgun we'd rather
	// catch at the CLI before a pointless network round-trip.
	explicit := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { explicit[f.Name] = true })
	if !explicit["schedule"] && !explicit["path"] && !explicit["enable"] && !explicit["disable"] {
		PrintUsage(os.Stderr, "usage: faas crons update <id> [--schedule EXPR] [--path PATH] [--enable|--disable]", "crons")
		return 1
	}
	// Local schedule shape check (5 whitespace tokens) mirrors the
	// server's validCron so a bad expression fails fast. We do NOT
	// validate field ranges — that's the scheduler's job.
	if explicit["schedule"] && len(strings.Fields(*schedule)) != 5 {
		PrintFail(os.Stderr, "Invalid --schedule %q (expected 5 fields, e.g. \"*/15 * * * *\")", *schedule)
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	var req api.UpdateCronRequest
	if explicit["schedule"] {
		s := *schedule
		req.Schedule = &s
	}
	if explicit["path"] {
		p := *path
		req.Path = &p
	}
	if explicit["enable"] {
		v := true
		req.Enabled = &v
	}
	if explicit["disable"] {
		v := false
		req.Enabled = &v
	}
	updated, err := client.UpdateCron(context.Background(), id, req)
	if err != nil {
		return printErr("Update failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(updated))
	}
	PrintOK(osStdout, "Updated cron %s", updated.ID)
	renderCronState(osStdout, updated)
	return 0
}

// cmdKeys: list/add/rm. Adding returns the plaintext token once (spec §2.2).
func cmdKeys(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: faas keys <list|add|rm> [args]", "keys")
		return 1
	}
	switch args[0] {
	case subList:
		client, err := authedClient()
		if err != nil {
			return printErr("Not logged in", err)
		}
		out, err := client.ListKeys(context.Background())
		if err != nil {
			return printErr("Request failed", err)
		}
		if jsonOutput {
			return jsonOut(writeNDJSON(out))
		}
		for _, k := range out {
			fmt.Printf("%-30s %s\n", k.Label, k.Prefix)
		}
		return 0
	case subAdd:
		if len(args) < 2 {
			PrintUsage(os.Stderr, "usage: faas keys add <label>", "keys")
			return 1
		}
		client, err := authedClient()
		if err != nil {
			return printErr("Not logged in", err)
		}
		k, err := client.CreateKey(context.Background(), args[1], nil)
		if err != nil {
			return printErr("Create failed", err)
		}
		PrintOK(osStdout, "New API key (shown ONCE):\n  %s", k.Plaintext)
		return 0
	case subRm:
		if len(args) != 2 {
			PrintUsage(os.Stderr, "usage: faas keys rm <id>", "keys")
			return 1
		}
		client, err := authedClient()
		if err != nil {
			return printErr("Not logged in", err)
		}
		if err := client.DeleteKey(context.Background(), args[1]); err != nil {
			return printErr("Delete failed", err)
		}
		PrintOK(osStdout, "Removed")
		return 0
	}
	fmt.Fprintf(os.Stderr, "unknown keys subcommand %q\n", args[0])
	return 1
}

// cmdUsage: dispatcher for `faas usage [summary]`.
//
//	faas usage                          → cmdUsageList  (per-app rows, current month)
//	faas usage --month YYYY-MM          → cmdUsageList  (per-app rows, explicit month)
//	faas usage summary                  → cmdUsageSummary (account roll-up, current month)
//	faas usage summary --month YYYY-MM  → cmdUsageSummary (account roll-up, explicit month)
//
// Strict positional dispatch matches cmdCrons / cmdDomains / cmdKeys:
// an unknown positional returns 1 with `unknown usage subcommand "..."`.
// Flag-leading args (e.g. `--month`) are forwarded to cmdUsageList so
// the legacy `faas usage --month YYYY-MM` invocation keeps working —
// the PR description's "back-compat" promise. Forwarding any flag-like
// arg to the leaf FlagSet also preserves its normal unknown-flag
// handling (cmdUsageList exits 1 on `--bogus`).
func cmdUsage(args []string) int {
	if len(args) == 0 {
		return cmdUsageList(nil)
	}
	if strings.HasPrefix(args[0], "-") {
		return cmdUsageList(args)
	}
	switch args[0] {
	case subSummary:
		return cmdUsageSummary(args[1:])
	}
	PrintUsage(os.Stderr, "usage: faas usage [--month YYYY-MM] | faas usage summary [--month YYYY-MM]", "usage")
	fmt.Fprintf(os.Stderr, "unknown usage subcommand %q\n", args[0])
	return 1
}

// cmdUsageList: GET /v1/usage?month=YYYY-MM. Defaults to the current
// month. Per-app rows (UsageResponse — AppID, MBSeconds, Requests).
func cmdUsageList(args []string) int {
	fs := flag.NewFlagSet("usage-list", flag.ContinueOnError)
	month := fs.String("month", "", "month (YYYY-MM); default: current month")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *month == "" {
		*month = time.Now().UTC().Format("2006-01")
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	u, err := client.GetUsage(context.Background(), *month)
	if err != nil {
		return printErr("Request failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(u))
	}
	_, _ = fmt.Fprintf(osStdout, "App %s — %d requests · %.3f GB-hours (included %d)\n", u.AppID, u.Requests, float64(u.MBSeconds)/3.6e6, u.IncludedGBHours)
	return 0
}

// cmdUsageSummary: GET /v1/usage/summary?month=YYYY-MM. Account-wide
// roll-up (used / included / overage / overage cost). Distinct from
// cmdUsageList which returns per-app rows.
//
// Default-month behavior matches the SDK contract: an unset
// --month passes "" through and the server defaults to the
// current month. We deliberately don't fs.Visit for explicit
// --month "" because the server treats "" and "unset" the same
// (issue #64 family: avoid four lines for unobservable behavior).
func cmdUsageSummary(args []string) int {
	fs := flag.NewFlagSet("usage-summary", flag.ContinueOnError)
	month := fs.String("month", "", "month (YYYY-MM); default: current month")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	s, err := client.UsageSummary(context.Background(), *month)
	if err != nil {
		return printErr("Request failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(s))
	}
	renderUsageSummary(osStdout, s)
	return 0
}

// cmdInvoices: GET /v1/invoices?month=YYYY-MM&before=RFC3339Nano&limit=N
// (issue #259). Surfaces the account's billing history in the same
// text/JSON dual-mode as cmdUsageSummary. --month and --limit are
// validated server-side (the handler returns 400 CodeValidation); the
// CLI just prints the server's RFC 7807 problem and exits 1 (user
// error per UX §3.2). Matches cmdUsageSummary's precedent — the CLI
// does not duplicate the validation that the server already does.
func cmdInvoices(args []string) int {
	fs := flag.NewFlagSet("invoices", flag.ContinueOnError)
	month := fs.String("month", "", "billing month (YYYY-MM); default: all months")
	before := fs.String("before", "", "pagination cursor (RFC3339Nano)")
	limit := fs.Int("limit", 25, "page size (1..100)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	page, err := client.ListInvoices(context.Background(), *month, *before, *limit)
	if err != nil {
		return printErr("Request failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(page))
	}
	renderInvoices(osStdout, page)
	return 0
}

// renderInvoices writes the account's invoice page as a tabular
// block. Widths are tuned for the typical Stripe + Paddle label set;
// PDF cell renders Y when available, - otherwise. The hosted PDF URL
// is never printed — only the public hosted_url is in the response,
// and we don't echo it from the CLI so the customer has to click
// through to the provider portal via the dashboard.
func renderInvoices(w io.Writer, page api.InvoiceListResponse) {
	if len(page.Items) == 0 {
		_, _ = fmt.Fprintln(w, "No invoices.")
		return
	}
	_, _ = fmt.Fprintln(w, "  ID                                NUMBER              PROVIDER PERIOD   STATUS  TOTAL      CUR PDF")
	for _, inv := range page.Items {
		cents := inv.TotalCents
		if cents < 0 {
			cents = -cents
		}
		total := fmt.Sprintf("%d.%02d", cents/100, cents%100)
		pdf := "-"
		if inv.PDFAvailable {
			pdf = "Y"
		}
		_, _ = fmt.Fprintf(w, "  %-33s %-19s %-8s %-7s %-6s %-10s %-3s %s\n",
			inv.ID, trunc(inv.Number, 19), inv.Provider, inv.PeriodEnd.Format("2006-01"),
			inv.Status, "€"+total, inv.Currency, pdf)
	}
	if page.NextBefore != "" {
		_, _ = fmt.Fprintf(w, "\n  next page: faas invoices --before %s\n", page.NextBefore)
	}
}

// trunc clamps s to at most n runes, appending "…" when truncated.
func trunc(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	runes := []rune(s)
	if n <= 1 {
		return string(runes[:n])
	}
	return string(runes[:n-1]) + "…"
}

// renderUsageSummary writes the account roll-up to w as a 5-row
// labelled block (matches the dashboard's usage page). Width = 13
// (longest label is "Overage cost"). GB-hour precision %.3f
// (matches cmdUsageList). Cents is integer.
//
// The customer-facing label is "Overage cost" — the wire field is
// `overage_cents` (cents, integer) but the dashboard labels it
// "overage cost" and the customer is reading the value, not the
// unit. The label here matches the dashboard.
func renderUsageSummary(w io.Writer, s api.UsageSummaryResponse) {
	const labelWidth = 13
	_, _ = fmt.Fprintf(w, "  %-*s %s\n", labelWidth, "Month:", s.Month)
	_, _ = fmt.Fprintf(w, "  %-*s %.3f GB-hours\n", labelWidth, "Used:", s.UsedGBHours)
	_, _ = fmt.Fprintf(w, "  %-*s %d GB-hours\n", labelWidth, "Included:", s.IncludedGBHours)
	_, _ = fmt.Fprintf(w, "  %-*s %.3f GB-hours\n", labelWidth, "Overage:", s.OverageGBHours)
	_, _ = fmt.Fprintf(w, "  %-*s %d cents\n", labelWidth, "Overage cost:", s.OverageCents)
}

func boolPtr(b bool) *bool { return &b }

// cmdConnect implements `faas connect <service>`. Today only
// "github" is supported; the flow opens the dashboard's account
// page where the customer finishes the OAuth + install steps via
// the slice-8 GitHub App flow.
//
// We deliberately don't perform the OAuth dance from the CLI:
// the GitHub App install + bind requires the customer's browser
// session (GitHub OAuth + repo permissions), and the only state
// the platform needs (install_id, install_token) belongs in the
// server, not the CLI's token file.
func cmdConnect(args []string) int {
	if len(args) != 1 {
		PrintUsage(os.Stderr, "usage: faas connect github", "connect")
		return 1
	}
	switch args[0] {
	case svcGithub:
		if _, err := authedClient(); err != nil {
			return printErr("Not logged in", err)
		}
		target := dashboardAccountURL(apiBase())
		fmt.Printf("Opening %s to connect GitHub…\n", target)
		if err := browser.Open(target); err != nil {
			PrintFail(os.Stderr, "Could not open browser: %v", err)
			fmt.Fprintf(os.Stderr, "  Open this URL manually:\n  %s\n", target)
			return 0
		}
		return 0
	default:
		PrintFail(os.Stderr, "unknown service %q (supported: %s)", args[0], svcGithub)
		return 1
	}
}

// cmdOpen implements `faas open <slug>`. Looks up the app's URL via
// the v1 API and launches the OS browser. With --dashboard, opens
// the dashboard's app-detail page instead of the public URL.
func cmdOpen(args []string) int {
	fs := flag.NewFlagSet("open", flag.ContinueOnError)
	dash := fs.Bool("dashboard", false, "open the dashboard page instead of the live URL")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: faas open <slug> [--dashboard]", "open")
		return 1
	}
	slug := fs.Arg(0)
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	app, err := client.GetApp(context.Background(), slug)
	if err != nil {
		return printErr("Could not fetch app", err)
	}
	target := app.URL
	if *dash {
		// Dashboard page is always served; skip the cold-wake probe.
		target = dashboardAppURL(apiBase(), slug)
	} else {
		// Cold-wake transparency (UX §6.4, issue #65 D1). Probe with
		// a 2 s deadline; if the response carries `x-faas-wake: cold`,
		// print the cold-start line immediately, then wait up to 8 s
		// total for the app to warm before opening — the user would
		// otherwise see a 502 from the gateway. Probe errors collapse
		// to "Opening." (don't block on a flaky probe).
		state, err := probeWakeState(target, 2*time.Second)
		switch {
		case err != nil:
			_, _ = fmt.Fprintln(osStdout, "Opening.")
		case state:
			_, _ = fmt.Fprintln(osStdout, "Waking app (cold start) — opening in your browser.")
			deadline := time.Now().Add(8 * time.Second)
			for state && time.Now().Before(deadline) {
				time.Sleep(500 * time.Millisecond)
				state, _ = probeWakeState(target, 2*time.Second)
			}
		default:
			_, _ = fmt.Fprintln(osStdout, "App is warm — opening.")
		}
	}
	_, _ = fmt.Fprintf(osStdout, "Opening %s\n", target)
	if err := browser.Open(target); err != nil {
		PrintFail(os.Stderr, "Could not open browser: %v", err)
		fmt.Fprintf(os.Stderr, "  Open this URL manually:\n  %s\n", target)
		return 0
	}
	return 0
}

// dashboardBaseURL returns the dashboard's public base URL. Today
// that's the API base minus /v1; the gatewayd reverse-proxy serves
// /dashboard/* from the same host. We use this so `faas open` and
// `faas connect` build a clickable URL the customer's browser can
// reach.
func dashboardBaseURL(api string) string {
	return strings.TrimRight(api, "/")
}

// dashboardAccountURL is the canonical "connect GitHub" entry point.
func dashboardAccountURL(api string) string {
	return dashboardBaseURL(api) + "/dashboard/account"
}

// dashboardAppURL is the canonical per-app dashboard page.
//
// Review finding #10: the previous url.PathEscape mismatch with the
// apid router's substring-match would round-trip badly for slugs
// containing '/'. App slugs cannot legitimately contain '/' (the
// store's CreateApp sanitizer already rejects them — see
// pkg/api.ValidateAppConfig), but a buggy caller could hand us one
// and a PathEscape would encode it as %2F, which the apid router
// wouldn't decode before substring-matching. Sanitize to '_' on
// the CLI side so the dashboard link is always a valid round-trip.
func dashboardAppURL(api, slug string) string {
	return dashboardBaseURL(api) + "/dashboard/apps/" + sanitizeSlugForURL(slug)
}

// sanitizeSlugForURL strips characters that would either be
// percent-encoded by url.PathEscape (causing the apid router's
// substring-match to miss) or that would split the URL into a new
// path segment. App slugs are validated as [a-z0-9-] by the store;
// anything else becomes '_'.
func sanitizeSlugForURL(slug string) string {
	out := make([]byte, 0, len(slug))
	for i := 0; i < len(slug); i++ {
		c := slug[i]
		switch {
		case c >= 'a' && c <= 'z',
			c >= 'A' && c <= 'Z',
			c >= '0' && c <= '9',
			c == '-' || c == '_' || c == '.':
			out = append(out, c)
		default:
			out = append(out, '_')
		}
	}
	if len(out) == 0 {
		return appSlugFallback
	}
	return string(out)
}

// dashboardRepoPickerURL is where the customer finishes the repo
// bind (after `faas deploy --repo` opens it). The dashboard reads
// `app` and `repo` from the query string and pre-selects the form.
func dashboardRepoPickerURL(api, slug, repoFullName string) string {
	u := dashboardBaseURL(api) + "/dashboard/connect/repos"
	q := url.Values{}
	q.Set("app", slug)
	q.Set("repo", repoFullName)
	return u + "?" + q.Encode()
}

// validateRepoSlug checks the owner/name shape so a malformed
// --repo doesn't reach the dashboard as a path-injection vector.
func validateRepoSlug(s string) error {
	parts := strings.Split(s, "/")
	if len(parts) != 2 {
		return fmt.Errorf("expected OWNER/NAME, got %q", s)
	}
	for _, p := range parts {
		if p == "" || len(p) > 64 {
			return fmt.Errorf("invalid repo segment in %q", s)
		}
		for _, r := range p {
			allowed := (r >= 'a' && r <= 'z') ||
				(r >= 'A' && r <= 'Z') ||
				(r >= '0' && r <= '9') ||
				r == '-' || r == '_' || r == '.'
			if !allowed {
				return fmt.Errorf("invalid character %q in %q", string(r), s)
			}
		}
	}
	return nil
}

// cmdLogs: tail app or deployment logs via SSE. Move 3 swaps the
// hand-rolled sseLineReader for the SDK's typed Decoder
// (pkg/api/sse.go) so the same parser powers faas logs, faas tail,
// and faas queue tail. signal.NotifyContext on os.Interrupt makes
// Ctrl-C tear down the in-flight request within ~50 ms instead of
// waiting for the body Close to be GC'd.
//
// Issue #309: --grep, --since (RFC3339), and --level (info|warn|error)
// narrow the stream server-side. Pre-validation runs locally so a
// typo'd --since or --level fails fast (no needless SSE open).
// Server-side filtering means the CLI never re-filters; the wire and
// the print loop stay consistent.
func cmdLogs(args []string) int {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	follow := fs.Bool("follow", false, "follow new lines")
	deployment := fs.String("deployment", "", "deployment id (default: latest)")
	grep := fs.String("grep", "", "substring filter; matches the raw line")
	since := fs.String("since", "", "RFC3339 lower bound on the log timestamp")
	level := fs.String("level", "", "minimum level: info, warn, or error")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: faas logs [--follow] [--deployment ID] [--grep S] [--since RFC3339] [--level info|warn|error] <slug>", "logs")
		return 1
	}
	// Pre-validate so a typo'd flag fails fast without opening the
	// SSE stream. The server also rejects bad `level` with 400; this
	// check exists to give a clean error message without burning a
	// round-trip on a clearly invalid input.
	if *since != "" {
		if _, err := time.Parse(time.RFC3339, *since); err != nil {
			return printErr("--since must be RFC3339 (e.g. 2026-07-27T00:00:00Z)", err)
		}
	}
	if *level != "" {
		switch strings.ToLower(*level) {
		case "info", "warn", "error":
		default:
			return printErr("--level must be one of info, warn, error",
				fmt.Errorf("got %q", *level))
		}
	}
	slug := fs.Arg(0)
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	body, err := client.StreamAppLogs(ctx, slug, *deployment, *follow,
		api.LogFilter{Grep: *grep, Since: *since, Level: *level})
	if err != nil {
		var ae *APIError
		if errors.As(err, &ae) {
			renderAPIError(os.Stderr, ae)
			return exitCodeForStatus(ae.Problem.Status)
		}
		return printErr("Could not reach the API", err)
	}
	defer func() { _ = body.Close() }()
	dec := api.NewDecoder(body)
	dec.SetCloseFn(body.Close)
	defer func() { _ = dec.Close() }()
	for {
		select {
		case <-ctx.Done():
			// Ctrl-C. Exit cleanly with status 130 (the
			// shell's standard for SIGINT exit).
			return 130
		case e, ok := <-dec.Events():
			if !ok {
				return 0
			}
			if e.Event == "not_implemented" {
				fmt.Fprintln(os.Stderr, "vmmd Logs(req) gRPC pending — Move 4")
				return 0
			}
			if e.Event == "end" {
				return 0
			}
			if e.Data != "" {
				fmt.Println(e.Data)
			}
		case err := <-dec.Errors():
			if errors.Is(err, io.EOF) {
				return 0
			}
			return printErr("Stream closed", err)
		}
	}
}

// streamDeployLogs opens GET /v1/deployments/{id}/logs?follow=1 and
// prints each `event: log` line until the server emits `event: status`.
// On `live` the function returns 0; on `failed` it renders one of the
// four UX §2.4 copy blocks via renderDeployFailure. If the stream
// breaks before a terminal frame arrives, it does one cheap
// GetDeployment poll to recover the terminal status; only if that
// also fails (or returns a non-terminal status) does it give up and
// tell the customer how to follow manually.
//
// Issue #64 D4 — replaces the old "✓ Queued build …" and exit.
func streamDeployLogs(c *Client, dep api.DeploymentResponse) int {
	PrintProgress(osStdout, "build queued for %s (deployment %s)", dep.AppID, dep.ID)
	ctx := context.Background()
	body, err := c.StreamDeploymentLogs(ctx, dep.ID, nil, 0, true)
	if err != nil {
		// Stream unreachable up front — try one GetDeployment poll in
		// case the build already finished before we opened the stream
		// (e.g., a fast tarball deploy on a slow link).
		if final, ok := pollDeploymentFinal(c, dep); ok {
			return terminalExitForDeployment(final)
		}
		PrintWarn(os.Stderr, "stream unreachable; follow manually: faas logs --deployment %s", dep.ID)
		return 3
	}
	defer func() { _ = body.Close() }()
	dec := api.NewDecoder(body)
	dec.SetCloseFn(body.Close)
	defer func() { _ = dec.Close() }()
streamLoop:
	for {
		select {
		case <-ctx.Done():
			return 130
		case e, ok := <-dec.Events():
			if !ok {
				break streamLoop
			}
			// Move 3: switch on the typed Event name. The decoder
			// preserves `event: <name>` so a single parser handles
			// all four frame shapes this stream emits (log, status,
			// end, error) plus heartbeat comments.
			switch e.Event {
			case "log":
				var entry struct {
					Line string `json:"line"`
				}
				if json.Unmarshal([]byte(e.Data), &entry) == nil && entry.Line != "" {
					fmt.Println(entry.Line)
				}
			case statusLiteral:
				var status struct {
					Status string `json:"status"`
				}
				if json.Unmarshal([]byte(e.Data), &status) == nil &&
					(status.Status == statusLive || status.Status == "failed") {
					if status.Status == statusLive {
						PrintOK(osStdout, "Deployed. https://%s.apps.DOMAIN", dep.AppID)
						printDeployColdWakeSentence()
						return 0
					}
					return renderDeployFailure(dep)
				}
			case "end":
				var end struct {
					Reason string `json:"reason"`
				}
				if json.Unmarshal([]byte(e.Data), &end) == nil && end.Reason != "" {
					PrintWarn(os.Stderr, "build log stream ended (%s); checking deployment status…", end.Reason)
				}
				break streamLoop
			case "error":
				PrintWarn(os.Stderr, "stream closed; follow manually: faas logs --deployment %s", dep.ID)
				return 3
			default:
				// Unknown frame shape — print raw so the customer can see it.
				if e.Data != "" {
					fmt.Println(e.Data)
				}
			}
		case err := <-dec.Errors():
			if errors.Is(err, io.EOF) {
				break streamLoop
			}
			PrintWarn(os.Stderr, "stream closed; follow manually: faas logs --deployment %s", dep.ID)
			return 3
		}
	}
	// Stream ended without a terminal frame — try one GetDeployment
	// poll so a fast build that raced the SSE open isn't reported as
	// "follow manually" when we actually have the answer.
	if final, ok := pollDeploymentFinal(c, dep); ok {
		return terminalExitForDeployment(final)
	}
	PrintWarn(os.Stderr, "stream ended without a terminal frame; follow manually: faas logs --deployment %s", dep.ID)
	return 3
}

// pollDeploymentFinal does one cheap GET on the deployment row and
// returns (final, true) when status is live or failed. Returns
// (_, false) on any error or non-terminal status — the caller treats
// both as "no answer, give up cleanly".
func pollDeploymentFinal(c *Client, dep api.DeploymentResponse) (api.DeploymentResponse, bool) {
	got, err := c.GetDeployment(context.Background(), dep.ID)
	if err != nil {
		return api.DeploymentResponse{}, false
	}
	if got.Status == statusLive || got.Status == "failed" {
		return got, true
	}
	return api.DeploymentResponse{}, false
}

// terminalExitForDeployment applies the same rendering rules as the
// in-stream `event: status` branch, but uses the polled deployment
// row (which has the canonical Error string from the DB).
func terminalExitForDeployment(d api.DeploymentResponse) int {
	if d.Status == statusLive {
		PrintOK(osStdout, "Deployed. https://%s.apps.DOMAIN", d.AppID)
		printDeployColdWakeSentence()
		return 0
	}
	return renderDeployFailure(d)
}

// printDeployColdWakeSentence emits the UX §2.5 cold-wake honesty
// line after every successful deploy. Routes through osStdout so
// tests can capture and assert. The two-line shape is verbatim
// from docs/faas_ux_spec.md:93-101.
func printDeployColdWakeSentence() {
	_, _ = fmt.Fprintln(osStdout,
		"  Your app scales to zero when idle. The first request after idle takes\n"+
			"  ~0.3–0.8s to wake; requests after that are instant. This is normal and free.")
}

// renderDeployFailure maps the deployment's Error string to one of the
// four UX §2.4 copy blocks and exits 3 for infra, 1 for the rest.
func renderDeployFailure(d api.DeploymentResponse) int {
	PrintFail(os.Stderr, "%s", mapFailureMessage(d.Error))
	if d.Error == "infra" {
		return 3
	}
	return 1
}

// mapFailureMessage returns the user-facing copy for one of the four
// failure classes UX §2.4 enumerates. Anything else falls back to
// "Build failed: <err>" so the customer sees the raw class at least.
func mapFailureMessage(err string) string {
	switch err {
	case "user_error":
		return "Build failed — see log above for the failing command."
	case "oom":
		return "Build ran out of memory (2 GB limit). Try fewer/smaller dependencies, or upgrade for a larger build. Docs: https://docs.faas.example/build/limits#memory"
	case "timeout":
		return "Build exceeded 10 min. Docs: https://docs.faas.example/build/limits#timeout"
	case "infra":
		return "Our build system hiccuped — we've been alerted and requeued your build automatically."
	}
	return "Build failed: " + err
}

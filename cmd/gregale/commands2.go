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

	"github.com/onebox-faas/faas/cmd/gregale/templates"
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
	// subLogsTail is the inner-subcommand name for `gregale logs
	// tail <slug>` (issue #315 / tier-2 DX). Lifted from the
	// inline literal at commands2.go:1719 + main.go:252 so goconst
	// stops flagging the three occurrences (two source +
	// PrintUsage doc line).
	subLogsTail = "tail"
	subInfo     = "info"
	subGet      = "get"

	statusPending  = "pending"
	statusVerified = "verified"

	// service names reused across cmdConnect + the usage hint
	// (commands2.go) so goconst stops flagging them.
	svcGithub = "github"

	// defaultTemplateHandler is the `handler.handler` value the
	// function-* templates force into `--handler` (the wire field
	// carries the customer's tarball stem, not the in-VM filename;
	// imaged's function-layer manifest rewrites it to /app/node*.js
	// or /app/handler.py at deploy time). Reused across node22,
	// node24, python312, python313 so goconst doesn't trip.
	defaultTemplateHandler = "handler.handler"

	// appSlugFallback is the placeholder slug sanitizeSlugForURL
	// returns when the input is entirely garbage (all stripped).
	// Lifted out of the literal so goconst stops flagging the
	// repeated "app" string across cmd/gregale (main.go dispatch,
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

	// streamEventError is the SSE event name emitted by the build
	// log stream when the upstream closes (5xx mid-stream, network
	// reset, etc.). Mirrors the `event:` field of the build-log
	// endpoint; see pkg/api streaming bridge for the producer.
	streamEventError = "error"

	// cmdNames reused across the run() dispatch table (main.go) so
	// goconst stops flagging the repeated "apps" / "status" / etc.
	// literals. Tests intentionally keep the literal form.
	dispatchApps = "apps"

	// Plural deployments list (mirrors dispatchApps shape). User runs
	// `gregale deployments` to list; pagination flags live on the handler.
	dispatchDeployments = "deployments"

	// Singular deployment-get. Lifted so the dispatch literal stays
	// constant-named (goconst); the constant does NOT route through
	// appSlugFallback — the dispatch table places it before the
	// "app" case so `gregale deployment <id>` is never read as an app slug.
	dispatchDeployment = "deployment"

	// Plural orgs list. Mirrors dispatchApps shape; user runs
	// `gregale orgs` to list accounts they belong to.
	dispatchOrgs = "orgs"
)

// cmdApp implements `gregale app <slug>` (GET /v1/apps/{slug}), `gregale app <slug>
// --ram N`, and `gregale apps -q <slug>` (DELETE) — UX §2.4.
//
// `--min N` (Pro/Scale only) sets the per-app cold-wake floor
// (ux_spec §6.5): N instances stay RUNNING regardless of idle
// timeout. 0 = scale to zero (default).
//
// `--warm-snapshot` / `--no-warm-snapshot` (issue #470 / PR C / ADR-074)
// opt the app into the warm tier: Park captures a warm-row snapshot
// alongside the init row, and the wake path prefers warm → init
// → cold-boot. `--warm-snapshot-min-requests N` and
// `--warm-snapshot-min-ms N` override the per-app gate thresholds
// (PR #525 defaults: 5 / 2000). The plan gate is still enforced at
// the API — Free/Hobby PATCHes return 403 even if the flag is set.
//
// UpdateAppRequest uses *int pointers on the wire so callers can distinguish
// "unset" from "explicit zero." We use fs.Visit to detect which flags the
// user actually passed — comparing flag values to sentinels (0 / -1) would
// silently drop valid inputs like `--ram 0` or `--idle -1`.
func cmdApp(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale app <slug> [--ram N] [--max-concurrency N] [--idle SEC] [--min N] [--autoscale-target-rps N] [--autoscale-target-cpu-pct N] [--warm-snapshot] [--no-warm-snapshot] [--warm-snapshot-min-requests N] [--warm-snapshot-min-ms N] [--concurrency] [--require-authn] [--no-require-authn] [--public-auth MODE] [--basic-user USER --basic-pass PASS]", "apps")
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
	// Issue #470 / PR C / ADR-074: warm-snapshot opt-in flags. The
	// pair is mutually exclusive — passing both is a usage error
	// rather than a silent last-one-wins. Visit-flag detection lets
	// the user distinguish "unset" (no patch) from explicit true/false.
	warm := fs.Bool("warm-snapshot", false, "enable warm-snapshot tier (Pro/Scale only)")
	noWarm := fs.Bool("no-warm-snapshot", false, "disable warm-snapshot tier (4th audit kind: app.warm_snapshot_disabled)")
	warmMinReq := fs.Int("warm-snapshot-min-requests", 0, "warm-snapshot min-request gate (1..100; 0 = use server default)")
	warmMinMs := fs.Int("warm-snapshot-min-ms", 0, "warm-snapshot min-ms-since-ready gate (100..60000; 0 = use server default)")
	// Issue #559: --concurrency is a read-only fast path. When set
	// (with no other flags), the CLI prints just the plan's
	// per-VM concurrency bound instead of the full app info block.
	// Skips the UpdateAppRequest branch — purely informational, no
	// PATCH. This is the CLI surface the issue requested
	// (`faas apps info --concurrency`); we wire it as a flag on the
	// existing `gregale app <slug>` command so a customer doesn't
	// need a second command tree for a one-line query.
	concurrencyOnly := fs.Bool("concurrency", false, "print only the per-VM concurrency bound for the app's plan (issue #559)")
	// Issue #475: per-app eviction tier. The CLI uses a single
	// string flag rather than the warm-snapshot's boolean pair
	// because the closed enum has only two values
	// ('best_effort' | 'reserved'); a --eviction-priority=reserved
	// flip-down to 'best_effort' is just `...=best_effort` with no
	// separate opt-out flag. The plan gate (Free + reserved = 402)
	// and the per-account cap (Hobby 1, Pro 2, Scale 4) are
	// enforced server-side.
	evictPriority := fs.String("eviction-priority", "", "per-app eviction tier: 'best_effort' (default) or 'reserved' (Free rejected; Hobby 1, Pro 2, Scale 4 apps per account)")
	// Issue #560: per-deployment token gate. The flag pair is
	// mutually exclusive — passing both is a usage error rather than
	// a silent last-one-wins. Visit-flag detection lets the user
	// distinguish "unset" (no patch) from explicit true/false, so
	// `gregale app <slug>` with neither flag still falls through to
	// the info-block print path. Pro/Scale only — the API rejects
	// Free/Hobby with 403 plan_require_authn_not_allowed, which
	// surfaces here as an "Update failed" error with the API's
	// problem code.
	requireAuthn := fs.Bool("require-authn", false, "require Authorization: Bearer <token> on every request (Pro/Scale only)")
	noRequireAuthn := fs.Bool("no-require-authn", false, "drop the token requirement; back to public-by-default")
	// Issue #477 / ADR-079: per-app public-URL auth mode.
	// The CLI uses a single string flag (open|bearer|basic)
	// plus optional --basic-user / --basic-pass plaintext
	// args for mode='basic'. The apid seal step encrypts
	// them under the APP_BASIC_AUTH secretbox namespace
	// before persistence. The CLI never sees the sealed
	// blob — the customer supplies plaintext at PATCH
	// time. Basic auth lands as basic_user + basic_pass
	// in the JSON body (the apid side handles the seal).
	//
	// Plan-gate (server-side, surfaces as an
	// "Update failed" error with the API's problem
	// code): Free PATCH 'bearer' = 402
	// plan_public_auth_bearer_not_allowed; Free/Hobby
	// PATCH 'basic' = 402
	// plan_public_auth_basic_not_allowed.
	publicAuth := fs.String("public-auth", "", "per-app public-URL auth: 'open' (default), 'bearer' (Hobby+), or 'basic' (Pro+; pair with --basic-user + --basic-pass)")
	basicUser := fs.String("basic-user", "", "basic-auth username (RFC 7617 §2); required when --public-auth=basic")
	basicPass := fs.String("basic-pass", "", "basic-auth password (RFC 7617 §2); required when --public-auth=basic")
	if err := fs.Parse(args[1:]); err != nil {
		return 1
	}
	if *warm && *noWarm {
		return printErr("Invalid flags", fmt.Errorf("--warm-snapshot and --no-warm-snapshot are mutually exclusive"))
	}
	// Issue #560: mutual exclusion check for the require-authn pair.
	// Mirrors the --warm-snapshot / --no-warm-snapshot guard above —
	// symmetric flag pairs intentionally use a usage error (not a
	// silent last-one-wins) so the customer sees the conflict instead
	// of an unexpected PATCH. The plan gate runs server-side; the
	// CLI's job is to keep the flag pair consistent.
	if *requireAuthn && *noRequireAuthn {
		return printErr("Invalid flags", fmt.Errorf("--require-authn and --no-require-authn are mutually exclusive"))
	}
	// Issue #559: --concurrency fast path. Refuse to mix with
	// update flags (mixing a read-only query with a write would
	// surprise the customer) and refuse with --json (the bound
	// is one integer; --json would be heavier than the text).
	if *concurrencyOnly {
		// --concurrency is purely informational. Reject mixing
		// with any other flag — fs.Visit only fires for
		// explicitly-passed flags, and we reject every flag
		// except `--concurrency` itself. Inverted-positive-list:
		// any future write flag added to this command will be
		// rejected here automatically, without a manual
		// allow-list to keep in sync.
		var conflict string
		fs.Visit(func(f *flag.Flag) {
			if f.Name != "concurrency" && conflict == "" {
				conflict = f.Name
			}
		})
		if conflict != "" {
			return printErr("Invalid flags",
				fmt.Errorf("--concurrency cannot be combined with --%s (read-only fast path)", conflict))
		}
		if jsonOutput {
			return printErr("Invalid flags",
				fmt.Errorf("--concurrency is a one-line text output; --json is not meaningful"))
		}
		client, err := authedClient()
		if err != nil {
			return printErr("Not logged in", err)
		}
		a, err := client.GetApp(context.Background(), slug)
		if err != nil {
			return printErr("Could not fetch app", err)
		}
		fmt.Println(a.ConcurrencyPerVMBound)
		return 0
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
	// Warm-snapshot fields. The boolean pair coalesces to a single
	// *bool on the wire so the apid side sees one canonical field.
	if explicit["warm-snapshot"] {
		v := true
		req.WarmSnapshotEnabled = &v
	}
	if explicit["no-warm-snapshot"] {
		v := false
		req.WarmSnapshotEnabled = &v
	}
	if explicit["warm-snapshot-min-requests"] {
		v := *warmMinReq
		req.WarmSnapshotMinRequests = &v
	}
	if explicit["warm-snapshot-min-ms"] {
		v := *warmMinMs
		req.WarmSnapshotMinMs = &v
	}
	// Issue #475: per-app eviction tier. The CLI uses a single
	// string flag — the closed enum is 'best_effort' | 'reserved'
	// (and any other value gets a clean 422 from the apid bounds
	// check). We validate the value locally too so a CLI typo
	// surfaces as a usage error before the round-trip.
	if explicit["eviction-priority"] {
		v := *evictPriority
		if v != "best_effort" && v != "reserved" {
			return printErr("Invalid --eviction-priority", fmt.Errorf("must be 'best_effort' or 'reserved'; got %q", v))
		}
		req.EvictionPriority = &v
	}
	// Issue #560: require-authn pair coalesces to a single *bool on
	// the wire so the apid side sees one canonical field. Each flag
	// of the pair sets an explicit value; the no-op guard below
	// checks `req.RequireAuthn == nil` to keep the bare
	// `gregale app <slug>` invocation on the info-block print path.
	if explicit["require-authn"] {
		v := true
		req.RequireAuthn = &v
	}
	if explicit["no-require-authn"] {
		v := false
		req.RequireAuthn = &v
	}
	// Issue #477 / ADR-079: public-auth block. The CLI
	// validates the mode locally (so a typo surfaces
	// before the round-trip) and forwards the
	// basic_user + basic_pass as plaintext — the apid
	// seal step encrypts them under the APP_BASIC_AUTH
	// secretbox namespace before persistence. The
	// CLI never sees the sealed blob.
	if explicit["public-auth"] {
		v := *publicAuth
		switch v {
		case api.AppPublicAuthModeOpen, api.AppPublicAuthModeBearer, api.AppPublicAuthModeBasic:
		default:
			return printErr("Invalid --public-auth",
				fmt.Errorf("must be 'open', 'bearer', or 'basic'; got %q", v))
		}
		block := &api.PublicAuthBlock{Mode: v}
		if v == api.AppPublicAuthModeBasic {
			bu := strings.TrimSpace(*basicUser)
			bp := strings.TrimSpace(*basicPass)
			if bu == "" {
				return printErr("Invalid --basic-user",
					fmt.Errorf("--basic-user is required when --public-auth=basic"))
			}
			if bp == "" {
				return printErr("Invalid --basic-pass",
					fmt.Errorf("--basic-pass is required when --public-auth=basic"))
			}
			block.BasicUser = bu
			block.BasicPass = bp
		}
		req.PublicAuth = block
	}

	if req.RAMMB == nil && req.MaxConcurrency == nil && req.IdleTimeoutS == nil && req.MinInstances == nil &&
		req.AutoscaleTargetRPS == nil && req.AutoscaleTargetCPUPct == nil &&
		req.WarmSnapshotEnabled == nil && req.WarmSnapshotMinRequests == nil && req.WarmSnapshotMinMs == nil &&
		req.EvictionPriority == nil && req.RequireAuthn == nil && req.PublicAuth == nil {
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
		// Issue #559: surface the platform-advertised per-VM
		// concurrency bound for the app's plan. Distinct from
		// `max concurrency` above (the per-app instance cap).
		// 0 is the fail-closed value for an unknown plan — render
		// as "—" so a customer reading the info block knows the
		// platform is unable to confirm the bound rather than
		// that the bound is zero.
		if a.ConcurrencyPerVMBound == 0 {
			fmt.Printf("%-30s %s\n", "concurrency per vm:", "—")
		} else {
			fmt.Printf("%-30s %d\n", "concurrency per vm:", a.ConcurrencyPerVMBound)
		}
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
		// allowlist in the text-mode `gregale app <slug>` output so a
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
		// Issue #470 / PR C / ADR-074: warm-snapshot state. Mirror
		// the autoscale rendering: enabled/disabled for the toggle,
		// bare value for the gating thresholds.
		if a.WarmSnapshotEnabled {
			fmt.Printf("%-30s %s\n", "warm snapshot:", "enabled")
		} else {
			fmt.Printf("%-30s %s\n", "warm snapshot:", "disabled")
		}
		fmt.Printf("%-30s %d\n", "warm snapshot min requests:", a.WarmSnapshotMinRequests)
		fmt.Printf("%-30s %d ms\n", "warm snapshot min ms:", a.WarmSnapshotMinMs)
		// Issue #475: surface the per-app eviction tier so the
		// customer can verify their PATCH round-tripped. The
		// empty-string fallback matches the historical default
		// ('best_effort') for pre-#475 rows — the column was
		// added with a NOT NULL DEFAULT 'best_effort', so any
		// pre-PR row will surface as 'best_effort' after the
		// migration applies.
		if a.EvictionPriority == "" {
			fmt.Printf("%-30s %s\n", "eviction priority:", "best_effort")
		} else {
			fmt.Printf("%-30s %s\n", "eviction priority:", a.EvictionPriority)
		}
		// Issue #560: surface the per-app token gate flag so the
		// customer can verify their PATCH round-tripped without
		// dropping into --json. Mirrors the warm-snapshot
		// enabled/disabled rendering above (a single on/off, no
		// extra gating threshold).
		if a.RequireAuthn {
			fmt.Printf("%-30s %s\n", "require authn:", "enabled")
		} else {
			fmt.Printf("%-30s %s\n", "require authn:", "disabled")
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

// cmdAppsRm implements `gregale apps -q <slug>` (DELETE /v1/apps/{slug}).
func cmdAppsRm(args []string) int {
	fs := flag.NewFlagSet("apps-rm", flag.ContinueOnError)
	quiet := fs.Bool("q", false, "suppress confirmation prompt")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale apps -q <slug>", "apps")
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
// buildCreateRequest stamps the issue #737 / ADR-083 fields onto the
// CreateAppRequest the CLI hands to apid. Two non-obvious fields:
//
//   - Type: shapeFunction → "function", else "" (apid treats empty
//     as "app"). Without this, apid stores the row as type=app and
//     the multipart validator at cmd/apid/deploy_inputs.go:144
//     rejects the function deploy.
//
//   - Runtime: only set when resolvedShape == shapeFunction AND
//     runtime is non-empty. apid's buildApp validator at
//     cmd/apid/handlers.go:98 requires Runtime to be in the function
//     whitelist on a function-typed app; an empty Runtime trips a
//     400 between the "Detected: function, ..." line and the
//     multipart upload, which would silently break the headline
//     auto-detection feature. The auto-detect path always populates
//     runtime via inferFunctionRuntime before this is called; the
//     explicit --function --tarball path relies on the explicit
//     --runtime flag, with --handler defaulting to "handler.handler"
//     (defaultTemplateHandler, commands2.go:48).
func buildCreateRequest(slug string, sh shape, runtime string, requireAuthnPtr *bool) api.CreateAppRequest {
	req := api.CreateAppRequest{
		Slug:         slug,
		RequireAuthn: requireAuthnPtr,
	}
	if sh == shapeFunction {
		req.Type = "function"
		if runtime != "" {
			req.Runtime = runtime
		}
	}
	return req
}

// cmdDeployTarball implements `gregale deploy` (image / tarball / repo
// / template / zero-config). Zero-config (issue #313) packs the cwd
// and proceeds down the --tarball path. Issue #737 / ADR-083 added the
// function-vs-app auto-detect on the zero-config path and the
// --function / --app explicit-shape flags.
//
// `--template NAME` materializes one of the eleven embedded starter
// projects (cmd/gregale/templates/embed.go) into a tempdir, tars+gzip it,
// and proceeds down the --tarball path. For the function templates
// (function-node, function-python) we force --runtime / --handler so
// the runner wires up correctly without the customer having to know
// those flags.
func cmdDeployTarball(args []string) int {
	fs := flag.NewFlagSet("deploy", flag.ContinueOnError)
	image := fs.String("image", "", "digest-pinned image reference")
	tarball := fs.String("tarball", "", "path to source archive (tar.gz)")
	repo := fs.String("repo", "", "GitHub repo to bind and deploy (owner/name)")
	templateName := fs.String("template", "", "start from an embedded template (run with a bad value to see available names)")
	dockerfile := fs.Bool("dockerfile", false, "build with the supplied Dockerfile inside --tarball")
	runtime := fs.String("runtime", "", "function runtime (node22|python312|go124|go124-alpine|node24|python313)")
	handler := fs.String("handler", "", "function handler (e.g. handler.handler)")
	name := fs.String("name", "", "app name (default: current directory)")
	// Issue #737 / ADR-083: explicit shape override. Without either flag
	// the CLI auto-detects from the cwd (handler.*-only → function,
	// otherwise app). With --function or --app, detection is skipped.
	// Mutually exclusive — silently mixing is exactly the bug this ADR
	// fixes. The --function path requires --runtime (or accepts the
	// default "handler.handler" wire value if --handler is unset).
	function := fs.Bool("function", false, "deploy as a function (single handler.* file); skips cwd auto-detection")
	app := fs.Bool("app", false, "deploy as an app (Railpack framework); skips cwd auto-detection and clears --runtime/--handler")
	// Phase 3 (repo decomposition) — one-key provision flags. Presence
	// of --only or --project-slug short-circuits the existing CreateApp
	// + DeployTarball path and routes through ScanProject →
	// ApplyProjectPlan. --yes / --json are absorbed by the global
	// json_flag.go layer and live alongside the others so a single
	// `gregale deploy --tarball X --yes --json --project-slug S` works.
	yes := fs.Bool("yes", false, "skip the apply confirmation prompt")
	deployOnly := fs.String("only", "", "comma-separated workload names to apply (triggers one-key provision)")
	projectSlug := fs.String("project-slug", "", "kebab slug for the project (triggers one-key provision)")
	// Issue #560: per-deployment require_authn opt-in (Cloud Run
	// --no-allow-unauthenticated analogue). Same flag pair as
	// cmdApp / cmdAppScale. Mirrors the --warm-snapshot /
	// --no-warm-snapshot pattern: positive and explicit-negative
	// are both opt-in (so customers can flip either way without a
	// separate set-true / set-false command); the unset value
	// (no flag) is the global default of false. The plan gate
	// (Pro/Scale only) is enforced server-side at the apid PATCH
	// + CreateApp handlers, so Free/Hobby customers get the same
	// 403 plan_require_authn_not_allowed whether they reach the
	// gate through `gregale deploy --require-authn` or `gregale
	// app <slug> --require-authn`.
	requireAuthn := fs.Bool("require-authn", false, "require Authorization: Bearer <token> on every request (Pro/Scale only)")
	noRequireAuthn := fs.Bool("no-require-authn", false, "drop the token requirement; back to public-by-default")
	// Issue #556 PR-A: per-deployment traffic-split weight (Pro/Scale
	// only). Sentinel value -1 = "unset" — `fs.Int` doesn't have a
	// pointer type, so the explicit `fs.Visit` check below
	// distinguishes "absent" from "explicit zero". The handler
	// validates [0, 100] (422) and the plan gate (403) on the
	// request path; we just thread the pointer through.
	trafficPercent := fs.Int("traffic-percent", -1, "split weight for this deployment (0-100, Pro/Scale only; -1 = server default 100)")
	if err := fs.Parse(args); err != nil {
		PrintUsage(os.Stderr, "usage: gregale deploy --image REF | --tarball PATH | --repo OWNER/NAME | --template NAME", "deploy")
		return 1
	}
	// Issue #560: flag-pair mutex check (mirrors cmdApp /
	// cmdAppScale --warm-snapshot/--no-warm-snapshot). Setting
	// both is unambiguous noise; reject before any side effects.
	if *requireAuthn && *noRequireAuthn {
		return printErr("Invalid flags", fmt.Errorf("--require-authn and --no-require-authn are mutually exclusive"))
	}
	// Issue #737 / ADR-083: --function and --app are mutually exclusive.
	// Setting both is ambiguous noise; reject before any side effects so
	// the customer's first response from the CLI is not a silent shape
	// pick. Mirrors the --require-authn/--no-require-authn check above.
	if *function && *app {
		return printErr("Invalid flags", fmt.Errorf("--function and --app are mutually exclusive"))
	}
	// --app clears any --runtime/--handler the customer also set. The
	// customer intended an app deploy; passing function fields is
	// either a typo or a leftover from a copy-paste, and silently
	// mixing is exactly the bug this ADR fixes. We still surface the
	// result so a confused customer can diagnose.
	if *app && (*runtime != "" || *handler != "") {
		PrintProgress(os.Stderr, "WARN: --app clears --runtime=%q and --handler=%q (function fields are ignored on app deploys)", *runtime, *handler)
		*runtime = ""
		*handler = ""
	}
	// --function without --runtime is allowed: the wire defaults to
	// "handler.handler" (matches the function-* template convention at
	// defaultTemplateHandler, line 48). What --function REQUIRES is
	// for the customer's source to actually be a function — handled
	// below when detectShape runs (or, for the --tarball path, when
	// apid's function-runtime whitelist rejects it).
	// fs.Visit distinguishes "unset" from "explicit zero": if the
	// customer passed either --require-authn or --no-require-authn
	// (but not both — checked above), we propagate the bool to the
	// CreateApp call so a fresh deploy can opt in/out at create
	// time. nil = unset → apid server default (false), so existing
	// customers see no behaviour change.
	explicit := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { explicit[f.Name] = true })
	var requireAuthnPtr *bool
	switch {
	case explicit["require-authn"]:
		v := true
		requireAuthnPtr = &v
	case explicit["no-require-authn"]:
		v := false
		requireAuthnPtr = &v
	}
	// Issue #556 PR-A: derive the optional traffic_percent pointer.
	// Sentinel -1 (the default value above) means "absent" — the
	// handler will default to 100 server-side. Any explicit value
	// (including 0) is forwarded as-is; the handler validates
	// [0, 100] (422) and the plan gate (403) on the request path.
	optTrafficPercent := func(v int) *int {
		if v < 0 {
			return nil
		}
		return &v
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
		// Phase 3 guard: --repo is the dashboard browser flow; the
		// one-key provision surface takes --tarball/--path, not
		// --repo. Mixing them is almost always a mistake.
		if *deployOnly != "" || *projectSlug != "" {
			PrintFail(os.Stderr, "--repo cannot be combined with --only or --project-slug")
			return 1
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
			// Default Node runtime is node22 (per docs/runtimes/go124.md
			// tier-1 stance: no default-flip in the same PR that adds a
			// new runtime). Use function-node24 for the Node 24 variant.
			*runtime = runtimeNode22
			*handler = defaultTemplateHandler
		case "function-node24":
			// Tier 1 PR 1 row: parallel to function-node, runtime is
			// node24 (Node 24 LTS). The handler filename
			// convention is the same; imaged's function-layer
			// manifest sets `--handler /app/node24.js`.
			*runtime = "node24"
			*handler = defaultTemplateHandler
		case "function-python":
			// Default Python runtime is python312 (no default-flip in
			// Tier 1; python313 stays opt-in via function-python313).
			*runtime = runtimePython312
			*handler = defaultTemplateHandler
		case "function-python313":
			// Tier 1 PR 1 row: parallel to function-python, runtime
			// is python313. Handler filename is identical
			// (/app/handler.py in the microVM, version-neutral).
			*runtime = "python313"
			*handler = defaultTemplateHandler
		case "function-go":
			// The customer's handler is a static Go binary; the
			// --handler wire field is vestigial for go124 (the imaged
			// manifest locks the entrypoint to /app/handler). We set
			// a non-empty value so the multipart writer doesn't skip
			// the field, but the value is never read by the runtime.
			*runtime = runtimeGo124
			*handler = "handler.go"
		}
		f, err := os.CreateTemp("", "gregale-template-*.tar.gz")
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
	// Issue #737 / ADR-083: resolved shape from cwd auto-detection or
	// the explicit --function/--app short-circuit. Defaults to
	// shapeApp so a --tarball / --image / --template deploy (no cwd
	// pack) stays on the existing app-shaped path. The CreateApp call
	// below reads resolvedShape and sets Type="function" on the wire
	// only when the cwd path picked function mode — explicit
	// --function/--app outside the cwd pack also write to
	// resolvedShape via this variable in the same branch.
	var resolvedShape = shapeApp
	// Issue #737 / ADR-083: explicit --function / --app on a
	// --tarball / --template path skips the cwd detector (no cwd
	// pack happens), but still flips resolvedShape so CreateApp
	// sends Type="function" when the customer asked for it. Without
	// this branch, `gregale deploy --tarball my.tgz --function` would
	// still create an app-type app row.
	if *function {
		resolvedShape = shapeFunction
		// Default the wire --handler to the function-template
		// convention so a customer who runs `gregale deploy
		// --tarball my.tgz --function --runtime node22` without
		// --handler gets the same shape as
		// `gregale --template function-node --tarball my.tgz`.
		// Without this, apid's function validator rejects the
		// empty handler form field with a 400.
		if *handler == "" {
			*handler = defaultTemplateHandler
		}
	} else if *app {
		resolvedShape = shapeApp
	}
	if *image == "" && *tarball == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return printErr("Could not read current directory", err)
		}
		// Issue #737 / ADR-083: resolveDeployShape does detect +
		// infer + print in one seam so the unit test can drive the
		// "Detected:" line without bringing up apid. The print goes
		// BEFORE the multipart upload so the customer's first
		// response from the CLI is the deploy shape. An explicit
		// --function / --app short-circuits the detector — see the
		// mutex block above.
		detected, rt, hnd, err := resolveDeployShape(cwd, *function, *app, jsonOutput)
		if err != nil {
			return printErr("No deployable source found in "+filepath.Base(cwd), err)
		}
		switch detected {
		case shapeFunction:
			// An explicit --runtime / --handler on the CLI wins over
			// the inferred value (customer may be overriding the
			// default-extension→runtime map). The helper already
			// printed "Detected: function, runtime=<rt>, handler=<h>"
			// using the inferred values; the wire uses whatever is
			// in *runtime / *handler here.
			if *runtime == "" {
				*runtime = rt
			}
			if *handler == "" {
				*handler = hnd
			}
			// Pack the cwd so the multipart upload has a tarball —
			// the function convention needs the file on the wire for
			// imaged to stage it.
			path, _, n, err := autoPackCwd(cwd)
			if err != nil {
				return printErr("Could not pack current directory", err)
			}
			defer func() { _ = os.Remove(path) }()
			PrintProgress(os.Stderr, "packing %d file(s) from %s", n, filepath.Base(cwd))
			*tarball = path
			resolvedShape = shapeFunction
		case shapeApp:
			path, fw, n, err := autoPackCwd(cwd)
			if err != nil {
				return printErr("Could not pack current directory", err)
			}
			defer func() { _ = os.Remove(path) }()
			if fw == fwDocker {
				*dockerfile = true
			}
			PrintProgress(os.Stderr, "packing %d file(s) from %s", n, filepath.Base(cwd))
			*tarball = path
			resolvedShape = shapeApp
		}
	}

	client, err := authedClientWithDeployTimeout(5 * time.Minute)
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()

	// Phase 3 (repo decomposition) one-key provision path. Triggered
	// by --only or --project-slug on a --tarball / --template / zero-config
	// pack. The plan is fetched via ScanProject, the apply is
	// transactional on the server (rollback on over-quota per
	// ADR-050), and the confirm prompt is gated on TTY + --yes.
	if *deployOnly != "" || *projectSlug != "" {
		// Make sure the tarball resolves the same way it does for
		// the legacy path: --template materialises, zero-config packs
		// $PWD. The block above already populated *tarball in those
		// cases; if it's still empty, we have no source.
		if *tarball == "" {
			return printErr("One-key provision requires --tarball, --template, or a TTY cwd",
				errors.New("no source resolved"))
		}
		openTarball, err := openCustomerFile(*tarball)
		if err != nil {
			return printErr("Could not open tarball", err)
		}
		defer func() { _ = openTarball.Close() }()
		prodBranch := "main"
		plan, err := client.ScanProject(ctx, openTarball, filepath.Base(*tarball),
			*projectSlug, prodBranch, 0, splitCSV(*deployOnly))
		if err != nil {
			return printErr("Scan failed", err)
		}
		if !plan.CanApply {
			if jsonOutput {
				return jsonOut(writeJSONProblem(planProblem(plan)))
			}
			printPlanText(osStdout, plan)
			return printErr("Plan is not applicable on this plan", errors.New("over-quota or unsupported configuration"))
		}
		if !*yes && !jsonOutput && stdoutIsTTY() && stdinIsTTY() {
			if !confirmPlan(osStdout, os.Stdin, plan) {
				return printErr("Aborted by user", errors.New("user declined at the confirm prompt"))
			}
		}
		// Re-open because the previous reader consumed the body.
		// openCustomerFile is the same helper used in the scan call
		// above; it's the documented path for any CLI-supplied tarball
		// (Lstat + symlink-follow guard).
		openTarball2, err := openCustomerFile(*tarball)
		if err != nil {
			return printErr("Could not reopen tarball", err)
		}
		defer func() { _ = openTarball2.Close() }()
		apply, err := client.ApplyProjectPlan(ctx, plan.PlanToken, openTarball2, filepath.Base(*tarball),
			*projectSlug, prodBranch, 0, splitCSV(*deployOnly))
		if err != nil {
			return printErr("Apply failed", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(apply))
		}
		PrintOK(osStdout, "Created project %s with %d app(s) and %d cron(s)",
			apply.ProjectID, len(apply.Apps), len(plan.Crons))
		// Per-workload build lines (PR-A, repo decomposition Phase 5
		// close-the-loop). The apply path enqueued one (deployment,
		// build) per added/changed workload; surface them so the
		// operator can `faas logs <build_id>` to follow progress.
		// Partial-failure rows have Error populated and no IDs.
		// We ignore Fprintf errors: stdout is the only sink and a
		// closed pipe (e.g. `... | head`) would otherwise flip the
		// exit code on a successful apply — matches the
		// commands_decompose_test stub which drops Fprintf errors
		// on the same path.
		for _, b := range apply.Builds {
			if b.Error != "" {
				_, _ = fmt.Fprintf(osStdout, "  ! %s: %s\n", b.Slug, b.Error)
				continue
			}
			_, _ = fmt.Fprintf(osStdout, "  ✓ %s: deployment=%s build=%s\n", b.Slug, b.DeploymentID, b.BuildID)
		}
		return 0
	}

	createReq := buildCreateRequest(slug, resolvedShape, *runtime, requireAuthnPtr)
	if _, err := client.CreateApp(ctx, createReq); err != nil {
		var ae *APIError
		if !errors.As(err, &ae) || ae.Problem.Status != 409 {
			return printErr("Could not create app", err)
		}
		// Issue #560: the slug already exists (409). The deploy
		// path used to silently swallow the dup and proceed with
		// no PATCH; now if the customer passed --require-authn
		// or --no-require-authn on this deploy, we follow up
		// with a PATCH to mirror the new flag onto the existing
		// app — the plan gate (Pro/Scale only) still fires at
		// the apid PATCH handler, so Free/Hobby customers
		// flipping --require-authn on an existing app get the
		// same 403 plan_require_authn_not_allowed as on a fresh
		// create. The unset case (no flag passed) is a no-op.
		if requireAuthnPtr != nil {
			if _, err := client.UpdateApp(ctx, slug, api.UpdateAppRequest{RequireAuthn: requireAuthnPtr}); err != nil {
				return printErr("Could not update existing app's require_authn", err)
			}
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
	dep, err := client.Deploy(ctx, slug, api.CreateDeploymentRequest{Image: *image, TrafficPercent: optTrafficPercent(*trafficPercent)})
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
		PrintUsage(os.Stderr, "usage: gregale rollback <slug>", "rollback")
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
		PrintUsage(os.Stderr, "usage: gregale park <slug>", "park-wake")
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
		PrintUsage(os.Stderr, "usage: gregale wake <slug>", "park-wake")
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

// cmdTrafficSet implements `gregale traffic set` (issue #556 PR-A).
// The dispatch from main() splits on the sub-command name: `set`
// lands here, future sub-commands (status, split) would route
// alongside. The flag pair is --deployment <id> + --percent N; the
// client.UpdateDeploymentTraffic method hits PATCH
// /v1/deployments/{id}/traffic. The handler enforces the plan gate
// (Pro+ only, 403) and range [0, 100] (422); the CLI just threads
// the values through and prints the canonical "Set … → N%" OK line.
//
// Flag-presence check mirrors --min-instances in cmdApp / cmdAppScale:
// absent --deployment or --percent fails loud with usage rather
// than silently PATCHing the wrong row.
func cmdTrafficSet(args []string) int {
	fs := flag.NewFlagSet("traffic set", flag.ContinueOnError)
	deployment := fs.String("deployment", "", "deployment id to set the traffic split on")
	percent := fs.Int("percent", -1, "traffic weight in [0, 100]; -1 = unset (server default 100)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *deployment == "" || *percent < 0 {
		PrintUsage(os.Stderr, "usage: gregale traffic set --deployment <id> --percent N", "traffic")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	dep, err := client.PatchDeploymentsIdTraffic(context.Background(), *deployment, *percent)
	if err != nil {
		return printErr("Traffic set failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(dep))
	}
	PrintOK(osStdout, "Set %s → %d%%", dep.ID, dep.TrafficPercent)
	return 0
}

// cmdTraffic dispatches the `traffic` sub-command. PR-A wires the
// `set` leaf; `status` is a follow-up that re-uses the same DTO.
func cmdTraffic(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale traffic <set|status> [args]", "traffic")
		return 1
	}
	switch args[0] {
	case "set":
		return cmdTrafficSet(args[1:])
	default:
		PrintUsage(os.Stderr, "usage: gregale traffic <set|status> [args]", "traffic")
		return 1
	}
}

// cmdDomains dispatches list/add/rm. Adding prints the TXT record the
// customer must publish for verification (spec §7).
func cmdDomains(args []string) int {
	parent, _ := lookupCliCommand("domains")
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale domains <list|add|rm> [args]", "domains")
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
			PrintUsage(os.Stderr, "usage: gregale domains add --domain <d> --app <slug>", "domains")
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
		fmt.Printf("  _gregale-verify.%s  TXT  %s\n\n", d.Domain, d.ChallengeToken)
		fmt.Printf("Then run 'gregale domains list' to see when verification completes.\n")
		return 0
	case subRm:
		if len(args) != 2 {
			PrintUsage(os.Stderr, "usage: gregale domains rm <domain>", "domains")
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
	sug, _ := suggestSubcommand(args[0], parent)
	maybeSuggestSub(sug)
	return 1
}

// cmdCrons: list/add/update/rm.
func cmdCrons(args []string) int {
	parent, _ := lookupCliCommand("crons")
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale crons <list|add|update|rm> [args]", "crons")
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
			PrintUsage(os.Stderr, "usage: gregale crons list --app <slug>", "crons")
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
			PrintUsage(os.Stderr, "usage: gregale crons add --app <slug> --schedule '*/5 * * * *' [--path /]", "crons")
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
			PrintUsage(os.Stderr, "usage: gregale crons rm <id>", "crons")
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
	sug, _ := suggestSubcommand(args[0], parent)
	maybeSuggestSub(sug)
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

// cmdCronsUpdate implements `gregale crons update <id> [--schedule EXPR]
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
		PrintUsage(os.Stderr, "usage: gregale crons update <id> [--schedule EXPR] [--path PATH] [--enable|--disable]", "crons")
		return 1
	}
	id := args[0]
	if !cronIDPattern.MatchString(id) {
		PrintUsage(os.Stderr, "usage: gregale crons update <id>   (id is 32 hex chars)", "crons")
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
		PrintUsage(os.Stderr, "usage: gregale crons update <id> [--schedule EXPR] [--path PATH] [--enable|--disable]", "crons")
		return 1
	}
	if *enable && *disable {
		PrintUsage(os.Stderr, "usage: gregale crons update --enable | --disable (mutually exclusive)", "crons")
		return 1
	}
	// Reject no-fields-set early; the server otherwise no-ops and
	// emits a cron-changed notification — a footgun we'd rather
	// catch at the CLI before a pointless network round-trip.
	explicit := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { explicit[f.Name] = true })
	if !explicit["schedule"] && !explicit["path"] && !explicit["enable"] && !explicit["disable"] {
		PrintUsage(os.Stderr, "usage: gregale crons update <id> [--schedule EXPR] [--path PATH] [--enable|--disable]", "crons")
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
	parent, _ := lookupCliCommand("keys")
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale keys <list|add|rm|rotate|grace-window> [args]", "keys")
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
			PrintUsage(os.Stderr, "usage: gregale keys add <label>", "keys")
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
			PrintUsage(os.Stderr, "usage: gregale keys rm <id>", "keys")
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
	case "rotate":
		return cmdKeysRotate(args[1:])
	case "grace-window":
		return cmdKeysGraceWindow(args[1:])
	}
	fmt.Fprintf(os.Stderr, "unknown keys subcommand %q\n", args[0])
	sug, _ := suggestSubcommand(args[0], parent)
	maybeSuggestSub(sug)
	return 1
}

// cmdKeysRotate issues POST /v1/keys/{id}/rotate. The new plaintext
// is returned ONCE (same posture as add); the old key remains
// usable until old_key_expires_at — the dashboard default grace is
// 7 days (api.DefaultAPIKeyGraceWindowDays), overridable via
// `gregale keys grace-window`.
func cmdKeysRotate(args []string) int {
	fs := flag.NewFlagSet("keys rotate", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale keys rotate <key-id>", "keys")
		return 1
	}
	id := fs.Arg(0)
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.RotateKey(context.Background(), id)
	if err != nil {
		return printErr("Rotate failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	PrintOK(osStdout, "Rotated key. New plaintext (shown ONCE):\n  %s", resp.KeyPlaintext)
	PrintProgress(osStdout, "  new id:        %s", resp.Key.ID)
	PrintProgress(osStdout, "  old key id:    %s", resp.OldKeyID)
	PrintProgress(osStdout, "  old key grace: %s", resp.OldKeyExpiresAt)
	return 0
}

// cmdKeysGraceWindow reads or updates the per-account API-key
// rotation grace window (issue #189 / IAM-5). With no arg, prints
// the current override + plan default. `--reset` clears the
// override (falls back to the plan default).
func cmdKeysGraceWindow(args []string) int {
	fs := flag.NewFlagSet("keys grace-window", flag.ContinueOnError)
	reset := fs.Bool("reset", false, "clear the per-account override (fall back to plan default)")
	days := fs.Int("days", -1, "new grace window in days (>=0)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		PrintUsage(os.Stderr, "usage: gregale keys grace-window [--reset | --days N]", "keys")
		return 1
	}
	if *reset && *days >= 0 {
		return printErr("Invalid flags", fmt.Errorf("--reset and --days are mutually exclusive"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if *days >= 0 {
		d := *days
		resp, err := client.SetGraceWindow(context.Background(), &d)
		if err != nil {
			return printErr("Set grace-window failed", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(resp))
		}
		PrintOK(osStdout, "Grace window set to %d days (plan default: %d).", *resp.Days, resp.PlanDefault)
		return 0
	}
	if *reset {
		resp, err := client.SetGraceWindow(context.Background(), nil)
		if err != nil {
			return printErr("Reset grace-window failed", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(resp))
		}
		PrintOK(osStdout, "Grace window cleared (plan default: %d days).", resp.PlanDefault)
		return 0
	}
	resp, err := client.GetGraceWindow(context.Background())
	if err != nil {
		return printErr("Get grace-window failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	if resp.Days == nil {
		PrintOK(osStdout, "Grace window: plan default (%d days)", resp.PlanDefault)
	} else {
		PrintOK(osStdout, "Grace window: %d days (plan default: %d)", *resp.Days, resp.PlanDefault)
	}
	return 0
}

// cmdUsage: dispatcher for `gregale usage [summary|daily|storage]`.
//
//	gregale usage                          → cmdUsageList     (per-app rows, current month)
//	gregale usage --month YYYY-MM          → cmdUsageList     (per-app rows, explicit month)
//	gregale usage summary                  → cmdUsageSummary  (account roll-up, current month)
//	gregale usage summary --month YYYY-MM  → cmdUsageSummary  (account roll-up, explicit month)
//	gregale usage daily [--day YYYY-MM-DD] → cmdUsageDaily    (per-(app, day) rollup, ADR-048 §5)
//	gregale usage storage [--day YYYY-MM-DD] → cmdUsageStorage (per-(app, day) snapshot+layer bytes, ADR-049 §B.3)
//
// Strict positional dispatch matches cmdCrons / cmdDomains / cmdKeys:
// an unknown positional returns 1 with `unknown usage subcommand "..."`.
// Flag-leading args (e.g. `--month`) are forwarded to cmdUsageList so
// the legacy `gregale usage --month YYYY-MM` invocation keeps working —
// the PR description's "back-compat" promise. Forwarding any flag-like
// arg to the leaf FlagSet also preserves its normal unknown-flag
// handling (cmdUsageList exits 1 on `--bogus`).
func cmdUsage(args []string) int {
	parent, _ := lookupCliCommand("usage")
	if len(args) == 0 {
		return cmdUsageList(nil)
	}
	if strings.HasPrefix(args[0], "-") {
		return cmdUsageList(args)
	}
	switch args[0] {
	case subSummary:
		return cmdUsageSummary(args[1:])
	case "daily":
		// Tier C: per-(app, day) usage rollup (ADR-048 §5).
		// Distinct from `usage summary` which aggregates the
		// whole month for billing.
		return cmdUsageDaily(args[1:])
	case "storage":
		// Tier C: per-(app, day) snapshot+layer byte rollup
		// (ADR-049 §B.3). Informational — not billed today.
		return cmdUsageStorage(args[1:])
	}
	PrintUsage(os.Stderr, "usage: gregale usage [--month YYYY-MM] | gregale usage summary [--month YYYY-MM] | gregale usage daily [--day YYYY-MM-DD] | gregale usage storage [--day YYYY-MM-DD]", "usage")
	fmt.Fprintf(os.Stderr, "unknown usage subcommand %q\n", args[0])
	sug, _ := suggestSubcommand(args[0], parent)
	maybeSuggestSub(sug)
	return 1
}

// cmdUsageList: GET /v1/usage?month=YYYY-MM. Defaults to the current
// month. Per-app rows (UsageResponse — AppID, MBSeconds, Requests).
//
// The wire shape is an ARRAY of UsageResponse objects — the OpenAPI
// spec, the server handler, the cross-language fixture, and the
// Node/Python SDKs all agree. See memory: getusage-wire-shape-mismatch.
// An empty month is a valid response (no traffic yet) and renders
// just the header row.
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
	rows, err := client.GetUsage(context.Background(), *month)
	if err != nil {
		return printErr("Request failed", err)
	}
	if jsonOutput {
		// NDJSON matches every other per-resource list (apps, instances,
		// crons, domains, keys, secrets, deployments). One object per line
		// is jq-friendly and streams. The prior single-array writeJSON
		// shape was tied to the broken single-struct decode that PR #439
		// didn't fix — switching to NDJSON aligns with the rest of the
		// CLI and removes that vestigial coupling.
		return jsonOut(writeNDJSON(rows))
	}
	if len(rows) == 0 {
		_, _ = fmt.Fprintf(osStdout, "No usage recorded for %s.\n", *month)
		return 0
	}
	_, _ = fmt.Fprintf(osStdout, "App — requests · GB-hours (included GB-h) · egress\n")
	for _, u := range rows {
		// ADR-046: tx_bytes (HTTP response bytes, gateway-side) and
		// net_tx_bytes (root-side vethHost interface bytes, includes
		// framing) are informational, NOT billed. Surfaced as a
		// trailing column so a customer can spot egress anomalies
		// without grepping --json. Only printed when at least one
		// counter is non-zero — most months most apps are 0 and the
		// trailing column is noise.
		if u.TXBytes > 0 || u.NetTxBytes > 0 {
			_, _ = fmt.Fprintf(osStdout, "%s — %d · %.3f (included %d) · egress %.3f GB (tx %.2f / net %.2f)\n",
				u.AppID, u.Requests, float64(u.MBSeconds)/3.6e6, u.IncludedGBHours,
				u.TotalEgressGB(),
				float64(u.TXBytes)/(1024*1024*1024),
				float64(u.NetTxBytes)/(1024*1024*1024))
			continue
		}
		_, _ = fmt.Fprintf(osStdout, "%s — %d · %.3f (included %d)\n", u.AppID, u.Requests, float64(u.MBSeconds)/3.6e6, u.IncludedGBHours)
	}
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
		PrintUsage(os.Stderr, "usage: gregale invoices [--month YYYY-MM] [--before C] [--limit N]", "invoices")
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
		_, _ = fmt.Fprintf(w, "\n  next page: gregale invoices --before %s\n", page.NextBefore)
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
	// Issue #279 / PR-B: per-month CPU-hours is informational —
	// not billed. Surfaced as a separate line so the customer
	// sees the measurement next to the billing total without
	// confusing the two.
	_, _ = fmt.Fprintf(w, "  %-*s %.6f CPU-hours\n", labelWidth, "CPU usage:", s.UsedCPUHours)
	// ADR-046: per-month egress is informational, NOT billed
	// (the future billing PR will pick the unit). Same shape as
	// CPU usage — a separate line, never folded into "Used:"
	// so the customer never confuses the two.
	_, _ = fmt.Fprintf(w, "  %-*s %.3f GB\n", labelWidth, "Egress:", s.UsedEgressGB)
}

func boolPtr(b bool) *bool { return &b }

// cmdConnect implements `gregale connect <service>`. Today only
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
		PrintUsage(os.Stderr, "usage: gregale connect github", "connect")
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

// cmdOpen implements `gregale open <slug>`. Looks up the app's URL via
// the v1 API and launches the OS browser. With --dashboard, opens
// the dashboard's app-detail page instead of the public URL.
//
// Subcommands (Tier A8.1):
//   - docs [--slug <slug>]
//     Opens docs.gregale.dev/cli/<slug> (or the top-level
//     docs.gregale.dev when no slug is given) in the default
//     browser. No API call needed — the docs site is the
//     canonical help surface and is reachable without an
//     authenticated session.
//
// The subcommand dispatch happens BEFORE the flag parse so the
// docs subcommand's own flags (--slug) don't collide with the
// parent `open` flags (--dashboard). The first positional arg
// selects the subcommand; everything after is forwarded.
func cmdOpen(args []string) int {
	// Subcommand dispatch: when the first positional is `docs`,
	// hand off to cmdOpenDocs with the remaining args.
	if len(args) > 0 && args[0] == "docs" {
		return cmdOpenDocs(args[1:])
	}
	fs := flag.NewFlagSet("open", flag.ContinueOnError)
	dash := fs.Bool("dashboard", false, "open the dashboard page instead of the live URL")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale open <slug> [--dashboard]", "open")
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
		// a 2 s deadline; if the response carries the cold-wake header
		// (see pkg/wire.WakeHeader), print the cold-start line
		// immediately, then wait up to 8 s total for the app to warm
		// before opening — the user would otherwise see a 502 from the
		// gateway. Probe errors collapse
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

// docsOpenTopic is the docs URL slug for the `open` command's
// own man page. The `open docs` subcommand (cmdOpenDocs below)
// does not surface a "Docs:" line because the user is already
// ON the docs surface; this constant is consumed by PrintUsage
// when the subcommand's flag parser fails.
const docsOpenTopic = "open"

// cmdOpenDocs implements `gregale open docs [--slug <slug>]`. Opens
// the customer-facing CLI docs in the default browser. The docs
// site is reachable without an authenticated session, so this
// subcommand does NOT call authedClient — a logged-out customer
// hitting `gregale open docs apps` (perhaps from a fresh shell)
// still gets the right page.
//
// Slug resolution:
//   - positional arg: `gregale open docs apps` → /cli/apps
//   - --slug flag:    `gregale open docs --slug queue` → /cli/queue
//   - both or neither is an error (mutually exclusive, but at
//     least one is required — opening the bare docs root would
//     be confusing; `gregale man` already covers that case).
//   - empty slug defaults to the docs root (docs.gregale.dev).
//
// The DocsTopic constant docsOpenTopic is exposed so the manifest
// entry below can pin the docs URL slug for the `open` command's
// own man page.
func cmdOpenDocs(args []string) int {
	fs := flag.NewFlagSet("open docs", flag.ContinueOnError)
	slugFlag := fs.String("slug", "", "docs page slug (e.g. apps, queue, deploy); opens the docs root when empty")
	if err := fs.Parse(args); err != nil {
		PrintUsage(os.Stderr, "usage: gregale open docs [<slug>] [--slug <slug>]", docsOpenTopic)
		return 1
	}
	slug := *slugFlag
	// Positional wins over --slug when both are given: the user
	// typed the slug directly, so the explicit position is more
	// intent-revealing than the flag.
	if fs.NArg() > 0 {
		if slug != "" && fs.Arg(0) != slug {
			PrintFail(os.Stderr, "conflicting slug: positional %q vs --slug %q", fs.Arg(0), slug)
			return 1
		}
		slug = fs.Arg(0)
	}
	if fs.NArg() > 1 {
		PrintFail(os.Stderr, "too many positional args (got %d, want 0 or 1)", fs.NArg())
		return 1
	}
	// Slug sanitization — the docs URL is rooted at /cli/<slug>,
	// so any non-path-safe character (slash, percent, etc.) is
	// stripped to '_' rather than silently percent-encoded. The
	// caller gets a path that cannot escape /cli/.
	safeSlug := sanitizeSlugForURL(slug)
	// sanitizeSlugForURL returns appSlugFallback ("app") for the
	// empty string — that's the dashboardAppURL contract (never
	// produce a bare /dashboard/apps/ path). For docs we want
	// the opposite: empty slug means "open the docs root", so
	// re-empty the slug here after sanitization if the caller
	// supplied no slug at all.
	if slug == "" {
		safeSlug = ""
	}
	var target string
	switch safeSlug {
	case "":
		// Top-level docs — strips the trailing /cli/ from
		// docsURLBase so the landing page renders.
		target = strings.TrimSuffix(docsURLBase, "/cli/")
	default:
		target = docsURLBase + safeSlug
	}
	if jsonOutput {
		// JSON path — emit the resolved URL and exit without
		// touching the browser. Scripting wrappers can pipe the
		// URL into a curl / xdg-open of their choice.
		return jsonOut(writeJSON(map[string]string{
			"url":  target,
			"slug": safeSlug,
		}))
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
// /dashboard/* from the same host. We use this so `gregale open` and
// `gregale connect` build a clickable URL the customer's browser can
// reach.
func dashboardBaseURL(api string) string {
	return strings.TrimRight(api, "/")
}

// dashboardAccountURL is the canonical "connect GitHub" entry point.
func dashboardAccountURL(api string) string {
	return dashboardBaseURL(api) + "/dashboard/account"
}

// dashboardStatelessURL is the customer-facing landing page for the
// stateless contract (Move 1 PR-A): the contract copy, the 8-base
// denylist, the 10 closed paths, and the account's 50 most recent
// stateless.advisory audit rows. Reached via `gregale dashboard --stateless`
// (commands5.go). Mirrors the apid route registered at
// /dashboard/stateless (handlers_dashboard.go:89).
func dashboardStatelessURL(api string) string {
	return dashboardBaseURL(api) + "/dashboard/stateless"
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
// bind (after `gregale deploy --repo` opens it). The dashboard reads
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
// (pkg/api/sse.go) so the same parser powers gregale logs, gregale tail,
// and gregale queue tail. signal.NotifyContext on os.Interrupt makes
// Ctrl-C tear down the in-flight request within ~50 ms instead of
// waiting for the body Close to be GC'd.
//
// Issue #309 (tier-2 DX): --grep, --since, --level pass through to
// the server as query params. Today apid's Move 3 stub accepts but
// does not yet act on them (Move 4 will filter against vmmd's
// per-instance ring buffer); the flags land now so the wire
// contract is stable.
//
// Issue #315 (tier-2 DX): `gregale logs tail <slug>` is a thin alias
// for `gregale logs <slug> --follow`. The inner-subcommand dispatch
// mirrors cmdQueueDispatch (commands5.go:715-729). `tail` is the only
// inner subcommand today; the switch leaves room for future siblings
// (e.g. `logs list` for batch tail of all app's deployments) without
// a wire-format break.
func cmdLogs(args []string) int {
	if len(args) > 0 && args[0] == subLogsTail {
		return cmdLogsTail(args[1:])
	}
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	follow := fs.Bool("follow", false, "follow new lines")
	deployment := fs.String("deployment", "", "deployment id (default: latest)")
	grep := fs.String("grep", "", "only show lines matching this substring")
	since := fs.String("since", "", "only show lines at or after this RFC3339 timestamp")
	level := fs.String("level", "", "only show lines at this level (info|warn|error)")
	if err := fs.Parse(args); err != nil {
		PrintUsage(os.Stderr, "usage: gregale logs <slug> [--follow] [--deployment ID] [--grep SUBSTR] [--since RFC3339] [--level info|warn|error]", "logs")
		return 1
	}
	if fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale logs <slug> [--follow] [--deployment ID] [--grep SUBSTR] [--since RFC3339] [--level info|warn|error]", "logs")
		return 1
	}
	// Validate --level early so a typo costs the customer a network
	// round-trip; --since is validated next so the SDK never sees a
	// malformed timestamp. Both call api.IsValidLogLevel / time.Parse
	// to share the wire contract with the apid handler (see
	// cmd/apid/handlers_ext.go::streamAppLogs), which re-validates
	// --level on the wire and rejects bad values with an
	// `event: error` SSE frame.
	if *level != "" && !api.IsValidLogLevel(*level) {
		PrintUsage(os.Stderr, "--level must be one of: info, warn, error", "logs")
		return 2
	}
	if *since != "" {
		if _, err := time.Parse(time.RFC3339, *since); err != nil {
			PrintUsage(os.Stderr, "--since must be an RFC3339 timestamp (e.g. 2026-07-28T00:00:00Z)", "logs")
			return 2
		}
	}
	return runLogs(context.Background(), fs.Arg(0), *deployment, api.LogFilter{
		Grep:  *grep,
		Since: *since,
		Level: *level,
	}, *follow)
}

// cmdLogsTail implements `gregale logs tail <slug>` — issue #315
// (tier-2 DX). Equivalent to `gregale logs <slug> --follow`; provided
// as a verb-form alias so muscle-memory keyboard shortcuts (Docker,
// kubectl, journalctl) work without translating to the long form.
//
// `--follow` is rejected explicitly: passing it is a no-op signal of
// confusion (the alias already implies follow) and silently ignoring
// it would mask a real customer mistake. All other logs flags pass
// through verbatim so the alias and the long form stay wire-equivalent.
func cmdLogsTail(args []string) int {
	fs := flag.NewFlagSet("logs tail", flag.ContinueOnError)
	follow := fs.Bool("follow", false, "follow new lines (alias always follows; flag is redundant)")
	deployment := fs.String("deployment", "", "deployment id (default: latest)")
	grep := fs.String("grep", "", "only show lines matching this substring")
	since := fs.String("since", "", "only show lines at or after this RFC3339 timestamp")
	level := fs.String("level", "", "only show lines at this level (info|warn|error)")
	if err := fs.Parse(args); err != nil {
		PrintUsage(os.Stderr, "usage: gregale logs tail <slug> [--deployment ID] [--grep SUBSTR] [--since RFC3339] [--level info|warn|error]", "logs")
		return 1
	}
	if fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale logs tail <slug> [--deployment ID] [--grep SUBSTR] [--since RFC3339] [--level info|warn|error]", "logs")
		return 1
	}
	if *follow {
		PrintFail(os.Stderr, "--follow is redundant with `logs tail` (alias always follows); drop the flag")
		return 2
	}
	if *level != "" && !api.IsValidLogLevel(*level) {
		PrintUsage(os.Stderr, "--level must be one of: info, warn, error", "logs")
		return 2
	}
	if *since != "" {
		if _, err := time.Parse(time.RFC3339, *since); err != nil {
			PrintUsage(os.Stderr, "--since must be an RFC3339 timestamp (e.g. 2026-07-28T00:00:00Z)", "logs")
			return 2
		}
	}
	return runLogs(context.Background(), fs.Arg(0), *deployment, api.LogFilter{
		Grep:  *grep,
		Since: *since,
		Level: *level,
	}, true)
}

// runLogs is the shared SSE pump behind `gregale logs` and `gregale
// logs tail`. It owns the auth round-trip, the signal-driven cancel,
// and the typed Decoder loop so both call sites stay byte-identical on
// the wire. Extracted from the original cmdLogs body during the
// issue #315 tail-alias refactor.
//
// Exits with 130 on Ctrl-C (shell SIGINT convention), 0 on a clean
// `event: end` or io.EOF, and surfaces a renderAPIError / printErr
// path on the auth or attach errors that precede the SSE loop.
func runLogs(ctx context.Context, slug, deployment string, filter api.LogFilter, follow bool) int {
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	body, err := client.StreamAppLogs(ctx, slug, deployment, follow, filter)
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
			// Move 4 (issue #254): the apid stub emits `event: degraded`
			// when schedd's StreamAppLogs RPC isn't wired yet (the
			// production-side path is a follow-up PR — this commit only
			// swaps the Move 3 stub for the real SSE shape on the apid
			// side). Move 3's `not_implemented` shape is dead code;
			// removed.
			if e.Event == "degraded" {
				fmt.Fprintln(os.Stderr, "Log stream degraded: the scheduler is temporarily unavailable")
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
		PrintWarn(os.Stderr, "stream unreachable; follow manually: gregale logs --deployment %s", dep.ID)
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
						PrintOK(osStdout, "Deployed. https://%s.apps.gregale.dev", dep.AppID)
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
			case streamEventError:
				PrintWarn(os.Stderr, "stream closed; follow manually: gregale logs --deployment %s", dep.ID)
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
			PrintWarn(os.Stderr, "stream closed; follow manually: gregale logs --deployment %s", dep.ID)
			return 3
		}
	}
	// Stream ended without a terminal frame — try one GetDeployment
	// poll so a fast build that raced the SSE open isn't reported as
	// "follow manually" when we actually have the answer.
	if final, ok := pollDeploymentFinal(c, dep); ok {
		return terminalExitForDeployment(final)
	}
	PrintWarn(os.Stderr, "stream ended without a terminal frame; follow manually: gregale logs --deployment %s", dep.ID)
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
		PrintOK(osStdout, "Deployed. https://%s.apps.gregale.dev", d.AppID)
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
		return "Build ran out of memory (2 GB limit). Try fewer/smaller dependencies, or upgrade for a larger build. Docs: https://docs.gregale.dev/build/limits#memory"
	case "timeout":
		return "Build exceeded 10 min. Docs: https://docs.gregale.dev/build/limits#timeout"
	case "infra":
		return "Our build system hiccuped — we've been alerted and requeued your build automatically."
	}
	return "Build failed: " + err
}

// cmdUsageDaily: GET /v1/usage/daily?day=YYYY-MM-DD. Per-(app, day)
// rollup (ADR-048 §5). Day is required by the server; we default
// to today UTC when omitted, matching the dashboard panel.
//
// Renders one row per app: <app_id> <day> <requests> <gb-hours>
// <egress GB>. The byte counters (tx_bytes, net_tx_bytes) follow
// ADR-046 — informational, not billed — and are rendered only when
// non-zero (matches cmdUsageList's trailing-column policy).
func cmdUsageDaily(args []string) int {
	fs := flag.NewFlagSet("usage-daily", flag.ContinueOnError)
	day := fs.String("day", "", "day (YYYY-MM-DD); default: today UTC")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *day == "" {
		*day = time.Now().UTC().Format("2006-01-02")
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.UsageDaily(context.Background(), *day)
	if err != nil {
		return printErr("Could not fetch daily usage", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	if len(resp.Items) == 0 {
		_, _ = fmt.Fprintf(os.Stdout, "No daily usage recorded for %s.\n", *day)
		return 0
	}
	for _, u := range resp.Items {
		gbh := float64(u.MBSeconds) / 3.6e6
		if u.TXBytes > 0 || u.NetTxBytes > 0 {
			txGB := float64(u.TXBytes) / (1024 * 1024 * 1024)
			netGB := float64(u.NetTxBytes) / (1024 * 1024 * 1024)
			fmt.Printf("%-36s %s %8d  %7.3f GB-h  egress %.3f GB (tx %.2f / net %.2f)\n",
				u.AppID, u.Day, u.Requests, gbh, txGB+netGB, txGB, netGB)
			continue
		}
		fmt.Printf("%-36s %s %8d  %7.3f GB-h\n", u.AppID, u.Day, u.Requests, gbh)
	}
	return 0
}

// cmdUsageStorage: GET /v1/usage/storage?day=YYYY-MM-DD. Per-(app,
// day) snapshot+layer byte rollup (ADR-049 §B.3). Informational
// only — not billed today. Renders one row per app: <app_id> <day>
// <snapshot MB> <layer MB> <total MB>.
func cmdUsageStorage(args []string) int {
	fs := flag.NewFlagSet("usage-storage", flag.ContinueOnError)
	day := fs.String("day", "", "day (YYYY-MM-DD); default: today UTC")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *day == "" {
		*day = time.Now().UTC().Format("2006-01-02")
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.StorageUsage(context.Background(), *day)
	if err != nil {
		return printErr("Could not fetch storage usage", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	if len(resp.Items) == 0 {
		_, _ = fmt.Fprintf(os.Stdout, "No storage rollup recorded for %s.\n", *day)
		return 0
	}
	for _, u := range resp.Items {
		fmt.Printf("%-36s %s snapshot=%6d MB  layer=%6d MB  total=%6d MB\n",
			u.AppID, u.Day,
			u.SnapshotBytes/(1024*1024),
			u.LayerBytes/(1024*1024),
			(u.SnapshotBytes+u.LayerBytes)/(1024*1024))
	}
	return 0
}

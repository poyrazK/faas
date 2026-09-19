package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/onebox-faas/faas/cmd/gregale/templates"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/browser"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/secretscan"
	"github.com/onebox-faas/faas/pkg/whycopy"
)

// Subcommand names — lifted to constants so goconst stops flagging the
// repeated "list"/"add"/"rm" string literals in the dispatch tables below.
const (
	subList    = "list"
	subAdd     = "add"
	subUpdate  = "update"
	subRm      = "rm"
	subRestore = "restore"
	subRuns    = "runs"
	// subRotate is reused across every resource's `… rotate …`
	// subcommand literal (host-age, keys, pki, secrets, sign-keys,
	// node-key, etc.) so goconst stops flagging the repeated
	// "rotate" string in the cli_meta.go manifest + the dispatch
	// switches in commands2/3.go. Per-resource dispatch sites that
	// want stronger typing keep their own name-spaced const
	// (subHostAgeRotate / subPKIRotate / etc.).
	subRotate  = "rotate"
	subSummary = "summary"
	// subLogsTail is the inner-subcommand name for `gregale logs
	// tail <slug>` (issue #315 / tier-2 DX). Lifted from the
	// inline literal at commands2.go:1719 + main.go:252 so goconst
	// stops flagging the three occurrences (two source +
	// PrintUsage doc line).
	subLogsTail = "tail"
	subInfo     = "info"
	subGet      = "get"
	subCreate   = "create"
	// Issue #961 / Mega-A PR-3: domains surface verbs. Lifted from
	// inline literals so goconst stops flagging the "verify" /
	// "show" / "set-default" strings in cli_meta.go + the dispatch
	// switch in commands2.go:cmdDomains.
	subDomainsSetDefault = "set-default"
	subDomainsVerify     = "verify"
	subDomainsShow       = "show"
	subDomainsStatus     = "status"
	subDomainsDoctor     = "doctor"

	statusPending  = "pending"
	statusVerified = "verified"

	// doctor check status tokens (ADR-120). Mirrors the stable
	// `name` enum on pkg/api.DomainDoctorCheck so the CLI's
	// filter / branch logic doesn't inline raw literals (goconst).
	doctorCheckOK   = "ok"
	doctorCheckFail = "fail"
	doctorCheckPend = "pending"
	doctorCheckNA   = "na"

	// service names reused across cmdConnect + the usage hint
	// (commands2.go) so goconst stops flagging them.
	svcGithub = "github"
	// svcRepo is the new Mega-B PR-1 `connect repo` subcommand
	// selector. Lives alongside svcGithub so the dispatcher's
	// error message can list both options, and so goconst stops
	// flagging the "repo" string at the dispatch + the new
	// commands_connect_repo.go handler.
	svcRepo = "repo"

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

	// Build status enum values from /v1/builds/{id} (DEPLOY-PROV-6
	// / ADR-089, issue #741). 4-state enum per schema.sql CHECK
	// constraint — matches BuildStatus constants in pkg/state.
	// Lifted out so the SSE polling fallback in streamDeployLogs +
	// terminalExitForBuild don't trip goconst when the same
	// string appears 3+ times across the file.
	buildStatusSucceeded = "succeeded"
	buildStatusFailed    = "failed"
	buildStatusCancelled = "cancelled"

	// Deployment status enum value (DEPLOY-PROV-6 sibling).
	// Lifted out so the SSE decoder branch + pollDeploymentFinal
	// + terminalExitForDeployment don't trip goconst once
	// buildStatusFailed exists — goconst cross-file matching is
	// by literal value, so we need two name-spaced constants even
	// though they're the same string semantically. (The build
	// status enum is a different 4-state set with `succeeded`/`failed`
	// vs deployment's `live`/`failed`.)
	deploymentStatusFailed     = "failed"
	deploymentStatusCancelled  = "cancelled"
	deploymentStatusSuperseded = "superseded"

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

	// Managed PostgreSQL is a customer-facing resource backed by the
	// provider-neutral API. Keep the noun in one place so the dispatcher,
	// completion metadata, and tests cannot drift.
	dispatchPostgres = "postgres"

	// Singular deployment-get. Lifted so the dispatch literal stays
	// constant-named (goconst); the constant does NOT route through
	// appSlugFallback — the dispatch table places it before the
	// "app" case so `gregale deployment <id>` is never read as an app slug.
	dispatchDeployment = "deployment"

	// Read-only operator verb (issue #952). Routes to cmdInspect
	// (commands_inspect.go), which owns the verb-level FlagSet +
	// slug validation and dispatches to the per-leaf file
	// (commands_inspect_upstreams.go for v1).
	dispatchInspect = "inspect"

	// Read-only post-stream drill-down verb (ADR-117 companion).
	// Distinct from dispatchDeployment (which is the singular GET
	// with --show-scan / --show-secret-scan drill-downs and
	// set-min-instances) and dispatchDeployments (which is the
	// paginated list). `deploys` is the noun-form cluster — today
	// it has two subcommands, `show <id>` and `status <id>`, which
	// read the closed 6-stage state column via GET
	// /v1/deployments/{id}/stages (and, for status, GET
	// /v1/deployments/{id} for the footer timestamp).
	dispatchDeploys = "deploys"

	// Error-explanations cluster (spec §6.4 amendment 1):
	// customer preflight that scans the local cwd for the 8
	// source-side failure modes the cluster's runtime detectors
	// catch post-deploy (commit 7-13). No auth required. Routes
	// to commands_doctor.go::cmdDoctor.
	dispatchDoctor = "doctor"
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
		PrintUsage(os.Stderr, "usage: gregale app <slug> [--visibility public|internal] [--profile micro|small|medium|large|xlarge] [--ram N] [--cpu-millicores 250|500|1000] [--max-concurrency N] [--concurrency-overflow queue|drop] [--max-queue-wait-ms N] [--wake-max-queue-depth N] [--wake-max-queue-wait-seconds N] [--idle SEC] [--min N] [--autoscale-target-rps N] [--autoscale-target-cpu-pct N] [--warm-snapshot] [--no-warm-snapshot] [--warm-snapshot-min-requests N] [--warm-snapshot-min-ms N] [--concurrency] [--require-authn] [--no-require-authn] [--head-wakes[=true|false]] [--crawler-policy wake|cached|block] [--health-path PATH] [--health-path-wakes] [--no-health-path-wakes] [--public-auth MODE] [--basic-user USER --basic-pass PASS] [--app-protocol http1|http2|grpc]", "apps")
		return 1
	}
	slug := args[0]
	fs := newFlagSet("app", flag.ContinueOnError)
	visibility := fs.String("visibility", "", "set public edge exposure: public|internal (Pro/Scale only for internal)")
	ram := fs.Int("ram", 0, "update RAM (MB)")
	cpuMillicores := fs.Int("cpu-millicores", 0, "update sustained CPU allowance (250, 500, or 1000 millicores)")
	profile := fs.String("profile", "", "update named resource profile: micro|small|medium|large|xlarge")
	conc := fs.Int("max-concurrency", 0, "update max concurrent requests")
	concurrencyOverflow := fs.String("concurrency-overflow", "", "saturated concurrency behavior: queue|drop")
	maxQueueWaitMS := fs.Int("max-queue-wait-ms", 0, "maximum queued concurrency wait in milliseconds (0 = plan default)")
	wakeMaxQueueDepth := fs.Int("wake-max-queue-depth", 0, "per-app cold-wake waiter cap (0 = plan default)")
	wakeMaxQueueWaitSeconds := fs.Int("wake-max-queue-wait-seconds", 0, "per-app cold-wake wait budget in seconds (0 = plan default, max 60)")
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
	noRequireAuthn := fs.Bool("no-require-authn", false, "drop the token requirement and open the public URL unless --public-auth is also set")
	// Only-allow-declared-routes is a plan-agnostic pre-wake gate. The
	// positive/negative pair mirrors require-authn: explicit false is useful
	// when temporarily rolling back a contract without deleting the document.
	onlyDeclaredRoutes := fs.Bool("only-declared-routes", false, "reject paths not declared by the OpenAPI document (or explicit route list) before wake")
	noOnlyDeclaredRoutes := fs.Bool("no-only-declared-routes", false, "disable the declared-route pre-wake gate")
	headWakes := fs.Bool("head-wakes", false, "wake a parked app for HEAD / instead of using the cached edge answer")
	crawlerPolicy := fs.String("crawler-policy", "", "known monitor/crawler policy: wake|cached|block")
	healthPath := fs.String("health-path", "", "monitor-facing health path (default /healthz)")
	healthPathWakes := fs.Bool("health-path-wakes", false, "allow health probes to wake the app (Pro/Scale only)")
	noHealthPathWakes := fs.Bool("no-health-path-wakes", false, "answer health probes at the edge without waking")
	// ADR-124: per-app wire-protocol selector. Single string
	// flag (closed set {http1, http2, grpc}) — empty value
	// means "use the per-plan default" (http1 universal). The
	// server validates the closed set + the per-plan gate
	// (Free + grpc = 403 plan_app_protocol_grpc_not_allowed).
	// No positive/negative pair because the closed set is
	// already a sentinel-friendly enum (unlike the bool pair
	// for require_authn).
	appProtocol := fs.String("app-protocol", "", "wire-protocol selector: http1|http2|grpc (omit to use server default)")
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
	// Tier A10 / ADR-088: per-app overflow_node preference.
	// The CLI takes the operator-supplied compute_nodes.name
	// (the human-readable label) — apid resolves to UUID
	// server-side. Empty string = clear the preference; non-
	// empty = set. fs.Visit (below) distinguishes "flag not
	// passed" (don't touch the column) from "flag passed with
	// empty value" (explicit clear). The mirror on the wire is
	// the `req.OverflowNode *string` pointer — same tri-state
	// contract as EvictionPriority.
	overflowNode := fs.String("overflow-node", "", "preferred overflow compute_node name (Tier A10; server resolves to UUID; '' clears)")
	if err := fs.Parse(args[1:]); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
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
	if *onlyDeclaredRoutes && *noOnlyDeclaredRoutes {
		return printErr("Invalid flags", fmt.Errorf("--only-declared-routes and --no-only-declared-routes are mutually exclusive"))
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
		if *ram <= 0 {
			return printErr("Invalid --ram", fmt.Errorf("must be greater than zero; got %d", *ram))
		}
		v := *ram
		req.RAMMB = &v
	}
	if explicit["visibility"] {
		v := *visibility
		if !api.AppVisibility(v).Valid() {
			return printErr("Invalid --visibility", fmt.Errorf("must be 'public' or 'internal'; got %q", v))
		}
		req.Visibility = &v
	}
	if explicit["cpu-millicores"] {
		v := *cpuMillicores
		req.CPUMillicores = &v
	}
	if explicit["profile"] {
		req.ResourceProfile = profile
	}
	if explicit["max-concurrency"] {
		v := *conc
		req.MaxConcurrency = &v
	}
	if explicit["concurrency-overflow"] || explicit["max-queue-wait-ms"] || explicit["wake-max-queue-depth"] || explicit["wake-max-queue-wait-seconds"] {
		policy, err := cliScalingPolicyPatchWithWake(ctx, client, slug, *concurrencyOverflow, *maxQueueWaitMS, explicit["concurrency-overflow"], explicit["max-queue-wait-ms"], *wakeMaxQueueDepth, *wakeMaxQueueWaitSeconds, explicit["wake-max-queue-depth"], explicit["wake-max-queue-wait-seconds"])
		if err != nil {
			return printErr("Invalid concurrency policy", err)
		}
		req.ScalingPolicy = policy
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
		// Secure-by-default paid plans also default public_auth to
		// bearer. Disabling only require_authn would therefore leave the
		// public URL returning 401, despite this flag promising a public
		// app. Keep an explicitly selected --public-auth mode authoritative.
		if !explicit["public-auth"] {
			req.PublicAuth = &api.PublicAuthBlock{Mode: api.AppPublicAuthModeOpen}
		}
	}
	if explicit["only-declared-routes"] {
		v := true
		req.OnlyAllowDeclaredRoutes = &v
	}
	if explicit["no-only-declared-routes"] {
		v := false
		req.OnlyAllowDeclaredRoutes = &v
	}
	if explicit["head-wakes"] {
		v := *headWakes
		req.HeadWakes = &v
	}
	if explicit["crawler-policy"] {
		v := *crawlerPolicy
		switch v {
		case api.CrawlerPolicyWake, api.CrawlerPolicyCached, api.CrawlerPolicyBlock:
		default:
			return printErr("Invalid --crawler-policy", fmt.Errorf("must be 'wake', 'cached', or 'block'; got %q", v))
		}
		req.CrawlerPolicy = &v
	}
	if *healthPathWakes && *noHealthPathWakes {
		return printErr("Invalid flags", fmt.Errorf("--health-path-wakes and --no-health-path-wakes are mutually exclusive"))
	}
	if explicit["health-path"] {
		v := *healthPath
		req.HealthPath = &v
	}
	if explicit["health-path-wakes"] {
		v := true
		req.HealthPathWakes = &v
	}
	if explicit["no-health-path-wakes"] {
		v := false
		req.HealthPathWakes = &v
	}
	// ADR-124: per-app wire-protocol selector. Validate the
	// closed set locally so a typo surfaces as a usage error
	// before the round-trip (the apid side returns the same
	// 400 app_protocol_invalid but with less context). Empty
	// value = "use server default" (omits the field so the
	// per-plan default applies).
	if explicit["app-protocol"] {
		v := *appProtocol
		if !api.IsValidAppProtocol(v) {
			return printErr("Invalid --app-protocol",
				fmt.Errorf("must be 'http1', 'http2', or 'grpc'; got %q", v))
		}
		req.AppProtocol = &v
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
	// Tier A10 / ADR-088: per-app overflow_node preference.
	// The fs.Visit branch distinguishes "flag not passed" (nil
	// pointer → don't touch the column) from "flag passed with
	// empty value" (pointer to "" → explicit clear) from
	// "flag passed with a value" (pointer to name → resolve
	// server-side). The empty-string form is a deliberate
	// CLI affordance — operators drain a node by deleting the
	// preference rather than waiting for the FK ON DELETE
	// SET NULL to land on the row.
	if explicit["overflow-node"] {
		v := *overflowNode
		req.OverflowNode = &v
	}

	if req.RAMMB == nil && req.CPUMillicores == nil && req.ResourceProfile == nil && req.MaxConcurrency == nil && req.IdleTimeoutS == nil && req.MinInstances == nil &&
		req.AutoscaleTargetRPS == nil && req.AutoscaleTargetCPUPct == nil &&
		req.WarmSnapshotEnabled == nil && req.WarmSnapshotMinRequests == nil && req.WarmSnapshotMinMs == nil &&
		req.EvictionPriority == nil && req.RequireAuthn == nil && req.PublicAuth == nil &&
		req.OverflowNode == nil && req.AppProtocol == nil && req.Visibility == nil && req.OnlyAllowDeclaredRoutes == nil && req.HeadWakes == nil && req.CrawlerPolicy == nil && req.HealthPath == nil && req.HealthPathWakes == nil && req.ScalingPolicy == nil {
		a, err := client.GetApp(ctx, slug)
		if err != nil {
			return printErr("Could not fetch app", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(a))
		}
		fmt.Printf("%-30s %s\n", "slug:", a.Slug)
		fmt.Printf("%-30s %s\n", "url:", a.URL)
		fmt.Printf("%-30s %s\n", "visibility:", api.NormalizeAppVisibility(api.AppVisibility(a.Visibility)))
		fmt.Printf("%-30s %d MB\n", "ram:", a.RAMMB)
		fmt.Printf("%-30s %d\n", "guest vcpu:", a.VCPU)
		fmt.Printf("%-30s %d mCPU\n", "cpu:", a.CPUMillicores)
		if a.ResourceProfile != "" {
			fmt.Printf("%-30s %s\n", "resource profile:", a.ResourceProfile)
		}
		fmt.Printf("%-30s %d\n", "max concurrency:", a.MaxConcurrency)
		if a.ScalingPolicy == nil || a.ScalingPolicy.ConcurrencyOverflow == "" {
			fmt.Printf("%-30s %s\n", "concurrency overflow:", api.ConcurrencyOverflowQueue)
		} else {
			fmt.Printf("%-30s %s\n", "concurrency overflow:", a.ScalingPolicy.ConcurrencyOverflow)
		}
		if a.ScalingPolicy != nil && a.ScalingPolicy.MaxQueueWaitMS > 0 {
			fmt.Printf("%-30s %d ms\n", "max queue wait:", a.ScalingPolicy.MaxQueueWaitMS)
		} else {
			fmt.Printf("%-30s %s\n", "max queue wait:", "plan default")
		}
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
		if l := a.EffectiveLimits; l.MemoryLimitMB > 0 {
			fmt.Printf("%-30s %d MB (plan max %d MB)\n", "effective memory:", l.MemoryLimitMB, l.PlanMemoryMaxMB)
			fmt.Printf("%-30s %d visible, %dm sustained\n", "effective cpu:", l.GuestVCPUs, l.CPULimitMillicores)
			fmt.Printf("%-30s %d\n", "cpu scheduling weight:", l.CPUWeight)
			fmt.Printf("%-30s %d instances × %d requests\n", "effective scaling:", l.MaxInstances, l.ConcurrencyPerInstance)
			fmt.Printf("%-30s %d rps (burst %d)\n", "app request rate:", l.AppRequestRateRPS, l.AppRequestBurst)
			fmt.Printf("%-30s %d rpm across apps\n", "account request rate:", l.AccountRequestRateRPM)
			fmt.Printf("%-30s %dms default, %dms max\n", "request budget:", l.RequestBudgetMS, l.RequestBudgetMaxMS)
			fmt.Printf("%-30s %ds\n", "response write timeout:", l.ResponseWriteTimeoutS)
			if l.RequestBodyMaxBytes > 0 {
				fmt.Printf("%-30s %d bytes (%d MiB)\n", "request body cap:", l.RequestBodyMaxBytes, l.RequestBodyMaxBytes/(1024*1024))
			}
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
		fmt.Printf("%-30s %s\n", "crawler policy:", a.Manifest.EffectiveCrawlerPolicy())
		fmt.Printf("%-30s %s\n", "health path:", a.Manifest.HealthPath)
		fmt.Printf("%-30s %t\n", "health path wakes:", a.Manifest.HealthPathWakes)
		if a.OnlyAllowDeclaredRoutes {
			fmt.Printf("%-30s %s\n", "only declared routes:", "enabled")
		} else {
			fmt.Printf("%-30s %s\n", "only declared routes:", "disabled")
		}
		// Tier A10 / ADR-088: surface the resolved overflow_node
		// preference (the UUID apid returns) so the customer can
		// verify their PATCH round-tripped. nil on the wire means
		// "no preference" — render the A9 fallback label so the
		// CLI output stays self-documenting (the customer doesn't
		// need to read ADR-088 to know what "no overflow_node"
		// means in practice).
		if a.OverflowNode == nil || *a.OverflowNode == "" {
			fmt.Printf("%-30s %s\n", "overflow node:", "none (A9 fallback)")
		} else {
			fmt.Printf("%-30s %s\n", "overflow node:", *a.OverflowNode)
		}
		fmt.Printf("%-30s %s\n", "status:", a.Status)
		// Issue #1053: the API already returns the trailing 30-day
		// cache hit-rate rollup. Keep it visible in the default app
		// view so developers can tell whether repeat deploys are
		// benefiting from the builder cache without switching to JSON.
		fmt.Printf("%-30s %.1f%% (last 30d)\n", "build cache hit rate:", a.BuildCacheHitRatePct)
		// Issue #1395 / A4: show a best-effort wake-tier recommendation
		// only for apps with enough recent wake history. JSON output stays
		// a stable AppResponse payload, so this is text-mode only.
		renderWakeRecommendation(ctx, client, slug, a)
		return 0
	}

	updated, err := client.UpdateApp(ctx, slug, req)
	if err != nil {
		return printErr("Update failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(updated))
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
// On the interactive path (no -q) the user must retype the slug
// verbatim (issue #312) so a stray `y` cannot delete the app.

func cmdAppsRm(args []string) int {
	fs := newFlagSet("apps-rm", flag.ContinueOnError)
	quiet := fs.Bool("q", false, "suppress confirmation prompt")
	fs.BoolVar(quiet, "quiet", false, "suppress confirmation prompt")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale apps [-q|--quiet] <slug>", "apps")
		return 1
	}
	slug := fs.Arg(0)
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if !*quiet {
		fmt.Fprintf(os.Stderr, "Delete %q and all its deployments?\n", slug)
		if !requireTyped(slug) {
			return 1
		}
	}
	if err := client.DeleteApp(context.Background(), slug); err != nil {
		return printErr("Delete failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]any{
			"slug":    slug,
			"status":  "deleted",
			"deleted": true,
		}))
	}
	PrintOK(osStdout, "Deleted %s", slug)
	return 0
}

// cmdAppsRestore implements `gregale apps restore <slug>`.
func cmdAppsRestore(args []string) int {
	if len(args) != 1 {
		PrintUsage(os.Stderr, "usage: gregale apps restore <slug>", "apps")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	app, err := client.RestoreApp(context.Background(), args[0])
	if err != nil {
		return printErr("Restore failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(app))
	}
	PrintOK(osStdout, "Restored %s", app.Slug)
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
func buildCreateRequest(slug string, sh shape, runtime string, requireAuthnPtr *bool, appProtocolPtr *string, resourceProfile ...string) api.CreateAppRequest {
	req := api.CreateAppRequest{
		Slug:         slug,
		RequireAuthn: requireAuthnPtr,
		AppProtocol:  appProtocolPtr,
	}
	if len(resourceProfile) > 0 {
		req.ResourceProfile = resourceProfile[0]
	}
	if sh == shapeFunction {
		req.Type = "function"
		if runtime != "" {
			req.Runtime = runtime
		}
	}
	return req
}

// validateDeploySourceSelection keeps the source transport explicit. Deploy
// accepts zero selectors for the local zero-config path, or exactly one of the
// explicit selectors below. A ref is meaningful only for the repository
// transport. Run this before authentication or source I/O so a malformed CI
// invocation cannot silently deploy different bytes.
func validateDeploySourceSelection(sourcePath string, worktree bool, image, archive, repo, templateName string, githubSnippet bool, ref string) error {
	var selected []string
	if sourcePath != "" || worktree {
		if sourcePath != "" {
			selected = append(selected, "--path")
		} else {
			selected = append(selected, "--worktree")
		}
	}
	if image != "" {
		selected = append(selected, "--image")
	}
	if archive != "" {
		selected = append(selected, "--tarball")
	}
	if repo != "" {
		selected = append(selected, "--repo")
	}
	if templateName != "" {
		selected = append(selected, "--template")
	}
	if githubSnippet {
		selected = append(selected, "--github")
	}
	if ref != "" && repo == "" {
		return errors.New("--ref requires --repo")
	}
	if len(selected) > 1 {
		return fmt.Errorf("source selectors are mutually exclusive: %s", strings.Join(selected, ", "))
	}
	return nil
}

func validateRepoDeployFlags(explicit map[string]bool) error {
	var unsupported []string
	for _, name := range []string{
		"function", "app", "runtime", "handler", "dockerfile", "vcpu",
		"require-authn", "no-require-authn", "app-protocol",
		"execution-mode", "restart-policy", "startup-deadline-s", "max-retries",
		"doctor-strict", "no-doctor", "secret-scan",
	} {
		if explicit[name] {
			unsupported = append(unsupported, "--"+name)
		}
	}
	if len(unsupported) == 0 {
		return nil
	}
	return fmt.Errorf("unsupported with --repo: %s", strings.Join(unsupported, ", "))
}

func validateExplicitDockerfile(sourceDir string) error {
	if sourceDir == "" {
		return errors.New("--dockerfile requires a local, tarball, or template source containing Dockerfile")
	}
	info, err := os.Stat(filepath.Join(sourceDir, "Dockerfile"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return errors.New("--dockerfile was set but Dockerfile was not found at the selected source root")
		}
		return fmt.Errorf("inspect Dockerfile: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("dockerfile at the selected source root must be a regular file")
	}
	return nil
}

func validateDeployLifecycleFlags(executionMode, restartPolicy string, startupDeadlineS, maxRetries int) error {
	manifest := api.AppManifest{
		ExecutionMode:    executionMode,
		RestartPolicy:    restartPolicy,
		StartupDeadlineS: startupDeadlineS,
		MaxRetries:       maxRetries,
	}
	return manifest.ValidateLifecyclePlan(api.PlanScale)
}

func applyDeployLifecycleToCreateRequest(req *api.CreateAppRequest, executionMode, restartPolicy string, startupDeadlineS, maxRetries int) {
	if req == nil {
		return
	}
	req.ExecutionMode = executionMode
	req.RestartPolicy = restartPolicy
	req.StartupDeadlineS = startupDeadlineS
	req.MaxRetries = maxRetries
}

// materializeCommittedGitSource builds the HEAD archive selected by the
// zero-config path, snapshots it, and returns the matching extracted source
// view. Callers must use sourceDir for every source-derived decision and
// archivePath for the eventual upload.
func materializeCommittedGitSource(prov zeroConfigProvenance, selectedSourceDir, sourceRoot, sourcePath, workspaceContextRoot string) (archivePath, sourceDir string, cleanup func(), err error) {
	tmpFile, err := os.CreateTemp("", "gregale-git-head-*.tar.gz")
	if err != nil {
		return "", "", nil, fmt.Errorf("could not create temp tarball: %w", err)
	}
	tmpPath := tmpFile.Name()
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", "", nil, fmt.Errorf("could not close temp tarball: %w", err)
	}
	defer func() { _ = os.Remove(tmpPath) }()

	switch {
	case workspaceContextRoot != "":
		err = gitArchiveHEAD(prov.Root, tmpPath)
	case sourcePath == "":
		err = gitArchiveHEAD(prov.Root, tmpPath)
	default:
		var relPath string
		relPath, err = gitRelativePath(prov.Root, selectedSourceDir)
		if err == nil {
			err = gitArchiveHEADPath(prov.Root, relPath, tmpPath)
		}
	}
	if err != nil {
		return "", "", nil, err
	}

	archivePath, archiveRoot, cleanup, err := materializeDeployArchive(tmpPath)
	if err != nil {
		return "", "", nil, err
	}
	sourceDir = archiveRoot
	if sourceRoot != "" {
		sourceDir = filepath.Join(archiveRoot, filepath.FromSlash(sourceRoot))
		info, statErr := os.Stat(sourceDir)
		if statErr != nil || !info.IsDir() {
			cleanup()
			if statErr != nil {
				return "", "", nil, fmt.Errorf("committed source root %q: %w", sourceRoot, statErr)
			}
			return "", "", nil, fmt.Errorf("committed source root %q is not a directory", sourceRoot)
		}
	}
	return archivePath, sourceDir, cleanup, nil
}

// incompatibleCreateOnlyFlags enforces an explicit allowlist for the mode
// that reserves app metadata without creating a deployment. Any option whose
// only destination is a deployment must fail instead of being silently lost.
func incompatibleCreateOnlyFlags(explicit map[string]bool) []string {
	allowed := map[string]struct{}{
		"create-only": {}, "name": {}, "template": {}, "path": {}, "worktree": {},
		"function": {}, "app": {}, "runtime": {}, "handler": {}, "profile": {},
		"vcpu": {}, "require-authn": {}, "no-require-authn": {}, "app-protocol": {},
		"execution-mode": {}, "restart-policy": {}, "startup-deadline-s": {}, "max-retries": {},
		"json": {},
	}
	var incompatible []string
	for name := range explicit {
		if _, ok := allowed[name]; !ok {
			incompatible = append(incompatible, "--"+name)
		}
	}
	sort.Strings(incompatible)
	return incompatible
}

// createOrFetchApp resolves an owned app before reserving a new slot. This
// makes redeploy independent of create-admission ordering when the account is
// already at its app cap. A missing app still falls through to CreateApp; a
// 409 then retries the lookup once to cover a concurrent same-account create.
func createOrFetchApp(ctx context.Context, client *Client, req api.CreateAppRequest, requireAuthnPtr *bool, appProtocolPtr *string, publicAuthPtr *api.PublicAuthBlock) error {
	existing, err := client.GetApp(ctx, req.Slug)
	if err == nil {
		return configureExistingApp(ctx, client, existing, req, requireAuthnPtr, appProtocolPtr, publicAuthPtr)
	}
	var ae *APIError
	if !errors.As(err, &ae) || ae.Problem.Status != http.StatusNotFound {
		return err
	}
	if _, err = client.CreateApp(ctx, req); err == nil {
		if publicAuthPtr != nil {
			_, err = client.UpdateApp(ctx, req.Slug, api.UpdateAppRequest{PublicAuth: publicAuthPtr})
		}
		return err
	}
	if !errors.As(err, &ae) || ae.Problem.Status != http.StatusConflict {
		return err
	}
	existing, err = client.GetApp(ctx, req.Slug)
	if err != nil {
		return fmt.Errorf("slug %q is already in use; pick a different --name", req.Slug)
	}
	return configureExistingApp(ctx, client, existing, req, requireAuthnPtr, appProtocolPtr, publicAuthPtr)
}

func configureExistingApp(ctx context.Context, client *Client, existing api.AppResponse, req api.CreateAppRequest, requireAuthnPtr *bool, appProtocolPtr *string, publicAuthPtr *api.PublicAuthBlock) error {
	requestedType := req.Type
	if requestedType == "" {
		requestedType = "app"
	}
	if _, problem := api.ValidateExistingAppShape(existing.Type, existing.Runtime, requestedType, req.Runtime); problem != nil {
		problem.Detail = fmt.Sprintf("app %q: %s", req.Slug, problem.Detail)
		return &api.APIError{Problem: *problem}
	}
	if requireAuthnPtr == nil && appProtocolPtr == nil && publicAuthPtr == nil && req.ResourceProfile == "" &&
		req.ExecutionMode == "" && req.RestartPolicy == "" && req.StartupDeadlineS == 0 && req.MaxRetries == 0 && req.ServiceReplicas == nil {
		return nil
	}
	upd := api.UpdateAppRequest{RequireAuthn: requireAuthnPtr, PublicAuth: publicAuthPtr, AppProtocol: appProtocolPtr}
	if req.ExecutionMode != "" {
		value := req.ExecutionMode
		upd.ExecutionMode = &value
	}
	if req.RestartPolicy != "" {
		value := req.RestartPolicy
		upd.RestartPolicy = &value
	}
	if req.StartupDeadlineS != 0 {
		value := req.StartupDeadlineS
		upd.StartupDeadlineS = &value
	}
	if req.MaxRetries != 0 {
		value := req.MaxRetries
		upd.MaxRetries = &value
	}
	if req.ServiceReplicas != nil {
		upd.ServiceReplicas = req.ServiceReplicas
	}
	if req.ResourceProfile != "" {
		profile := req.ResourceProfile
		upd.ResourceProfile = &profile
	}
	_, err := client.UpdateApp(ctx, req.Slug, upd)
	return err
}

// manifestCronClient is the narrow surface deployManifestTriggers
// reads from the SDK. Splitting it lets the unit test inject a
// recording fake (cmd/gregale/manifest_test.go) without an httptest
// server and without coupling the test to the SDK's full method
// surface. *api.Client satisfies it by structural typing — no
// adapter required at the call site.
type manifestCronClient interface {
	ListCrons(ctx context.Context, slug string) ([]api.CronResponse, error)
	CreateCron(ctx context.Context, slug string, req api.CreateCronRequest) (api.CronResponse, error)
	UpdateCron(ctx context.Context, id string, req api.UpdateCronRequest) (api.CronResponse, error)
	DeleteCron(ctx context.Context, id string) error
	Whoami(ctx context.Context) (api.AccountResponse, error)
}

type manifestQueueBindingClient interface {
	ListQueueBindings(ctx context.Context, slug string) ([]api.QueueBindingResponse, error)
	CreateQueueBinding(ctx context.Context, slug string, req api.CreateQueueBindingRequest) (api.QueueBindingResponse, error)
	UpdateQueueBinding(ctx context.Context, slug, id string, req api.UpdateQueueBindingRequest) (api.QueueBindingResponse, error)
	DeleteQueueBinding(ctx context.Context, slug, id string) error
}

// manifestScalingClient is the narrow surface used to apply the app-level
// scaling declaration after a deployment is accepted. Keeping it separate
// from the cron seam makes the manifest helpers easy to exercise with small
// fakes and avoids coupling trigger tests to app PATCH behavior.
type manifestScalingClient interface {
	GetApp(ctx context.Context, slug string) (api.AppResponse, error)
	UpdateApp(ctx context.Context, slug string, req api.UpdateAppRequest) (api.AppResponse, error)
}

// cliScalingPolicyPatch builds a complete replacement policy for the public
// PATCH shape. The API intentionally treats scaling_policy as a replacement,
// so the CLI must read and preserve existing fields before changing only the
// concurrency admission knobs.
func cliScalingPolicyPatch(ctx context.Context, client interface {
	GetApp(context.Context, string) (api.AppResponse, error)
}, slug, overflow string, maxQueueWaitMS int, setOverflow, setMaxQueueWait bool) (*api.ScalingPolicy, error) {
	return cliScalingPolicyPatchWithWake(ctx, client, slug, overflow, maxQueueWaitMS, setOverflow, setMaxQueueWait, 0, 0, false, false)
}

func cliScalingPolicyPatchWithWake(ctx context.Context, client interface {
	GetApp(context.Context, string) (api.AppResponse, error)
}, slug, overflow string, maxQueueWaitMS int, setOverflow, setMaxQueueWait bool, wakeMaxQueueDepth, wakeMaxQueueWaitSeconds int, setWakeDepth, setWakeWait bool) (*api.ScalingPolicy, error) {
	if setOverflow && overflow != api.ConcurrencyOverflowQueue && overflow != api.ConcurrencyOverflowDrop {
		return nil, fmt.Errorf("--concurrency-overflow must be %q or %q; got %q", api.ConcurrencyOverflowQueue, api.ConcurrencyOverflowDrop, overflow)
	}
	if setMaxQueueWait && (maxQueueWaitMS < 0 || maxQueueWaitMS > api.MaxConcurrencyQueueWaitMS) {
		return nil, fmt.Errorf("--max-queue-wait-ms must be between 0 and %d; got %d", api.MaxConcurrencyQueueWaitMS, maxQueueWaitMS)
	}
	if setWakeDepth && wakeMaxQueueDepth < 0 {
		return nil, fmt.Errorf("--wake-max-queue-depth must be >= 0; got %d", wakeMaxQueueDepth)
	}
	if setWakeWait && (wakeMaxQueueWaitSeconds < 0 || wakeMaxQueueWaitSeconds > api.WakeQueueMaxWaitSeconds) {
		return nil, fmt.Errorf("--wake-max-queue-wait-seconds must be between 0 and %d; got %d", api.WakeQueueMaxWaitSeconds, wakeMaxQueueWaitSeconds)
	}
	app, err := client.GetApp(ctx, slug)
	if err != nil {
		return nil, fmt.Errorf("read app before updating concurrency policy: %w", err)
	}
	policy := &api.ScalingPolicy{
		MinInstances:      app.MinInstances,
		ScaleOutCooldownS: 5,
		ScaleInCooldownS:  60,
	}
	if app.ScalingPolicy != nil {
		copyPolicy := *app.ScalingPolicy
		if app.ScalingPolicy.Target != nil {
			target := *app.ScalingPolicy.Target
			copyPolicy.Target = &target
		}
		policy = &copyPolicy
	}
	// App.MinInstances is the current canonical floor even when a legacy
	// scaling_policy JSON object omitted the mirrored field.
	policy.MinInstances = app.MinInstances
	// Older/default rows can round-trip as a non-nil `{}` policy. The API
	// validates cooldowns whenever a replacement policy is supplied, so
	// preserving those zero values makes an unrelated queue-policy update
	// fail with invalid_cooldown. Fill only the absent cooldowns; valid
	// existing values remain untouched.
	if policy.ScaleOutCooldownS == 0 {
		policy.ScaleOutCooldownS = 5
	}
	if policy.ScaleInCooldownS == 0 {
		policy.ScaleInCooldownS = 60
	}
	if setOverflow {
		policy.ConcurrencyOverflow = overflow
	}
	if setMaxQueueWait {
		policy.MaxQueueWaitMS = maxQueueWaitMS
	}
	if setWakeDepth {
		policy.WakeMaxQueueDepth = wakeMaxQueueDepth
	}
	if setWakeWait {
		policy.WakeMaxQueueWaitSeconds = wakeMaxQueueWaitSeconds
	}
	return policy, nil
}

func scalingPolicyEqual(a, b *api.ScalingPolicy) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if a.MinInstances != b.MinInstances || a.MaxInstances != b.MaxInstances ||
		a.ScaleOutCooldownS != b.ScaleOutCooldownS || a.ScaleInCooldownS != b.ScaleInCooldownS ||
		a.ConcurrencyOverflow != b.ConcurrencyOverflow || a.MaxQueueWaitMS != b.MaxQueueWaitMS {
		return false
	}
	if a.Target == nil || b.Target == nil {
		return a.Target == nil && b.Target == nil
	}
	return a.Target.Metric == b.Target.Metric && a.Target.Value == b.Target.Value
}

func serviceReplicasEqual(a, b *api.ServiceReplicas) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Min == b.Min && a.Max == b.Max && a.Desired == b.Desired
}

func lifecyclePatchNeeded(current api.AppResponse, desired api.UpdateAppRequest) bool {
	manifest := current.Manifest
	if desired.ExecutionMode != nil && manifest.ExecutionMode != *desired.ExecutionMode {
		return true
	}
	if desired.RestartPolicy != nil && manifest.RestartPolicy != *desired.RestartPolicy {
		return true
	}
	if desired.StartupDeadlineS != nil && manifest.StartupDeadlineS != *desired.StartupDeadlineS {
		return true
	}
	if desired.MaxRetries != nil && manifest.MaxRetries != *desired.MaxRetries {
		return true
	}
	if desired.ServiceReplicas != nil && !serviceReplicasEqual(manifest.ServiceReplicas, desired.ServiceReplicas) {
		return true
	}
	return false
}

// applyManifestLifecycle reads and applies the optional lifecycle block. It
// runs after the app exists, allowing OCI image deploys to use worker mode
// without requiring an HTTP listener or an image-derived app.json first.
func applyManifestLifecycle(ctx context.Context, client manifestScalingClient, slug, cwd string) error {
	if cwd == "" {
		return nil
	}
	m, ok, err := gregalemanifest.Load(cwd)
	if err != nil {
		return err
	}
	if !ok || m == nil || m.Lifecycle == nil || m.Lifecycle.Empty() {
		return nil
	}
	if err := m.Validate(); err != nil {
		return err
	}
	desired := m.Lifecycle.ToAPI()
	current, err := client.GetApp(ctx, slug)
	if err != nil {
		return fmt.Errorf("read app before applying lifecycle policy: %w", err)
	}
	if !lifecyclePatchNeeded(current, desired) {
		return nil
	}
	if _, err := client.UpdateApp(ctx, slug, desired); err != nil {
		return fmt.Errorf("apply lifecycle policy: %w", err)
	}
	return nil
}

// applyManifestScalingPolicy reads and applies optional app defaults from the
// manifest. The API remains authoritative for plan gates and validation.
// It runs after the deployment has been accepted so a failed build/upload
// cannot leave app configuration changed. The API remains authoritative for
// plan gates and workload-class compatibility.
func applyManifestScalingPolicy(ctx context.Context, client manifestScalingClient, slug, cwd string) error {
	if cwd == "" {
		return nil
	}
	m, ok, err := gregalemanifest.Load(cwd)
	if err != nil {
		return err
	}
	if !ok || m == nil || (m.Scaling == nil && m.RetryPolicy == nil) {
		return nil
	}
	if err := m.Validate(); err != nil {
		return err
	}
	current, err := client.GetApp(ctx, slug)
	if err != nil {
		return fmt.Errorf("read app before applying scaling policy: %w", err)
	}
	update := api.UpdateAppRequest{}
	changed := false
	if m.Scaling != nil {
		desired := m.Scaling.ToAPI()
		if !scalingPolicyEqual(current.ScalingPolicy, desired) {
			update.ScalingPolicy = desired
			changed = true
		}
	}
	if m.RetryPolicy != nil {
		desired := &api.RetryPolicyDTO{
			MaxAttempts: m.RetryPolicy.MaxAttempts, BaseSeconds: m.RetryPolicy.BaseSeconds,
			MaxSeconds: m.RetryPolicy.MaxSeconds, JitterSeconds: m.RetryPolicy.JitterSeconds,
		}
		if !retryPolicyDTOEqual(current.RetryPolicy, desired) {
			update.RetryPolicy = desired
			changed = true
		}
	}
	if !changed {
		return nil
	}
	if _, err := client.UpdateApp(ctx, slug, update); err != nil {
		return fmt.Errorf("apply manifest app defaults: %w", err)
	}
	return nil
}

func retryPolicyDTOEqual(left, right *api.RetryPolicyDTO) bool {
	zero := func(p *api.RetryPolicyDTO) bool {
		return p == nil || (p.MaxAttempts == 0 && p.BaseSeconds == 0 && p.MaxSeconds == 0 && p.JitterSeconds == 0)
	}
	if zero(left) || zero(right) {
		return zero(left) && zero(right)
	}
	return left.MaxAttempts == right.MaxAttempts && left.BaseSeconds == right.BaseSeconds &&
		left.MaxSeconds == right.MaxSeconds && left.JitterSeconds == right.JitterSeconds
}

const manifestTriggerCleanupTimeout = 10 * time.Second

type manifestCronRollbackStep struct {
	description string
	undo        func(context.Context) error
}

// manifestCronTransaction records the inverse of each successful desired-state
// mutation. The deployment caller commits it only after upload succeeds; every
// earlier failure restores the previous cron set in reverse order.
type manifestCronTransaction struct {
	steps []manifestCronRollbackStep
}

func (t *manifestCronTransaction) commit() {
	if t != nil {
		t.steps = nil
	}
}

func (t *manifestCronTransaction) rollback(ctx context.Context) error {
	if t == nil || len(t.steps) == 0 {
		return nil
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), manifestTriggerCleanupTimeout)
	defer cancel()
	var cleanupErrs []error
	for i := len(t.steps) - 1; i >= 0; i-- {
		step := t.steps[i]
		if err := step.undo(cleanupCtx); err != nil {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("%s: %w", step.description, err))
		}
	}
	t.steps = nil
	return errors.Join(cleanupErrs...)
}

func manifestCronKey(schedule, path string) string {
	return schedule + "\x00" + path
}

func manifestCronRequest(slug string, trigger gregalemanifest.Trigger) api.CreateCronRequest {
	enabled := trigger.IsEnabled()
	timezone := trigger.Timezone
	if timezone == "" {
		timezone = "UTC"
	}
	skip := false
	if trigger.SkipIfRunning != nil {
		skip = *trigger.SkipIfRunning
	}
	return api.CreateCronRequest{
		AppID: slug, Schedule: trigger.Schedule, Path: trigger.Path,
		Enabled: &enabled, Timezone: timezone, SkipIfRunning: &skip,
	}
}

func cronCreateRequestFromResponse(cron api.CronResponse) api.CreateCronRequest {
	enabled, skip := cron.Enabled, cron.SkipIfRunning
	return api.CreateCronRequest{
		AppID: cron.AppID, Schedule: cron.Schedule, Path: cron.Path,
		Enabled: &enabled, Timezone: cron.Timezone, SkipIfRunning: &skip,
	}
}

func cronUpdateRequestFromCreate(req api.CreateCronRequest) api.UpdateCronRequest {
	schedule, path, timezone := req.Schedule, req.Path, req.Timezone
	return api.UpdateCronRequest{
		Schedule: &schedule, Path: &path, Enabled: req.Enabled,
		Timezone: &timezone, SkipIfRunning: req.SkipIfRunning,
	}
}

func cronResponseMatchesRequest(cron api.CronResponse, req api.CreateCronRequest) bool {
	enabled, skip := true, false
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	if req.SkipIfRunning != nil {
		skip = *req.SkipIfRunning
	}
	timezone := req.Timezone
	if timezone == "" {
		timezone = "UTC"
	}
	return cron.Enabled == enabled && cron.Timezone == timezone && cron.SkipIfRunning == skip
}

// loadWorkflowManifestForDeploy performs the workflow-only preflight before
// the CLI creates or fetches the target app and returns the definitions to
// include in the deployment request.
func loadWorkflowManifestForDeploy(ctx context.Context, client manifestCronClient, cwd string) ([]api.WorkflowSpec, error) {
	if cwd == "" {
		return nil, nil
	}
	m, ok, err := gregalemanifest.Load(cwd)
	if err != nil {
		return nil, err
	}
	if !ok || m == nil {
		return nil, nil
	}
	// Validate the complete manifest before CreateApp. This keeps hosting
	// overrides and trigger typos from leaving a newly-created app behind
	// when the later fan-out would otherwise be the first validation point.
	if err := m.Validate(); err != nil {
		return nil, err
	}
	if len(m.Workflows) == 0 {
		// A present manifest with no workflows is an explicit desired empty
		// set. Preserve non-nil so preview can report removal from the latest
		// deployment rather than treating it as omitted intent.
		return []api.WorkflowSpec{}, nil
	}
	acct, err := client.Whoami(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve account plan for workflow manifest: %w", err)
	}
	if err := m.ValidateForPlan(api.Plan(acct.Plan)); err != nil {
		return nil, err
	}
	return append([]api.WorkflowSpec{}, m.Workflows...), nil
}

// validateSingleAppManifestTargets prevents the single-app deploy path from
// silently dropping declarations it cannot apply. Project deploy owns
// cross-workload and unified broker-trigger reconciliation; the direct path
// currently supports only cron triggers for its selected slug.
func validateSingleAppManifestTargets(cwd, slug string) error {
	if cwd == "" {
		return nil
	}
	m, ok, err := gregalemanifest.Load(cwd)
	if err != nil {
		return err
	}
	if !ok || m == nil {
		return nil
	}
	if err := m.Validate(); err != nil {
		return err
	}
	for i, trigger := range m.Triggers {
		if trigger.App != slug {
			return fmt.Errorf("trigger %d targets app %q, but this single-app deploy targets %q; fix the app name or use --project", i+1, trigger.App, slug)
		}
		if trigger.Kind != gregalemanifest.TriggerKindCron {
			return fmt.Errorf("trigger %d uses kind %q; deploy does not reconcile non-cron manifest triggers yet; pass --no-triggers and create it with `gregale triggers add` after deployment", i+1, trigger.Kind)
		}
	}
	return nil
}

// validateProjectManifestConfig rejects app-only declarations that the
// multi-workload planner cannot safely fan out. Failing explicitly is safer
// than silently applying only triggers while ignoring a scaling block.
func validateProjectManifestConfig(cwd string) error {
	if cwd == "" {
		return nil
	}
	m, ok, err := gregalemanifest.Load(cwd)
	if err != nil {
		return err
	}
	if !ok || m == nil {
		return nil
	}
	if err := m.Validate(); err != nil {
		return err
	}
	if m.Scaling != nil {
		return errors.New("scaling is supported on single-app deploys; configure each workload separately after project apply")
	}
	if m.RetryPolicy != nil {
		return errors.New("retry_policy is supported on single-app deploys; configure each workload separately after project apply")
	}
	return nil
}

// deployManifestTriggers applies the manifest as desired state and commits the
// result immediately. The deployment path uses the transaction-returning
// helper below so a later upload/build-submission failure can restore the
// complete previous set.
func deployManifestTriggers(ctx context.Context, client manifestCronClient, slug, cwd string) error {
	txn, err := deployManifestTriggersWithRollback(ctx, client, slug, cwd)
	if err == nil {
		txn.commit()
	}
	return err
}

// deployManifestTriggersWithRollback reconciles creates, mutable scheduling
// options, and removals. Presence of a cron for slug opts that app into
// replacement semantics; an explicit `triggers: []` clears the target app.
// A non-empty manifest containing only other app slugs leaves this app alone.
// The stable identity is (schedule,path), so changing either is a remove+add.
func deployManifestTriggersWithRollback(ctx context.Context, client manifestCronClient, slug, cwd string) (*manifestCronTransaction, error) {
	txn := &manifestCronTransaction{}
	if cwd == "" {
		return txn, nil
	}
	m, ok, err := gregalemanifest.Load(cwd)
	if err != nil {
		return txn, err
	}
	if !ok {
		return txn, nil
	}
	if err := m.Validate(); err != nil {
		return txn, err
	}
	manage := m.Triggers != nil && len(m.Triggers) == 0
	var matching []gregalemanifest.Trigger
	for _, t := range m.Triggers {
		if t.Kind == gregalemanifest.TriggerKindCron && t.App == slug {
			manage = true
			matching = append(matching, t)
		}
	}
	if !manage {
		return txn, nil
	}

	existing, err := client.ListCrons(ctx, slug)
	if err != nil {
		return txn, fmt.Errorf("list existing crons: %w", err)
	}
	if acct, err := client.Whoami(ctx); err == nil {
		if l, ok := api.LimitsFor(api.Plan(acct.Plan)); ok {
			if len(matching) > l.CronLimitPerApp {
				return txn, fmt.Errorf("cron quota exceeded: %d triggers in manifest, plan allows %d; raise plan or drop triggers",
					len(matching), l.CronLimitPerApp)
			}
		}
	}

	desiredByKey := make(map[string]api.CreateCronRequest, len(matching))
	desiredOrder := make([]string, 0, len(matching))
	for _, trigger := range matching {
		key := manifestCronKey(trigger.Schedule, trigger.Path)
		desiredByKey[key] = manifestCronRequest(slug, trigger)
		desiredOrder = append(desiredOrder, key)
	}
	existingByKey := make(map[string]api.CronResponse, len(existing))
	for _, cron := range existing {
		existingByKey[manifestCronKey(cron.Schedule, cron.Path)] = cron
	}

	fail := func(operation string, err error) (*manifestCronTransaction, error) {
		rollbackErr := txn.rollback(ctx)
		if rollbackErr != nil {
			return txn, fmt.Errorf("%s: %w; trigger rollback incomplete: %w", operation, err, rollbackErr)
		}
		return txn, fmt.Errorf("%s: %w; previous trigger state restored", operation, err)
	}

	applied := 0
	// Update same-identity rows first. These operations do not consume quota.
	for _, key := range desiredOrder {
		current, exists := existingByKey[key]
		if !exists {
			continue
		}
		desired := desiredByKey[key]
		if cronResponseMatchesRequest(current, desired) {
			continue
		}
		previous := cronCreateRequestFromResponse(current)
		if _, err := client.UpdateCron(ctx, current.ID, cronUpdateRequestFromCreate(desired)); err != nil {
			return fail("update manifest cron "+current.ID, err)
		}
		cronID := current.ID
		txn.steps = append(txn.steps, manifestCronRollbackStep{
			description: "restore trigger " + cronID,
			undo: func(rollbackCtx context.Context) error {
				_, err := client.UpdateCron(rollbackCtx, cronID, cronUpdateRequestFromCreate(previous))
				return err
			},
		})
		applied++
	}

	// Remove stale rows before creates so replacement at the exact cap has
	// headroom. Each delete records enough data to recreate the prior row.
	for _, cron := range existing {
		if _, keep := desiredByKey[manifestCronKey(cron.Schedule, cron.Path)]; keep {
			continue
		}
		if err := client.DeleteCron(ctx, cron.ID); err != nil {
			return fail("remove stale manifest cron "+cron.ID, err)
		}
		previous := cronCreateRequestFromResponse(cron)
		txn.steps = append(txn.steps, manifestCronRollbackStep{
			description: "recreate trigger " + cron.ID,
			undo: func(rollbackCtx context.Context) error {
				_, err := client.CreateCron(rollbackCtx, slug, previous)
				return err
			},
		})
		applied++
	}

	for i, key := range desiredOrder {
		if _, exists := existingByKey[key]; exists {
			continue
		}
		created, err := client.CreateCron(ctx, slug, desiredByKey[key])
		if err != nil {
			req := desiredByKey[key]
			return fail(fmt.Sprintf("trigger %d/%d (%s %q %s) rejected after %d trigger change(s)",
				i+1, len(desiredOrder), slug, req.Schedule, req.Path, applied), err)
		}
		createdID := created.ID
		if createdID != "" {
			txn.steps = append(txn.steps, manifestCronRollbackStep{
				description: "delete staged trigger " + createdID,
				undo:        func(rollbackCtx context.Context) error { return client.DeleteCron(rollbackCtx, createdID) },
			})
		}
		applied++
	}
	if !jsonOutput && applied > 0 {
		_, _ = fmt.Fprintf(osStdout, "  ✓ %s: %d trigger(s) applied\n", slug, applied)
	}
	return txn, nil
}

// deployManifestQueueBindings reconciles the optional queue_bindings block as
// desired state. Binding identity is the stable name; queue_name changes are
// PATCHed in place so the consumer/autoscaling controller keeps its binding id.
func deployManifestQueueBindings(ctx context.Context, client manifestQueueBindingClient, slug, cwd string) error {
	if cwd == "" {
		return nil
	}
	m, ok, err := gregalemanifest.Load(cwd)
	if err != nil || !ok || m == nil || m.QueueBindings == nil {
		return err
	}
	if err := m.Validate(); err != nil {
		return err
	}
	existing, err := client.ListQueueBindings(ctx, slug)
	if err != nil {
		return fmt.Errorf("list existing queue bindings: %w", err)
	}
	existingByName := make(map[string]api.QueueBindingResponse, len(existing))
	for _, row := range existing {
		existingByName[row.Name] = row
	}
	desired := make(map[string]gregalemanifest.QueueBinding, len(m.QueueBindings))
	for _, binding := range m.QueueBindings {
		desired[binding.Name] = binding
	}
	for _, row := range existing {
		if _, keep := desired[row.Name]; !keep {
			if err := client.DeleteQueueBinding(ctx, slug, row.ID); err != nil {
				return fmt.Errorf("delete stale queue binding %s: %w", row.Name, err)
			}
		}
	}
	for _, binding := range m.QueueBindings {
		mode := binding.Mode
		if mode == "" {
			mode = "pull"
		}
		class := binding.WorkloadClass
		if class == "" {
			class = "worker"
		}
		maxConcurrency := binding.MaxConcurrency
		if maxConcurrency == 0 {
			maxConcurrency = 1
		}
		enabled := true
		if binding.Enabled != nil {
			enabled = *binding.Enabled
		}
		retry := (*api.RetryPolicyDTO)(nil)
		if binding.RetryPolicy != nil {
			retry = &api.RetryPolicyDTO{MaxAttempts: binding.RetryPolicy.MaxAttempts, BaseSeconds: binding.RetryPolicy.BaseSeconds, MaxSeconds: binding.RetryPolicy.MaxSeconds, JitterSeconds: binding.RetryPolicy.JitterSeconds}
		}
		current, exists := existingByName[binding.Name]
		if !exists {
			if _, err := client.CreateQueueBinding(ctx, slug, api.CreateQueueBindingRequest{Name: binding.Name, QueueName: binding.QueueName, Mode: mode, WorkloadClass: class, Enabled: &enabled, MaxConcurrency: maxConcurrency, RetryPolicy: retry}); err != nil {
				return fmt.Errorf("create queue binding %s: %w", binding.Name, err)
			}
			continue
		}
		retryChanged := retry != nil && !queueBindingRetryPolicyEqual(current.RetryPolicy, retry)
		if current.QueueName == binding.QueueName && current.Mode == mode && current.WorkloadClass == class && current.Enabled == enabled && current.MaxConcurrency == maxConcurrency && !retryChanged {
			continue
		}
		if _, err := client.UpdateQueueBinding(ctx, slug, current.ID, api.UpdateQueueBindingRequest{QueueName: &binding.QueueName, Mode: &mode, WorkloadClass: &class, Enabled: &enabled, MaxConcurrency: &maxConcurrency, RetryPolicy: retry}); err != nil {
			return fmt.Errorf("update queue binding %s: %w", binding.Name, err)
		}
	}
	return nil
}

func queueBindingRetryPolicyEqual(a, b *api.RetryPolicyDTO) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.MaxAttempts == b.MaxAttempts && a.BaseSeconds == b.BaseSeconds && a.MaxSeconds == b.MaxSeconds && a.JitterSeconds == b.JitterSeconds
}

// templateFunctionConfig returns the wire defaults for templates whose
// source is a function handler. The same values are recorded in each
// template's gregale.yaml so a materialized project and a direct template
// deploy select the same runtime and handler.
func templateFunctionConfig(name string) (runtime, handler string, ok bool) {
	switch name {
	case "function-node":
		return runtimeNode22, defaultTemplateHandler, true
	case "function-node24":
		return runtimeNode24, defaultTemplateHandler, true
	case "function-python":
		return runtimePython312, defaultTemplateHandler, true
	case "function-python313":
		return runtimePython313, defaultTemplateHandler, true
	case "function-go":
		// The Go handler is a static binary; the wire handler value is
		// vestigial, but the deploy API still requires it to be non-empty.
		return runtimeGo124, "handler.go", true
	case "cron-worker":
		return runtimeNode22, defaultTemplateHandler, true
	default:
		return "", "", false
	}
}

// cmdDeployTarball implements `gregale deploy` (image / tarball / repo
// / template / zero-config). Zero-config (issue #313) packs the selected source directory
// and proceeds down the --tarball path. Issue #737 / ADR-083 added the
// function-vs-app auto-detect on the zero-config path and the
// --function / --app explicit-shape flags.
//
// `--template NAME` materializes one of the embedded starter
// projects (cmd/gregale/templates/embed.go) into a tempdir, tars+gzip it,
// and proceeds down the --tarball path. For the function templates
// we force --runtime / --handler so the runner wires up correctly without
// the customer having to know those flags.
func cmdDeployTarball(args []string) int {
	return cmdDeployTarballToExisting(context.Background(), args, false)
}

// deployExecution lets long-lived CLI workflows own cancellation and observe
// the deployment ID as soon as the upload has been accepted. Ordinary
// `gregale deploy` calls leave both fields unset and retain the signal-driven
// behavior below.
type deployExecution struct {
	onQueued            func(api.DeploymentResponse)
	onSourceSync        func(time.Duration, error)
	onStage             func(string, string, int64, string)
	onTerminal          func(api.DeploymentResponse) int
	onFailure           func(api.DeploymentResponse, string, string)
	onError             func(error)
	prefixBuildLogs     bool
	streamLogsOnJSON    bool
	developerSource     *devSourceSyncState
	extraSourceExcludes []string
}

func (e deployExecution) notifyQueued(dep api.DeploymentResponse) {
	if e.onQueued != nil {
		e.onQueued(dep)
	}
}

// cmdDeployTarballToExisting is the shared deploy implementation. `gregale
// dev` has already reserved its preview app, so it skips the create-or-fetch
// probe; this also avoids incorrectly tripping the app-count quota while
// redeploying an existing developer environment.
func cmdDeployTarballToExisting(ctx context.Context, args []string, existingApp bool, executions ...deployExecution) int {
	// The preflight flag is package-global because the packer lives in a
	// separate file. Reset it per invocation so a previous strict deploy
	// cannot suppress scans in the next CLI command or test.
	doctorPreflightRan = false
	execution := deployExecution{}
	if len(executions) > 0 {
		execution = executions[0]
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	developerSync := execution.developerSource
	fs := newFlagSet("deploy", flag.ContinueOnError)
	image := fs.String("image", "", "digest-pinned image reference")
	tarball := fs.String("tarball", "", "path to source archive (tar.gz)")
	sourcePath := fs.String("path", "", "deploy this source directory (relative to the current directory)")
	worktree := fs.Bool("worktree", false, "deploy the selected source directory from the working tree, including local changes")
	// Issue #739 / ADR-092: --repo pairs with --ref to drive the
	// headless source-ref deploy (server-side foundation lives in
	// cmd/apid/handlers_source_ref.go). The previous M7.5 dashboard
	// browser flow is deleted in PR-B; --repo without --ref is an
	// explicit 1-exit error.
	repo := fs.String("repo", "", "GitHub repo to deploy from (owner/name)")
	ref := fs.String("ref", "", "git ref for --repo (branch, tag, or 40-char SHA)")
	bindingRepo := fs.String("repository", "", "GitHub owner/name to bind to a project")
	installID := fs.Int64("install-id", 0, "GitHub installation id for a project binding")
	productionBranch := fs.String("production-branch", "main", "production branch for a project binding")
	// Issue #270: --github emits a copy-paste-ready GitHub Actions
	// workflow snippet to stdout and exits 0. No auth, no side effects,
	// mirrors `cmdBillingPortal --print` (commands_billing.go:104-157).
	// See cmd_deploy_github.go for the snippet body.
	githubSnippet := fs.Bool("github", false, "emit a GitHub Actions workflow snippet for the Gregale deploy action")
	templateName := fs.String("template", "", "start from an embedded template (run with a bad value to see available names)")
	dockerfile := fs.Bool("dockerfile", false, "build with the supplied Dockerfile inside --tarball")
	runtime := fs.String("runtime", "", "function runtime (node22|python312|go124|go124-alpine|node24|python313)")
	handler := fs.String("handler", "", "function handler (e.g. handler.handler)")
	name := fs.String("name", "", "app name (default: selected source directory, or current directory)")
	profile := fs.String("profile", "", "named app resource profile: micro|small|medium|large|xlarge")
	vcpu := fs.Int("vcpu", 0, "assert the plan guest vCPU shape (omit to use the plan default)")
	executionMode := fs.String("execution-mode", "", "app lifecycle mode: request|service|worker|job")
	restartPolicy := fs.String("restart-policy", "", "restart policy: no|on-failure|always|unless-stopped")
	startupDeadlineS := fs.Int("startup-deadline-s", 0, "maximum startup deadline in seconds (0 = plan default)")
	maxRetries := fs.Int("max-retries", 0, "maximum lifecycle restart attempts (0 = plan default)")
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
	// ApplyProjectPlan. --project is the discoverable opt-in for the
	// same path when the project slug should be derived from --name or
	// the selected source; explicit --project-slug remains available for
	// stable CI names. --yes / --json are absorbed by the global
	// json_flag.go layer and live alongside the others so a single
	// `gregale deploy --tarball X --yes --json --project-slug S` works.
	yes := fs.Bool("yes", false, "skip the apply confirmation prompt")
	deployOnly := fs.String("only", "", "comma-separated workloads to apply; retain unselected project workloads")
	// ADR-124 inverse-allowlist. Mutex with --only (server rejects
	// overlap with code='exclude_only_overlap' but the CLI short-
	// circuits so the operator gets the error pre-flight).
	deployExclude := fs.String("exclude", "", "comma-separated workload names to omit from the apply set (ADR-124)")
	// ADR-124 ship-blocker #4 (PR-followup): when --exclude
	// produced a destructive subset (Removed non-empty), print a
	// one-line warning before the plan view. The full WillDeploy /
	// Skipped / Unaffected / Removed partition is opt-in via
	// --show-affected so the default render stays terse; the warn
	// is the nudge that drives the operator to re-run with the
	// flag when a soft-delete is in play. ADR §3 documents this
	// as "warning + show-affected opt-in" (not auto-promote).
	deployShowAffected := fs.Bool("show-affected", false, "render the WillDeploy + Skipped + Unaffected + Removed partition (ADR-124)")
	// ADR-124 follow-up #3 (PR-B commit 5): --persist-exclude is the
	// write-side complement to --exclude. When set, the operator's
	// excluded slugs are recorded into deployment_scope_exclusions
	// on a successful apply; subsequent deploys without --exclude
	// honor the persisted set automatically (the apply path folds
	// them into the engine call's excludeList, see
	// cmd/apid/scan_service.go::scanService apply-time fallback).
	// Default OFF — the operator's intent is explicit. The audit
	// log (kind=project.scope.excluded) is the durable record beyond
	// the 90-day active window; ADR-127 §1 documents the no-FK
	// posture.
	deployPersistExclude := fs.Bool("persist-exclude", false, "record --exclude slugs into deployment_scope_exclusions for future deploys (ADR-124 follow-up #3)")
	projectDeploy := fs.Bool("project", false, "deploy all detected workloads as one project (slug defaults from --name or source)")
	projectSlug := fs.String("project-slug", "", "kebab slug for the project (triggers one-key provision)")
	// --environment targets a registered project environment. The server
	// resolves the name to the deployment scope after checking the app's
	// project registry; omitted preserves the legacy default scope.
	environment := fs.String("environment", "", "registered project environment to deploy to (for example staging)")
	// SAFE-RELEASES production-leveling Stream F: canary ladder
	// selectors. --canary-preset picks a catalog entry
	// (none/slow/balanced/aggressive/1-10-50-100) or "custom";
	// --canary-stages is a comma-separated
	// "percent@duration" list (e.g. "1@30s,10@2m,100@0s")
	// required only when --canary-preset=custom. The CLI parses
	// + validates BEFORE the network round-trip so a typo
	// surfaces as an exit-2 error instead of a 422.
	canaryPreset := fs.String("canary-preset", "", "canary preset name (none|slow|balanced|aggressive|1-10-50-100|custom); empty = no canary")
	canaryStages := fs.String("canary-stages", "", "comma-separated percent@duration pairs for --canary-preset=custom (e.g. \"1@30s,10@2m,100@0s\")")
	safeDeploy := fs.Bool("safe", false, "deploy with the balanced health-gated rollout (Pro/Scale only)")
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
	noRequireAuthn := fs.Bool("no-require-authn", false, "drop the token requirement and open the public URL")
	// ADR-124: per-app wire-protocol selector (PATCH path).
	// Same single-string flag shape as the CREATE path above.
	// Empty value = no change (the Set bit in UpdateAppParams
	// is unset, so the SQL keeps the existing value).
	appProtocol := fs.String("app-protocol", "", "wire-protocol selector: http1|http2|grpc (omit to leave unchanged)")
	// Issue #556 PR-A: per-deployment traffic-split weight (Pro/Scale
	// only). Sentinel value -1 = "unset" — `fs.Int` doesn't have a
	// pointer type, so the explicit `fs.Visit` check below
	// distinguishes "absent" from "explicit zero". The handler
	// validates [0, 100] (422) and the plan gate (403) on the
	// request path; we just thread the pointer through.
	trafficPercent := fs.Int("traffic-percent", -1, "split weight for this deployment (0-100, Pro/Scale only; -1 = server default 100)")
	rollbackOn5xx := fs.Bool("rollback-on-5xx", false, "automatically roll back after repeated first-wake 5xx responses (Pro/Scale only)")
	// Issue #791 PR-C / ADR-090: skip the `gregale.yaml` triggers fan-out.
	// The flag is the explicit opt-out; without it, a present
	// gregale.yaml with a `triggers:` block is applied after app
	// provisioning (and before the deploy body ships) — see
	// deployManifestTriggers. The source-ref path stages against its
	// already-existing app before posting its JSON request.
	noTriggers := fs.Bool("no-triggers", false, "skip the `gregale.yaml` triggers fan-out (issue #791 PR-C)")
	// Deployment completion is wait-by-default for compatibility with the
	// existing deploy command; --no-wait returns once apid queues the
	// deployment so CI and scripts can continue immediately.
	waitDeploy := fs.Bool("wait", false, "wait for the deployment to become live (default)")
	noWaitDeploy := fs.Bool("no-wait", false, "return after the deployment is queued")
	// --create-only reserves the app metadata without uploading a deployment.
	// This supports service templates whose secrets must be configured before
	// their first process starts, while reusing the normal shape/runtime path.
	createOnly := fs.Bool("create-only", false, "create or reserve the app without uploading a deployment")
	waitTimeoutSeconds := fs.Int("timeout", defaultDeployWaitTimeoutSeconds, fmt.Sprintf("maximum seconds to wait for deployment readiness (default %d)", defaultDeployWaitTimeoutSeconds))
	idempotencyKey := fs.String("idempotency-key", "", "stable logical retry key for this deployment (optional)")
	// --secret-scan toggles the pkg/secretscan pre-pack pass that
	// drops credential-shaped lines (Stripe live keys, GitHub PATs, AWS
	// access keys, OpenAI, Anthropic, Google API, PEM private keys, and
	// Shannon-entropy-flagged unknowns) from .env* files before they are
	// sealed into the upload tarball. Default ON because the failure mode
	// (a Stripe key committed to .env.production by accident) ships a
	// secret to the customer's running microVM where it's far harder to
	// detect. Override with `--secret-scan=off` for local dev sandboxes
	// that genuinely need to pass a Stripe test key at boot — the server
	// still receives whatever the CLI ships.
	secretScan := fs.String("secret-scan", "on", "scan .env* files for known credential patterns before packing (on|off; default on)")
	secretsFile := fs.String("secrets-file", "", "read KEY=VALUE pairs and seal them before the first deployment")
	// PR-0 of the deploy-diff cluster (see docs/adr/ draft):
	// gregale deploy --diff renders what the deploy would change
	// against the live state and exits per the gate. --json emits
	// the stable wire shape; --strict (default) blocks on schema
	// break / quota violation / missing required env; --lenient
	// exits 0 even on breaks (still renders them). --server-diff
	// routes the baseline + projection through apid's
	// POST /v1/apps/{slug}/diff (PR-1), not the SDK client
	// locally. The flag pair --strict / --lenient mirrors the
	// existing --require-authn / --no-require-authn mutex shape
	// at commands2.go:791-799.
	diff := fs.Bool("diff", false, "preview what would change without deploying")
	// `--dry-run` is the discoverable deploy-preflight spelling. Keep
	// `--diff` as the compatibility spelling, but make the intent clear
	// to users who are asking "will this deploy work?" rather than
	// inspecting a state diff. Both paths are strictly read-only: no
	// CreateApp, upload, deployment, or other write is allowed after the
	// authenticated client is acquired.
	dryRun := fs.Bool("dry-run", false, "run deploy preflight without uploading or changing remote state")
	diffJSON := fs.Bool("json", false, "emit JSON output (with --diff or --dry-run)")
	diffStrict := fs.Bool("strict", false, "exit non-zero on schema/quota/env breaks (default with --diff)")
	diffLenient := fs.Bool("lenient", false, "exit zero even on breaks; --diff still renders them")
	serverDiff := fs.Bool("server-diff", false, "compute the diff on apid via POST /v1/apps/{slug}/diff (PR-1) instead of locally")
	// Cluster A (error-explanations, spec §6.4 amendment 1):
	// run `gregale doctor` first and abort the deploy on any
	// error-class finding. The doctor runs over the local cwd (or
	// the auto-pack temp dir) BEFORE any HTTP call, so a
	// customer who has a top-level data/ directory gets the
	// stateless_only_violation prose locally rather than
	// uploading + 422-ing. Warnings remain warn-only (mirrors the
	// standalone cmdDoctor semantics). Scoped via --doctor-strict
	// because --strict/--lenient are taken by --diff above.
	//
	// Explicit archives are extracted into an authoritative temporary source
	// view below, so --doctor-strict also scans --tarball/--template contents.
	// Images still skip the local doctor; server-side validators remain the
	// source of truth for image deploys.
	doctorStrict := fs.Bool("doctor-strict", false, "run `gregale doctor` first; abort the deploy on any error-class finding (warnings are warn-only)")
	noDoctor := fs.Bool("no-doctor", false, "skip the automatic local doctor preflight")
	// Issue #977 / ADR-116: deployment annotations surface. Four
	// flags on the cmdDeployTarball path; the zero-config path
	// auto-captures deployed_by from `git config user.name` and
	// leaves reason/tag/PRNumber unset (no flags exposed there —
	// reason/tag are operator input). --reason / --tag / --deployed-by
	// map 1:1 to the api.DeployAnnotations struct that the multipart
	// writer (pkg/api/multipart.go) emits as form fields. --tag is
	// closed-set; the validator below rejects any other value.
	// --pr-number is the GitHub Action path's escape hatch (CI
	// knows the PR number from ${{ github.event.pull_request.number }});
	// manual CLI users almost never set it.
	reason := fs.String("reason", "", "free-text deploy reason recorded on the row (≤280 chars)")
	tag := fs.String("tag", "", "annotation tag (incident_recovery|hotfix|scheduled_maintenance|compliance_hold|partner_request)")
	deployedBy := fs.String("deployed-by", "", "operator label (auto-resolved from `git config user.name` when in a repo)")
	prNumber := fs.Int("pr-number", 0, "PR number (positive int; 0 = absent). Default unset; CI paths stamp via the GitHub Action.")
	if err := fs.Parse(args); err != nil {
		PrintUsage(os.Stderr, "usage: gregale deploy [--dry-run|--diff|--create-only|--safe] [--doctor-strict|--no-doctor] [--path DIR] [--worktree] --image REF | --tarball PATH | --repo OWNER/NAME --ref REF | --template NAME [--repository OWNER/NAME --install-id N --production-branch BRANCH]", "deploy")
		return 1
	}
	// Deploy has no positional arguments. Go's flag parser stops at the
	// first positional token, so accepting one also silently ignores every
	// later flag. Reject the complete remainder before validation, auth, or
	// source I/O; this prevents a stray token from bypassing --dry-run or
	// changing the target app.
	if fs.NArg() != 0 {
		return printErr("Invalid arguments", fmt.Errorf(
			"gregale deploy accepts flags only; unexpected positional arguments: %s",
			strings.Join(fs.Args(), " ")))
	}
	// Capture explicit presence once. Several deploy flags use sentinel values,
	// and create-only/preflight validation must distinguish an omitted default
	// from a customer-supplied value before authentication or source I/O.
	explicit := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { explicit[f.Name] = true })
	var rollbackOn5xxPtr *bool
	if explicit["rollback-on-5xx"] {
		value := *rollbackOn5xx
		rollbackOn5xxPtr = &value
	}
	// Project scope controls are all planner inputs. Treat each one as a
	// project deploy request even when the operator omitted the discoverable
	// --project spelling; otherwise --exclude/--show-affected silently fell
	// through to the single-app upload path and were ignored.
	projectRequested := *deployOnly != "" || *deployExclude != "" ||
		*deployPersistExclude || *deployShowAffected || explicit["project-slug"] || *projectDeploy
	if *waitTimeoutSeconds <= 0 {
		return printErr("Invalid --timeout", fmt.Errorf("must be greater than zero seconds"))
	}
	if *waitTimeoutSeconds > int((24*time.Hour)/time.Second) {
		return printErr("Invalid --timeout", fmt.Errorf("must be at most 86400 seconds"))
	}
	if err := validateDeployIdempotencyKey(*idempotencyKey); err != nil {
		return printErr("Invalid --idempotency-key", err)
	}
	// run() consumes the global --json before dispatch. Keep the
	// deploy-local --json spelling equivalent for the diff path,
	// whose renderer uses a separate option field.
	*diffJSON = *diffJSON || jsonOutput
	if (*diffStrict || *diffLenient) && !*dryRun && !*diff && !*serverDiff {
		return printErr("Invalid flags", fmt.Errorf("--strict and --lenient require --dry-run, --diff, or --server-diff"))
	}
	preview, previewErr := deployPreviewRequested(*dryRun, *diff, *serverDiff)
	if previewErr != nil {
		return printErr("Invalid flags", previewErr)
	}
	// Normalise the new spelling before any source resolution. This keeps
	// the existing, well-tested read-only diff path as the single
	// implementation while making `gregale deploy --dry-run` safe by
	// construction.
	*diff = preview
	// A linked environment is meaningful for real deployments and project
	// previews. The single-app diff engine has no environment input, so keep
	// its existing read-only behavior when the scope was not explicit.
	if !explicit["environment"] && (!preview || projectRequested) {
		resolvedEnvironment, resolveErr := resolveEnvironmentFlagOrContext(*environment)
		if resolveErr != nil {
			return printErr("Could not read local project context", resolveErr)
		}
		*environment = resolvedEnvironment
	}
	if *environment != "" && *diff && !projectRequested {
		return printErr("Invalid flags", fmt.Errorf("--environment cannot be combined with --dry-run or --diff"))
	}
	// --strict / --lenient mutex. Same rationale as
	// --require-authn / --no-require-authn above.
	if *diffStrict && *diffLenient {
		return printErr("Invalid flags", fmt.Errorf("--strict and --lenient are mutually exclusive"))
	}
	if *createOnly && *diff {
		return printErr("Invalid flags", fmt.Errorf("--create-only cannot be combined with --dry-run or --diff"))
	}
	if *createOnly && *secretsFile != "" {
		return printErr("Invalid flags", fmt.Errorf("--create-only cannot be combined with --secrets-file; configure secrets after reserving the app"))
	}
	if *createOnly && existingApp {
		return printErr("Invalid flags", fmt.Errorf("--create-only cannot be used from an existing developer app"))
	}
	if *createOnly {
		if incompatible := incompatibleCreateOnlyFlags(explicit); len(incompatible) > 0 {
			return printErr("Invalid flags", fmt.Errorf("--create-only cannot be combined with deployment-only options: %s", strings.Join(incompatible, ", ")))
		}
	}
	if *safeDeploy {
		if explicit["canary-preset"] || explicit["canary-stages"] || explicit["traffic-percent"] {
			return printErr("Invalid rollout policy", fmt.Errorf("--safe cannot be combined with --canary-preset, --canary-stages, or --traffic-percent"))
		}
		if (explicit["wait"] && !*waitDeploy) || (explicit["no-wait"] && *noWaitDeploy) {
			return printErr("Invalid flags", fmt.Errorf("--safe cannot be combined with --no-wait; safe deploys wait for the health-gated rollout to complete"))
		}
		*canaryPreset = "balanced"
	}
	if *safeDeploy && projectRequested {
		return printErr("Invalid flags", errors.New("--safe currently supports single-app deploys only; use --canary-preset with a project deploy"))
	}
	if *environment != "" {
		if !api.ValidProjectEnvironmentSlug(*environment) {
			return printErr("Invalid --environment", fmt.Errorf("must be a lowercase project environment slug; got %q", *environment))
		}
		if problem := api.ValidateScope(*environment); problem != nil {
			return printErr("Invalid --environment", &api.APIError{Problem: *problem})
		}
	}
	// Issue #560: flag-pair mutex check (mirrors cmdApp /
	// cmdAppScale --warm-snapshot/--no-warm-snapshot). Setting
	// both is unambiguous noise; reject before any side effects.
	if *requireAuthn && *noRequireAuthn {
		return printErr("Invalid flags", fmt.Errorf("--require-authn and --no-require-authn are mutually exclusive"))
	}
	if *doctorStrict && *noDoctor {
		return printErr("Invalid flags", fmt.Errorf("--doctor-strict and --no-doctor are mutually exclusive"))
	}
	if *secretsFile != "" && (*githubSnippet || *diff || *dryRun || *repo != "") {
		return printErr("Invalid flags", fmt.Errorf("--secrets-file cannot be combined with --github, --diff, --dry-run, or --repo"))
	}
	if projectRequested {
		if *image != "" {
			return printErr("Invalid flags", errors.New("project deploy requires a source archive; --image deploys one app"))
		}
		if *githubSnippet {
			return printErr("Invalid flags", errors.New("project deploy cannot be combined with --github"))
		}
		if *function || *app || *runtime != "" || *handler != "" {
			return printErr("Invalid flags", errors.New("project deploy cannot be combined with --function, --app, --runtime, or --handler"))
		}
		if _, _, ok := templateFunctionConfig(*templateName); ok {
			return printErr("Invalid flags", errors.New("project deploy cannot be combined with a function template"))
		}
	}
	if *profile != "" {
		if _, ok := api.ResourceProfileSpecFor(*profile); !ok {
			return printErr("Invalid --profile", fmt.Errorf("must be one of micro, small, medium, large, xlarge; got %q", *profile))
		}
	}
	if *vcpu < 0 {
		return printErr("Invalid --vcpu", fmt.Errorf("must be zero (plan default) or greater; got %d", *vcpu))
	}
	if explicit["app-protocol"] && !api.IsValidAppProtocol(*appProtocol) {
		problem := api.NewProblem(http.StatusBadRequest, api.CodeAppProtocolInvalid,
			"Invalid app protocol", "app_protocol must be one of: http1, http2, grpc")
		return printErr("Invalid --app-protocol", &api.APIError{Problem: *problem})
	}
	if err := validateDeployLifecycleFlags(*executionMode, *restartPolicy, *startupDeadlineS, *maxRetries); err != nil {
		return printErr("Invalid lifecycle flags", err)
	}
	if *trafficPercent < -1 || *trafficPercent > 100 {
		return printErr("Invalid --traffic-percent", &api.APIError{Problem: *api.ErrInvalidTrafficPercent(*trafficPercent)})
	}
	canarySpec, canaryErr := buildCanarySpec(*canaryPreset, *canaryStages)
	if canaryErr != nil {
		return printErr("Invalid canary rollout", &api.APIError{Problem: *api.ErrInvalidCanaryPreset(canaryErr.Error())})
	}
	if explicit["traffic-percent"] && canarySpec != nil {
		return printErr("Invalid rollout policy", &api.APIError{Problem: *api.ErrValidation("traffic_percent and canary are mutually exclusive rollout policies")})
	}
	// Issue #737 / ADR-083: --function and --app are mutually exclusive.
	// Setting both is ambiguous noise; reject before any side effects so
	// the customer's first response from the CLI is not a silent shape
	// pick. Mirrors the --require-authn/--no-require-authn check above.
	if *function && *app {
		return printErr("Invalid flags", fmt.Errorf("--function and --app are mutually exclusive"))
	}
	if err := validateDeploySourceSelection(*sourcePath, *worktree, *image, *tarball, *repo, *templateName, *githubSnippet, *ref); err != nil {
		return printErr("Invalid flags", err)
	}
	if *image != "" && !api.ValidDeploymentImage(*image) {
		problem := api.NewProblem(http.StatusBadRequest, api.CodeImageRequired,
			"Image required", "image: deploys require a digest-pinned reference, e.g. registry.gregale.dev/app@sha256:...")
		return printErr("Invalid --image", &api.APIError{Problem: *problem})
	}
	// Resolve template shape into immutable command intent before any source
	// materialization. The parsed flag pointers remain an exact record of what
	// the customer supplied, while every preview/apply adapter uses these
	// effective values.
	deployFunction, deployApp := *function, *app
	deployRuntime, deployHandler := *runtime, *handler
	if *templateName != "" {
		if !templates.Exists(*templateName) {
			return printErr("Invalid --template", fmt.Errorf("unknown template %q (known: %s)", *templateName, strings.Join(templates.Names, ", ")))
		}
		if rt, hnd, functionTemplate := templateFunctionConfig(*templateName); functionTemplate {
			if deployApp {
				return printErr("Invalid template shape", fmt.Errorf("function template %q cannot be deployed with --app", *templateName))
			}
			if deployRuntime != "" && deployRuntime != rt {
				return printErr("Invalid template runtime", fmt.Errorf("template %q requires runtime %q; got %q", *templateName, rt, deployRuntime))
			}
			if deployHandler != "" && deployHandler != hnd {
				return printErr("Invalid template handler", fmt.Errorf("template %q requires handler %q; got %q", *templateName, hnd, deployHandler))
			}
			deployFunction, deployRuntime, deployHandler = true, rt, hnd
		} else if deployFunction {
			return printErr("Invalid template shape", fmt.Errorf("app template %q cannot be deployed with --function", *templateName))
		}
	}
	// --secret-scan=off is the documented escape hatch for customers who
	// genuinely need to ship a Stripe test key at boot (local dev
	// sandboxes). Validate the value eagerly so a typo (--secret-scan=0,
	// --secret-scan=false) fails fast with a clear message rather than
	// silently being treated as "on".
	secretScanMode, secretScanErr := parseSecretScanFlag(*secretScan)
	if secretScanErr != nil {
		return printErr("Invalid flags", secretScanErr)
	}
	// Issue #977 / ADR-116: --tag closed-set validation. Server-side
	// validation is the source of truth (DB CHECK + handler regex);
	// the CLI pre-validates so a typo never ships a half-formed
	// annotation row. Reject early before any side effects so the
	// customer's first response from the CLI is the explicit failure.
	// Empty is allowed (no tag); non-empty must match the closed set.
	if *tag != "" && !isValidDeploymentAnnotationTag(*tag) {
		return printErr("Invalid --tag", fmt.Errorf("must be one of: incident_recovery, hotfix, scheduled_maintenance, compliance_hold, partner_request"))
	}
	// --reason length cap mirrors the DB CHECK (≤280 chars). Operators
	// get a fast clear error here rather than a 422 after the upload.
	if reasonErr := validateDeploymentReason(*reason); reasonErr != nil {
		return printErr("Invalid --reason", reasonErr)
	}
	if *prNumber < 0 {
		return printErr("Invalid --pr-number", fmt.Errorf("must be a positive integer or 0 for absent"))
	}
	// Function-only fields on an explicit app are contradictory. Reject them
	// instead of clearing values that preview would otherwise display but apply
	// could never persist.
	if deployApp && (deployRuntime != "" || deployHandler != "") {
		return printErr("Invalid flags", fmt.Errorf("--runtime and --handler require a function-shaped deploy and cannot be combined with --app"))
	}
	// fs.Visit distinguishes "unset" from "explicit zero": if the
	// customer passed either --require-authn or --no-require-authn
	// (but not both — checked above), we propagate the bool to the
	// CreateApp call so a fresh deploy can opt in/out at create
	// time. nil = unset → apid server default (false), so existing
	// customers see no behaviour change.
	if projectRequested {
		var unsupported []string
		for _, name := range []string{
			"traffic-percent", "canary-preset", "canary-stages", "safe",
			"rollback-on-5xx",
			"reason", "tag", "deployed-by", "pr-number",
			// Project plans currently infer each workload's execution
			// configuration from the scanned source. Reject single-app
			// overrides here instead of silently dropping them from both
			// the scan and apply requests.
			"function", "app", "runtime", "handler", "dockerfile",
			"vcpu", "profile", "require-authn", "no-require-authn",
			"app-protocol", "execution-mode", "restart-policy", "startup-deadline-s", "max-retries",
		} {
			if explicit[name] {
				unsupported = append(unsupported, "--"+name)
			}
		}
		if len(unsupported) > 0 {
			return printErr("Unsupported project deploy flags", fmt.Errorf(
				"%s cannot be combined with project scope controls; project deploy policy is not yet supported",
				strings.Join(unsupported, ", ")))
		}
	}
	if explicit["wait"] && explicit["no-wait"] {
		return printErr("Invalid flags", fmt.Errorf("--wait and --no-wait are mutually exclusive"))
	}
	waitForDeploy := true
	if explicit["wait"] {
		waitForDeploy = *waitDeploy
	}
	if explicit["no-wait"] && *noWaitDeploy {
		waitForDeploy = false
	}
	// Output format must not change deployment lifecycle semantics. JSON
	// deploys wait by default just like human-readable deploys; --no-wait is
	// the only mode that returns the queued receipt immediately.
	jsonWait := jsonOutput && waitForDeploy
	// `gregale dev --json` needs to observe the real stage stream so it can
	// emit the edit-to-live receipt instead of the ordinary terminal JSON
	// deployment receipt.
	streamLogsOnJSON := jsonOutput && execution.streamLogsOnJSON
	if streamLogsOnJSON {
		waitForDeploy = true
		jsonWait = false
	}
	var requireAuthnPtr *bool
	var publicAuthPtr *api.PublicAuthBlock
	switch {
	case explicit["require-authn"]:
		v := true
		requireAuthnPtr = &v
	case explicit["no-require-authn"]:
		v := false
		requireAuthnPtr = &v
		publicAuthPtr = &api.PublicAuthBlock{Mode: api.AppPublicAuthModeOpen}
	}
	// ADR-124: per-app wire-protocol selector (deploy path).
	// Single-string flag (closed set); empty value = omit so
	// the per-plan default applies server-side. The
	// commands2.go:cmdApp handler validates the closed set
	// already; this path surfaces as a JSON body field only.
	var appProtocolPtr *string
	if explicit["app-protocol"] {
		v := *appProtocol
		appProtocolPtr = &v
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
		cwdForContext, cwdErr := os.Getwd()
		if cwdErr != nil {
			return printErr("Could not read current directory", cwdErr)
		}
		var nameErr error
		slug, nameErr = linkedDefaultAppName(cwdForContext)
		if nameErr != nil {
			return printErr("Could not read local project context", nameErr)
		}
	}
	if !api.ValidAppSlug(slug) {
		problem := api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid slug", "slug must be 3–40 chars, lowercase letters, digits, and hyphens")
		return printErr("Invalid --name", &api.APIError{Problem: *problem})
	}

	// Issue #1182 §P1 follow-up: receipt emission needs the
	// zero-config provenance (commit_sha + dirty) at the --json
	// emission site, which sits well below the zero-config branch.
	// Hoist a pointer so the branch can populate and the emission
	// site can read. Stays nil on image / source-ref / non-git
	// cwd-auto-pack paths; the receipt constructor handles a nil
	// prov cleanly (commit_sha and dirty zero-valued).
	var prov *zeroConfigProvenance

	// --github emits a copy-paste GitHub Actions workflow snippet to
	// stdout and exits 0 (issue #270). No auth, no side effects — this
	// is a documentation-generation path, not a deploy path. The snippet
	// uses the resolved slug from --name / cwd (slug variable above)
	// and emits ${{ github.* }} placeholders by default, or concrete
	// values when running inside a Actions runner (GITHUB_REPOSITORY +
	// GITHUB_SHA env vars). Slots above the --repo short-circuit so a
	// customer can run `gregale deploy --github --name my-app` without
	// a --ref. The slug is the only required input.
	if *githubSnippet {
		if *dryRun {
			return printErr("Invalid flags", fmt.Errorf("--dry-run cannot be combined with --github"))
		}
		if *safeDeploy {
			return printErr("Invalid flags", fmt.Errorf("--safe cannot be combined with --github; add safe rollout policy to the generated workflow explicitly"))
		}
		return cmdDeployGithubSnippet([]string{"--app", slug})
	}

	// --repo is the headless source-ref deploy path (issue #739 /
	// ADR-092). The previous M7.5 dashboard browser flow was deleted
	// in PR-B; the server resolves the install token from
	// github_installations, so CI runs need only FAAS_TOKEN + --ref.
	if *repo != "" {
		if err := validateRepoDeployFlags(explicit); err != nil {
			return printErr("Invalid flags", err)
		}
		if *createOnly {
			return printErr("Invalid flags", fmt.Errorf("--create-only is not supported with --repo; use --template or --path"))
		}
		if *diff {
			return printErr("Invalid flags", fmt.Errorf("--diff/--dry-run/--server-diff cannot be combined with --repo; source-ref preview is not supported"))
		}
		if *profile != "" {
			return printErr("Invalid flags", fmt.Errorf("--profile cannot be combined with --repo"))
		}
		if err := validateRepoSlug(*repo); err != nil {
			return printErr("Invalid --repo", err)
		}
		// --ref is required because the new endpoint needs a
		// buildable commit. A bare --repo (no --ref) is almost
		// always a CI mis-copy; reject before any side effects so
		// the customer's first response is the explicit failure.
		if *ref == "" {
			PrintFail(os.Stderr, "missing --ref (required with --repo)")
			return 1
		}
		// Phase 3 guard: --repo is the source-ref path; the
		// one-key provision surface takes --tarball/--path, not
		// --repo. Mixing them is almost always a mistake.
		if projectRequested {
			PrintFail(os.Stderr, "--repo cannot be combined with --project, --only, or --project-slug")
			return 1
		}
		refIntent := deployIdempotencyIntent{
			Slug: slug, Repo: *repo, Ref: *ref, Reason: *reason, Tag: *tag,
			DeployedBy: resolveDeployedBy(*deployedBy), PRNumber: *prNumber,
			TrafficPercent: *trafficPercent, CanaryPreset: *canaryPreset,
			CanaryStages: *canaryStages, Environment: *environment, RollbackOn5xx: rollbackOn5xxPtr,
		}
		refKey, keyErr := deployIdempotencyKey(*idempotencyKey, refIntent)
		if keyErr != nil {
			return printErr("Invalid --idempotency-key", keyErr)
		}
		code := cmdDeployRepoSourceRefContextWithJSONWaitOptionsAndManifestAndRollout(ctx, slug, *repo, *ref, api.DeployAnnotations{
			Reason:         *reason,
			Tag:            *tag,
			Environment:    *environment,
			DeployedBy:     resolveDeployedBy(*deployedBy),
			PRNumber:       *prNumber,
			TrafficPercent: optTrafficPercent(*trafficPercent),
			Canary:         canarySpec,
			RollbackOn5xx:  rollbackOn5xxPtr,
		}, waitForDeploy, jsonWait, refKey, time.Duration(*waitTimeoutSeconds)*time.Second, *noTriggers, *safeDeploy)
		return code
	}

	// Remember whether the source was explicitly supplied. The zero-config
	// path may populate *tarball later with an auto-packed cwd archive, but its
	// metadata source must remain the selected working tree rather than an
	// extracted copy.
	explicitTarball := *tarball != ""

	// --template materializes an embedded starter project. For function
	// templates we force the runtime + handler so the customer doesn't
	// need to know the convention; for app templates we leave them
	// unset so imaged auto-detects.
	if *templateName != "" {
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
		explicitTarball = true
		// --image would have precedence over --template by accident;
		// reject it explicitly so the customer isn't surprised by
		// which one wins.
		if *image != "" {
			PrintFail(os.Stderr, "--template and --image are mutually exclusive")
			return 1
		}
	}
	if deployFunction && deployRuntime == "" {
		return printErr("Invalid --runtime", fmt.Errorf("--function requires one of: %s", strings.Join(api.FunctionRuntimes, ", ")))
	}
	if deployRuntime != "" && !api.ValidFunctionRuntime(deployRuntime) {
		return printErr("Invalid --runtime", fmt.Errorf("functions require runtime %s; got %q", strings.Join(api.FunctionRuntimes, ", "), deployRuntime))
	}

	// Read and validate the optional secret bundle before any app mutation.
	// The values stay in memory until the app exists; they are never included
	// in the source archive or printed by the CLI.
	var deploySecrets []secretsPair
	if *secretsFile != "" {
		var secretErr error
		deploySecrets, secretErr = readSecretsFile(*secretsFile)
		if secretErr != nil {
			return printErr("Could not read --secrets-file", secretErr)
		}
		if *templateName != "" {
			if err := validateTemplateSecrets(*templateName, deploySecrets); err != nil {
				return printErr("Invalid --secrets-file", err)
			}
		}
	}

	// Zero-config (issue #313): no source flag → pack the selected source
	// directory and deploy it, mirroring the --template branch above (temp
	// tarball → set *tarball → fall through to the shared upload path). This
	// is an App-type deploy (runtime/handler stay unset) so the server's
	// builder detects the framework; we only detect locally for the UX line and
	// to set --dockerfile when a Dockerfile is at the root. --repo returned
	// earlier and --template already set *tarball, so reaching here with both
	// *image and *tarball empty means the customer gave no source at all.
	// Issue #737 / ADR-083: resolved shape from the selected source directory or
	// the explicit --function/--app short-circuit. Defaults to
	// shapeApp so a --tarball / --image / --template deploy (no cwd
	// pack) stays on the existing app-shaped path. The CreateApp call
	// below reads resolvedShape and sets Type="function" on the wire
	// only when the cwd path picked function mode — explicit
	// --function/--app outside the cwd pack also write to
	// resolvedShape via this variable in the same branch.
	var resolvedShape = shapeApp
	var gitArchivePath string
	var gitArchiveBuildOnlyFiles map[string][]byte
	// sourceRoot is persisted only for workspace-context deploys. An empty
	// value means the uploaded archive root and preserves the legacy wire shape.
	var sourceRoot string
	var workspaceContextRoot string
	// Issue #737 / ADR-083: explicit --function / --app on a
	// --tarball / --template path skips the cwd detector (no cwd
	// pack happens), but still flips resolvedShape so CreateApp
	// sends Type="function" when the customer asked for it. Without
	// this branch, `gregale deploy --tarball my.tgz --function` would
	// still create an app-type app row.
	if deployFunction {
		resolvedShape = shapeFunction
		// Default the wire --handler to the function-template
		// convention so a customer who runs `gregale deploy
		// --tarball my.tgz --function --runtime node22` without
		// --handler gets the same shape as
		// `gregale --template function-node --tarball my.tgz`.
		// Without this, apid's function validator rejects the
		// empty handler form field with a 400.
		if deployHandler == "" {
			deployHandler = defaultTemplateHandler
		}
	} else if deployApp {
		resolvedShape = shapeApp
	}
	cwd, cwdErr := os.Getwd()
	if cwdErr != nil {
		// Don't fail the deploy yet — only the no-(--image|--tarball)
		// path actually reads cwd. We still need it for the
		// gregale.yaml fan-out (issue #791 PR-C), so we surface
		// the error there if needed.
		cwd = ""
	}
	sourceDir := cwd
	if *image != "" {
		// An immutable image has no local source view. Keep cwd out of
		// framework detection, doctor, manifest, workflow, and trigger paths.
		sourceDir = ""
	}
	if *sourcePath != "" {
		if cwdErr != nil {
			return printErr("Could not resolve deploy source", cwdErr)
		}
		var sourceErr error
		sourceDir, sourceErr = resolveDeploySourceDir(cwd, *sourcePath)
		if sourceErr != nil {
			return printErr("Invalid deploy source", sourceErr)
		}
		if *name == "" {
			slug = sanitizeSlug(filepath.Base(sourceDir))
		}
		// Resolve workspace membership independently of GitHub provenance so
		// --worktree also gets the same repository context when the repo has no
		// origin. Only an explicitly selected workload can expand the upload
		// scope; a plain deploy from a repository root remains unchanged.
		if repoRoot, rootErr := gitRootFromCwd(sourceDir); rootErr == nil {
			contextRoot, selectedRoot, workspace, contextErr := resolveWorkspaceContext(repoRoot, sourceDir)
			if contextErr != nil {
				return printErr("Could not resolve workspace build context", contextErr)
			}
			if workspace {
				workspaceContextRoot = contextRoot
				sourceRoot = selectedRoot
			}
		}
	}
	if !api.ValidAppSlug(slug) {
		problem := api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid slug", "slug must be 3–40 chars, lowercase letters, digits, and hyphens")
		return printErr("Invalid --name", &api.APIError{Problem: *problem})
	}
	// --project is an explicit, safe opt-in to project apply. Keep the
	// existing single-app slug rules for --name and --path, while making
	// tarball/template invocations intuitive by using their basename when
	// no name was supplied. An explicit --project-slug always wins.
	if projectRequested && *projectSlug == "" {
		projectName := slug
		switch {
		case *name == "" && *tarball != "":
			projectName = defaultProjectSlug(*tarball)
		case *name == "" && *templateName != "":
			projectName = *templateName
		}
		*projectSlug = sanitizeProjectSlug(projectName)
	}
	if projectRequested && !api.ValidProjectSlug(*projectSlug) {
		return printErr("Invalid --project-slug", projectSlugValidationError(*projectSlug))
	}
	// Authenticate before any zero-config source scan or archive extraction. The
	// zero-config path can inspect the working tree, run doctor checks, and
	// materialise a potentially large archive; doing that for an unauthenticated
	// invocation wastes customer CPU/IO and can expose source-side diagnostics
	// before the CLI reports the actionable login error. Explicit image/tarball
	// deploys retain their historical auth point below because they do not scan
	// or package the working tree here.
	localZeroConfig := *image == "" && *tarball == ""
	var client *Client
	var err error
	if localZeroConfig || explicitTarball {
		var authErr error
		client, authErr = authedClientWithDeployTimeout(5 * time.Minute)
		if authErr != nil {
			return printErr("Not logged in", authErr)
		}
	}
	// An explicitly shaped reservation is complete without source bytes. Stop
	// before zero-config discovery, git inspection, doctor, hashing, or archive
	// creation so an unrelated working tree cannot affect create-only latency.
	if *createOnly && *templateName == "" && *sourcePath == "" && !*worktree && (deployFunction || deployApp) {
		createReq := buildCreateRequest(slug, resolvedShape, deployRuntime, requireAuthnPtr, appProtocolPtr, *profile)
		applyDeployLifecycleToCreateRequest(&createReq, *executionMode, *restartPolicy, *startupDeadlineS, *maxRetries)
		if *vcpu != 0 {
			createReq.VCPU = *vcpu
		}
		if err := createOrFetchApp(ctx, client, createReq, requireAuthnPtr, appProtocolPtr, publicAuthPtr); err != nil {
			return printErr("Could not create or fetch app", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(map[string]any{"slug": slug, "status": "ready"}))
		}
		PrintOK(osStdout, "App %s is ready for configuration; no deployment uploaded.", slug)
		return 0
	}
	if explicitTarball {
		// Snapshot and extract the explicit archive before any doctor,
		// preview, manifest, or trigger work. The snapshot is also the path
		// uploaded below, so every local decision is made against the exact
		// bytes that reach the API rather than against the caller's cwd.
		archivePath, archiveSourceDir, archiveCleanup, archiveErr := materializeDeployArchive(*tarball)
		if archiveErr != nil {
			return printErr("Bad --tarball", archiveErr)
		}
		*tarball = archivePath
		sourceDir = archiveSourceDir
		defer archiveCleanup()
	}
	if localZeroConfig {
		selectedSourceDir := sourceDir
		if provVal, ok, perr := resolveZeroConfigProvenance(selectedSourceDir); ok {
			prov = &provVal
			if provVal.Dirty {
				if dirtyOut, dirtyErr := runGitCmd(provVal.Root, "status", "--porcelain"); dirtyErr == nil {
					dirtyFiles := 0
					for _, line := range strings.Split(strings.TrimRight(dirtyOut, "\n"), "\n") {
						if line != "" {
							dirtyFiles++
						}
					}
					if !jsonOutput && dirtyFiles > 0 && *worktree {
						PrintProgress(os.Stdout, "Note: working tree has %d dirty file(s); deploying working-tree source (%s)", dirtyFiles, provVal.SHA[:7])
					} else if !jsonOutput && dirtyFiles > 0 {
						PrintProgress(os.Stdout, "Note: working tree has %d dirty file(s); deploying HEAD (%s) only — commit first to include the changes", dirtyFiles, provVal.SHA[:7])
					}
				}
			}
			if *deployedBy == "" && provVal.DeployedBy != "" {
				*deployedBy = provVal.DeployedBy
			}
			if !*worktree {
				archivePath, committedSourceDir, committedCleanup, archiveErr := materializeCommittedGitSource(
					provVal, selectedSourceDir, sourceRoot, *sourcePath, workspaceContextRoot,
				)
				if archiveErr != nil {
					return printErr("Could not archive git HEAD", archiveErr)
				}
				gitArchivePath = archivePath
				sourceDir = committedSourceDir
				defer committedCleanup()
				if workspaceContextRoot != "" {
					PrintProgress(os.Stderr, "archiving workspace HEAD (%s) with build root %s", provVal.SHA[:7], sourceRoot)
				} else if *sourcePath == "" {
					PrintProgress(os.Stderr, "archiving HEAD (%s) from %s", provVal.SHA[:7], filepath.Base(cwd))
				} else {
					PrintProgress(os.Stderr, "archiving HEAD (%s) from --path %s", provVal.SHA[:7], *sourcePath)
				}
			}
		} else if !errors.Is(perr, ErrNotInGitRepo) && !errors.Is(perr, ErrNoGitRemote) {
			return printErr("Could not resolve git metadata", perr)
		}
	}
	if (deployRuntime != "" || deployHandler != "") && !deployFunction {
		functionSource := localZeroConfig && detectShape(sourceDir) == shapeFunction
		if !functionSource {
			return printErr("Invalid function flags", fmt.Errorf("--runtime and --handler require an explicit or detected function-shaped deploy"))
		}
	}
	if *dockerfile {
		if dockerfileErr := validateExplicitDockerfile(sourceDir); dockerfileErr != nil {
			return printErr("Invalid --dockerfile", dockerfileErr)
		}
	}
	// Cluster A: local doctor preflight. Zero-config deploys run the
	// deterministic source checks automatically in warn-only mode; the
	// explicit --doctor-strict variant keeps the fail-fast policy gate.
	// Runs runDoctorChecks against the selected source directory (cwd for
	// zero-config, extracted archive contents for --tarball/--template) BEFORE
	// any HTTP / pack. Errors exit 1 with the doctor report printed to stderr
	// (pre-network, no half-state).
	// Warnings render but don't fail (mirrors the standalone cmdDoctor
	// exit semantics). The selected source is scanned regardless of whether
	// it came from --tarball, --template, or zero-config — the doctor catches
	// source-side failure modes
	// (stateless_only_violation, app_loopback_bound, env_var_missing) from the
	// selected source. Only when that source is unreachable (cwdErr != nil for
	// zero-config) does the gate skip — in that case the server-side validators
	// on upload are the catch.
	doctorEnabled := *doctorStrict || (!*noDoctor && localZeroConfig)
	if doctorEnabled && sourceDir != "" {
		doctorShape := resolvedShape
		if !deployFunction && !deployApp {
			doctorShape = detectShape(sourceDir)
		}
		rep := runDoctorChecksForShape(sourceDir, doctorShape)
		if *doctorStrict && rep.HasErrors() {
			if jsonOutput {
				_ = json.NewEncoder(osStderr).Encode(struct {
					Doctor doctorReport `json:"doctor"`
					Exit   int          `json:"exit"`
				}{rep, 1})
			} else {
				renderDoctorHuman(osStderr, rep)
			}
			return 1
		}
		if *doctorStrict && rep.HasWarnings() && !jsonOutput {
			renderDoctorHuman(osStderr, rep)
		}
		if !*doctorStrict && (rep.HasErrors() || rep.HasWarnings() || rep.HasProfileWarnings()) {
			renderDoctorDeployPreflight(rep, jsonOutput)
		}
		// Cluster A (F7 perf): doctor already walked the selected source. Signal
		// runPackPreflight to skip its own loopback-bind and
		// arch-mismatch scans so we don't double-walk the repo.
		doctorPreflightRan = true
	}
	if *image == "" && *tarball == "" {
		if cwdErr != nil {
			return printErr("Could not read current directory", cwdErr)
		}
		// Issue #1182: refactored zero-config `gregale deploy` (no
		// flags). When the selected source is in a git repo with an
		// `origin` remote we pack via `git archive HEAD` (the committed
		// tree, not the working tree) and fall through to the normal
		// buildCreateRequest → CreateApp → DeployTarball pipeline
		// below. The legacy cmdDeployZeroConfig + mustOpen +
		// sourceTarballSidecar trio is gone — that path bypassed
		// CreateApp and dropped every deploy flag. The new path
		// preserves ADR-115's source-tarball trust boundary (the
		// source-tarball endpoint stays as the install-token CI
		// path) by NOT routing through it; it goes through the same
		// CreateApp + DeployTarball wire as --tarball / --template.
		//
		// `--path` normally changes only the selected tree: `git archive
		// HEAD:path/to/app` makes that directory the archive root. When the
		// selected path is a declared workspace workload, the full repository
		// is retained as the build context and sourceRoot points at the member.
		// The explicit `--worktree` opt-in skips the Git archive and packs the
		// selected directory (or its workspace repository context) from disk.
		//
		// Three outcomes from resolveZeroConfigProvenance:
		//   - ok=true,  err=nil   → pack HEAD, stamp provenance
		//   - ok=false, err=ErrNotInGitRepo / ErrNoGitRemote → fall
		//     through to the cwd-auto-pack branch below
		//     (existing behavior preserved for non-git dirs and for
		//     git repos without origin)
		//   - ok=false, err=other → surface the error
		// Provenance and the committed archive were resolved before doctor.
		// sourceDir now names the extracted HEAD view in default mode, or the
		// selected working directory when --worktree/non-git fallback applies.
		// Issue #737 / ADR-083: resolveDeployShape does detect +
		// infer + print in one seam so the unit test can drive the
		// "Detected:" line without bringing up apid. The print goes
		// BEFORE the multipart upload so the customer's first
		// response from the CLI is the deploy shape. An explicit
		// --function / --app short-circuits the detector — see the
		// mutex block above.
		//
		// Issue #1182: resolve the per-plan upload cap from the
		// customer's account before any packing happens. The CLI used
		// to use the Free/Hobby floor (100 MB) for every customer
		// because it lacked an authed round-trip this early; that
		// silently truncated Pro/Scale archives to 100 MB even though
		// the server would have accepted 250 MB. The Whoami call uses
		// a separate authed client so the deploy timeout on
		// authedClientWithDeployTimeout (5 min, line 1382) is not
		// affected — Whoami is a small JSON GET and never needs the
		// deploy budget.
		planCapMB := defaultZeroConfigSourceCapMB
		if client != nil {
			// 5-second budget: Whoami is a tiny JSON GET, but a flaky
			// apid used to hang the CLI for the full HTTP timeout (30s)
			// before falling back to the floor. Bound it explicitly so
			// the zero-config deploy stays snappy on the unhappy path.
			whoCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			if acct, werr := client.Whoami(whoCtx); werr == nil {
				planCapMB = api.MustLimitsFor(api.Plan(acct.Plan)).SourceTarballMaxMB
			} else {
				PrintWarn(osStderr, "Whoami round-trip for per-plan cap failed (%v); using %d MB Free/Hobby floor", werr, planCapMB)
			}
			cancel()
		}
		if gitArchivePath != "" {
			// Project deploys deliberately have no single root workload. The
			// server-side planner scans the complete repository and selects each
			// workload independently, so asking the single-app detector to classify
			// the repository root rejects valid monorepos whose markers only live in
			// nested members.
			if !projectRequested && !deployFunction && !deployApp {
				detected, rt, hnd, detectErr := detectGitArchiveShape(gitArchivePath, sourceRoot)
				if detectErr != nil {
					return printErr("Could not detect committed deploy source", detectErr)
				}
				if detected == shapeFunction {
					resolvedShape = shapeFunction
					displayRuntime, displayHandler := rt, hnd
					if deployRuntime != "" {
						displayRuntime = deployRuntime
					} else {
						deployRuntime = rt
					}
					if deployHandler != "" {
						displayHandler = deployHandler
					} else {
						deployHandler = hnd
					}
					if !jsonOutput {
						PrintOK(osStdout, "Detected: function, runtime=%s, handler=%s, class=function", displayRuntime, displayHandler)
					}
				}
			}
			buildOnlyFiles, buildOnlyErr := gitArchiveGoBuildOnlyFiles(
				gitArchivePath, sourceRoot, resolvedShape, deployRuntime,
			)
			if buildOnlyErr != nil {
				return printErr("Could not prepare committed deploy source", buildOnlyErr)
			}
			gitArchiveBuildOnlyFiles = buildOnlyFiles
			path, n, scanFindings, packErr := packGitArchive(gitArchivePath, planCapMB, secretScanMode, gitArchiveBuildOnlyFiles)
			if packErr != nil {
				return printErr("Could not pack committed HEAD", packErr)
			}
			if n == 0 {
				return printErr("No deployable source found in "+filepath.Base(sourceDir),
					errors.New("the committed HEAD contains no regular source files after deploy exclusions"))
			}
			defer func() { _ = os.Remove(path) }()
			renderSecretScanWarnings(scanFindings, osStderr)
			PrintProgress(os.Stderr, "packing %d file(s) from committed HEAD", n)
			*tarball = path
		}
		// Run the source-directory auto-detect + auto-pack switch when
		// no Git archive was selected. This covers the existing
		// non-git/non-origin fallback and the explicit --worktree mode.
		if *tarball == "" && projectRequested {
			// Preserve the complete selected repository for ScanProject. A project
			// root is allowed to contain only nested workload markers; framework
			// detection belongs to the planner, not this single-app CLI path.
			overrides, scanFindings, scanErr := scanAndRedactEnvFiles(sourceDir, secretScanMode)
			if scanErr != nil {
				return printErr("Secret scan failed", scanErr)
			}
			path, _, n, packErr := autoPackSource(sourceDir, sourceDir, false, planCapMB, overrides, execution.extraSourceExcludes...)
			if packErr != nil {
				return printErr("Could not pack project source", packErr)
			}
			if n == 0 {
				_ = os.Remove(path)
				return printErr("No project source found in "+filepath.Base(sourceDir), errors.New("the selected directory contains no regular source files after deploy exclusions"))
			}
			defer func() { _ = os.Remove(path) }()
			renderSecretScanWarnings(scanFindings, osStderr)
			PrintProgress(os.Stderr, "packing %d project file(s) from %s", n, filepath.Base(sourceDir))
			*tarball = path
		}
		if *tarball == "" {
			detected, rt, hnd, err := resolveDeployShape(sourceDir, deployFunction, deployApp, jsonOutput, deployRuntime, deployHandler)
			if err != nil {
				return printErr("No deployable source found in "+filepath.Base(sourceDir), err)
			}
			switch detected {
			case shapeFunction:
				// An explicit --runtime / --handler on the CLI wins over
				// the inferred value (customer may be overriding the
				// default-extension→runtime map). The helper already
				// printed "Detected: function, runtime=<rt>, handler=<h>"
				// using the inferred values; the wire uses whatever is
				// in deployRuntime / deployHandler here.
				if deployRuntime == "" {
					deployRuntime = rt
				}
				if deployHandler == "" {
					deployHandler = hnd
				}
				// Pack the cwd so the multipart upload has a tarball —
				// the function convention needs the file on the wire for
				// imaged to stage it. The secret-scan pass runs before the
				// tarball is sealed so a Stripe key committed to
				// .env.production by accident is dropped before it leaves
				// the workstation; --secret-scan=off disables it.
				packRoot := sourceDir
				flatContext := false
				if workspaceContextRoot != "" {
					packRoot = workspaceContextRoot
					flatContext = true
				}
				overrides, scanFindings, scanErr := scanAndRedactEnvFiles(packRoot, secretScanMode)
				if scanErr != nil {
					return printErr("Secret scan failed", scanErr)
				}
				path, _, n, err := autoPackSource(sourceDir, packRoot, flatContext, planCapMB, overrides, execution.extraSourceExcludes...)
				if err != nil {
					return printErr("Could not pack deploy source", err)
				}
				defer func() { _ = os.Remove(path) }()
				renderSecretScanWarnings(scanFindings, osStderr)
				PrintProgress(os.Stderr, "packing %d file(s) from %s", n, filepath.Base(sourceDir))
				*tarball = path
				resolvedShape = shapeFunction
			case shapeApp:
				packRoot := sourceDir
				flatContext := false
				if workspaceContextRoot != "" {
					packRoot = workspaceContextRoot
					flatContext = true
				}
				overrides, scanFindings, scanErr := scanAndRedactEnvFiles(packRoot, secretScanMode)
				if scanErr != nil {
					return printErr("Secret scan failed", scanErr)
				}
				path, fw, n, err := autoPackSource(sourceDir, packRoot, flatContext, planCapMB, overrides, execution.extraSourceExcludes...)
				if err != nil {
					return printErr("Could not pack deploy source", err)
				}
				defer func() { _ = os.Remove(path) }()
				renderSecretScanWarnings(scanFindings, osStderr)
				if fw == fwDocker {
					*dockerfile = true
				}
				PrintProgress(os.Stderr, "packing %d file(s) from %s", n, filepath.Base(sourceDir))
				*tarball = path
				resolvedShape = shapeApp
			}
		}
	}

	if client == nil {
		var authErr error
		client, authErr = authedClientWithDeployTimeout(5 * time.Minute)
		if authErr != nil {
			return printErr("Not logged in", authErr)
		}
	}
	if !projectRequested && !*noTriggers {
		if manifestErr := validateSingleAppManifestTargets(sourceDir, slug); manifestErr != nil {
			return printErr("Invalid deploy manifest", manifestErr)
		}
	}
	if projectRequested {
		if manifestErr := validateProjectManifestConfig(sourceDir); manifestErr != nil {
			return printErr("Invalid project deploy manifest", manifestErr)
		}
	}
	if explicitTarball && *diff {
		if archiveErr := validatePreviewArchivePlanLimit(ctx, client, *tarball); archiveErr != nil {
			return printErr("Bad --tarball", archiveErr)
		}
	}
	// Fingerprint local source bytes before the first deployment mutation so
	// the default logical retry key follows the exact archive being shipped.
	// Normal deploys with an explicit key skip this extra read; previews still
	// fingerprint the archive so their source identity is meaningful.
	sourceSHA256 := ""
	// Preview requests always need the digest so the diff can identify the
	// exact source archive, even when the user supplied an explicit
	// idempotency key. Normal deploys retain the cheaper historical path.
	if *tarball != "" && (strings.TrimSpace(*idempotencyKey) == "" || *diff) {
		sourceSHA256, err = tarballSHA256(*tarball)
		if err != nil {
			// Keep the established CLI error title for invalid customer
			// tarballs (including symlink rejection); fingerprinting is
			// a preflight detail, not a new failure category.
			return printErr("Bad --tarball", err)
		}
	}
	appProtocolIntent := ""
	if appProtocolPtr != nil {
		appProtocolIntent = *appProtocolPtr
	}
	deployIntent := deployIdempotencyIntent{
		Slug: slug, Shape: resolvedShape, Runtime: deployRuntime, Handler: deployHandler,
		Image: *image, SourceSHA256: sourceSHA256, SourceRoot: sourceRoot,
		Profile: *profile, Dockerfile: *dockerfile, RequireAuthn: requireAuthnPtr,
		AppProtocol: appProtocolIntent, ExecutionMode: *executionMode, RestartPolicy: *restartPolicy,
		StartupDeadlineS: *startupDeadlineS, MaxRetries: *maxRetries, Reason: *reason, Tag: *tag,
		DeployedBy: resolveDeployedBy(*deployedBy), PRNumber: *prNumber,
		TrafficPercent: *trafficPercent, CanaryPreset: *canaryPreset,
		CanaryStages: *canaryStages, Environment: *environment, RollbackOn5xx: rollbackOn5xxPtr, NoTriggers: *noTriggers,
		ProjectSlug: *projectSlug, DeployOnly: *deployOnly, DeployExclude: *deployExclude,
	}
	deployKey, keyErr := deployIdempotencyKey(*idempotencyKey, deployIntent)
	if keyErr != nil {
		return printErr("Invalid --idempotency-key", keyErr)
	}
	// Deploy preview short-circuit (PR-0 of the deploy-diff cluster).
	// Runs AFTER authedClient so the SDK reads can resolve, and
	// BEFORE the Phase 3 / CreateApp / Deploy body so no writes
	// happen. --diff and --dry-run never ship a deploy.
	if *diff {
		if *dockerfile && *tarball != "" {
			if err := validateExplicitDockerfileArchive(*tarball, sourceRoot); err != nil {
				return printErr("Dockerfile build unavailable", err)
			}
		}
		previewWorkflows, workflowErr := loadWorkflowManifestForDeploy(ctx, client, sourceDir)
		if workflowErr != nil {
			return printErr("Workflow manifest validation failed", workflowErr)
		}
		// Project deploys have a different preview contract from a
		// single-app diff: the apply path is driven by ScanProject, so
		// preview must use that same planner and render the complete
		// workload/managed/warning response. Keeping this branch ahead
		// of runDiff prevents a project preview from silently falling
		// back to the root-app diff (issue #1976).
		if projectRequested {
			if *profile != "" {
				return printErr("Invalid flags", fmt.Errorf("--profile applies to a single app and cannot be combined with --project, --only, or --project-slug"))
			}
			return runProjectDeployPreviewWithMode(ctx, client, *tarball, *projectSlug,
				*bindingRepo, *productionBranch, *deployOnly, *deployExclude, *installID,
				*deployShowAffected, *diffJSON,
				*diffStrict || !*diffLenient, *noTriggers, *environment)
		}
		opts := buildDiffOptionsWithLifecycle(slug, resolvedShape, deployRuntime, deployHandler, *image, sourceDir, requireAuthnPtr, appProtocolPtr, *profile, *vcpu, *executionMode, *restartPolicy, *startupDeadlineS, *maxRetries)
		opts.BuildPlan = buildPreviewBuildPlan(sourceDir, resolvedShape, deployRuntime, deployHandler, sourceSHA256, *image != "", *dockerfile)
		opts.TrafficPercent = optTrafficPercent(*trafficPercent)
		opts.Canary = canarySpec
		opts.Workflows = previewWorkflows
		opts.PRNumber = *prNumber
		opts.NoTriggers = *noTriggers
		opts.Safe = *safeDeploy
		opts.JSON = *diffJSON
		// --strict is the default; --lenient opts out.
		opts.Strict = !*diffLenient
		opts.Lenient = *diffLenient
		opts.ServerDiff = *serverDiff
		return runDiff(ctx, client, opts)
	}

	// Phase 3 (repo decomposition) one-key provision path. Triggered
	// by any project scope control (--project, --project-slug, --only,
	// --exclude, --persist-exclude, or --show-affected) on a --tarball /
	// --template / zero-config
	// pack. The plan is fetched via ScanProject, the apply is
	// transactional on the server (rollback on over-quota per
	// ADR-050), and mutation requires --yes or an interactive prompt;
	// non-TTY invocations fail closed after rendering the plan.
	if projectRequested {
		if *createOnly {
			return printErr("Invalid flags", fmt.Errorf("--create-only cannot be combined with --only or --project-slug"))
		}
		if *profile != "" {
			return printErr("Invalid flags", fmt.Errorf("--profile applies to a single app and cannot be combined with --project, --only, or --project-slug"))
		}
		// Make sure the tarball resolves the same way it does for
		// the legacy path: --template materialises, zero-config packs
		// $PWD. The block above already populated *tarball in those
		// cases; if it's still empty, we have no source.
		if *tarball == "" {
			return printErr("One-key provision requires --tarball, --template, or a TTY cwd",
				errors.New("no source resolved"))
		}
		if (*bindingRepo == "") != (*installID == 0) {
			return printErr("Invalid project binding", errors.New("--repository and --install-id must be provided together"))
		}
		if *bindingRepo != "" {
			if err := validateRepoSlug(*bindingRepo); err != nil {
				return printErr("Invalid --repository", err)
			}
		}
		openTarball, err := openCustomerFile(*tarball)
		if err != nil {
			return printErr("Could not open tarball", err)
		}
		defer func() { _ = openTarball.Close() }()
		prodBranch := *productionBranch
		onlyList := splitCSV(*deployOnly)
		excludeList := splitCSV(*deployExclude)
		if ok, clash := intersect(onlyList, excludeList); ok {
			return printErr("Invalid flags", fmt.Errorf(
				"--only and --exclude share workload(s): %s",
				strings.Join(clash, ", ")))
		}
		plan, err := client.ScanProjectWithBindingEnvironment(ctx, openTarball, filepath.Base(*tarball),
			*projectSlug, *bindingRepo, prodBranch, *installID, onlyList, excludeList, *deployPersistExclude, *noTriggers, *environment)
		if err != nil {
			return printErr("Scan failed", err)
		}
		if !plan.CanApply {
			if jsonOutput {
				return jsonOut(writeJSONProblem(planProblem(plan)))
			}
			printPlanText(osStdout, plan, excludeList, *deployShowAffected)
			return printErr("Plan is not applicable on this plan", errors.New("over-quota or unsupported configuration"))
		}
		if !*yes {
			// JSON output is intentionally non-interactive: prompting would
			// corrupt the machine-readable stdout stream. Emit the complete
			// plan first, then fail closed so an operator or CI job must make
			// the approval explicit with --yes.
			if jsonOutput {
				if code := jsonOut(writeJSON(plan)); code != 0 {
					return code
				}
				return printErr("Confirmation required", errors.New(
					"project deploy requires --yes in JSON mode"))
			}
			if stdoutIsTTY() && stdinIsTTY() {
				if !confirmPlan(osStdout, osStdin, plan, excludeList, *deployShowAffected) {
					return printErr("Aborted by user", errors.New("user declined at the confirm prompt"))
				}
			} else {
				// A redirected stream, CI runner, pipeline, or unsupported
				// platform must never turn the absence of a prompt into an
				// implicit approval. Render the same complete plan the TTY
				// prompt would have shown so removals remain visible.
				printPlanText(osStdout, plan, excludeList, true)
				return printErr("Confirmation required", errors.New(
					"project deploy requires --yes when stdin or stdout is not a TTY; review the plan and rerun with --yes"))
			}
		}
		approvalToken := ""
		if plan.EnvironmentProtected {
			approval, approvalErr := client.ApproveProjectEnvironment(ctx, *projectSlug, *environment,
				api.CreateProjectEnvironmentApprovalRequest{PlanToken: plan.PlanToken})
			if approvalErr != nil {
				return printErr("Protected environment approval failed", approvalErr)
			}
			approvalToken = approval.ApprovalToken
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
		applyCtx := api.ContextWithIdempotencyKey(ctx, deployOperationIdempotencyKey(deployKey, "project-apply"))
		apply, err := client.ApplyProjectPlanWithBindingEnvironmentApproval(applyCtx, plan.PlanToken, openTarball2, filepath.Base(*tarball),
			*projectSlug, *bindingRepo, prodBranch, *installID, onlyList, excludeList, *deployPersistExclude, *noTriggers, *environment, approvalToken)
		if err != nil {
			return printErr("Apply failed", err)
		}
		var projectWaitTimedOut bool
		if waitForDeploy && len(apply.Builds) > 0 {
			apply, projectWaitTimedOut = waitForProjectApply(ctx, client, apply,
				time.Duration(*waitTimeoutSeconds)*time.Second)
		}
		applyStatus := summarizeProjectApply(apply)
		// Project deploys share the same sealed app-secret storage as
		// single-app deploys. Apply the validated bundle to every workload
		// selected by this project plan before returning the apply receipt;
		// this keeps existing host-key rekey and secret redaction paths in
		// force while making one-command monorepo deploys usable.
		if len(deploySecrets) > 0 {
			configured, secretErr := setProjectDeploySecretsWithScope(ctx, client, plan.Workloads, deploySecrets, *environment)
			if secretErr != nil {
				return printErr("Could not configure --secrets-file", secretErr)
			}
			if !jsonOutput {
				PrintOK(osStdout, "Configured %d secret(s) across %d workload(s)", len(deploySecrets), configured)
			}
		}
		if jsonOutput {
			if code := jsonOut(writeJSON(apply)); code != 0 {
				return code
			}
			if applyStatus.buildsFailed > 0 {
				reportProjectApplyFailure(osStderr, applyStatus)
				if projectWaitTimedOut {
					return 3
				}
				return 1
			}
			return 0
		}
		code := renderProjectApplyResult(osStdout, plan, apply)
		if projectWaitTimedOut {
			return 3
		}
		return code
	}

	var workflowDefs []api.WorkflowSpec
	if !*createOnly {
		workflowDefs, err = loadWorkflowManifestForDeploy(ctx, client, sourceDir)
		if err != nil {
			return printErr("Workflow manifest validation failed", err)
		}
	}
	manifestScope, err := manifestDeploymentScope(slug, sourceDir, *environment)
	if err != nil {
		return printErr("Manifest resource scope resolution failed", err)
	}
	if !existingApp {
		createReq := buildCreateRequest(slug, resolvedShape, deployRuntime, requireAuthnPtr, appProtocolPtr, *profile)
		applyDeployLifecycleToCreateRequest(&createReq, *executionMode, *restartPolicy, *startupDeadlineS, *maxRetries)
		if *vcpu != 0 {
			createReq.VCPU = *vcpu
		}
		if err := createOrFetchApp(ctx, client, createReq, requireAuthnPtr, appProtocolPtr, publicAuthPtr); err != nil {
			return printErr("Could not create or fetch app", err)
		}
		if *createOnly {
			if err := deployManifestPostgresBindings(ctx, client, slug, sourceDir, *environment); err != nil {
				return printErr("Manifest database bindings failed", err)
			}
			if err := deployManifestObjectStorageBindings(ctx, client, slug, sourceDir, *environment); err != nil {
				return printErr("Manifest object-storage bindings failed", err)
			}
			if jsonOutput {
				return jsonOut(writeJSON(map[string]any{
					"slug":   slug,
					"status": "ready",
				}))
			}
			PrintOK(osStdout, "App %s is ready for configuration; no deployment uploaded.", slug)
			return 0
		}
	} else if *profile != "" {
		// Developer sessions reuse an existing app and skip the
		// create-or-fetch probe. Apply the requested profile explicitly so
		// `gregale dev ... --profile` has the same effect as a normal deploy.
		if _, err := client.UpdateApp(ctx, slug, api.UpdateAppRequest{ResourceProfile: profile}); err != nil {
			return printErr("Could not update app resource profile", err)
		}
	}
	if err := deployManifestPostgresBindings(ctx, client, slug, sourceDir, *environment); err != nil {
		return printErr("Manifest database bindings failed", err)
	}
	if err := deployManifestObjectStorageBindings(ctx, client, slug, sourceDir, *environment); err != nil {
		return printErr("Manifest object-storage bindings failed", err)
	}
	if len(deploySecrets) > 0 {
		if err := setDeploySecretsWithScope(ctx, client, slug, deploySecrets, *environment); err != nil {
			return printErr("Could not configure --secrets-file", err)
		}
		if !jsonOutput {
			PrintOK(osStdout, "Configured %d secret(s) before deployment", len(deploySecrets))
		}
	}

	// Issue #791 PR-C / ADR-090: gregale.yaml triggers are staged after
	// CreateApp so the slug exists for the FK, and before the deployment
	// request. If upload/build/submission fails, the deferred compensation
	// reverses every create, update, and removal to restore the prior trigger
	// set. A queued no-wait deployment commits the staged state;
	// waited deployments commit only once they reach live. --no-triggers
	// opts out of the entire fan-out.
	var stagedManifestTriggerTxn *manifestCronTransaction
	defer func() {
		if stagedManifestTriggerTxn == nil || len(stagedManifestTriggerTxn.steps) == 0 {
			return
		}
		changes := len(stagedManifestTriggerTxn.steps)
		if err := stagedManifestTriggerTxn.rollback(ctx); err != nil {
			PrintWarn(osStderr, "Manifest trigger rollback incomplete: %v", err)
			return
		}
		PrintProgress(osStderr, "Manifest trigger rollback complete (%d change(s) reverted)", changes)
	}()
	commitManifestTriggers := func() {
		stagedManifestTriggerTxn.commit()
	}
	applyManifestScaling := func() error {
		if err := applyManifestLifecycle(ctx, client, slug, sourceDir); err != nil {
			return err
		}
		return applyManifestScalingPolicy(ctx, client, slug, sourceDir)
	}
	if !*noTriggers {
		var triggerErr error
		stagedManifestTriggerTxn, triggerErr = deployManifestTriggersWithRollback(ctx, client, slug, sourceDir)
		if triggerErr != nil {
			return printErr("Manifest triggers fan-out failed", triggerErr)
		}
	}
	if err := deployManifestQueueBindings(ctx, client, slug, sourceDir); err != nil {
		return printErr("Manifest queue bindings failed", err)
	}

	if *tarball != "" {
		sourceURL, commitSHA := zeroConfigSourceProvenance(prov)
		ann := api.DeployAnnotations{
			Scope:          manifestScope,
			SourceURL:      sourceURL,
			CommitSHA:      commitSHA,
			Environment:    *environment,
			Reason:         *reason,
			Tag:            *tag,
			DeployedBy:     resolveDeployedBy(*deployedBy),
			PRNumber:       *prNumber,
			Workflows:      workflowDefs,
			TrafficPercent: optTrafficPercent(*trafficPercent),
			Canary:         canarySpec,
			RollbackOn5xx:  rollbackOn5xxPtr,
		}
		var (
			dep           api.DeploymentResponse
			usedResumable bool
		)
		if developerSync != nil {
			var deployErr error
			sourceSyncStarted := time.Now()
			dep, deployErr = deployDeveloperSource(client, ctx, slug, *tarball, deployRuntime, deployHandler, *dockerfile, sourceRoot, ann, developerSync)
			if execution.onSourceSync != nil {
				execution.onSourceSync(time.Since(sourceSyncStarted), deployErr)
			}
			if deployErr != nil {
				if errors.Is(deployErr, context.Canceled) || ctx.Err() != nil {
					return 130
				}
				code := printErr("Bad --tarball", deployErr)
				if execution.onError != nil {
					execution.onError(deployErr)
				}
				return code
			}
		} else if canUseResumableUpload(resolvedShape, deployRuntime, deployHandler, *dockerfile, sourceRoot, ann, *trafficPercent, *canaryPreset, *canaryStages) {
			uploadOptions := api.UploadDeployOptions{
				Runtime: deployRuntime, Handler: deployHandler, Dockerfile: *dockerfile,
				SourceRoot: sourceRoot, Scope: ann.Scope, SourceURL: ann.SourceURL, CommitSHA: ann.CommitSHA,
				Environment: ann.Environment, RollbackOn5xx: ann.RollbackOn5xx,
				Reason: ann.Reason, Tag: ann.Tag,
				DeployedBy: ann.DeployedBy, PRNumber: ann.PRNumber, Workflows: workflowDefs,
			}
			var progress resumableUploadProgress
			if !jsonOutput {
				lastPercent := -1
				progress = func(uploaded, total int64) {
					percent := int(uploaded * 100 / total)
					if percent == lastPercent && uploaded != total {
						return
					}
					lastPercent = percent
					PrintProgress(osStderr, "uploading source: %d%% (%d/%d MiB)", percent, uploaded/(1024*1024), total/(1024*1024))
				}
			}
			var uploadErr error
			uploadCtx := api.ContextWithIdempotencyKey(ctx, deployKey)
			dep, sourceSHA256, usedResumable, uploadErr = DeployResumableTarball(client, uploadCtx, slug, *tarball, progress, uploadOptions)
			if uploadErr == nil && !usedResumable {
				multipartCtx := api.ContextWithIdempotencyKey(ctx, deployOperationIdempotencyKey(deployKey, "multipart"))
				dep, uploadErr = DeployTarballWithSourceRoot(client, multipartCtx, slug, *tarball, deployRuntime, deployHandler, *dockerfile, sourceRoot, ann)
			}
			if uploadErr != nil {
				if errors.Is(uploadErr, context.Canceled) || ctx.Err() != nil {
					return 130
				}
				code := printErr("Bad --tarball", uploadErr)
				if execution.onError != nil {
					execution.onError(uploadErr)
				}
				return code
			}
		} else {
			var deployErr error
			multipartCtx := api.ContextWithIdempotencyKey(ctx, deployOperationIdempotencyKey(deployKey, "multipart"))
			dep, deployErr = DeployTarballWithSourceRoot(client, multipartCtx, slug, *tarball, deployRuntime, deployHandler, *dockerfile, sourceRoot, ann)
			if deployErr != nil {
				if errors.Is(deployErr, context.Canceled) || ctx.Err() != nil {
					return 130
				}
				code := printErr("Bad --tarball", deployErr)
				if execution.onError != nil {
					execution.onError(deployErr)
				}
				return code
			}
		}
		execution.notifyQueued(dep)
		if jsonOutput && !streamLogsOnJSON {
			// Legacy multipart uploads do not calculate the digest while
			// streaming, so preserve the stable receipt field there by
			// hashing after the request completes.
			if sourceSHA256 == "" {
				if sha, hashErr := tarballSHA256(*tarball); hashErr != nil {
					PrintWarn(osStderr, "could not hash source tarball for receipt (%v); source_sha256 omitted", hashErr)
				} else {
					sourceSHA256 = sha
				}
			}
			if !jsonWait {
				if err := applyManifestScaling(); err != nil {
					return printErr("Manifest scaling policy failed", err)
				}
				code := jsonOut(writeJSON(newDeployReceipt(dep, prov, deployedAppURL(slug), sourceSHA256)))
				if code == 0 {
					commitManifestTriggers()
				}
				return code
			}
		}
		if !waitForDeploy {
			if err := applyManifestScaling(); err != nil {
				return printErr("Manifest scaling policy failed", err)
			}
			commitManifestTriggers()
			PrintOK(osStdout, "Deployment %s queued. %s", dep.ID, deployedAppURL(slug))
			return 0
		}
		if jsonWait {
			code := writeWaitedDeploymentReceiptUntilWithOptions(ctx, client, dep, prov, deployedAppURL(slug), sourceSHA256, slug, time.Duration(*waitTimeoutSeconds)*time.Second, *safeDeploy)
			if code == 0 {
				if err := applyManifestScaling(); err != nil {
					return printErr("Manifest scaling policy failed", err)
				}
				commitManifestTriggers()
			}
			return code
		}
		code := streamDeployLogsContextWithOptions(ctx, client, dep, slug, streamDeployOptions{
			onStage:         execution.onStage,
			onTerminal:      execution.onTerminal,
			onFailure:       execution.onFailure,
			prefixBuildLogs: execution.prefixBuildLogs,
			quiet:           streamLogsOnJSON,
			waitTimeout:     time.Duration(*waitTimeoutSeconds) * time.Second,
			waitForRollout:  *safeDeploy,
		})
		if code == 0 {
			if err := applyManifestScaling(); err != nil {
				return printErr("Manifest scaling policy failed", err)
			}
			commitManifestTriggers()
		}
		return code
	}
	// Issue #977 / ADR-116: the image-deploy path uses the JSON wire
	// (CreateDeploymentRequest), so the annotation fields ride on the
	// pointer shape — nil vs empty-string matches the DTO convention.
	annPtr := func(v string) *string {
		if v == "" {
			return nil
		}
		return &v
	}
	annIntPtr := func(v int) *int {
		if v <= 0 {
			return nil
		}
		return &v
	}
	deployCtx := api.ContextWithIdempotencyKey(ctx, deployOperationIdempotencyKey(deployKey, "json"))
	dep, err := client.Deploy(deployCtx, slug, api.CreateDeploymentRequest{
		Image:          *image,
		Scope:          manifestScope,
		Environment:    *environment,
		RollbackOn5xx:  rollbackOn5xxPtr,
		Workflows:      workflowDefs,
		TrafficPercent: optTrafficPercent(*trafficPercent),
		Reason:         annPtr(*reason),
		Tag:            annPtr(*tag),
		DeployedBy:     annPtr(resolveDeployedBy(*deployedBy)),
		PRNumber:       annIntPtr(*prNumber),
		Canary:         canarySpec,
	})
	if err != nil {
		code := printErr("Deploy failed", err)
		if execution.onError != nil {
			execution.onError(err)
		}
		return code
	}
	execution.notifyQueued(dep)
	if jsonOutput && !jsonWait && !streamLogsOnJSON {
		// Image deploy path: no source tarball bytes (the digest
		// rides on dep.ImageDigest), no git detection (prov is
		// nil from the function-scope hoist), so commit_sha /
		// dirty / source_sha256 stay empty in the receipt. The
		// receipt's only delta here is app_url, computed from
		// the CLI-known slug (not the 32-hex AppID — the
		// gateway routes on slug, so the receipt's URL has to
		// be slug-shaped to actually resolve).
		if err := applyManifestScaling(); err != nil {
			return printErr("Manifest scaling policy failed", err)
		}
		code := jsonOut(writeJSON(newDeployReceipt(dep, nil, deployedAppURL(slug), "")))
		if code == 0 {
			commitManifestTriggers()
		}
		return code
	}
	if !waitForDeploy {
		if err := applyManifestScaling(); err != nil {
			return printErr("Manifest scaling policy failed", err)
		}
		commitManifestTriggers()
		PrintOK(osStdout, "Deployment %s queued. %s", dep.ID, deployedAppURL(slug))
		return 0
	}
	if jsonWait {
		code := writeWaitedDeploymentReceiptUntilWithOptions(ctx, client, dep, nil, deployedAppURL(slug), "", slug, time.Duration(*waitTimeoutSeconds)*time.Second, *safeDeploy)
		if code == 0 {
			if err := applyManifestScaling(); err != nil {
				return printErr("Manifest scaling policy failed", err)
			}
			commitManifestTriggers()
		}
		return code
	}
	code := streamDeployLogsContextWithOptions(ctx, client, dep, slug, streamDeployOptions{
		onStage:         execution.onStage,
		onTerminal:      execution.onTerminal,
		onFailure:       execution.onFailure,
		prefixBuildLogs: execution.prefixBuildLogs,
		quiet:           streamLogsOnJSON,
		waitTimeout:     time.Duration(*waitTimeoutSeconds) * time.Second,
		waitForRollout:  *safeDeploy,
	})
	if code == 0 {
		if err := applyManifestScaling(); err != nil {
			return printErr("Manifest scaling policy failed", err)
		}
		commitManifestTriggers()
	}
	return code
}

func validateDeploymentReason(reason string) error {
	count := utf8.RuneCountInString(reason)
	if count > 280 {
		return fmt.Errorf("must be ≤280 characters (got %d)", count)
	}
	return nil
}

const rollbackUsage = "usage: gregale rollback <slug> [--to <deployment_id>] [--json]"

// cmdRollback, cmdPark, cmdWake implement their eponymous routes.
//
// SAFE-RELEASES-G (issue #976, PR-G): cmdRollback now honours an
// optional `--to <deployment_id>` flag. When set, the handler validates
// that the named deployment (a) belongs to this app and (b) has
// status='superseded', and returns a typed error otherwise. When
// omitted, behaviour is unchanged — rollback to the most-recent
// superseded deployment. --json (top-level) emits the
// DeploymentResponse on stdout for SDK / e2e consumers.
func cmdRollback(args []string) int {
	if hasHelpFlag(args) {
		PrintUsage(osStdout, rollbackUsage, "rollback")
		return 0
	}
	if len(args) < 1 {
		PrintUsage(os.Stderr, rollbackUsage, "rollback")
		return 1
	}
	slug := args[0]
	var to string
	rest := args[1:]
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		switch {
		case a == "--to":
			i++
			if i >= len(rest) {
				return printErr("Missing value", fmt.Errorf("--to requires a deployment_id"))
			}
			to = rest[i] //nolint:gosec // G602: bounds checked immediately above
		case strings.HasPrefix(a, "--to="):
			to = a[len("--to="):]
		default:
			return printErr("Unknown flag", fmt.Errorf("%q (rollback accepts no positional after <slug>)", a))
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	dep, err := client.RollbackTo(context.Background(), slug, to)
	if err != nil {
		return printErr("Rollback failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(dep))
	}
	if dep.Status != "live" {
		if to != "" {
			PrintOK(osStdout, "Rollback started for %s (%s); validating explicit target %s", dep.ID, dep.Status, to)
			return 0
		}
		PrintOK(osStdout, "Rollback started for %s (%s)", dep.ID, dep.Status)
		return 0
	}
	if to != "" {
		PrintOK(osStdout, "Rolled back to %s (%s) via explicit target %s", dep.ID, dep.Status, to)
		return 0
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
	if jsonOutput {
		return jsonOut(writeJSON(map[string]string{
			"slug":   args[0],
			"status": "parked",
			"state":  "cold",
		}))
	}
	PrintOK(osStdout, "Parked (cold)")
	return 0
}

func cmdWake(args []string) int {
	fs := newFlagSet("wake", flag.ContinueOnError)
	wait := fs.Bool("wait", false, "wait for the requested wake to reach running")
	waitTimeout := fs.Duration("timeout", time.Minute, "maximum time to wait for the requested wake")
	pollInterval := fs.Duration("poll-interval", 250*time.Millisecond, "interval between instance status checks")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 1 || *waitTimeout <= 0 || *pollInterval < 100*time.Millisecond {
		PrintUsage(os.Stderr, "usage: gregale wake [--wait] [--timeout D] [--poll-interval D] <slug>", "park-wake")
		return 1
	}
	slug := fs.Arg(0)
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	response, err := client.Wake(context.Background(), slug)
	if err != nil {
		return printErr("Wake failed", err)
	}
	if strings.TrimSpace(response.WakeID) == "" {
		return printErr("Wake failed", errors.New("server accepted the wake without returning a wake_id"))
	}
	status := "waking"
	instanceID := ""
	if *wait {
		instance, waitErr := waitForAppWake(context.Background(), client, slug, response.WakeID, *waitTimeout, *pollInterval)
		if waitErr != nil {
			return printErr("Wake failed", waitErr)
		}
		status = instance.State
		instanceID = instance.ID
	}
	if jsonOutput {
		receipt := map[string]string{
			"slug":    slug,
			"status":  status,
			"wake_id": response.WakeID,
		}
		if instanceID != "" {
			receipt["instance_id"] = instanceID
		}
		return jsonOut(writeJSON(receipt))
	}
	if *wait {
		PrintOK(osStdout, "Wake completed (%s, instance %s)", response.WakeID, instanceID)
		return 0
	}
	PrintOK(osStdout, "Wake queued (%s)", response.WakeID)
	return 0
}

func waitForAppWake(ctx context.Context, client *Client, slug, wakeID string, timeout, pollInterval time.Duration) (api.InstanceResponse, error) {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	var lastReadErr error
	for {
		instances, err := client.ListInstancesWithHistory(waitCtx, slug, true)
		if err != nil {
			lastReadErr = err
		} else {
			for _, instance := range instances {
				if instance.WakeID != wakeID {
					continue
				}
				switch instance.State {
				case "running":
					return instance, nil
				case "failed", "stopped", "parked", "evicting_account_deleting":
					return api.InstanceResponse{}, fmt.Errorf("wake %s reached terminal state %s", wakeID, instance.State)
				}
			}
		}

		select {
		case <-waitCtx.Done():
			if lastReadErr != nil {
				return api.InstanceResponse{}, fmt.Errorf("wake %s did not reach running within %s (last status read failed: %w)", wakeID, timeout, lastReadErr)
			}
			return api.InstanceResponse{}, fmt.Errorf("wake %s did not reach running within %s", wakeID, timeout)
		case <-ticker.C:
		}
	}
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
	fs := newFlagSet("traffic set", flag.ContinueOnError)
	deployment := fs.String("deployment", "", "deployment id to set the traffic split on")
	percent := fs.Int("percent", -1, "traffic weight in [0, 100]; -1 = unset (server default 100)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
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

// cmdTrafficStatus prints the live deployment weights that currently make up
// an app's routing table. Read access is available on every plan; Free and
// Hobby apps normally show one 100% row while Pro/Scale may show a split.
func cmdTrafficStatus(args []string) int {
	if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
		PrintUsage(os.Stderr, "usage: gregale traffic status <slug>", "traffic")
		return 1
	}
	slug := strings.TrimSpace(args[0])
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	deployments, err := client.ListAppDeploymentsAll(context.Background(), slug)
	if err != nil {
		return printErr("Traffic status failed", err)
	}
	live := make([]api.DeploymentResponse, 0, len(deployments))
	total := 0
	for _, deployment := range deployments {
		if deployment.Status != statusLive {
			continue
		}
		live = append(live, deployment)
		total += deployment.TrafficPercent
	}
	if jsonOutput {
		return jsonOut(writeJSON(struct {
			App          string                   `json:"app"`
			Deployments  []api.DeploymentResponse `json:"deployments"`
			TotalPercent int                      `json:"total_percent"`
		}{App: slug, Deployments: live, TotalPercent: total}))
	}
	if len(live) == 0 {
		_, _ = fmt.Fprintf(osStdout, "No live deployments for app %q.\n", slug)
		return 0
	}
	_, _ = fmt.Fprintln(osStdout, "DEPLOYMENT\tSTATUS\tTRAFFIC")
	for _, deployment := range live {
		_, _ = fmt.Fprintf(osStdout, "%s\t%s\t%d%%\n", deployment.ID, deployment.Status, deployment.TrafficPercent)
	}
	_, _ = fmt.Fprintf(osStdout, "Total\t\t%d%%\n", total)
	return 0
}

// cmdTraffic dispatches the implemented traffic leaves.
func cmdTraffic(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale traffic <set|status> [args]", "traffic")
		return 1
	}
	switch args[0] {
	case "set":
		return cmdTrafficSet(args[1:])
	case "status":
		return cmdTrafficStatus(args[1:])
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
		fs := newFlagSet("domains-add", flag.ContinueOnError)
		domain := fs.String("domain", "", "domain to attach (required)")
		slug := fs.String("app", "", "app slug to attach to (required)")
		if err := fs.Parse(args[1:]); err != nil {
			return 1
		}
		if rejectUnexpectedFlagArgs(fs) {
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
		fmt.Printf("  _faas-verify.%s  TXT  %s\n\n", d.Domain, d.ChallengeToken)
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
	case subDomainsVerify:
		return cmdDomainsVerify(args[1:])
	case subDomainsShow:
		return cmdDomainsShow(args[1:])
	case subDomainsStatus:
		return cmdDomainsStatus(args[1:])
	case subDomainsDoctor:
		return cmdDomainsDoctor(args[1:])
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
		PrintUsage(os.Stderr, "usage: gregale crons <list|add|update|rm|runs> [args]", "crons")
		return 1
	}
	switch args[0] {
	case subList:
		fs := newFlagSet("crons-list", flag.ContinueOnError)
		slug := fs.String("app", "", "app slug (required)")
		if err := fs.Parse(args[1:]); err != nil {
			return 1
		}
		if rejectUnexpectedFlagArgs(fs) {
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
			} else if c.SuspendedReason != "" {
				state = "suspended: " + c.SuspendedReason
			}
			fmt.Printf("%-30s %-15s %s\n", c.Schedule, state, c.Path)
		}
		return 0
	case subAdd:
		fs := newFlagSet("crons-add", flag.ContinueOnError)
		slug := fs.String("app", "", "app slug (required)")
		schedule := fs.String("schedule", "", "cron expression (required)")
		path := fs.String("path", "/", "request path")
		timezone := fs.String("timezone", "", "IANA timezone (defaults to UTC)")
		skipIfRunning := fs.Bool("skip-if-running", false, "skip a scheduled fire while the previous cron run is active")
		if err := fs.Parse(args[1:]); err != nil {
			return 1
		}
		if rejectUnexpectedFlagArgs(fs) {
			return 1
		}
		if *slug == "" || *schedule == "" {
			PrintUsage(os.Stderr, "usage: gregale crons add --app <slug> --schedule '*/5 * * * *' [--path /] [--timezone UTC] [--skip-if-running]", "crons")
			return 1
		}
		client, err := authedClient()
		if err != nil {
			return printErr("Not logged in", err)
		}
		c, err := client.CreateCron(context.Background(), *slug, api.CreateCronRequest{
			AppID: *slug, Schedule: *schedule, Path: *path, Enabled: boolPtr(true),
			Timezone: *timezone, SkipIfRunning: boolPtr(*skipIfRunning),
		})
		if err != nil {
			return printErr("Create failed", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(c))
		}
		PrintOK(osStdout, "Cron scheduled: %s %s", c.Schedule, c.Path)
		return 0
	case subUpdate:
		return cmdCronsUpdate(args[1:])
	case subInfo:
		return cmdCronsInfo(args[1:])
	case subRuns:
		return cmdCronsRuns(args[1:])
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
		if jsonOutput {
			return jsonOut(writeJSON(map[string]any{
				"id":      args[1],
				"status":  "deleted",
				"deleted": true,
			}))
		}
		PrintOK(osStdout, "Removed")
		return 0
	case "run":
		// PR-C / issue #791: `gregale crons run <id>` enqueues a
		// fire-now request. Implementation lives in
		// commands_crons_fire_now.go (cmdCronsRun).
		return cmdCronsRun(args[1:])
	case "fire-now":
		// PR-D / issue #791: poll a fire-now request row by
		// request_id. Implementation in
		// commands_crons_fire_now.go (cmdCronsFireNowGet).
		return cmdCronsFireNowGet(args[1:])
	}
	fmt.Fprintf(os.Stderr, "unknown crons subcommand %q\n", args[0])
	sug, _ := suggestSubcommand(args[0], parent)
	maybeSuggestSub(sug)
	return 1
}

// cronIDPattern is the 32-hex shape used by the API for cron ids
// (CronResponse.ID, the path segment of /v1/crons/{id}). Mirrors
// deploymentIDPattern — same 32-hex convention across the platform.
var cronIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{32}$|^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

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
	if c.SuspendedReason != "" {
		_, _ = fmt.Fprintf(w, "  %-10s %s\n", "suspended:", c.SuspendedReason)
	}
}

// cmdCronsUpdate implements `gregale crons update <id> [--schedule EXPR]
// [--path PATH] [--timezone TZ] [--skip-if-running|--allow-overlap]
// [--enable|--disable]`. Partial-update semantics:
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
		PrintUsage(os.Stderr, "usage: gregale crons update <id> [--schedule EXPR] [--path PATH] [--timezone TZ] [--skip-if-running|--allow-overlap] [--enable|--disable]", "crons")
		return 1
	}
	id := args[0]
	if !cronIDPattern.MatchString(id) {
		PrintUsage(os.Stderr, "usage: gregale crons update <id>   (id is 32 hex chars)", "crons")
		return 1
	}
	fs := newFlagSet("crons-update", flag.ContinueOnError)
	schedule := fs.String("schedule", "", "cron expression (5 fields)")
	path := fs.String("path", "", "request path")
	timezone := fs.String("timezone", "", "IANA timezone (empty resets to UTC)")
	enable := fs.Bool("enable", false, "enable the cron")
	disable := fs.Bool("disable", false, "disable the cron")
	skipIfRunning := fs.Bool("skip-if-running", false, "skip scheduled fires while a prior run is active")
	allowOverlap := fs.Bool("allow-overlap", false, "allow scheduled fires to overlap")
	if err := fs.Parse(args[1:]); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		PrintUsage(os.Stderr, "usage: gregale crons update <id> [--schedule EXPR] [--path PATH] [--timezone TZ] [--skip-if-running|--allow-overlap] [--enable|--disable]", "crons")
		return 1
	}
	if *enable && *disable {
		PrintUsage(os.Stderr, "usage: gregale crons update --enable | --disable (mutually exclusive)", "crons")
		return 1
	}
	if *skipIfRunning && *allowOverlap {
		PrintUsage(os.Stderr, "usage: gregale crons update --skip-if-running | --allow-overlap (mutually exclusive)", "crons")
		return 1
	}
	// Reject no-fields-set early; the server otherwise no-ops and
	// emits a cron-changed notification — a footgun we'd rather
	// catch at the CLI before a pointless network round-trip.
	explicit := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { explicit[f.Name] = true })
	if !explicit["schedule"] && !explicit["path"] && !explicit["timezone"] && !explicit["enable"] && !explicit["disable"] && !explicit["skip-if-running"] && !explicit["allow-overlap"] {
		PrintUsage(os.Stderr, "usage: gregale crons update <id> [--schedule EXPR] [--path PATH] [--timezone TZ] [--skip-if-running|--allow-overlap] [--enable|--disable]", "crons")
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
	if explicit["timezone"] {
		tz := *timezone
		req.Timezone = &tz
	}
	if explicit["enable"] {
		v := true
		req.Enabled = &v
	}
	if explicit["disable"] {
		v := false
		req.Enabled = &v
	}
	if explicit["skip-if-running"] {
		v := true
		req.SkipIfRunning = &v
	}
	if explicit["allow-overlap"] {
		v := false
		req.SkipIfRunning = &v
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
		if hasHelpFlag(args[1:]) {
			PrintUsage(osStdout, "usage: gregale keys add <label>", "keys")
			return 0
		}
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
	case subRotate:
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
	fs := newFlagSet("keys rotate", flag.ContinueOnError)
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
	fs := newFlagSet("keys grace-window", flag.ContinueOnError)
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
	fs := newFlagSet("usage-list", flag.ContinueOnError)
	month := fs.String("month", "", "month (YYYY-MM); default: current month")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
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
	fs := newFlagSet("usage-summary", flag.ContinueOnError)
	month := fs.String("month", "", "month (YYYY-MM); default: current month")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
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
	fs := newFlagSet("invoices", flag.ContinueOnError)
	month := fs.String("month", "", "billing month (YYYY-MM); default: all months")
	before := fs.String("before", "", "pagination cursor (RFC3339Nano)")
	limit := fs.Int("limit", 25, "page size (1..100)")
	if err := fs.Parse(args); err != nil {
		PrintUsage(os.Stderr, "usage: gregale invoices [--month YYYY-MM] [--before C] [--limit N]", "invoices")
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
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
	_, _ = fmt.Fprintf(w, "  %-*s %.3f GB\n", labelWidth, "Egress:", s.UsedEgressGB)
	if s.Executions != nil {
		_, _ = fmt.Fprintf(w, "  %-*s %d runs (%d succeeded, %d failed, %d cancelled)\n",
			labelWidth, "Executions:", s.Executions.Runs, s.Executions.Succeeded,
			s.Executions.Failed, s.Executions.Cancelled)
		_, _ = fmt.Fprintf(w, "  %-*s %d ms wall / %d ms CPU\n",
			labelWidth, "Run compute:", s.Executions.WallTimeMS, s.Executions.CPUTimeMS)
	}
	if s.EgressBillingMode != "" {
		_, _ = fmt.Fprintf(w, "  %-*s %s\n", labelWidth, "Egress mode:", s.EgressBillingMode)
		_, _ = fmt.Fprintf(w, "  %-*s %s\n", labelWidth, "Egress from:", s.EgressBillingFrom)
		_, _ = fmt.Fprintf(w, "  %-*s %.0f GB\n", labelWidth, "Egress incl:", s.IncludedEgressGB)
		_, _ = fmt.Fprintf(w, "  %-*s %.3f GB @ %.3f cents/GB\n", labelWidth, "Egress over:", s.EgressOverageGB,
			float64(s.EgressMillicentsPerGB)/float64(api.MillicentsPerCent))
	}
}

func boolPtr(b bool) *bool { return &b }

// cmdConnect implements `gregale connect <service>`. Two surfaces:
//
//   - github: opens the dashboard's account page where the customer
//     finishes the OAuth + install steps via the slice-8 GitHub App flow.
//   - repo <owner>/<name>: opens the dashboard's /dashboard/apps/new
//     wizard pre-filled with the repo. The dashboard handles the
//     install + bind (PR-3). The CLI never leaves the repo string as
//     a query parameter unvalidated.
//
// We deliberately don't perform the OAuth dance from the CLI:
// the GitHub App install + bind requires the customer's browser
// session (GitHub OAuth + repo permissions), and the only state
// the platform needs (install_id, install_token) belongs in the
// server, not the CLI's token file.
//
// Issue #961 / Mega-B PR-1: `connect repo` widens the trust-root
// decision. The dashboard is the install-token trust root; the CLI
// is the source-of-truth root for the deploy input. See
// docs/adr/0XX-megab-trust-root.md.
func cmdConnect(args []string) int {
	if len(args) < 1 {
		PrintUsage(os.Stderr, "usage: gregale connect {github|repo <owner>/<name>}", "connect")
		return 1
	}
	switch args[0] {
	case svcGithub:
		// `connect github` takes no positional args.
		if len(args) != 1 {
			PrintUsage(os.Stderr, "usage: gregale connect github", "connect")
			return 1
		}
		if _, err := authedClient(); err != nil {
			return printErr("Not logged in", err)
		}
		target := dashboardAccountURL(apiBase())
		if jsonOutput {
			return jsonOut(writeJSON(map[string]any{
				"url":     target,
				"service": "github",
			}))
		}
		fmt.Printf("Opening %s to connect GitHub…\n", target)
		if err := browser.Open(target); err != nil {
			PrintFail(os.Stderr, "Could not open browser: %v", err)
			fmt.Fprintf(os.Stderr, "  Open this URL manually:\n  %s\n", target)
			return 0
		}
		return 0
	case svcRepo:
		// The repo subcommand takes a positional <owner>/<name> after
		// the verb. We invoke the dispatcher with the remaining args
		// so the subcommand handler can run its own validation +
		// usage error. The CLI surfaces the URL even when the
		// customer is not logged in (the dashboard will redirect
		// them to the login page followed by the wizard — the bind
		// flow is server-side, no API key required).
		return cmdConnectRepo(args[1:])
	default:
		PrintFail(os.Stderr, "unknown service %q (supported: %s, %s)", args[0], svcGithub, svcRepo)
		return 1
	}
}

// cmdOpen implements `gregale open <slug>`. Looks up the app's URL via
// the v1 API and launches the OS browser. With --dashboard, opens
// the dashboard's app-detail page instead of the public URL.
//
// Subcommands (Tier A8.1):
//   - docs [--slug <slug>]
//     Opens a public /docs/<slug> page when one exists, or the
//     consolidated /docs/cli reference when no page exists, in the default
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
	fs := newFlagSet("open", flag.ContinueOnError)
	dash := fs.Bool("dashboard", false, "open the dashboard page instead of the live URL")
	flags, positional := splitArgsForFlags(args, "dashboard")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) > 1 {
		PrintUsage(os.Stderr, "usage: gregale open [<slug>] [--dashboard] (slug defaults to linked project context)", "open")
		return 1
	}
	slug := ""
	if len(positional) == 1 {
		slug = positional[0]
	} else {
		var resolveErr error
		slug, resolveErr = resolveAppFlagOrContext("")
		if resolveErr != nil {
			if errors.Is(resolveErr, errProjectContextNotFound) {
				PrintUsage(os.Stderr, "usage: gregale open [<slug>] [--dashboard] (slug defaults to linked project context)", "open")
				return 1
			}
			return printErr("Could not read local project context", resolveErr)
		}
	}
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
		state, err := probeWakeState(target, openWakeProbeTimeout)
		switch {
		case err != nil:
			_, _ = fmt.Fprintln(osStdout, "Opening.")
		case state:
			_, _ = fmt.Fprintln(osStdout, "Waking app (cold start) — opening in your browser.")
			deadline := time.Now().Add(openWakeDeadline)
			for state && time.Now().Before(deadline) {
				time.Sleep(openWakePollInterval)
				state, _ = probeWakeState(target, openWakeProbeTimeout)
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
//   - positional arg: `gregale open docs storage` → /docs/storage
//   - --slug flag:    `gregale open docs --slug queue` → /docs/cli
//   - both or neither is an error (mutually exclusive, but at
//     least one is required — opening the bare docs root would
//     be confusing; `gregale man` already covers that case).
//   - empty slug defaults to the public docs root (/docs).
//
// The DocsTopic constant docsOpenTopic is exposed so the manifest
// entry below can pin the docs URL slug for the `open` command's
// own man page.
func cmdOpenDocs(args []string) int {
	fs := newFlagSet("open docs", flag.ContinueOnError)
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
	// Slug sanitization keeps the JSON result deterministic. The public docs
	// site has one consolidated CLI page, so command topics are resolved by
	// docsURLForTopic rather than appended to a retired per-command route.
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
	target := docsURLForTopic(safeSlug)
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
// that's the API base minus /v1; the gatewayd-public reverse-proxy serves
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

// dashboardAppsNewURL is the canonical "connect <repo>" entry point.
// Issue #961 / Mega-B PR-1: the CLI opens the dashboard's new-app
// wizard pre-filled with the repo's owner/name. The wizard renders
// the GitHub install + bind flows in the browser so the cookie
// session stays the install-token trust root.
//
// The query parameter is the GitHub-style "owner/name" string the
// customer already knows. The dashboard wizard decodes it (after
// sessionAuth) and lists the customer's installable repos for the
// selected installation. Encode it as a query value so this helper
// remains correct even when called outside the validated CLI path.
func dashboardAppsNewURL(api, ownerRepo string) string {
	q := url.Values{}
	q.Set("repo", ownerRepo)
	return dashboardBaseURL(api) + "/dashboard/apps/new?" + q.Encode()
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
	fs := newFlagSet("logs", flag.ContinueOnError)
	follow := fs.Bool("follow", false, "follow new lines")
	deployment := fs.String("deployment", "", "deployment id (default: latest)")
	grep := fs.String("grep", "", "only show lines matching this substring")
	since := fs.String("since", "", "only show lines at or after this RFC3339 timestamp")
	level := fs.String("level", "", "only show lines at this level (info|warn|error)")
	archive := fs.Bool("archive", false, "read durable logs for one instance and UTC day")
	archiveInstance := fs.String("instance", "", "instance id for --archive")
	archiveDate := fs.String("date", "", "UTC day for --archive (YYYY-MM-DD)")
	// Error-explanations cluster (spec §6.4 amendment 1): when the
	// stream ends, print a 3-line summary covering the last failure
	// (lifted from the deployment's persisted error_code), the count
	// of error-level lines, and the top 3 most-frequent error
	// patterns. The summary is what makes `gregale logs <slug>
	// --explain` actionable — the customer no longer has to read the
	// whole stream to know which error fired.
	explain := fs.Bool("explain", false, "on stream end, print a 3-line summary (failure, error count, top patterns)")
	if err := parseAppLogFlags(fs, args); err != nil {
		PrintUsage(os.Stderr, "usage: gregale logs <slug> [--follow] [--deployment ID] [--grep SUBSTR] [--since RFC3339] [--level info|warn|error] [--explain] [--archive --instance ID --date YYYY-MM-DD]", "logs")
		return 1
	}
	if *explain && jsonOutput {
		PrintUsage(osStderr, "--explain cannot be combined with --json (explanation is human-readable)", "logs")
		return 2
	}
	if fs.NArg() > 1 {
		PrintUsage(os.Stderr, "usage: gregale logs [<slug>] [--follow] [--deployment ID] [--grep SUBSTR] [--since RFC3339] [--level info|warn|error] [--explain] [--archive --instance ID --date YYYY-MM-DD] (slug defaults to linked project context)", "logs")
		return 1
	}
	slug := ""
	if fs.NArg() == 1 {
		slug = fs.Arg(0)
	} else {
		var resolveErr error
		slug, resolveErr = resolveAppFlagOrContext("")
		if resolveErr != nil {
			if errors.Is(resolveErr, errProjectContextNotFound) {
				PrintUsage(os.Stderr, "usage: gregale logs [<slug>] ... (slug defaults to linked project context)", "logs")
				return 1
			}
			return printErr("Could not read local project context", resolveErr)
		}
	}
	archiveRequested := *archive || *archiveInstance != "" || *archiveDate != ""
	var archiveSelector *api.ArchiveLogSelector
	if archiveRequested {
		if !*archive || *archiveInstance == "" || *archiveDate == "" {
			PrintUsage(os.Stderr, "--archive, --instance ID, and --date YYYY-MM-DD must be used together", "logs")
			return 2
		}
		if *follow || *deployment != "" || *grep != "" || *since != "" || *level != "" {
			PrintUsage(os.Stderr, "--archive cannot be combined with --follow, --deployment, --grep, --since, or --level", "logs")
			return 2
		}
		parsedDate, err := time.Parse("2006-01-02", *archiveDate)
		if err != nil || parsedDate.Format("2006-01-02") != *archiveDate {
			PrintUsage(os.Stderr, "--date must use YYYY-MM-DD (for example, 2026-09-14)", "logs")
			return 2
		}
		archiveSelector = &api.ArchiveLogSelector{InstanceID: *archiveInstance, Date: *archiveDate}
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
	return runLogs(context.Background(), slug, *deployment, api.LogFilter{
		Grep:  *grep,
		Since: *since,
		Level: *level,
	}, archiveSelector, *follow, *explain)
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
	fs := newFlagSet("logs tail", flag.ContinueOnError)
	follow := fs.Bool("follow", false, "follow new lines (alias always follows; flag is redundant)")
	deployment := fs.String("deployment", "", "deployment id (default: latest)")
	grep := fs.String("grep", "", "only show lines matching this substring")
	since := fs.String("since", "", "only show lines at or after this RFC3339 timestamp")
	level := fs.String("level", "", "only show lines at this level (info|warn|error)")
	if err := parseAppLogFlags(fs, args); err != nil {
		PrintUsage(os.Stderr, "usage: gregale logs tail <slug> [--deployment ID] [--grep SUBSTR] [--since RFC3339] [--level info|warn|error]", "logs")
		return 1
	}
	if fs.NArg() > 1 {
		PrintUsage(os.Stderr, "usage: gregale logs tail [<slug>] [--deployment ID] [--grep SUBSTR] [--since RFC3339] [--level info|warn|error] (slug defaults to linked project context)", "logs")
		return 1
	}
	slug := ""
	if fs.NArg() == 1 {
		slug = fs.Arg(0)
	} else {
		var resolveErr error
		slug, resolveErr = resolveAppFlagOrContext("")
		if resolveErr != nil {
			if errors.Is(resolveErr, errProjectContextNotFound) {
				PrintUsage(os.Stderr, "usage: gregale logs tail [<slug>] ... (slug defaults to linked project context)", "logs")
				return 1
			}
			return printErr("Could not read local project context", resolveErr)
		}
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
	return runLogs(context.Background(), slug, *deployment, api.LogFilter{
		Grep:  *grep,
		Since: *since,
		Level: *level,
	}, nil, true, false)
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
func runLogs(ctx context.Context, slug, deployment string, filter api.LogFilter, archive *api.ArchiveLogSelector, follow bool, explain bool) int {
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	var body io.ReadCloser
	if archive != nil {
		body, err = client.StreamAppArchivedLogs(ctx, slug, *archive)
	} else {
		body, err = client.StreamAppLogs(ctx, slug, deployment, follow, filter)
	}
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
	// --explain accumulator. Only allocates when explain=true (the
	// common path doesn't pay for the map). The collector runs the
	// stream through, captures error-level lines + pattern counts,
	// and emits a 3-line summary on stream end. The summary lives
	// here (not in a separate file) because it shares the SSE loop
	// and pulling it out would force the collector across the
	// channel boundary — over-engineered for the surface size.
	var collector *explainCollector
	if explain {
		collector = newExplainCollector(slug, deployment)
	}
	code, streamErr := consumeLogStream(ctx, dec.Events(), dec.Errors(), func(e api.Event) (bool, int) {
		// Move 4 (issue #254): the apid stub emits `event: degraded`
		// when schedd's StreamAppLogs RPC isn't wired yet (the
		// production-side path is a follow-up PR — this commit only
		// swaps the Move 3 stub for the real SSE shape on the apid
		// side). Move 3's `not_implemented` shape is dead code;
		// removed.
		if e.Event == "degraded" {
			if jsonOutput {
				_ = writeJSONProblem(appLogsDegradedProblem(e.Data))
			} else {
				fmt.Fprintln(os.Stderr, appLogsDegradedMessage(e.Data))
			}
			if collector != nil && !jsonOutput {
				collector.flush(osStdout)
			}
			return true, 3
		}
		if e.Event == "end" {
			if collector != nil && !jsonOutput {
				collector.flush(osStdout)
			}
			return true, 0
		}
		if e.Data != "" {
			_, _ = fmt.Fprintln(osStdout, e.Data)
			if collector != nil {
				collector.observe(e.Data)
			}
		}
		return false, 0
	})
	if collector != nil && !jsonOutput && code == 130 {
		collector.flush(osStdout)
	}
	if streamErr != nil {
		return printErr("Stream closed", streamErr)
	}
	return code
}

// consumeLogStream preserves SSE wire order when the decoder makes both its
// event and terminal-error channels ready at once. A select may otherwise
// choose io.EOF before a buffered terminal frame, turning a degraded stream
// into a false success. Once a terminal error is observed we drain the event
// channel before interpreting it; Decoder closes that channel immediately
// after publishing the terminal condition.
func consumeLogStream(ctx context.Context, events <-chan api.Event, errs <-chan error, visit func(api.Event) (bool, int)) (int, error) {
	handle := func(event api.Event) (bool, int) {
		if visit == nil {
			return false, 0
		}
		return visit(event)
	}
	terminal := func(err error) (int, error) {
		if err == nil || errors.Is(err, io.EOF) {
			return 0, nil
		}
		return 0, err
	}
	for {
		select {
		case <-ctx.Done():
			return 130, nil
		case event, ok := <-events:
			if !ok {
				err, ok := <-errs
				if !ok {
					return 0, nil
				}
				return terminal(err)
			}
			if done, code := handle(event); done {
				return code, nil
			}
		case err, ok := <-errs:
			if !ok {
				err = nil
			}
			for {
				select {
				case <-ctx.Done():
					return 130, nil
				case event, more := <-events:
					if !more {
						return terminal(err)
					}
					if done, code := handle(event); done {
						return code, nil
					}
				}
			}
		}
	}
}

// explainCollector aggregates log lines for the --explain summary.
// One instance per cmdLogs invocation. Allocation: per-line (a
// map[string]int for pattern counts + per-level counters). The
// memory footprint is bounded by the stream length (typical wake
// log is <1k lines); on longer streams the pattern-count map
// stays bounded because the buckets are normalised to a 64-byte
// prefix (see observe()).
type explainCollector struct {
	slug       string
	deployment string
	errorCount int
	warnCount  int
	infoCount  int
	patterns   map[string]int
	lastError  string
}

// explainLogRecord is the production app-log SSE data envelope. Line is a
// pointer so a JSON object without a line field can still fall through to the
// legacy text parser instead of being treated as an empty log line.
type explainLogRecord struct {
	Line     *string         `json:"line"`
	Stream   string          `json:"stream"`
	Level    json.RawMessage `json:"level"`
	Severity json.RawMessage `json:"severity"`
}

// newExplainCollector constructs the per-invocation collector.
// The patterns map is allocated lazily on first observe() — most
// log lines are info/warn, and we only count errors.
func newExplainCollector(slug, deployment string) *explainCollector {
	return &explainCollector{
		slug:       slug,
		deployment: deployment,
		patterns:   map[string]int{},
	}
}

// observe ingests one SSE data line. Production streams carry a JSON
// envelope with a raw guest line plus optional structured severity; older
// streams use the legacy `{ts} {level} {message}` shape. The latter remains
// byte-compatible while the former gets a conservative stdout/stderr
// fallback when no level was classified server-side.
func (c *explainCollector) observe(line string) {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "{") {
		var record explainLogRecord
		if err := json.Unmarshal([]byte(trimmed), &record); err == nil && record.Line != nil {
			msg := *record.Line
			level := explainStructuredLevel(record.Level, record.Severity)
			if level == "" {
				if nestedLevel, nestedMessage := explainNestedStructuredLine(msg); nestedLevel != "" {
					level, msg = nestedLevel, nestedMessage
				}
			}
			if level == "" {
				level = explainFallbackLevel(record.Stream, msg)
			}
			c.observeLevel(level, msg)
			return
		}
	}
	// Split into at most 3 parts: ts, level, message.
	parts := strings.SplitN(line, " ", 3)
	if len(parts) < 3 {
		return
	}
	c.observeLevel(parts[1], parts[2])
}

// observeLevel updates the summary counters for an already-canonical level.
// Keeping this bookkeeping in one place prevents structured and legacy rows
// from drifting apart.
func (c *explainCollector) observeLevel(level, msg string) {
	switch level {
	case "error":
		c.errorCount++
		c.lastError = msg
		// Pattern bucket: first 64 bytes of the message. Enough
		// to coalesce the same error fired 100x into one bucket,
		// small enough to keep the map bounded. UTF-8 safe: a
		// naive byte-slice on a multi-byte rune (e.g. an emoji
		// or CJK character) would split the rune and produce an
		// invalid prefix that flush() can't render. Back off to
		// the last rune boundary when the 64th byte lands in the
		// middle of a sequence.
		prefix := msg
		if len(prefix) > 64 {
			prefix = prefix[:64]
			for len(prefix) > 0 {
				if r, size := utf8.DecodeLastRuneInString(prefix); r == utf8.RuneError && size == 1 {
					prefix = prefix[:len(prefix)-1]
				} else {
					break
				}
			}
		}
		c.patterns[prefix]++
	case "warn":
		c.warnCount++
	case "info":
		c.infoCount++
	}
}

// explainStructuredLevel maps the string and numeric severity encodings used
// by common guest loggers (Pino/Zap/Cloud Logging) to the CLI's three-level
// vocabulary. If both fields are present, the most severe recognized value
// wins.
func explainStructuredLevel(fields ...json.RawMessage) string {
	best := ""
	for _, raw := range fields {
		value := strings.TrimSpace(string(raw))
		if value == "" || value == "null" {
			continue
		}
		level := ""
		if strings.HasPrefix(value, "\"") {
			var text string
			if json.Unmarshal([]byte(value), &text) == nil {
				level = explainNormalizeLevel(text)
			}
		} else if number, err := strconv.Atoi(value); err == nil {
			level = explainNormalizeNumericLevel(number)
		}
		if explainLevelRank(level) > explainLevelRank(best) {
			best = level
		}
	}
	return best
}

func explainNormalizeLevel(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "trace", "debug", "info", "notice":
		return "info"
	case "warn", "warning":
		return "warn"
	case "error", "err", "fatal", "critical", "alert", "emergency":
		return "error"
	default:
		return ""
	}
}

func explainNormalizeNumericLevel(value int) string {
	switch {
	case value >= 10 && value <= 30:
		return "info"
	case value == 40:
		return "warn"
	case value >= 50:
		return "error"
	default:
		return ""
	}
}

func explainLevelRank(level string) int {
	switch level {
	case "error":
		return 2
	case "warn":
		return 1
	case "info":
		return 0
	default:
		return -1
	}
}

// explainNestedStructuredLine handles a guest line that is itself a JSON log
// object when the server did not attach its additive outer level field.
func explainNestedStructuredLine(line string) (string, string) {
	if !strings.HasPrefix(strings.TrimSpace(line), "{") {
		return "", line
	}
	var record struct {
		Level    json.RawMessage `json:"level"`
		Severity json.RawMessage `json:"severity"`
		Msg      string          `json:"msg"`
		Message  string          `json:"message"`
		Error    string          `json:"error"`
	}
	if err := json.Unmarshal([]byte(line), &record); err != nil {
		return "", line
	}
	level := explainStructuredLevel(record.Level, record.Severity)
	if level == "" {
		return "", line
	}
	for _, message := range []string{record.Msg, record.Message, record.Error} {
		if message != "" {
			return level, message
		}
	}
	return level, line
}

// explainFallbackLevel gives raw guest output a useful, deterministic level
// when no structured field was emitted. Explicit error/warning words win;
// stderr is otherwise a warning and stdout (or an unknown stream) is info.
func explainFallbackLevel(stream, msg string) string {
	lower := strings.ToLower(msg)
	for _, keyword := range []string{"fatal", "panic", "error", "failed", "failure"} {
		if strings.Contains(lower, keyword) {
			return "error"
		}
	}
	for _, keyword := range []string{"warn", "warning"} {
		if strings.Contains(lower, keyword) {
			return "warn"
		}
	}
	if strings.EqualFold(strings.TrimSpace(stream), "stderr") {
		return "warn"
	}
	return "info"
}

// flush emits the 3-line summary on stream end. Output shape:
//
//	── explain: <slug> (deployment <id>) ──
//	error:  <lastError or "none">
//	levels: error=N warn=N info=N
//	top:    pattern1 (Nx) | pattern2 (Nx) | pattern3 (Nx)
//
// The summary is printed on os.Stdout (not os.Stderr) so a
// `gregale logs <slug> --explain > out.txt` captures both the
// stream AND the summary. Error path (when there's no failure)
// prints "(none)" so the script-friendly shape is preserved.
func (c *explainCollector) flush(w io.Writer) {
	_, _ = fmt.Fprintf(w, "\n── explain: %s", c.slug)
	if c.deployment != "" {
		_, _ = fmt.Fprintf(w, " (deployment %s)", c.deployment)
	}
	_, _ = fmt.Fprintln(w, " ──")
	if c.lastError == "" {
		_, _ = fmt.Fprintln(w, "error:  (none)")
	} else {
		_, _ = fmt.Fprintf(w, "error:  %s\n", c.lastError)
	}
	_, _ = fmt.Fprintf(w, "levels: error=%d warn=%d info=%d\n", c.errorCount, c.warnCount, c.infoCount)
	top := topPatterns(c.patterns, 3)
	if len(top) == 0 {
		_, _ = fmt.Fprintln(w, "top:    (no error patterns)")
	} else {
		_, _ = fmt.Fprintf(w, "top:    %s\n", strings.Join(top, " | "))
	}
}

// topPatterns returns the top-N error patterns by count, formatted
// as "pattern (Nx)". Stable sort: ties resolve to alphabetical
// order so the output is deterministic. Empty input → empty slice.
func topPatterns(patterns map[string]int, n int) []string {
	if len(patterns) == 0 {
		return nil
	}
	type kv struct {
		k string
		v int
	}
	pairs := make([]kv, 0, len(patterns))
	for k, v := range patterns {
		pairs = append(pairs, kv{k, v})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].v != pairs[j].v {
			return pairs[i].v > pairs[j].v
		}
		return pairs[i].k < pairs[j].k
	})
	if len(pairs) > n {
		pairs = pairs[:n]
	}
	out := make([]string, len(pairs))
	for i, p := range pairs {
		out[i] = fmt.Sprintf("%s (N%d)", p.k, p.v)
	}
	return out
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
//
// ADR-117 §3: also drives the 6-row deploy progress ticker via
// `event: stage` frames. The ticker is constructed before the SSE
// decoder loop and `Close()`d on every exit path so the customer's
// terminal never shows a half-drawn block. The `enabled` flag
// short-circuits the constructor when the customer piped the
// output (`gregale deploy … | tee /tmp/log`) — the static fallback
// in renderStageSummary is the path that fires instead.
type streamDeployOptions struct {
	onStage         func(string, string, int64, string)
	onTerminal      func(api.DeploymentResponse) int
	onFailure       func(api.DeploymentResponse, string, string)
	prefixBuildLogs bool
	quiet           bool
	waitTimeout     time.Duration
	waitForRollout  bool
}

func streamDeployLogsContextWithOptions(ctx context.Context, c *Client, dep api.DeploymentResponse, appSlug string, opts streamDeployOptions) int {
	waitTimeout := opts.waitTimeout
	if waitTimeout <= 0 {
		waitTimeout = defaultDeployWaitTimeout
	}
	waitCtx, cancel := context.WithTimeout(ctx, waitTimeout)
	defer cancel()
	if !opts.quiet {
		PrintProgress(osStdout, "build queued for %s (deployment %s)", dep.AppID, dep.ID)
	}
	terminalDeploymentWithFailure := func(d api.DeploymentResponse, phase, reason string) int {
		if opts.waitForRollout && d.Status == statusLive {
			// The SSE terminal frame contains only lifecycle status on some
			// server versions. Refresh once so the rollout fields are present
			// before deciding whether safe deploy is actually complete.
			d = deploymentWithReceipt(waitCtx, c, d)
			if !deploymentRolloutComplete(d) {
				final, ok := waitForDeploymentRollout(waitCtx, c, d)
				if !ok {
					if errors.Is(waitCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
						warnDeploymentRolloutTimeout(appSlug, dep.ID, waitTimeout)
						return 3
					}
					return 130
				}
				d = final
			}
			if d.Status == statusLive && d.RolloutState == rolloutStateAborted {
				return renderSuccessfulDeployment(ctx, c, d, appSlug)
			}
		}
		if d.Status == deploymentStatusFailed && opts.onFailure != nil {
			opts.onFailure(d, phase, reason)
		}
		if opts.onTerminal != nil {
			return opts.onTerminal(d)
		}
		return terminalExitForDeploymentWithFailureContext(ctx, c, d, appSlug, nil)
	}
	terminalDeployment := func(d api.DeploymentResponse) int {
		return terminalDeploymentWithFailure(d, "", d.Error)
	}
	terminalBuild := func(b api.BuildResponse) int {
		status := statusLive
		if b.Status == buildStatusFailed {
			status = deploymentStatusFailed
		}
		dep := api.DeploymentResponse{ID: b.DeploymentID, BuildID: b.ID, Status: status, Error: b.FailureClass}
		if status == deploymentStatusFailed && opts.onFailure != nil {
			opts.onFailure(dep, "image_build", b.FailureClass)
		}
		if status == statusLive {
			if final, ok := pollDeploymentFinalUntilContext(waitCtx, c, dep, waitTimeout); ok {
				return terminalDeployment(final)
			}
			return 3
		}
		if opts.onTerminal != nil {
			return opts.onTerminal(dep)
		}
		return terminalExitForBuildWithFailureContext(ctx, c, b, appSlug, nil)
	}
	body, err := c.StreamDeploymentLogs(waitCtx, dep.ID, nil, 0, true)
	if err != nil {
		if waitCtx.Err() != nil {
			if errors.Is(waitCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
				warnDeploymentTimeoutForMode(appSlug, dep.ID, waitTimeout, opts.waitForRollout)
				return 3
			}
			return 130
		}
		// Stream unreachable up front — first try the new
		// /v1/builds/{id} poller (DEPLOY-PROV-6 / ADR-089); only if
		// the build is still queued/running OR the new endpoint is
		// unavailable do we fall through to the legacy
		// pollDeploymentFinal. A fast tarball deploy on a slow link
		// is the canonical case where the stream never opened and
		// the build row is already terminal.
		if b, ok := pollBuildStatusContext(waitCtx, c, dep, 5*time.Second); ok {
			return terminalBuild(b)
		}
		if final, ok := pollDeploymentFinalContext(waitCtx, c, dep); ok {
			return terminalDeployment(final)
		}
		PrintWarn(os.Stderr, "stream unreachable; follow manually: gregale logs %s --deployment %s --follow", appSlug, dep.ID)
		return 3
	}
	defer func() { _ = body.Close() }()
	dec := api.NewDecoder(body)
	dec.SetCloseFn(body.Close)
	defer func() { _ = dec.Close() }()
	// ADR-117 §3: ticker construction happens AFTER the decoder so
	// a decoder init failure doesn't draw a half-rendered block.
	tickerWriter := io.Writer(osStdout)
	if opts.quiet {
		tickerWriter = io.Discard
	}
	ticker := renderStageTicker(tickerWriter)
	defer ticker.Close()
	failedStage := ""
	failedReason := ""
streamLoop:
	for {
		select {
		case <-waitCtx.Done():
			if errors.Is(waitCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
				warnDeploymentTimeoutForMode(appSlug, dep.ID, waitTimeout, opts.waitForRollout)
				return 3
			}
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
				if !opts.quiet && json.Unmarshal([]byte(e.Data), &entry) == nil && entry.Line != "" {
					if opts.prefixBuildLogs {
						fmt.Printf("build | %s\n", entry.Line)
					} else {
						fmt.Println(entry.Line)
					}
				}
			case "stage":
				// ADR-117 §3: server-side stage diff — drive the
				// ticker's per-row state. The decoder's `e.Data`
				// is the verbatim JSON line so the struct
				// shape mirrors what the server emits at
				// cmd/apid/handlers_ext.go::emitStageFrame:
				// {"name", "started_at", "duration_ms",
				//  "status", "reason"?}. Unknown statuses
				// pass through unchanged — the ticker treats
				// them as "pending" so a future server-side
				// status string renders without a CLI update.
				var stage struct {
					Name       string `json:"name"`
					Status     string `json:"status"`
					DurationMs int64  `json:"duration_ms"`
					Reason     string `json:"reason"`
				}
				if json.Unmarshal([]byte(e.Data), &stage) == nil && stage.Name != "" {
					ticker.HandleStageFrame(stage.Name, stage.Status, stage.DurationMs, stage.Reason)
					if stage.Status == stageStatusFailed {
						failedStage = stage.Name
						failedReason = stage.Reason
					}
					if opts.onStage != nil {
						opts.onStage(stage.Name, stage.Status, stage.DurationMs, stage.Reason)
					}
				}
			case statusLiteral:
				var status struct {
					Status string `json:"status"`
				}
				if json.Unmarshal([]byte(e.Data), &status) == nil && isTerminalDeploymentStatus(status.Status) {
					terminal := dep
					terminal.Status = status.Status
					if status.Status == statusLive && len(dep.StageState) > 0 {
						if got, err := c.GetDeployment(waitCtx, dep.ID); err == nil && isCompletedDeployment(got) {
							return terminalDeploymentWithFailure(got, failedStage, failedReason)
						}
						continue
					}
					return terminalDeploymentWithFailure(terminal, failedStage, failedReason)
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
				PrintWarn(os.Stderr, "stream closed; follow manually: gregale logs %s --deployment %s --follow", appSlug, dep.ID)
				return 3
			default:
				// Unknown frame shape — print raw so the customer can see it.
				if e.Data != "" {
					if !opts.quiet {
						if opts.prefixBuildLogs {
							fmt.Printf("build | %s\n", e.Data)
						} else {
							fmt.Println(e.Data)
						}
					}
				}
			}
		case err := <-dec.Errors():
			if waitCtx.Err() != nil {
				if errors.Is(waitCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
					warnDeploymentTimeoutForMode(appSlug, dep.ID, waitTimeout, opts.waitForRollout)
					return 3
				}
				return 130
			}
			if errors.Is(err, io.EOF) {
				break streamLoop
			}
			PrintWarn(os.Stderr, "stream closed; follow manually: gregale logs %s --deployment %s --follow", appSlug, dep.ID)
			return 3
		}
	}
	if waitCtx.Err() != nil {
		if errors.Is(waitCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
			warnDeploymentTimeoutForMode(appSlug, dep.ID, waitTimeout, opts.waitForRollout)
			return 3
		}
		return 130
	}
	// Stream ended without a terminal frame — poll the new
	// /v1/builds/{id} endpoint (DEPLOY-PROV-6 / ADR-089, issue
	// #741) so a fast build that raced the SSE open isn't reported
	// as "follow manually" when we actually have the answer. Only
	// fall back to pollDeploymentFinal when the new poll reports
	// the build is still queued or running.
	if b, ok := pollBuildStatusContext(waitCtx, c, dep, 60*time.Second); ok {
		return terminalBuild(b)
	}
	// Tarball/function deployments created by older API paths may not carry
	// BuildID.  In that case the build poll above is intentionally skipped,
	// and a single deployment GET is too early: the build may have finished
	// while the scheduler is still priming and parking the VM.  Keep polling
	// the deployment row through that recovery window so a healthy deployment
	// is not reported as exit 3 merely because the SSE stream ended first.
	if final, ok := pollDeploymentFinalUntilContext(waitCtx, c, dep, waitTimeout); ok {
		return terminalDeployment(final)
	}
	if errors.Is(waitCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
		warnDeploymentTimeoutForMode(appSlug, dep.ID, waitTimeout, opts.waitForRollout)
		return 3
	}
	PrintWarn(os.Stderr, "stream ended without a terminal frame; follow manually: gregale logs %s --deployment %s --follow", appSlug, dep.ID)
	return 3
}

// pollDeploymentFinal does one cheap GET on the deployment row and
// returns (final, true) when status is live or failed. Returns
// (_, false) on any error or non-terminal status — the caller treats
// both as "no answer, give up cleanly".
//
// Deprecated by DEPLOY-PROV-6 / ADR-089 (issue #741): pollBuildStatus
// is the more-correct fallback now that /v1/builds/{id} exists.
// pollDeploymentFinal stays as a last-ditch safety net so a server
// where /v1/builds/{id} is unavailable still degrades gracefully;
// streamDeployLogs prefers the new path.
func pollDeploymentFinalContext(ctx context.Context, c *Client, dep api.DeploymentResponse) (api.DeploymentResponse, bool) {
	got, err := c.GetDeployment(ctx, dep.ID)
	if err != nil {
		return api.DeploymentResponse{}, false
	}
	if isCompletedDeployment(got) {
		return got, true
	}
	return api.DeploymentResponse{}, false
}

// pollDeploymentFinalUntil is the deployment-row counterpart to
// pollBuildStatus. It is used when the create response has no BuildID, so the
// deployment status is the only durable terminal signal available to the CLI.
// The first GET is immediate; subsequent requests use a small capped backoff
// and are bounded by deadline.
func pollDeploymentFinalUntilContext(ctx context.Context, c *Client, dep api.DeploymentResponse, deadline time.Duration) (api.DeploymentResponse, bool) {
	if deadline <= 0 {
		return pollDeploymentFinalContext(ctx, c, dep)
	}
	pollCtx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	backoff := time.Second
	for {
		if final, ok := pollDeploymentFinalContext(pollCtx, c, dep); ok {
			return final, true
		}
		timer := time.NewTimer(backoff)
		select {
		case <-pollCtx.Done():
			timer.Stop()
			return api.DeploymentResponse{}, false
		case <-timer.C:
		}
		if backoff < 5*time.Second {
			backoff *= 2
		}
	}
}

// pollBuildStatus polls GET /v1/builds/{id} until the build reaches
// a terminal status (succeeded|failed) or the deadline elapses.
// Replaces the one-shot pollDeploymentFinal the SSE fallback in
// streamDeployLogs used before DEPLOY-PROV-6 / ADR-089; with a real
// status endpoint there's no reason to give up after a single GET.
//
// Backoff: 1s base, capped at 5s, jittered ±10% to avoid the
// thundering-herd many CI jobs would trigger when they all exit SSE
// at the same instant. Deadline defaults to 60s; CLI flow currently
// uses the default — a future --wait flag could override.
//
// Context: each iteration derives a per-call context.WithTimeout
// capped at the remaining budget, so a hung server connection
// (e.g. server-side stall without an http.Client timeout firing)
// can't block the loop past the deadline. The SDK's http.Client
// also enforces a 30s per-call timeout as a second line of
// defence; both work together so the worst-case wall-clock here
// is `deadline` even on a pathological server.
//
// Returns (BuildResponse, true) on terminal status; (zero, false)
// on deadline elapse or persistent transient error so the SSE caller
// can fall back to the "follow manually" hint.
// Real-time knobs for the wait loops below. Package-level so tests can
// shrink wall-clock budgets to milliseconds; production never changes
// them. See docs: `gregale open` cold-wake wait (UX §6.4) and the build
// status poll (PROV-6).
var (
	openWakeProbeTimeout    = 2 * time.Second
	openWakeDeadline        = 8 * time.Second
	openWakePollInterval    = 500 * time.Millisecond
	buildPollInitialBackoff = 1 * time.Second
)

func pollBuildStatus(c *Client, dep api.DeploymentResponse, deadline time.Duration) (api.BuildResponse, bool) {
	return pollBuildStatusContext(context.Background(), c, dep, deadline)
}

func pollBuildStatusContext(ctx context.Context, c *Client, dep api.DeploymentResponse, deadline time.Duration) (api.BuildResponse, bool) {
	if dep.BuildID == "" {
		// No build_id on the deployment row — server pre-dates
		// PROV-6, or the deployment was created via the fast-
		// path that skips the builds table. Bail out so the
		// caller falls through to pollDeploymentFinal.
		return api.BuildResponse{}, false
	}
	// parent context is the wall-clock deadline; per-iteration
	// children derive from this so the loop honors the budget.
	parent, cancelParent := context.WithTimeout(ctx, deadline)
	defer cancelParent()
	end := time.Now().Add(deadline)
	backoff := buildPollInitialBackoff
	for time.Now().Before(end) {
		// Per-call timeout: remaining budget, capped at the SDK's
		// 30s per-request timeout (lower of the two wins). Keeps
		// a single hung request from blocking past deadline.
		remaining := time.Until(end)
		if remaining <= 0 {
			return api.BuildResponse{}, false
		}
		callCtx, cancelCall := context.WithTimeout(parent, remaining)
		b, err := c.GetBuildsId(callCtx, dep.BuildID)
		cancelCall()
		if err == nil && isTerminalBuildStatus(b.Status) {
			return b, true
		}
		// Jitter ±10% of the current backoff so N concurrent CI
		// jobs don't wake at the same wall-clock tick. Pseudo-
		// random via time.Now().UnixNano() avoids a math/rand
		// import (and its seeding dance in Go 1.20+). The mod
		// picks magnitude in [0, span); subtracting span/2
		// recentres it so the signed jitter is symmetric around
		// zero. Review finding #3 pinned the previous version
		// (which was always non-negative, producing [0, +20%]
		// rather than the documented ±20%) — this formula gives
		// the symmetric ±10% spread the docstring promises.
		span := int64(backoff / 5)
		if span < 1 {
			span = 1
		}
		jitter := time.Duration(time.Now().UnixNano()%span) - time.Duration(span/2)
		timer := time.NewTimer(backoff + jitter)
		select {
		case <-parent.Done():
			timer.Stop()
			return api.BuildResponse{}, false
		case <-timer.C:
		}
		if backoff < 5*time.Second {
			backoff *= 2
		}
	}
	return api.BuildResponse{}, false
}

func terminalExitForDeploymentWithFailureContext(ctx context.Context, c *Client, d api.DeploymentResponse, appSlug string, onFailure func(api.DeploymentResponse, string, string)) int {
	if d.Status == statusLive {
		return renderSuccessfulDeployment(ctx, c, d, appSlug)
	}
	if onFailure != nil {
		onFailure(d, "", d.Error)
	}
	return renderDeployFailure(d)
}

// terminalExitForBuild maps a polled terminal BuildResponse to a CLI
// exit code. Mirrors terminalExitForDeployment but reads from the
// build row (DEPLOY-PROV-6 / ADR-089, issue #741) — the polled
// build row lacks the rich Error string from the deployment row,
// so on failure we render a compact "BuildStatus=failed
// failure_class=…" block and exit 2 (same exit-code convention as
// terminalExitForDeployment's renderDeployFailure path).
func terminalExitForBuild(b api.BuildResponse, appSlug string) int {
	return terminalExitForBuildContext(context.Background(), nil, b, appSlug)
}

func terminalExitForBuildContext(ctx context.Context, c *Client, b api.BuildResponse, appSlug string) int {
	return terminalExitForBuildWithFailureContext(ctx, c, b, appSlug, nil)
}

func terminalExitForBuildWithFailureContext(ctx context.Context, c *Client, b api.BuildResponse, appSlug string, onFailure func(api.DeploymentResponse, string, string)) int {
	if b.Status == buildStatusSucceeded {
		dep := api.DeploymentResponse{ID: b.DeploymentID, Status: statusLive}
		return renderSuccessfulDeployment(ctx, c, dep, appSlug)
	}
	if b.Status == buildStatusCancelled {
		PrintWarn(os.Stderr, "build %s was cancelled; deployment %s did not complete", b.ID, b.DeploymentID)
		return 2
	}
	// Failed build — surface the lifecycle info. End users hitting
	// this path are CI scripts that lost their SSE; the canonical
	// log path includes the required app slug positional argument.
	PrintWarn(os.Stderr, "build %s failed (failure_class=%s); inspect logs with: gregale logs %s --deployment %s --follow",
		b.ID, b.FailureClass, appSlug, b.DeploymentID)
	if onFailure != nil {
		onFailure(api.DeploymentResponse{
			ID:        b.DeploymentID,
			BuildID:   b.ID,
			Error:     b.FailureClass,
			ErrorCode: inferDevDiagnosticCode(b.FailureClass, "image_build", ""),
		}, "image_build", b.FailureClass)
	}
	return 2
}

// deployedAppURL builds the customer-facing URL from the same configurable
// suffix used by the daemons. FAAS_APPS_DOMAIN is intentionally optional for
// the CLI: the public release defaults to the certificate-backed
// `*.gregale.dev` contract, while operators can point a CLI at another fleet.
func deployedAppURL(appID string) string {
	domain := strings.Trim(strings.TrimSpace(os.Getenv("FAAS_APPS_DOMAIN")), ".")
	if domain == "" || domain == "apps.gregale.dev" {
		domain = "gregale.dev"
	}
	return "https://" + appID + "." + domain
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
//
// Error-explanations cluster (spec §6.4 amendment 1): when the
// deployment row carries a typed ErrorCode (one of the 9 cluster
// codes), prefer the whycopy catalog prose via mapFailureProblem
// so the customer sees the full Hint/Why/Fix shape rather than
// the legacy 4-class copy. Falls back to mapFailureMessage for
// pre-cluster rows that only have the raw failure_class string.
func renderDeployFailure(d api.DeploymentResponse) int {
	if d.ErrorCode != "" {
		problem := &api.Problem{
			Code:   d.ErrorCode,
			Status: 422,
			Title:  d.ErrorCode,
			Detail: d.Error,
			Hint:   d.ErrorHint,
			Why:    d.ErrorWhy,
			Fix:    d.ErrorFix,
		}
		if lifted := mapFailureProblem(problem); lifted != "" {
			PrintFail(os.Stderr, "%s", lifted)
			if d.Error == "infra" {
				return 3
			}
			return 1
		}
	}
	PrintFail(os.Stderr, "%s", mapFailureMessage(d.Error))
	if d.Error == "infra" {
		return 3
	}
	return 1
}

// mapFailureMessage returns the user-facing copy for one of the four
// failure classes UX §2.4 enumerates. Anything else falls back to
// "Deploy failed: <err>" because post-build imaging and snapshot failures
// reach this same renderer.
//
// Error-explanations cluster (spec §6.4 amendment 1): when the
// caller already has a *api.Problem, the whycopy catalog lookup
// wins — the catalog carries the customer-facing hint/why/fix
// prose and is the single source of truth for explanation copy.
// The legacy 4-bucket switch stays as a fallback for when the
// caller only has the raw failure_class string (pre-cluster paths:
// legacy builds that never stamped the RFC 7807 code).
func mapFailureMessage(err string) string {
	switch err {
	case "user_error":
		return "Build failed — see log above for the failing command."
	case "oom":
		return "Build ran out of memory (2 GB limit). Try fewer/smaller dependencies, or upgrade for a larger build. Docs: " + deployFromSourceDocsURL
	case "timeout":
		return "Build exceeded 10 min. Docs: " + deployFromSourceDocsURL
	case "infra":
		return "Our build system hiccuped — we've been alerted and requeued your build automatically."
	}
	return "Deploy failed: " + err
}

// mapFailureProblem maps a deployment's *api.Problem to the
// user-facing copy via the whycopy catalog. When the catalog has
// no row for the code (codes the cluster did not catalog yet), it
// returns "" so the caller falls back to the legacy
// mapFailureMessage. This is the post-cluster entry point —
// detection sites (commits 7-13) emit typed *api.Problem and the
// CLI renderer calls mapFailureProblem to lift the hint/why/fix
// without re-classifying the failure.
func mapFailureProblem(p *api.Problem) string {
	if p == nil || p.Code == "" {
		return ""
	}
	_ = whycopy.Decorate(p, p.Code, nil)
	return p.Hint
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
	fs := newFlagSet("usage-daily", flag.ContinueOnError)
	day := fs.String("day", "", "day (YYYY-MM-DD); default: today UTC")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
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
				u.AppID, u.Day, u.Requests, gbh, netGB, txGB, netGB)
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
	fs := newFlagSet("usage-storage", flag.ContinueOnError)
	day := fs.String("day", "", "day (YYYY-MM-DD); default: today UTC")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
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

// renderSecretScanWarnings prints one two-line stderr block per finding
// emitted by pkg/secretscan, followed by a single summary line.
// Lives here (not in the scan package) because the message format is
// CLI-specific UX and the renderer needs access to the CLI's PrintWarn
// + osStderr. Findings are written to stderr specifically so a customer
// running `gregale deploy --json | jq .build_id` sees no warning noise
// on stdout — the JSON contract is preserved.
//
// Two-line shape per finding:
//
//	! Secret detected in .env.production:12 (STRIPE_SECRET_KEY → stripe_live, high)
//	  ↳ sk_liv…p7dc
//
// Then a single summary line if any findings fired:
//
//	! 1 secret line(s) skipped from the upload. Move to: gregale secrets set
//
// The summary is suppressed when no findings fired, so a clean deploy
// prints nothing from this function.

// renderApplyRescue writes the ADR-124 follow-up #1 post-apply
// rescue signal to w. Fires only when apply.GateRescuedByExclude is
// true; the wire invariant from cmd/apid/scan_service.go:864 is
// `gateRescuedByExclude := !preCanApply && canApply`, so the helper
// reaches the writer only when the post-exclude apply succeeded but
// the pre-exclude gate would have blocked. Reasons come from the
// wire verbatim; an empty reasons slice still renders the header so
// the operator sees the rescue signal even when the server omitted
// the per-reason detail. Extracted from cmdDeployTarball so unit
// tests can pin the wire shape without standing up the full deploy
// command (auth + scan + confirmation prompt + apply).
//
// Code-review fix #7: distinguish per-deploy --exclude from
// persisted carry-forward exclusions in the rendered copy. The
// wire carries both via apply.PersistedExclusions (the slugs
// the server folded in from the deployment_scope_exclusions
// table on this deploy); when the slice is non-empty, the rescue
// signal could equally have come from the operator's --exclude
// OR the persisted set, and the previous render always said "by
// --exclude" — misleading operators who didn't pass --exclude on
// this run. The new copy reads "by excluded workloads" when a
// persisted set carried forward, falling back to "by --exclude"
// for the per-deploy-only case.
func renderApplyRescue(w io.Writer, apply api.ApplyResponse) {
	if !apply.GateRescuedByExclude {
		return
	}
	source := "by --exclude"
	if len(apply.PersistedExclusions) > 0 {
		source = "by excluded workloads (some persisted via --persist-exclude)"
	}
	if len(apply.CanApplyReasons) == 0 {
		_, _ = fmt.Fprintf(w, "  Note: gate was rescued %s (pre-exclude would have blocked).\n", source)
		return
	}
	_, _ = fmt.Fprintf(w, "  Note: gate was rescued %s (pre-exclude would have blocked); reasons: %s\n",
		source, strings.Join(apply.CanApplyReasons, "; "))
}

func renderSecretScanWarnings(findings []secretscan.Finding, w io.Writer) {
	if len(findings) == 0 {
		return
	}
	for _, f := range findings {
		PrintWarn(w, "Secret detected in %s:%d (%s → %s, %s)",
			f.File, f.Line, f.Key, f.Provider, f.Severity)
		// The snippet line uses no glyph — it's a continuation of the
		// warning above, not a new event. Indented two spaces to read as
		// a sub-line in the terminal. Fprintf errors are intentionally
		// discarded (same convention as writeStatus — see output.go).
		_, _ = fmt.Fprintf(w, "  ↳ %s\n", f.Snippet)
	}
	PrintWarn(w, "%d secret line(s) skipped from the upload. Move to: gregale secrets set",
		len(findings))
}

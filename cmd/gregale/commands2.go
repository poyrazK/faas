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
	subList   = "list"
	subAdd    = "add"
	subUpdate = "update"
	subRm     = "rm"
	subRuns   = "runs"
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

	// Deployment status enum value (DEPLOY-PROV-6 sibling).
	// Lifted out so the SSE decoder branch + pollDeploymentFinal
	// + terminalExitForDeployment don't trip goconst once
	// buildStatusFailed exists — goconst cross-file matching is
	// by literal value, so we need two name-spaced constants even
	// though they're the same string semantically. (The build
	// status enum is a different 4-state set with `succeeded`/`failed`
	// vs deployment's `live`/`failed`.)
	deploymentStatusFailed = "failed"

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
		PrintUsage(os.Stderr, "usage: gregale app <slug> [--ram N] [--max-concurrency N] [--idle SEC] [--min N] [--autoscale-target-rps N] [--autoscale-target-cpu-pct N] [--warm-snapshot] [--no-warm-snapshot] [--warm-snapshot-min-requests N] [--warm-snapshot-min-ms N] [--concurrency] [--require-authn] [--no-require-authn] [--public-auth MODE] [--basic-user USER --basic-pass PASS] [--app-protocol http1|http2|grpc]", "apps")
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

	if req.RAMMB == nil && req.MaxConcurrency == nil && req.IdleTimeoutS == nil && req.MinInstances == nil &&
		req.AutoscaleTargetRPS == nil && req.AutoscaleTargetCPUPct == nil &&
		req.WarmSnapshotEnabled == nil && req.WarmSnapshotMinRequests == nil && req.WarmSnapshotMinMs == nil &&
		req.EvictionPriority == nil && req.RequireAuthn == nil && req.PublicAuth == nil &&
		req.OverflowNode == nil && req.AppProtocol == nil {
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
// On the interactive path (no -q) the user must retype the slug
// verbatim (issue #312) so a stray `y` cannot delete the app.

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
		fmt.Fprintf(os.Stderr, "Delete %q and all its deployments?\n", slug)
		if !requireTyped(slug) {
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
func buildCreateRequest(slug string, sh shape, runtime string, requireAuthnPtr *bool, appProtocolPtr *string) api.CreateAppRequest {
	req := api.CreateAppRequest{
		Slug:         slug,
		RequireAuthn: requireAuthnPtr,
		AppProtocol:  appProtocolPtr,
	}
	if sh == shapeFunction {
		req.Type = "function"
		if runtime != "" {
			req.Runtime = runtime
		}
	}
	return req
}

// createOrFetchApp issues CreateApp and, on a 409 (the slug is taken),
// probes the server with GetApp to disambiguate "owned by this account"
// from "owned by another account". Returns nil on success (either a fresh
// create or an in-account match).
//
// Issue #1182 / pre-existing soft-#560 behaviour:
//   - CreateApp → 200/201 → nil
//   - CreateApp → 409 → GetApp(slug):
//   - 200 → the slug exists in this account → mirror --require-authn /
//     --app-protocol via UpdateApp (preserves the existing #560 PATCH
//     semantics), return nil
//   - 404 → apid's loadAppAndPreflight returns a silent 404 for IDOR
//     (the slug is owned by another account), so we cannot tell apart
//     "different account" from "race against a peer that just
//     deleted". The hybrid probe HARD-FAILS here rather than silently
//     falling through to DeployTarball — DeployTarball would otherwise
//     404 at apid with the less informative "no such app" message, and
//     the customer would never learn that the slug is taken globally.
//   - CreateApp → non-409 error → returned unwrapped; the caller's
//     printErr prefix is the single user-facing message. Wrapping the
//     APIError here would produce a confusing double-prefix like
//     "Could not create or fetch app: could not create app: ...".
//
// The probe costs one extra round-trip on the slug-conflict path, which
// is rare in normal use (zero-config deploy on a fresh repo is the only
// caller that hits it). The happy path is unchanged.
func createOrFetchApp(ctx context.Context, client *Client, req api.CreateAppRequest, requireAuthnPtr *bool, appProtocolPtr *string) error {
	if _, err := client.CreateApp(ctx, req); err == nil {
		return nil
	} else {
		var ae *APIError
		if !errors.As(err, &ae) || ae.Problem.Status != 409 {
			return err
		}
		// Conflict: probe with GetApp to disambiguate same-account vs
		// other-account ownership. The server's loadAppAndPreflight
		// enforces IDOR via silent 404, so a 200 means "ours" and a 404
		// means "either race-with-peer or other-account — we cannot tell,
		// so refuse to deploy and tell the operator".
		if _, gerr := client.GetApp(ctx, req.Slug); gerr != nil {
			return fmt.Errorf("slug %q is already in use; pick a different --name", req.Slug)
		}
		// Same account: mirror --require-authn / --no-require-authn (and
		// --app-protocol, when set) onto the existing app via PATCH. The
		// plan gate (Pro/Scale only) still fires at the apid PATCH handler
		// — the existing #560 contract is preserved verbatim.
		if requireAuthnPtr != nil || appProtocolPtr != nil {
			upd := api.UpdateAppRequest{RequireAuthn: requireAuthnPtr}
			if appProtocolPtr != nil {
				upd.AppProtocol = appProtocolPtr
			}
			if _, err := client.UpdateApp(ctx, req.Slug, upd); err != nil {
				return err
			}
		}
		return nil
	}
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
	Whoami(ctx context.Context) (api.AccountResponse, error)
}

// deployManifestTriggers fans the manifest's `triggers:` block out to
// apid via the existing CreateCron wire. Issue #791 PR-C / ADR-090.
//
// No manifest → no-op (returns nil). Bad manifest → wrapped error
// from gregalemanifest.Validate, surfaced verbatim by printErr. The
// fan-out itself is fail-fast: stop on the first CreateCron error,
// report progress, exit non-zero. Identical (app, schedule, path)
// triples are deduped by the server-side UNIQUE on crons, so a
// re-running deploy is a no-op for the rows that already exist.
//
// Pre-count: the CLI tallies `existing` + `wanted` against the
// account's plan limit and aborts with a clean 402 message before
// any CreateCron. The authoritative check is server-side
// (CreateCronIfUnderQuota takes FOR UPDATE on the apps row); the
// pre-count is UX fast-fail only.
func deployManifestTriggers(ctx context.Context, client manifestCronClient, slug, cwd string) error {
	m, ok, err := gregalemanifest.Load(cwd)
	if err != nil {
		return err
	}
	if !ok {
		return nil // no manifest — nothing to do
	}
	if err := m.Validate(); err != nil {
		return err
	}

	// Filter to triggers for THIS app's slug. Triggers targeting
	// other slugs in a multi-app project are silently ignored on
	// this deploy — `gregale deploy` is one-app-at-a-time, and the
	// trigger for app-b should ship when the customer deploys app-b.
	var matching []gregalemanifest.Trigger
	for _, t := range m.Triggers {
		if t.App == slug {
			matching = append(matching, t)
		}
	}
	if len(matching) == 0 {
		return nil
	}

	// Pre-count against the server. ListCrons returns the existing
	// rows; the per-plan CronLimitPerApp gate is the server's
	// authority, so a clean pre-count is just a UX fast-fail. We
	// look up the active account's plan via Whoami — silent on
	// failure (an unknown plan falls through; the server gates with
	// 402 ErrPlanCronsNotAllowed on the first CreateCron).
	existing, err := client.ListCrons(ctx, slug)
	if err != nil {
		return fmt.Errorf("list existing crons: %w", err)
	}
	var plan api.Limits
	if acct, err := client.Whoami(ctx); err == nil {
		if l, ok := api.LimitsFor(api.Plan(acct.Plan)); ok {
			plan = l
		}
	}
	wanted := len(matching)
	headroom := plan.CronLimitPerApp - len(existing)
	if headroom < wanted {
		return fmt.Errorf("cron quota exceeded: %d triggers in manifest, plan allows %d (currently %d/%d); raise plan or drop triggers",
			wanted, plan.CronLimitPerApp, len(existing), plan.CronLimitPerApp)
	}

	created := 0
	for i, t := range matching {
		enabled := t.IsEnabled()
		req := api.CreateCronRequest{
			AppID:    slug,
			Schedule: t.Schedule,
			Path:     t.Path,
			Enabled:  &enabled,
		}
		if _, err := client.CreateCron(ctx, slug, req); err != nil {
			// Staticcheck ST1005 — the format string must not end
			// in a newline+period. The summary block reads as two
			// sentences; the trailing newline from the original
			// design flipped the staticcheck rule, so the second
			// sentence now flows inline. Operators still see the
			// "N triggers created, M not attempted" progress line.
			return fmt.Errorf("trigger %d/%d (%s %q %s) rejected: %w — %d triggers created, %d not attempted (re-run deploy after fixing; creation is idempotent by (app, schedule, path))",
				i+1, len(matching), t.App, t.Schedule, t.Path, err, created, len(matching)-i)
		}
		created++
	}
	if !jsonOutput && created > 0 {
		_, _ = fmt.Fprintf(osStdout, "  ✓ %s: %d trigger(s) applied\n", slug, created)
	}
	return nil
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
	// Issue #739 / ADR-092: --repo pairs with --ref to drive the
	// headless source-ref deploy (server-side foundation lives in
	// cmd/apid/handlers_source_ref.go). The previous M7.5 dashboard
	// browser flow is deleted in PR-B; --repo without --ref is an
	// explicit 1-exit error.
	repo := fs.String("repo", "", "GitHub repo to deploy from (owner/name)")
	ref := fs.String("ref", "", "git ref for --repo (branch, tag, or 40-char SHA)")
	// Issue #270: --github emits a copy-paste-ready GitHub Actions
	// workflow snippet to stdout and exits 0. No auth, no side effects,
	// mirrors `cmdBillingPortal --print` (commands_billing.go:104-157).
	// See cmd_deploy_github.go for the snippet body.
	githubSnippet := fs.Bool("github", false, "emit a GitHub Actions workflow snippet for the faas-deploy-action")
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
	projectSlug := fs.String("project-slug", "", "kebab slug for the project (triggers one-key provision)")
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
	// Issue #791 PR-C / ADR-090: skip the `gregale.yaml` triggers fan-out.
	// The flag is the explicit opt-out; without it, a present
	// gregale.yaml with a `triggers:` block is applied AFTER CreateApp
	// (and BEFORE the deploy body ships) — see deployManifestTriggers.
	noTriggers := fs.Bool("no-triggers", false, "skip the `gregale.yaml` triggers fan-out (issue #791 PR-C)")
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
	diffJSON := fs.Bool("json", false, "emit JSON output (only with --diff)")
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
	// v1 only fires on the cwd / auto-pack path. --tarball and
	// --image skip the doctor (the source isn't a directory the
	// doctor can scan); the server-side validators still run on
	// upload.
	doctorStrict := fs.Bool("doctor-strict", false, "run `gregale doctor` first; abort the deploy on any error-class finding (warnings are warn-only)")
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
		PrintUsage(os.Stderr, "usage: gregale deploy [--doctor-strict] --image REF | --tarball PATH | --repo OWNER/NAME --ref REF | --template NAME", "deploy")
		return 1
	}
	// --strict / --lenient mutex. Same rationale as
	// --require-authn / --no-require-authn above.
	if *diffStrict && *diffLenient {
		return printErr("Invalid flags", fmt.Errorf("--strict and --lenient are mutually exclusive"))
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
	if len(*reason) > 280 {
		return printErr("Invalid --reason", fmt.Errorf("must be ≤280 characters (got %d)", len(*reason)))
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
		slug = deriveName()
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
		return cmdDeployGithubSnippet([]string{"--app", slug})
	}

	// --repo is the headless source-ref deploy path (issue #739 /
	// ADR-092). The previous M7.5 dashboard browser flow was deleted
	// in PR-B; the server resolves the install token from
	// github_installations, so CI runs need only FAAS_TOKEN + --ref.
	if *repo != "" {
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
		if *deployOnly != "" || *projectSlug != "" {
			PrintFail(os.Stderr, "--repo cannot be combined with --only or --project-slug")
			return 1
		}
		return cmdDeployRepoSourceRef(slug, *repo, *ref, api.DeployAnnotations{
			Reason:     *reason,
			Tag:        *tag,
			DeployedBy: resolveDeployedBy(*deployedBy),
			PRNumber:   *prNumber,
		})
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
			*function = true
			// Default Node runtime is node22 (per docs/runtimes/go124.md
			// tier-1 stance: no default-flip in the same PR that adds a
			// new runtime). Use function-node24 for the Node 24 variant.
			*runtime = runtimeNode22
			*handler = defaultTemplateHandler
		case "function-node24":
			*function = true
			// Tier 1 PR 1 row: parallel to function-node, runtime is
			// node24 (Node 24 LTS). The handler filename
			// convention is the same; imaged's function-layer
			// manifest sets `--handler /app/node24.js`.
			*runtime = "node24"
			*handler = defaultTemplateHandler
		case "function-python":
			*function = true
			// Default Python runtime is python312 (no default-flip in
			// Tier 1; python313 stays opt-in via function-python313).
			*runtime = runtimePython312
			*handler = defaultTemplateHandler
		case "function-python313":
			*function = true
			// Tier 1 PR 1 row: parallel to function-python, runtime
			// is python313. Handler filename is identical
			// (/app/handler.py in the microVM, version-neutral).
			*runtime = "python313"
			*handler = defaultTemplateHandler
		case "function-go":
			*function = true
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
	cwd, cwdErr := os.Getwd()
	if cwdErr != nil {
		// Don't fail the deploy yet — only the no-(--image|--tarball)
		// path actually reads cwd. We still need it for the
		// gregale.yaml fan-out (issue #791 PR-C), so we surface
		// the error there if needed.
		cwd = ""
	}
	// Cluster A: --doctor-strict pre-upload gate. Runs runDoctorChecks
	// against the cwd BEFORE any HTTP / pack. Errors exit 1 with the
	// doctor report printed to stderr (pre-network, no half-state).
	// Warnings render but don't fail (mirrors the standalone cmdDoctor
	// exit semantics). The cwd scan fires regardless of --tarball /
	// --image — the doctor catches source-side failure modes
	// (stateless_only_violation, app_loopback_bound, env_var_missing)
	// that the tarball/image bytes alone can't reveal. Only when
	// cwd itself is unreachable (cwdErr != nil) does the gate
	// skip — in that case the server-side validators on upload are
	// the catch.
	if *doctorStrict && cwd != "" {
		rep := runDoctorChecks(cwd)
		if rep.HasErrors() {
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
		if rep.HasWarnings() && !jsonOutput {
			renderDoctorHuman(osStderr, rep)
		}
		// Cluster A (F7 perf): doctor already walked cwd. Signal
		// runPackPreflight to skip its own loopback-bind and
		// arch-mismatch scans so we don't double-walk the repo.
		doctorPreflightRan = true
	}
	if *image == "" && *tarball == "" {
		if cwdErr != nil {
			return printErr("Could not read current directory", cwdErr)
		}
		// Issue #1182: refactored zero-config `gregale deploy` (no
		// flags). When cwd is in a git repo with an `origin` remote
		// we pack via `git archive HEAD` (the committed tree, not the
		// working tree) and fall through to the normal
		// buildCreateRequest → CreateApp → DeployTarball pipeline
		// below. The legacy cmdDeployZeroConfig + mustOpen +
		// sourceTarballSidecar trio is gone — that path bypassed
		// CreateApp and dropped every deploy flag. The new path
		// preserves ADR-115's source-tarball trust boundary (the
		// source-tarball endpoint stays as the install-token CI
		// path) by NOT routing through it; it goes through the same
		// CreateApp + DeployTarball wire as --tarball / --template.
		//
		// Three outcomes from resolveZeroConfigProvenance:
		//   - ok=true,  err=nil   → pack HEAD, stamp provenance
		//   - ok=false, err=ErrNotInGitRepo / ErrNoGitRemote → fall
		//     through to the cwd-auto-pack branch below
		//     (existing behavior preserved for non-git dirs and for
		//     git repos without origin)
		//   - ok=false, err=other → surface the error
		if provVal, ok, perr := resolveZeroConfigProvenance(cwd); ok {
			prov = &provVal
			if provVal.Dirty {
				// Print a dirty warning naming the SHA + dirty count
				// so the operator sees exactly what they're shipping
				// (HEAD only, no working-tree changes). The deploy
				// still proceeds — Ctrl-C is the operator's opt-out.
				if dirtyOut, derr := runGitCmd(provVal.Root, "status", "--porcelain"); derr == nil {
					dirtyFiles := 0
					for _, line := range strings.Split(strings.TrimRight(dirtyOut, "\n"), "\n") {
						if line != "" {
							dirtyFiles++
						}
					}
					if dirtyFiles > 0 {
						PrintProgress(os.Stdout, "Note: working tree has %d dirty file(s); deploying HEAD (%s) only — commit first to include the changes",
							dirtyFiles, provVal.SHA[:7])
					}
				}
			}
			// Materialise HEAD as a temp gzipped tar via `git archive`.
			// os.CreateTemp returns a *File we close immediately —
			// gitArchiveHEAD writes to the path, no fd leak on this
			// path. The temp file is removed by the defer; the open
			// that follows uses openCustomerFile + defer Close (the
			// existing --tarball branch), which is fd-safe.
			tmpFile, terr := os.CreateTemp("", "gregale-git-*.tar.gz")
			if terr != nil {
				return printErr("Could not create temp tarball", terr)
			}
			tmpPath := tmpFile.Name()
			_ = tmpFile.Close()
			defer func() { _ = os.Remove(tmpPath) }()
			if err := gitArchiveHEAD(provVal.Root, tmpPath); err != nil {
				return printErr("Could not archive git HEAD", err)
			}
			*tarball = tmpPath
			PrintProgress(os.Stderr, "packing HEAD (%s) from %s",
				provVal.SHA[:7], filepath.Base(cwd))
			// Auto-capture `git config user.name` as deployed_by
			// unless the operator explicitly passed --deployed-by.
			// Mirrors the legacy path at cmd_deploy_zero_config.go
			// (issue #977 / ADR-116).
			if *deployedBy == "" && provVal.DeployedBy != "" {
				*deployedBy = provVal.DeployedBy
			}
			// resolvedShape stays shapeApp — `git archive HEAD` ships
			// the committed tree and the server-side builder detects
			// the framework from there (same as a --tarball upload).
			// The cwd shape detector at lines 1264+ only runs when
			// no git origin is present, which is the only case where
			// a *tarball-less path can land here after this block.
		} else if !errors.Is(perr, ErrNotInGitRepo) && !errors.Is(perr, ErrNoGitRemote) {
			return printErr("Could not resolve git metadata", perr)
		}
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
		if wcli, werr := authedClient(); werr == nil {
			// 5-second budget: Whoami is a tiny JSON GET, but a flaky
			// apid used to hang the CLI for the full HTTP timeout (30s)
			// before falling back to the floor. Bound it explicitly so
			// the zero-config deploy stays snappy on the unhappy path.
			whoCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if acct, werr := wcli.Whoami(whoCtx); werr == nil {
				planCapMB = api.MustLimitsFor(api.Plan(acct.Plan)).SourceTarballMaxMB
			} else {
				PrintWarn(osStderr, "Whoami round-trip for per-plan cap failed (%v); using %d MB Free/Hobby floor", werr, planCapMB)
			}
			cancel()
		} else {
			PrintWarn(osStderr, "authed client for Whoami round-trip failed (%v); using %d MB Free/Hobby floor", werr, planCapMB)
		}
		// Issue #1182 §3.3: only run the cwd auto-detect + auto-pack
		// switch when no tarball was set by the git-archive branch
		// above. The previous run unconditionally re-packed the
		// working tree, silently overwriting the HEAD tarball and
		// shipping uncommitted / untracked files despite the
		// "deploying HEAD only" warning. With this guard the
		// auto-pack branch is reachable only from the non-git /
		// non-origin cwd-auto-pack fallback (existing behaviour).
		if *tarball == "" {
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
				// imaged to stage it. The secret-scan pass runs before the
				// tarball is sealed so a Stripe key committed to
				// .env.production by accident is dropped before it leaves
				// the workstation; --secret-scan=off disables it.
				overrides, scanFindings, scanErr := scanAndRedactEnvFiles(cwd, secretScanMode)
				if scanErr != nil {
					return printErr("Secret scan failed", scanErr)
				}
				path, _, n, err := autoPackCwd(cwd, planCapMB, overrides)
				if err != nil {
					return printErr("Could not pack current directory", err)
				}
				defer func() { _ = os.Remove(path) }()
				renderSecretScanWarnings(scanFindings, osStderr)
				PrintProgress(os.Stderr, "packing %d file(s) from %s", n, filepath.Base(cwd))
				*tarball = path
				resolvedShape = shapeFunction
			case shapeApp:
				overrides, scanFindings, scanErr := scanAndRedactEnvFiles(cwd, secretScanMode)
				if scanErr != nil {
					return printErr("Secret scan failed", scanErr)
				}
				path, fw, n, err := autoPackCwd(cwd, planCapMB, overrides)
				if err != nil {
					return printErr("Could not pack current directory", err)
				}
				defer func() { _ = os.Remove(path) }()
				renderSecretScanWarnings(scanFindings, osStderr)
				if fw == fwDocker {
					*dockerfile = true
				}
				PrintProgress(os.Stderr, "packing %d file(s) from %s", n, filepath.Base(cwd))
				*tarball = path
				resolvedShape = shapeApp
			}
		}
	}

	client, err := authedClientWithDeployTimeout(5 * time.Minute)
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()

	// Deploy-diff short-circuit (PR-0 of the deploy-diff cluster).
	// Runs AFTER authedClient so the SDK reads can resolve, and
	// BEFORE the Phase 3 / CreateApp / Deploy body so no writes
	// happen. --diff never ships a deploy.
	if *diff {
		opts := buildDiffOptions(slug, resolvedShape, *runtime, *handler, *image, cwd, requireAuthnPtr, appProtocolPtr)
		opts.JSON = *diffJSON
		// --strict is the default; --lenient opts out.
		opts.Strict = !*diffLenient
		opts.Lenient = *diffLenient
		opts.ServerDiff = *serverDiff
		return runDiff(ctx, client, opts)
	}

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
		onlyList := splitCSV(*deployOnly)
		excludeList := splitCSV(*deployExclude)
		if ok, clash := intersect(onlyList, excludeList); ok {
			return printErr("Invalid flags", fmt.Errorf(
				"--only and --exclude share workload(s): %s",
				strings.Join(clash, ", ")))
		}
		plan, err := client.ScanProject(ctx, openTarball, filepath.Base(*tarball),
			*projectSlug, prodBranch, 0, onlyList, excludeList, *deployPersistExclude)
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
		if !*yes && !jsonOutput && stdoutIsTTY() && stdinIsTTY() {
			if !confirmPlan(osStdout, os.Stdin, plan, excludeList, *deployShowAffected) {
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
			*projectSlug, prodBranch, 0, onlyList, excludeList, *deployPersistExclude)
		if err != nil {
			return printErr("Apply failed", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(apply))
		}
		PrintOK(osStdout, "Created project %s with %d app(s) and %d cron(s)",
			apply.ProjectID, len(apply.Apps), len(plan.Crons))
		// ADR-124 follow-up #1 (post-apply rescue signal). The wire
		// invariant from cmd/apid/scan_service.go:864 is
		// `gateRescuedByExclude := !preCanApply && canApply` so this
		// fires only when the post-exclude apply succeeded but the
		// pre-exclude gate would have blocked. Extracted into a
		// helper so unit tests can pin the wire shape without
		// standing up the full deploy command. The render is
		// suppressed under --json (the jsonOutput branch returns
		// above with a byte-shape write; the human-readable note
		// would otherwise duplicate the JSON for those operators).
		renderApplyRescue(osStdout, apply)
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

	createReq := buildCreateRequest(slug, resolvedShape, *runtime, requireAuthnPtr, appProtocolPtr)
	if err := createOrFetchApp(ctx, client, createReq, requireAuthnPtr, appProtocolPtr); err != nil {
		return printErr("Could not create or fetch app", err)
	}

	// Issue #791 PR-C / ADR-090: gregale.yaml triggers fan-out. Runs
	// after CreateApp so the slug exists for the FK, and before
	// DeployTarball so a deploy-body error doesn't leave partial
	// trigger rows in a confused state. Fail-fast on the first
	// CreateCron error; the deploy is not rolled back (the tarball
	// hasn't shipped yet, but CreateCronIfUnderQuota is the durable
	// record). --no-triggers opts out of the entire fan-out.
	if !*noTriggers {
		if err := deployManifestTriggers(ctx, client, slug, cwd); err != nil {
			return printErr("Manifest triggers fan-out failed", err)
		}
	}

	if *tarball != "" {
		// Issue #977 / ADR-116: capture annotation fields onto the
		// multipart form via the DeployAnnotations struct. The image
		// path below uses CreateDeploymentRequest's *string/*int
		// pointers so the wire can distinguish "absent" from
		// "explicit zero" (omitempty). The tarball path goes through
		// multipart so DeployAnnotations (value-type with zero
		// collapsing to "absent") is the right shape.
		ann := api.DeployAnnotations{
			Reason:     *reason,
			Tag:        *tag,
			DeployedBy: resolveDeployedBy(*deployedBy),
			PRNumber:   *prNumber,
		}
		dep, err := DeployTarball(client, ctx, slug, *tarball, *runtime, *handler, *dockerfile, ann)
		if err != nil {
			return printErr("Bad --tarball", err)
		}
		if jsonOutput {
			// Issue #1182 §P1 follow-up: receipt wraps the deploy
			// response with commit_sha (zero-config only),
			// dirty (zero-config only), app_url (always,
			// computed from the CLI-known slug not the 32-hex
			// AppID so the URL is actually routable), and
			// source_sha256 (sha256 of the tarball bytes just
			// shipped). The tempfile is still on disk at this
			// point — the deferred os.Remove fires at function
			// return, so reading for the digest here is safe.
			var sourceSHA256 string
			if *tarball != "" {
				if sha, hashErr := tarballSHA256(*tarball); hashErr != nil {
					// Surface a non-fatal warning so the operator
					// sees the receipt is missing source_sha256.
					// CI consumers that pin source_sha256 will
					// notice the absence; silently dropping the
					// key would make a hash-failure indistinguishable
					// from a legitimate image / source-ref deploy.
					PrintWarn(osStderr, "could not hash source tarball for receipt (%v); source_sha256 omitted", hashErr)
				} else {
					sourceSHA256 = sha
				}
			}
			return jsonOut(writeJSON(newDeployReceipt(dep, prov, deployedAppURL(slug), sourceSHA256)))
		}
		return streamDeployLogs(client, dep)
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
	dep, err := client.Deploy(ctx, slug, api.CreateDeploymentRequest{
		Image:          *image,
		TrafficPercent: optTrafficPercent(*trafficPercent),
		Reason:         annPtr(*reason),
		Tag:            annPtr(*tag),
		DeployedBy:     annPtr(resolveDeployedBy(*deployedBy)),
		PRNumber:       annIntPtr(*prNumber),
		Canary:         buildCanarySpec(*canaryPreset, *canaryStages),
	})
	if err != nil {
		return printErr("Deploy failed", err)
	}
	if jsonOutput {
		// Image deploy path: no source tarball bytes (the digest
		// rides on dep.ImageDigest), no git detection (prov is
		// nil from the function-scope hoist), so commit_sha /
		// dirty / source_sha256 stay empty in the receipt. The
		// receipt's only delta here is app_url, computed from
		// the CLI-known slug (not the 32-hex AppID — the
		// gateway routes on slug, so the receipt's URL has to
		// be slug-shaped to actually resolve).
		return jsonOut(writeJSON(newDeployReceipt(dep, nil, deployedAppURL(slug), "")))
	}
	return streamDeployLogs(client, dep)
}

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
	if len(args) < 1 {
		PrintUsage(os.Stderr, "usage: gregale rollback <slug> [--to <deployment_id>] [--json]", "rollback")
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
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	follow := fs.Bool("follow", false, "follow new lines")
	deployment := fs.String("deployment", "", "deployment id (default: latest)")
	grep := fs.String("grep", "", "only show lines matching this substring")
	since := fs.String("since", "", "only show lines at or after this RFC3339 timestamp")
	level := fs.String("level", "", "only show lines at this level (info|warn|error)")
	// Error-explanations cluster (spec §6.4 amendment 1): when the
	// stream ends, print a 3-line summary covering the last failure
	// (lifted from the deployment's persisted error_code), the count
	// of error-level lines, and the top 3 most-frequent error
	// patterns. The summary is what makes `gregale logs <slug>
	// --explain` actionable — the customer no longer has to read the
	// whole stream to know which error fired.
	explain := fs.Bool("explain", false, "on stream end, print a 3-line summary (failure, error count, top patterns)")
	if err := fs.Parse(args); err != nil {
		PrintUsage(os.Stderr, "usage: gregale logs <slug> [--follow] [--deployment ID] [--grep SUBSTR] [--since RFC3339] [--level info|warn|error] [--explain]", "logs")
		return 1
	}
	if fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale logs <slug> [--follow] [--deployment ID] [--grep SUBSTR] [--since RFC3339] [--level info|warn|error] [--explain]", "logs")
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
	}, *follow, *explain)
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
	}, true, false)
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
func runLogs(ctx context.Context, slug, deployment string, filter api.LogFilter, follow bool, explain bool) int {
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
	for {
		select {
		case <-ctx.Done():
			// Ctrl-C. Exit cleanly with status 130 (the
			// shell's standard for SIGINT exit).
			if collector != nil {
				collector.flush(os.Stdout)
			}
			return 130
		case e, ok := <-dec.Events():
			if !ok {
				if collector != nil {
					collector.flush(os.Stdout)
				}
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
				if collector != nil {
					collector.flush(os.Stdout)
				}
				return 0
			}
			if e.Event == "end" {
				if collector != nil {
					collector.flush(os.Stdout)
				}
				return 0
			}
			if e.Data != "" {
				fmt.Println(e.Data)
				if collector != nil {
					collector.observe(e.Data)
				}
			}
		case err := <-dec.Errors():
			if errors.Is(err, io.EOF) {
				if collector != nil {
					collector.flush(os.Stdout)
				}
				return 0
			}
			return printErr("Stream closed", err)
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

// observe ingests one SSE data line. The line shape is
// `{ts} {level} {message}` (the apid's Move 3 wire shape). We
// bucket by level + count the first 64 bytes of each error-level
// message as a pattern bucket (sufficient for naive grep
// de-duplication without a real log-classifier).
func (c *explainCollector) observe(line string) {
	// Split into at most 3 parts: ts, level, message.
	parts := strings.SplitN(line, " ", 3)
	if len(parts) < 3 {
		return
	}
	level := parts[1]
	msg := parts[2]
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
func streamDeployLogs(c *Client, dep api.DeploymentResponse) int {
	PrintProgress(osStdout, "build queued for %s (deployment %s)", dep.AppID, dep.ID)
	ctx := context.Background()
	body, err := c.StreamDeploymentLogs(ctx, dep.ID, nil, 0, true)
	if err != nil {
		// Stream unreachable up front — first try the new
		// /v1/builds/{id} poller (DEPLOY-PROV-6 / ADR-089); only if
		// the build is still queued/running OR the new endpoint is
		// unavailable do we fall through to the legacy
		// pollDeploymentFinal. A fast tarball deploy on a slow link
		// is the canonical case where the stream never opened and
		// the build row is already terminal.
		if b, ok := pollBuildStatus(c, dep, 5*time.Second); ok {
			return terminalExitForBuild(b, dep.AppID)
		}
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
	// ADR-117 §3: ticker construction happens AFTER the decoder so
	// a decoder init failure doesn't draw a half-rendered block.
	ticker := renderStageTicker(osStdout)
	defer ticker.Close()
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
				}
			case statusLiteral:
				var status struct {
					Status string `json:"status"`
				}
				if json.Unmarshal([]byte(e.Data), &status) == nil &&
					(status.Status == statusLive || status.Status == deploymentStatusFailed) {
					if status.Status == statusLive {
						PrintOK(osStdout, "Deployed. %s", deployedAppURL(dep.AppID))
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
	// Stream ended without a terminal frame — poll the new
	// /v1/builds/{id} endpoint (DEPLOY-PROV-6 / ADR-089, issue
	// #741) so a fast build that raced the SSE open isn't reported
	// as "follow manually" when we actually have the answer. Only
	// fall back to pollDeploymentFinal when the new poll reports
	// the build is still queued or running.
	if b, ok := pollBuildStatus(c, dep, 60*time.Second); ok {
		return terminalExitForBuild(b, dep.AppID)
	}
	// Tarball/function deployments created by older API paths may not carry
	// BuildID.  In that case the build poll above is intentionally skipped,
	// and a single deployment GET is too early: the build may have finished
	// while the scheduler is still priming and parking the VM.  Keep polling
	// the deployment row through that recovery window so a healthy deployment
	// is not reported as exit 3 merely because the SSE stream ended first.
	if final, ok := pollDeploymentFinalUntil(c, dep, 5*time.Minute); ok {
		return terminalExitForDeployment(final)
	}
	PrintWarn(os.Stderr, "stream ended without a terminal frame; follow manually: gregale logs --deployment %s", dep.ID)
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
func pollDeploymentFinal(c *Client, dep api.DeploymentResponse) (api.DeploymentResponse, bool) {
	got, err := c.GetDeployment(context.Background(), dep.ID)
	if err != nil {
		return api.DeploymentResponse{}, false
	}
	if got.Status == statusLive || got.Status == deploymentStatusFailed {
		return got, true
	}
	return api.DeploymentResponse{}, false
}

// pollDeploymentFinalUntil is the deployment-row counterpart to
// pollBuildStatus. It is used when the create response has no BuildID, so the
// deployment status is the only durable terminal signal available to the CLI.
// The first GET is immediate; subsequent requests use a small capped backoff
// and are bounded by deadline.
func pollDeploymentFinalUntil(c *Client, dep api.DeploymentResponse, deadline time.Duration) (api.DeploymentResponse, bool) {
	if deadline <= 0 {
		return pollDeploymentFinal(c, dep)
	}
	end := time.Now().Add(deadline)
	backoff := time.Second
	for {
		if final, ok := pollDeploymentFinal(c, dep); ok {
			return final, true
		}
		remaining := time.Until(end)
		if remaining <= 0 {
			return api.DeploymentResponse{}, false
		}
		wait := backoff
		if wait > remaining {
			wait = remaining
		}
		time.Sleep(wait)
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
func pollBuildStatus(c *Client, dep api.DeploymentResponse, deadline time.Duration) (api.BuildResponse, bool) {
	if dep.BuildID == "" {
		// No build_id on the deployment row — server pre-dates
		// PROV-6, or the deployment was created via the fast-
		// path that skips the builds table. Bail out so the
		// caller falls through to pollDeploymentFinal.
		return api.BuildResponse{}, false
	}
	// parent context is the wall-clock deadline; per-iteration
	// children derive from this so the loop honors the budget.
	parent, cancelParent := context.WithTimeout(context.Background(), deadline)
	defer cancelParent()
	end := time.Now().Add(deadline)
	backoff := 1 * time.Second
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
		if err == nil && (b.Status == buildStatusSucceeded || b.Status == buildStatusFailed) {
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
		time.Sleep(backoff + jitter)
		if backoff < 5*time.Second {
			backoff *= 2
		}
	}
	return api.BuildResponse{}, false
}

// terminalExitForDeployment applies the same rendering rules as the
// in-stream `event: status` branch, but uses the polled deployment
// row (which has the canonical Error string from the DB).
func terminalExitForDeployment(d api.DeploymentResponse) int {
	if d.Status == statusLive {
		PrintOK(osStdout, "Deployed. %s", deployedAppURL(d.AppID))
		printDeployColdWakeSentence()
		return 0
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
func terminalExitForBuild(b api.BuildResponse, appID string) int {
	if b.Status == buildStatusSucceeded {
		PrintOK(osStdout, "Deployed. %s", deployedAppURL(appID))
		printDeployColdWakeSentence()
		return 0
	}
	// Failed build — surface the lifecycle info. End users hitting
	// this path are CI scripts that lost their SSE; the canonical
	// log path is `gregale logs --deployment <deployment_id>`.
	PrintWarn(os.Stderr, "build %s failed (failure_class=%s); inspect logs with: gregale logs --deployment %s",
		b.ID, b.FailureClass, b.DeploymentID)
	return 2
}

// deployedAppURL builds the customer-facing URL from the same configurable
// suffix used by the daemons. FAAS_APPS_DOMAIN is intentionally optional for
// the CLI: the public release defaults to the certificate-backed
// `*.gregale.dev` contract, while operators can point a CLI at another fleet.
func deployedAppURL(appID string) string {
	domain := strings.Trim(strings.TrimSpace(os.Getenv("FAAS_APPS_DOMAIN")), ".")
	if domain == "" {
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
// "Build failed: <err>" so the customer sees the raw class at least.
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
	return "Build failed: " + err
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

// commands5.go — UX §3.1 commands that landed in issue #63:
//
//   faas ps <app>          instances + state (humanizes parked → sleeping)
//   faas status            personal SLO snapshot from GET /status/slo.json
//   faas env pull|push     local .env <-> sealed secrets (key-only pull per §11/G2)
//   faas app <slug> scale  per-app scale knobs (--ram/--max-concurrency/--idle/--min)
//   faas app <slug> rename atomic slug swap (full-stack: server + state + CLI)
//   faas plan <plan>       top-level plan change (account-scoped)
//   faas dashboard         opens the account-level dashboard in the browser
//
// main.go wires the top-level dispatch; cmdAppDispatch (here) routes
// the `faas app` subcommand form. Reuses the authedClient / printErr
// helpers from commands.go and the dashboard URL helpers from
// commands2.go.

package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/browser"
)

// `faas app` subcommand names — lifted to constants so goconst stops
// flagging the repeated "scale"/"rename" string literals across the
// dispatch table (cmdAppDispatch) and the usage hints.
const (
	subScale  = "scale"
	subRename = "rename"
)

// validCLISlug matches the server-side validSlug regex in cmd/apid/handlers.go.
// Duplicated here so the CLI can reject malformed slugs before paying a
// network round-trip — the server still re-validates as defence in depth.
func validCLISlug(s string) bool {
	if len(s) < 3 || len(s) > 40 {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-':
			if i == 0 || i == len(s)-1 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// --- ps --------------------------------------------------------------------

// cmdPS lists an app's live instances + state. The schema state
// vocabulary is parked | waking | cold_booting | running | snapshotting |
// stopped | failed (migrations/00001_init.sql:85). Parked instances
// are rendered as "sleeping" because that's how the dashboard badge
// (§6) talks about them to humans — the wire value stays unchanged.
func cmdPS(args []string) int {
	if len(args) != 1 {
		PrintUsage(os.Stderr, "usage: faas ps <app>", "ps")
		return 1
	}
	slug := args[0]
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ins, err := client.ListInstances(context.Background(), slug)
	if err != nil {
		return printErr("Could not list instances", err)
	}
	if jsonOutput {
		// NDJSON: empty slice renders as zero lines (no header), which
		// `jq -c '.'` handles gracefully.
		return jsonOut(writeNDJSON(ins))
	}
	if len(ins) == 0 {
		_, _ = fmt.Fprintf(osStdout, "%s: no instances (app is parked)\n", slug)
		return 0
	}
	_, _ = fmt.Fprintf(osStdout, "%-36s %-12s %6s %-20s %-20s %-36s\n", "ID", "STATE", "RAM_MB", "STARTED", "LAST_REQUEST", "WAKE_ID")
	for _, i := range ins {
		_, _ = fmt.Fprintf(osStdout, "%-36s %-12s %6d %-20s %-20s %-36s\n",
			i.ID, humanizeInstanceState(i.State), i.RAMMB,
			i.StartedAt, i.LastRequestAt, i.WakeID)
	}
	return 0
}

// humanizeInstanceState maps the wire-level state string to a
// user-friendly rendering. The full vocabulary lives in
// pkg/state/machine.go:14-26 (parked / waking / cold_booting / running
// / snapshotting / stopped / failed) — issue #63 §1 lists the
// customer-facing subset (running | cold-booting | waking | sleeping |
// parked).
//
// Two translations:
//
//	parked → sleeping    (the dashboard badge wording; §6 uses the
//	                      euphemism so customers don't see a
//	                      stop-anxiety signal)
//	cold_booting → cold-booting  (snake → kebab so it reads as a
//	                              single hyphenated word, matching the
//	                              spec)
//
// All other states render verbatim — waking, running, snapshotting,
// stopped, failed — they read naturally and any silent rename would
// hide the wire vocabulary from operators tailing `faas ps`.
func humanizeInstanceState(state string) string {
	switch state {
	case "parked":
		return "sleeping"
	case "cold_booting":
		return "cold-booting"
	}
	return state
}

// --- status ----------------------------------------------------------------

// cmdStatus prints the personal SLO snapshot from GET /status/slo.json.
// The endpoint is unauthenticated (spec §12 public status page), so a
// fresh CLI without a stored token still works. With a token, the
// numbers are the same fleet-wide ones; personal account SLOs land in
// a follow-up.
//
// --json (issue #63 §2) emits the raw api.StatusPage so pipelines can
// jq the SLO numbers. JSON tag set lives on the struct in
// pkg/api/dto.go — renames there propagate here automatically.
func cmdStatus(args []string) int {
	fs := flag.NewFlagSet(statusLiteral, flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "emit raw api.StatusPage as JSON (issue #63 §2)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		PrintUsage(os.Stderr, "usage: faas status [--json]", "status")
		return 1
	}
	// Use the raw Client (not authedClient) so the public endpoint
	// works without a stored token. The Client still sends the bearer
	// header if present; apid mounts /status/slo.json on the PUBLIC
	// mux (server.go:359) before any auth middleware, so the token is
	// never inspected.
	client := NewClient(apiBase(), loadToken())
	page, err := client.GetStatusSLO(context.Background())
	if err != nil {
		return printErr("Status failed", err)
	}
	if *asJSON || jsonOutput {
		// Per-command --json (PR #66) OR top-level --json (issue #64 D1).
		// Marshal via the shared writeJSON helper so the DTO JSON tags
		// in pkg/api/dto.go are the single source of truth.
		return jsonOut(writeJSON(page))
	}
	_, _ = fmt.Fprintf(osStdout, "availability: %.2f%%\n", page.APIAvailabilityPct)
	_, _ = fmt.Fprintf(osStdout, "wake p95:     %.0f ms\n", page.WakeP95MS)
	_, _ = fmt.Fprintf(osStdout, "builds ok:    %.2f%%\n", page.BuildSuccessPct)
	_, _ = fmt.Fprintf(osStdout, "as of:        %s\n", page.AsOf.Format("2006-01-02 15:04:05 UTC"))
	_, _ = fmt.Fprintf(osStdout, "source:       %s\n", page.Source)
	return 0
}

// --- env -------------------------------------------------------------------

// cmdEnv dispatches `faas env pull|push --app <slug>`. The pull path
// writes a KEY-only .env template (empty values) per the §11/G2
// sealed-secrets boundary — the server never returns plaintext. The
// push path re-uses the secrets API PUT with the same rotation-hint
// flow as `faas secrets set`.
func cmdEnv(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: faas env <pull|push> --app <slug>", "env")
		return 1
	}
	switch args[0] {
	case "pull":
		return envPull(args[1:])
	case "push":
		return envPush(args[1:])
	}
	fmt.Fprintf(os.Stderr, "faas env: unknown subcommand %q\n", args[0])
	return 1
}

func envPull(args []string) int {
	fs := flag.NewFlagSet("env pull", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	out := fs.String("o", ".env", "output file (default .env)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *app == "" {
		PrintUsage(os.Stderr, "usage: faas env pull --app <slug> [-o .env]", "env")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.ListSecrets(context.Background(), *app)
	if err != nil {
		return printErr("List failed", err)
	}
	var b strings.Builder
	for _, s := range resp.Secrets {
		// KEY-only template: the G2 boundary (§11) means the server
		// never returns plaintext, so we intentionally write an empty
		// value. The customer fills values by hand before `env push`.
		fmt.Fprintf(&b, "%s=\n", s.Key)
	}
	if err := os.WriteFile(*out, []byte(b.String()), 0o600); err != nil {
		return printErr("Could not write .env", err)
	}
	if resp.Count == 0 {
		PrintOK(osStdout, "Wrote empty %s (%s has no secrets)", *out, *app)
		return 0
	}
	PrintOK(osStdout, "Wrote %d key(s) to %s (values intentionally blank — fill by hand)",
		resp.Count, *out)
	return 0
}

func envPush(args []string) int {
	fs := flag.NewFlagSet("env push", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	in := fs.String("f", ".env", "input file (default .env)")
	fromStdin := fs.Bool("from-stdin", false, "read KEY=VALUE pairs from stdin (one per line)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *app == "" {
		PrintUsage(os.Stderr, "usage: faas env push --app <slug> [-f .env | --from-stdin]", "env")
		return 1
	}
	if *fromStdin && *in != ".env" {
		// fs.Changed isn't available pre-Go-1.21 in some toolchains;
		// the default for -f is ".env", so anything else means the
		// customer explicitly named a file. Mutually exclusive with
		// --from-stdin so we never read both.
		PrintFail(os.Stderr, "--from-stdin and -f are mutually exclusive")
		return 1
	}
	type pair struct{ k, v string }
	var pairs []pair
	if *fromStdin {
		// Issue #63 §3: respect the --from-stdin semantics already used
		// by `faas secrets set` (commands3.go:92). Tests pipe a string
		// into osStdin (commands5_test.go); customers pipe a heredoc
		// or process substitution. Same line cap (64 KB) — Scale's
		// SecretValueMaxBytes (32 KB) plus the key name fits, anything
		// larger truncates and the apid byte cap rejects.
		scanner := bufio.NewScanner(osStdin)
		scanner.Buffer(make([]byte, 0, 64*1024), 64*1024)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			p, err := parseSecretsPair(line)
			if err != nil {
				return printErr("Bad stdin line", err)
			}
			pairs = append(pairs, pair{k: p.Key, v: p.Value})
		}
		if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
			return printErr("Read stdin", err)
		}
	} else {
		f, err := openCustomerFile(*in)
		if err != nil {
			return printErr("Could not read .env", err)
		}
		defer func() { _ = f.Close() }()
		// Reuse parseSecretsPair from commands3.go (single '=' split, same
		// edge cases). Skip blanks + comments ourselves so the parser sees
		// only candidate lines.
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 0, 64*1024), 64*1024)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			p, err := parseSecretsPair(line)
			if err != nil {
				return printErr("Bad .env line", err)
			}
			pairs = append(pairs, pair{k: p.Key, v: p.Value})
		}
		if err := scanner.Err(); err != nil {
			return printErr("Read .env", err)
		}
	}
	if len(pairs) == 0 {
		PrintFail(os.Stderr, "no KEY=VALUE pairs in input")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	// Same rotation-hint flow as secretsSet (commands3.go).
	existing := map[string]bool{}
	if list, err := client.ListSecrets(context.Background(), *app); err == nil {
		for _, s := range list.Secrets {
			existing[s.Key] = true
		}
	}
	rotated := 0
	for _, p := range pairs {
		if existing[p.k] {
			rotated++
		}
	}
	if rotated > 0 {
		_, _ = fmt.Fprintf(osStdout,
			"note: %d secret(s) already existed and are being rotated.\n"+
				"  Any parked snapshots still hold the previous plaintext until the next wake.\n"+
				"  Deploy, or call `faas wake %s`, to force an overstamp.\n",
			rotated, *app)
	}
	for _, p := range pairs {
		if err := client.SetSecret(context.Background(), *app, p.k, p.v); err != nil {
			return printErr("Set "+p.k+" failed", err)
		}
		PrintOK(osStdout, "%s set", p.k)
	}
	return 0
}

// openCustomerFile opens any customer-supplied file path with defense
// against symlink-mediated content exfiltration. Used by both:
//
//	faas env push -f .env --app x        (cmdEnvPush)
//	faas deploy --tarball source.tar.gz  (Client.DeployTarball)
//
// Without this guard, a customer could `ln -s /etc/passwd .env` (or
// any other readable file) and then `faas env push -f .env --app x`.
// The scanner would feed the file's lines through parseSecretsPair;
// anything matching KEY=VALUE would be PUT to the server. The parallel
// `faas deploy --tarball` attack is byte-exfiltration: whatever the
// symlink points at gets streamed verbatim into the multipart source
// part and ends up in the build artefact the builder microVM ingests.
//
// Two checks:
//  1. Lstat the FINAL component. If it's a symlink, the kernel would
//     follow it on Open — refuse before opening.
//  2. After Open, Lstat again on the resolved path. If a symlink was
//     swapped in between (TOCTOU race), the second Lstat catches it.
//     Also confirms the file is a regular file, not a device or FIFO.
//
// Note 1: we intentionally don't EvalSymlinks the whole path and
// compare strings. On macOS, /var is itself a symlink to /private/var
// (a system-level layout quirk), so EvalSymlinks("/var/folders/...")
// returns "/private/var/folders/..." even for plain files. Comparing
// strings there would reject every legitimate customer file on macOS
// dev boxes. Lstat-on-the-final-component catches the actual attack
// (a symlink AT the path the customer named) without false-positives.
// The same reasoning applies to the tarball call site — a customer
// may legitimately point `--tarball` at /var/folders/.../foo.tar.gz.
//
// Note 2: both call sites — cmdEnvPush (env push) and
// Client.DeployTarball (deploy --tarball) — now share this helper.
// If you add a third caller that ships customer bytes over the wire,
// route it through here too; duplicating the Lstat discipline is
// what causes TOCTOU drift.
func openCustomerFile(path string) (*os.File, error) {
	absIn, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("could not absolutize %q: %w", path, err)
	}
	// Pre-open: refuse if the final component is a symlink.
	preInfo, err := os.Lstat(absIn)
	if err != nil {
		return nil, err
	}
	if preInfo.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("refusing to follow symlink at %q", path)
	}
	// The Lstat guards above (pre-open + post-open) ARE the security
	// boundary that the .golangci.yml forbidigo rule exists to enforce.
	// This call site is the documented escape hatch: any other os.Open
	// in this repo is a tripwire and must route through a vetted
	// helper. The pre-open + post-open Lstat discipline below is the
	// security boundary; the bare Open on the next line is necessary.
	//nolint:forbidigo // openCustomerFile IS the security boundary — pre-open + post-open Lstat discipline above is what makes os.Open safe here.
	f, err := os.Open(absIn)
	if err != nil {
		return nil, err
	}
	// Post-open: confirm the path is still a regular file. Catches
	// TOCTOU swaps (someone `ln -sf`'ing the path between our preInfo
	// check and the Open) and refuses devices / FIFOs / directories.
	postInfo, err := os.Lstat(absIn)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if postInfo.Mode()&os.ModeSymlink != 0 {
		_ = f.Close()
		return nil, fmt.Errorf("refusing to follow symlink at %q (raced after open)", path)
	}
	if !postInfo.Mode().IsRegular() {
		_ = f.Close()
		return nil, fmt.Errorf("refusing non-regular file %q (mode %s)", path, postInfo.Mode())
	}
	return f, nil
}

// --- app scale / rename (called from cmdAppDispatch) ------------------------

// cmdAppScale is the subcommand form of `faas app <slug> scale ...`.
// Mirrors cmdApp (commands2.go:53-126) but with no --plan — plan
// changes live on `faas plan`. Uses the same fs.Visit pattern so 0 is
// distinguishable from "unset".
func cmdAppScale(slug string, args []string) int {
	fs := flag.NewFlagSet("app scale", flag.ContinueOnError)
	ram := fs.Int("ram", 0, "update RAM (MB)")
	conc := fs.Int("max-concurrency", 0, "update max concurrent requests")
	idle := fs.Int("idle", 0, "update idle timeout (seconds)")
	min := fs.Int("min", 0, "min instances kept warm (Pro/Scale only; 0 = scale to zero)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
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
	if req.RAMMB == nil && req.MaxConcurrency == nil &&
		req.IdleTimeoutS == nil && req.MinInstances == nil {
		PrintUsage(os.Stderr, "usage: faas app <slug> scale [--ram N] [--max-concurrency N] [--idle SEC] [--min N]", "apps")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	updated, err := client.UpdateApp(context.Background(), slug, req)
	if err != nil {
		return printErr("Scale failed", err)
	}
	PrintOK(osStdout, "Updated")
	if explicit["min"] && *min > 0 {
		// Silent on Whoami failure (mid-rotation token, transient
		// API blip). The cost echo is a transparency affordance;
		// don't surface an unrelated auth issue right after a
		// successful update.
		if acct, err := client.Whoami(context.Background()); err == nil {
			printResidentCostEcho(api.Plan(acct.Plan), updated.RAMMB, *min)
		}
	}
	return 0
}

// cmdAppRename swaps an app's slug atomically. The server validates the
// new slug (same regex as CreateApp) and returns 409 CodeAppRenameFailed
// on collisions, which client.go surfaces as APIError.
func cmdAppRename(slug, newSlug string) int {
	if !validCLISlug(newSlug) {
		PrintFail(os.Stderr, "invalid slug (3-40 chars, lowercase letters/digits/hyphens, no leading/trailing hyphen)")
		return 1
	}
	if newSlug == slug {
		// Idempotent no-op so the customer can re-run safely.
		PrintOK(osStdout, "%s already has that slug", slug)
		return 0
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	updated, err := client.RenameApp(context.Background(), slug, newSlug)
	if err != nil {
		return printErr("Rename failed", err)
	}
	// The mid-string `→` here is a semantic from-to arrow (rename
	// from old slug to new slug), not the §3.2 "in-progress" symbol.
	// It stays as a literal even when stdout is not a TTY: in pipes
	// and CI logs the arrow is still load-bearing for distinguishing
	// "old" from "new". The leading glyph goes through PrintOK so the
	// §3.2 NO_COLOR rule still applies to the status-indicator half.
	PrintOK(osStdout, "Renamed %s → %s", slug, updated.Slug)
	return 0
}

// cmdAppDispatch routes `faas app <slug> ...` to either the new
// subcommand form (scale / rename) or the legacy flag-form (`faas app
// <slug> --ram N`, `faas app <slug>`). Pulled out of main.go so the
// switch stays small.
func cmdAppDispatch(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: faas app <slug> [scale|rename <new>|--ram N|--max-concurrency N|--idle SEC|--min N]", "apps")
		return 1
	}
	slug := args[0]
	if len(args) >= 2 {
		switch args[1] {
		case subScale:
			return cmdAppScale(slug, args[2:])
		case subRename:
			if len(args) != 3 {
				PrintUsage(os.Stderr, "usage: faas app <slug> rename <new-slug>", "apps")
				return 1
			}
			return cmdAppRename(slug, args[2])
		}
	}
	// Backwards-compat: legacy flag-form dispatch is the existing cmdApp.
	return cmdApp(args)
}

// --- plan ------------------------------------------------------------------

// planRank assigns an ordinal for downgrade-detection. We only
// confirm on paid→downgrade transitions because going free→paid or
// hobby→pro is harmless; the Stripe webhook handles the money side
// regardless.
var planRank = map[api.Plan]int{
	api.PlanFree:  0,
	api.PlanHobby: 1,
	api.PlanPro:   2,
	api.PlanScale: 3,
}

// cmdPlan is `faas plan <plan>`. Validates the plan name against the
// 4 known constants, then asks Whoami to check the current plan and
// prompts for y/N on paid→downgrade transitions.
func cmdPlan(args []string) int {
	if len(args) != 1 {
		PrintUsage(os.Stderr, "usage: faas plan <free|hobby|pro|scale>", "plan")
		return 1
	}
	target := api.Plan(args[0])
	if !target.Valid() {
		PrintFail(os.Stderr, "unknown plan %q (expected: free|hobby|pro|scale)", args[0])
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	acct, err := client.Whoami(context.Background())
	if err != nil {
		return printErr("Could not fetch account", err)
	}
	if acct.Plan != "" && planRank[api.Plan(acct.Plan)] > planRank[target] {
		fmt.Fprintf(os.Stderr,
			"Downgrade from %s to %s: existing apps may exceed the new plan's limits. "+
				"Continue? [y/N] ", acct.Plan, target)
		var ans string
		_, _ = fmt.Scanln(&ans)
		if strings.ToLower(strings.TrimSpace(ans)) != "y" {
			_, _ = fmt.Fprintln(osStdout, "aborted")
			return 1
		}
	}
	updated, err := client.ChangePlan(context.Background(), string(target))
	if err != nil {
		return printErr("Plan change failed", err)
	}
	PrintOK(osStdout, "Plan changed to %s", updated.Plan)
	return 0
}

// --- dashboard -------------------------------------------------------------

// cmdDashboard opens the account-level dashboard in the browser. Same
// fallback-to-URL pattern as cmdDeployRepo (commands2.go:283-288). Tests
// substitute browser.Default via withRecorder.
//
// Exit code on browser-open failure: 0, intentionally. The URL is
// printed to stderr so the customer can paste it into a browser
// themselves — the work the customer asked for (giving them the
// dashboard URL) is done. Mirrors cmdDeployRepo and matches the §11
// "open the URL, fall back gracefully" UX convention. Exit 1 here
// would make CI scripts and `&&`-chained shell commands treat a
// missing $DISPLAY as a hard failure, which is the wrong signal.
func cmdDashboard(args []string) int {
	if len(args) != 0 {
		PrintUsage(os.Stderr, "usage: faas dashboard", "dashboard")
		return 1
	}
	if _, err := authedClient(); err != nil {
		return printErr("Not logged in", err)
	}
	target := dashboardAccountURL(apiBase())
	_, _ = fmt.Fprintf(osStdout, "Opening %s\n", target)
	if err := browser.Open(target); err != nil {
		PrintFail(os.Stderr, "Could not open browser: %v", err)
		fmt.Fprintf(os.Stderr, "  Open this URL manually:\n  %s\n", target)
		return 0
	}
	return 0
}

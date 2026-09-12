// commands3.go — `gregale secrets` subcommand (spec §11/G2).
//
// `gregale secrets {list,set,unset} --app <slug>` is the customer surface for
// sealed-at-rest env injection. The CLI transports plaintext values only
// over TLS to apid; the seal happens server-side and the ciphertext never
// re-enters the CLI.
//
// Operations:
//   gregale secrets list   --app <slug> [--scope <name>]
//   gregale secrets set    --app <slug> KEY=VALUE [--from-stdin] [--scope <name>]
//   gregale secrets unset  --app <slug> KEY [--scope <name>]
//
// `--from-stdin` reads the value from stdin (one pair per line, KEY=VALUE)
// for pipelines that need to avoid putting the plaintext in shell
// history. Most usage is the inline form.
//
// `--scope` (ADR-092 PR-B) selects which env-scope the call targets;
// the reserved sentinel `__all__` is rejected server-side as
// env_scope_reserved on PUT/DELETE/POST (single-row writes) but is
// accepted on GET (where it returns the nested `secrets_by_scope`
// map). Omitted scope defaults to `default` server-side, so
// pre-PR-B callers see no behaviour change.

package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// osStdout and osStdin are the package-level I/O seams so tests can pipe
// data in (--from-stdin) and capture output (success messages) without
// spawning a subprocess. Production wiring points them at the real
// os.Stdout / os.Stdin.
var (
	osStdout io.Writer = os.Stdout
	osStdin  io.Reader = os.Stdin
	// osStderr is the same seam for stderr, used by the issue #744 /
	// ADR-086 NestedMarkerHintError path so tests can capture the hint
	// line without a subprocess. Production wiring points at os.Stderr.
	osStderr io.Writer = os.Stderr
)

// secretsCmdScopeFlag is the CLI flag name. Mirrors the
// cmd/apid/handlers_env.go::scopeQueryParam literal — kept as a
// distinct constant so a future rename is a one-line change here
// and one-line change in the handler.
const secretsCmdScopeFlag = "scope"

func cmdSecrets(args []string) int {
	parent, _ := lookupCliCommand("secrets")
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale secrets <list|set|unset|list-all> --app <slug> [args]", "secrets")
		return 1
	}
	switch args[0] {
	case subList:
		return secretsList(args[1:])
	case "set":
		return secretsSet(args[1:])
	case "unset":
		return secretsUnset(args[1:])
	case "list-all":
		return secretsListAll(args[1:])
	case subRotate:
		return secretsRotate(args[1:])
	}
	fmt.Fprintf(os.Stderr, "unknown secrets subcommand %q\n", args[0])
	sug, _ := suggestSubcommand(args[0], parent)
	maybeSuggestSub(sug)
	return 1
}

// --- list ------------------------------------------------------------------

func secretsList(args []string) int {
	fs := flag.NewFlagSet("secrets list", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	scope := fs.String(secretsCmdScopeFlag, "", "env scope filter (omit for default; '__all__' returns nested secrets_by_scope)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *app == "" {
		PrintUsage(os.Stderr, "usage: gregale secrets list --app <slug> [--scope <name>|__all__]", "secrets")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.ListSecretsWithScope(context.Background(), *app, *scope)
	if err != nil {
		return printErr("List failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	if resp.Count == 0 {
		_, _ = fmt.Fprintf(osStdout, "%s: no secrets (0/%d)\n", *app, resp.Quota)
		return 0
	}
	// ADR-092 PR-B: nested secrets_by_scope vs flat secrets is the
	// discriminated union — render whichever arm the server returned.
	if len(resp.SecretsByScope) > 0 {
		renderSecretsByScope(osStdout, *app, &resp)
		return 0
	}
	renderFlatSecrets(osStdout, *app, &resp)
	return 0
}

// renderSecretsByScope emits the `__all__` arm of the secrets-list
// discriminated union: a header summarising the cross-scope total,
// then one section per scope (sorted ASC) listing "<scope>/<key>"
// rows. Extracted from secretsList (commands3.go:85) so the routing
// function stays under the 50-line cap (CLAUDE.md).
//
// Mirror of the env route's nested-map renderer at
// cmd/gregale/commands_inspect.go — same shape, secrets-flavored.
func renderSecretsByScope(w io.Writer, app string, resp *api.AppSecretListResponse) {
	scopes := make([]string, 0, len(resp.SecretsByScope))
	for s := range resp.SecretsByScope {
		scopes = append(scopes, s)
	}
	sort.Strings(scopes)
	_, _ = fmt.Fprintf(w, "%s: %d/%d secrets (across %d scopes)\n",
		app, resp.Count, resp.Quota, len(scopes))
	for _, s := range scopes {
		for _, row := range resp.SecretsByScope[s] {
			_, _ = fmt.Fprintf(w, "  %s/%s\n", s, row.Key)
		}
	}
}

// renderFlatSecrets emits the per-scope arm of the secrets-list
// discriminated union: a header, then one row per secret rendered
// as "<scope>/<key>". An empty Scope (legacy pre-PR-B server rows
// without the column populated) renders as "default" — the same
// server-side collapse that cmd/apid/handlers_secrets.go::scopeFromQuery
// applies, kept symmetric in the CLI for human-readable output.
//
// Extracted from secretsList (commands3.go:85) so the routing
// function stays under the 50-line cap (CLAUDE.md).
func renderFlatSecrets(w io.Writer, app string, resp *api.AppSecretListResponse) {
	_, _ = fmt.Fprintf(w, "%s: %d/%d secrets\n", app, resp.Count, resp.Quota)
	for _, s := range resp.Secrets {
		_, _ = fmt.Fprintf(w, "  %s/%s\n", scopeOrDefault(s.Scope), s.Key)
	}
}

// --- set -------------------------------------------------------------------

func secretsSet(args []string) int {
	fs := flag.NewFlagSet("secrets set", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	fromStdin := fs.Bool("from-stdin", false, "read KEY=VALUE pairs from stdin (one per line)")
	scope := fs.String(secretsCmdScopeFlag, "", "env scope to write into (omit for default)")
	orderedArgs, err := reorderSecretsSetArgs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "secret set:", err)
		return 1
	}
	if err := fs.Parse(orderedArgs); err != nil {
		return 1
	}
	if *app == "" {
		PrintUsage(os.Stderr, "usage: gregale secrets set --app <slug> KEY=VALUE [...] [--from-stdin] [--scope <name>]", "secrets")
		return 1
	}

	pairs := []secretsPair{}
	if *fromStdin {
		if fs.NArg() != 0 {
			fmt.Fprintln(os.Stderr, "secret set: --from-stdin takes no positional pairs")
			return 1
		}
		scanner := bufio.NewScanner(osStdin)
		// A 64 KB line cap is enough for SecretValueMaxBytes at Scale (32 KB)
		// plus the key name. Larger lines silently truncate today; the
		// apid-side byte cap will still reject the request.
		scanner.Buffer(make([]byte, 0, 64*1024), 64*1024)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			p, err := parseSecretsPair(line)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			pairs = append(pairs, p)
		}
		if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
			fmt.Fprintln(os.Stderr, "read stdin:", err)
			return 1
		}
	} else {
		for _, a := range fs.Args() {
			p, err := parseSecretsPair(a)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			pairs = append(pairs, p)
		}
	}
	if len(pairs) == 0 {
		fmt.Fprintln(os.Stderr, "secret set: at least one KEY=VALUE pair is required")
		return 1
	}
	// Validate the complete batch before authenticating or issuing a PUT.
	// SetSecretWithScope writes one key per request, so discovering an invalid
	// later key after the first request would otherwise leave a partial update.
	for _, p := range pairs {
		if problem := api.ValidateSecretKey(p.Key); problem != nil {
			fmt.Fprintln(os.Stderr, problem.Detail)
			return 1
		}
	}

	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}

	// Snapshot-rotation hint (ADR-020 D5): drive1's cleartext env is
	// frozen into every parked snapshot. When a customer rotates a
	// secret value, the old value remains visible to anyone restoring
	// from a previously-parked snapshot — the new value reaches the
	// guest only at the next wake. Surface that fact before the PUT
	// so a hasty rotation doesn't leave the customer thinking the
	// new value is live everywhere.
	existing := map[string]bool{}
	if list, err := client.ListSecretsWithScope(context.Background(), *app, *scope); err == nil {
		for _, s := range list.Secrets {
			existing[s.Key] = true
		}
	}
	rotated := 0
	for _, p := range pairs {
		if existing[p.Key] {
			rotated++
		}
	}
	if rotated > 0 {
		_, _ = fmt.Fprintf(osStdout,
			"note: %d secret(s) already existed in scope=%q and are being rotated.\n"+
				"  Any parked snapshots still hold the previous plaintext until the next wake.\n"+
				"  Deploy, or call `gregale wake %s`, to force an overstamp.\n",
			rotated, scopeOrDefault(*scope), *app)
	}

	for _, p := range pairs {
		if err := client.SetSecretWithScope(context.Background(), *app, p.Key, p.Value, *scope); err != nil {
			return printErr("Set "+p.Key+" failed", err)
		}
		PrintOK(osStdout, "%s set (scope=%s)", p.Key, scopeOrDefault(*scope))
	}
	// Move 1 PR-A: post-write quota stamp. After every successful
	// set, follow up with a ListSecrets and print "<slug>: N/M
	// secrets" so the customer knows how close they are to the
	// per-app cap (Free 8 / Hobby 25 / Pro 50 / Scale 100, in
	// pkg/api/limits.go's SecretCountMax). The cap is looked up
	// from /v1/account's plan via the local limits table — no
	// server round-trip beyond the one already needed for the
	// ListSecrets count.
	//
	// Failure here is non-fatal: the PUT already succeeded, so we
	// log and move on rather than masking the success with a
	// quota-stamp error. Three modes:
	//
	//   - both succeed   → "<slug>: N/M secrets"
	//   - ListSecrets OK, plan unknown → "<slug>: N secrets" (no cap)
	//   - ListSecrets fails → no stamp (can't compute N without it)
	//
	// ADR-092 PR-B: stamp counts across all scopes (per-app-across
	// scopes posture — pkg/api/limits.go::SecretCountMax doc). Pass
	// scope="" to ListSecretsWithScope for the cross-scope total.
	printSecretsQuotaStamp(client, *app, *scope)
	return 0
}

// reorderSecretsSetArgs keeps the documented "KEY=VALUE ... [flags]" form
// compatible with Go's flag package, which otherwise stops parsing at the
// first assignment. Only this command's known flags are moved; every other
// token remains positional and is validated before any secret is written.
func reorderSecretsSetArgs(args []string) ([]string, error) {
	flags := make([]string, 0, len(args))
	pairs := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			pairs = append(pairs, args[i+1:]...)
			break
		}
		switch {
		case a == "--app" || a == "-app" || a == "--scope" || a == "-scope":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("%s requires a value", a)
			}
			flags = append(flags, a, args[i+1])
			i++
		case strings.HasPrefix(a, "--app=") || strings.HasPrefix(a, "-app=") ||
			strings.HasPrefix(a, "--scope=") || strings.HasPrefix(a, "-scope=") ||
			a == "--from-stdin" || a == "-from-stdin" ||
			strings.HasPrefix(a, "--from-stdin=") || strings.HasPrefix(a, "-from-stdin="):
			flags = append(flags, a)
		default:
			pairs = append(pairs, a)
		}
	}
	return append(flags, pairs...), nil
}

// printSecretsQuotaStamp prints "<app>: N/M secrets" after a
// successful secrets set. Both inputs come from cheap GET endpoints;
// failure is silent. Pulled out so the failure-mode logic stays out
// of secretsSet's body.
//
// scope is the customer's --scope flag value; the stamp calls
// ListSecretsWithScope with scope="" to get the cross-scope total
// (the per-app SecretCountMax counts across all scopes — see
// pkg/api/limits.go).
//
// ADR-092 PR-B: the cross-scope total is `list.Count`, NOT
// `len(list.Secrets)`. The scope="" query collapses server-side to
// "default" (handlers_secrets.go::scopeFromQuery), so `Secrets`
// only contains the default-scope rows — the prod/staging rows
// silently drop. `list.Count` is the server-side cross-scope total
// (handlers_secrets.go::listSecrets calls CountAppSecrets across
// every scope). Using `len(list.Secrets)` would under-report for
// any customer with non-default-scope rows.
func printSecretsQuotaStamp(client *api.Client, app, scope string) {
	_ = scope // accepted for symmetry with secretsSet; the stamp itself
	// always reads the cross-scope total.
	list, err := client.ListSecretsWithScope(context.Background(), app, "")
	if err != nil {
		return
	}
	used := list.Count
	if acct, err := client.Whoami(context.Background()); err == nil {
		if l, ok := api.LimitsFor(api.Plan(acct.Plan)); ok && l.SecretCountMax > 0 {
			_, _ = fmt.Fprintf(osStdout, "%s: %d/%d secrets\n", app, used, l.SecretCountMax)
			return
		}
	}
	_, _ = fmt.Fprintf(osStdout, "%s: %d secrets\n", app, used)
}

type secretsPair struct {
	Key   string
	Value string
}

// parseSecretsPair splits KEY=VALUE. The first '=' is the split point, so
// values may contain more '=' (e.g. base64 'A=B=C'). Empty KEY is rejected.
func parseSecretsPair(s string) (secretsPair, error) {
	i := strings.IndexByte(s, '=')
	if i <= 0 {
		return secretsPair{}, fmt.Errorf("secret set must look like KEY=VALUE")
	}
	key := s[:i]
	value := s[i+1:]
	if key == "" {
		return secretsPair{}, fmt.Errorf("secret set has an empty KEY")
	}
	return secretsPair{Key: key, Value: value}, nil
}

// readSecretsFile loads the KEY=VALUE input used by deploy's
// --secrets-file flag. The file is deliberately parsed before any network
// mutation so a typo cannot create an app and leave the first deploy without
// its configuration. Values never appear in returned errors or diagnostics.
func readSecretsFile(path string) ([]secretsPair, error) {
	f, err := openCustomerFile(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 64*1024)
	pairs := make([]secretsPair, 0, 8)
	seen := make(map[string]struct{})
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		pair, parseErr := parseSecretsPair(line)
		if parseErr != nil {
			return nil, fmt.Errorf("line %d: %w", lineNo, parseErr)
		}
		if _, ok := seen[pair.Key]; ok {
			return nil, fmt.Errorf("line %d: duplicate secret key %s", lineNo, pair.Key)
		}
		if problem := api.ValidateSecretKey(pair.Key); problem != nil {
			return nil, fmt.Errorf("line %d: %s", lineNo, problem.Detail)
		}
		seen[pair.Key] = struct{}{}
		pairs = append(pairs, pair)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(pairs) == 0 {
		return nil, errors.New("no KEY=VALUE pairs found")
	}
	return pairs, nil
}

// setDeploySecrets seals a validated secret bundle after CreateApp and before
// the deployment upload. It is intentionally separate from secretsSet: deploy
// must not perform the extra list/quota reads or print one line per key, and
// JSON deploys must keep stdout as a single receipt.
func setDeploySecrets(ctx context.Context, client *Client, app string, pairs []secretsPair) error {
	for _, pair := range pairs {
		if err := client.SetSecret(ctx, app, pair.Key, pair.Value); err != nil {
			return fmt.Errorf("set %s: %w", pair.Key, err)
		}
	}
	return nil
}

// setProjectDeploySecrets applies a validated deploy bundle to each workload
// in a project plan. Project plans can contain duplicate names only when a
// detector has merged the same workload, so de-duplicate by the API slug to
// avoid issuing duplicate writes. Values remain in memory and are passed only
// to the existing sealed app-secret endpoint; they are never logged.
func setProjectDeploySecrets(ctx context.Context, client *Client, workloads []api.PlanWorkload, pairs []secretsPair) (int, error) {
	seen := make(map[string]struct{}, len(workloads))
	configured := 0
	for _, workload := range workloads {
		app := strings.TrimSpace(workload.Name)
		if app == "" {
			continue
		}
		key := strings.ToLower(app)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if err := setDeploySecrets(ctx, client, app, pairs); err != nil {
			return configured, fmt.Errorf("workload %s: %w", app, err)
		}
		configured++
	}
	return configured, nil
}

// --- unset -----------------------------------------------------------------

func secretsUnset(args []string) int {
	fs := flag.NewFlagSet("secrets unset", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	scope := fs.String(secretsCmdScopeFlag, "", "env scope to delete from (omit for default)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *app == "" || fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale secrets unset --app <slug> KEY [--scope <name>]", "secrets")
		return 1
	}
	key := fs.Arg(0)
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if err := client.UnsetSecretWithScope(context.Background(), *app, key, *scope); err != nil {
		return printErr("Unset failed", err)
	}
	PrintOK(osStdout, "%s unset (scope=%s)", key, scopeOrDefault(*scope))
	return 0
}

// scopeOrDefault returns "default" for empty scope, else scope
// itself. Used for human-facing messages (PrintOK / hint lines)
// where an empty scope would render as "" and confuse the
// customer — server-side, empty scope also collapses to "default"
// via cmd/apid/handlers_env.go::scopeFromQuery, so the two
// stay byte-equivalent.
func scopeOrDefault(scope string) string {
	if scope == "" {
		return "default"
	}
	return scope
}

// --- list-all (account-wide) ----------------------------------------------
//
// secretsListAll walks GET /v1/secrets (account-wide; per-app sealed
// envelopes). Operator compliance flow needs a flat row stream across
// every app — they cannot get this view without iterating apps
// themselves and stitching the per-app responses together.
//
// The wire shape carries ONLY the sealed ciphertext (no plaintext),
// so this leaf is safe for log + JSON output. Pagination via the
// (slug, key) cursor — same convention as /v1/invoices.
func secretsListAll(args []string) int {
	fs := flag.NewFlagSet("secrets list-all", flag.ContinueOnError)
	before := fs.String("before", "", "pagination cursor from a previous call's next_before (slug|key)")
	limit := fs.Int("limit", 100, "page size (1..200; server caps at 200)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *limit < 1 || *limit > 200 {
		return printErr("Invalid --limit", fmt.Errorf("must be in [1,200]; got %d", *limit))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.GetSecrets(context.Background(), *before, *limit)
	if err != nil {
		return printErr("List-all failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	if len(resp.Secrets) == 0 {
		_, _ = fmt.Fprintln(osStdout, "(no secrets)")
		return 0
	}
	for _, s := range resp.Secrets {
		fmt.Printf("%-32s %-32s %s\n", s.AppSlug, s.Key, s.UpdatedAt)
	}
	if resp.NextBefore != "" {
		fmt.Fprintf(os.Stderr, "next page: --before %s\n", resp.NextBefore)
	}
	return 0
}

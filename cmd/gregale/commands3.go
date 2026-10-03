// commands3.go — `gregale secrets` subcommand (spec §11/G2).
//
// `gregale secrets {list,set,unset} --app <slug>` is the customer surface for
// sealed-at-rest env injection. The CLI transports plaintext values only
// over TLS to apid; the seal happens server-side and the ciphertext never
// re-enters the CLI.
//
// Operations:
//   gregale secrets list   --app <slug> [--scope <name>] [--class persistent|ephemeral]
//   gregale secrets set    --app <slug> KEY=VALUE [--from-stdin] [--scope <name>] [--class persistent|ephemeral]
//   gregale secrets unset  --app <slug> KEY [--scope <name>] [--wait-for-ack [--timeout 2m]]
//
// `--from-stdin` reads the value from stdin (one pair per line, KEY=VALUE)
// for pipelines that need to avoid putting the plaintext in shell
// history. Most usage is the inline form.
//
// `--scope` (ADR-092 PR-B) selects which env-scope the call targets; when it
// is omitted, a linked project environment is used when available;
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
	"strconv"
	"strings"
	"time"

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
		PrintUsage(os.Stderr, "usage: gregale secrets <list|set|unset|list-all|audit> [args]", "secrets")
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
	case "audit":
		return secretsAudit(args[1:])
	case subRotate:
		return secretsRotate(args[1:])
	case "refs":
		return cmdSecretReferences(args[1:])
	}
	printCommandValidation(os.Stderr, "unknown secrets subcommand %q\n", args[0])
	sug, _ := suggestSubcommand(args[0], parent)
	maybeSuggestSub(sug)
	return 1
}

// --- list ------------------------------------------------------------------

func secretsList(args []string) int {
	fs := newFlagSet("secrets list", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	scope := fs.String(secretsCmdScopeFlag, "", "env scope filter (defaults to linked project environment; '__all__' returns nested secrets_by_scope)")
	secretClass := fs.String("class", "", "filter by snapshot-retention class (persistent or ephemeral)")
	olderThan := fs.String("older-than", "", "filter secrets not updated within a duration (for example 90d or 2160h)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if !validSecretClass(*secretClass) {
		return printErr("Invalid --class", fmt.Errorf("must be %q or %q; got %q", api.SecretClassPersistent, api.SecretClassEphemeral, *secretClass))
	}
	age, err := parseSecretAge(*olderThan)
	if err != nil {
		return printErr("Invalid --older-than", err)
	}
	if *app == "" {
		PrintUsage(os.Stderr, "usage: gregale secrets list --app <slug> [--scope <name>|__all__] [--class persistent|ephemeral] [--older-than 90d]", "secrets")
		return 1
	}
	resolvedScope, resolveErr := resolveEnvironmentFlagOrContext(*scope)
	if resolveErr != nil {
		return printErr("Could not read local project context", resolveErr)
	}
	*scope = resolvedScope
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.ListSecretsWithScope(context.Background(), *app, *scope)
	if err != nil {
		return printErr("List failed", err)
	}
	filterAppSecretListByClass(&resp, *secretClass)
	if age > 0 {
		filterAppSecretListByAge(&resp, time.Now().Add(-age))
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	if resp.Count == 0 {
		if *secretClass != "" && age > 0 {
			_, _ = fmt.Fprintf(osStdout, "%s: no %s secrets older than %s (0/%d)\n", *app, *secretClass, *olderThan, resp.Quota)
			return 0
		}
		if *secretClass != "" {
			_, _ = fmt.Fprintf(osStdout, "%s: no %s secrets (0/%d)\n", *app, *secretClass, resp.Quota)
			return 0
		}
		if age > 0 {
			_, _ = fmt.Fprintf(osStdout, "%s: no secrets older than %s (0/%d)\n", *app, *olderThan, resp.Quota)
			return 0
		}
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
	for s, rows := range resp.SecretsByScope {
		if len(rows) > 0 {
			scopes = append(scopes, s)
		}
	}
	sort.Strings(scopes)
	_, _ = fmt.Fprintf(w, "%s: %d/%d secrets (across %d scopes)\n",
		app, resp.Count, resp.Quota, len(scopes))
	for _, s := range scopes {
		for _, row := range resp.SecretsByScope[s] {
			_, _ = fmt.Fprintf(w, "  %-48s %-10s · %s · %s%s\n", s+"/"+row.Key, secretClassLabel(row.SecretClass),
				secretDeliveryLabel(row.DeliveryStatus), secretRuntimeReloadLabel(row.DeliveryVersion,
					row.LastRuntimeReloadVersion, row.LastRuntimeReloadProjection, row.LastRuntimeReloadSignal, row.LastRuntimeReloadInstanceID,
					row.RuntimeReloadObservations, row.RuntimeReloadTargetsComplete), secretUpdatedAtSuffix(row.UpdatedAt))
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
		_, _ = fmt.Fprintf(w, "  %-48s %-10s · %s · %s%s\n", scopeOrDefault(s.Scope)+"/"+s.Key, secretClassLabel(s.SecretClass),
			secretDeliveryLabel(s.DeliveryStatus), secretRuntimeReloadLabel(s.DeliveryVersion,
				s.LastRuntimeReloadVersion, s.LastRuntimeReloadProjection, s.LastRuntimeReloadSignal, s.LastRuntimeReloadInstanceID,
				s.RuntimeReloadObservations, s.RuntimeReloadTargetsComplete), secretUpdatedAtSuffix(s.UpdatedAt))
	}
}

func secretUpdatedAtSuffix(updatedAt string) string {
	if updatedAt == "" {
		return ""
	}
	return " · updated " + updatedAt
}

// parseSecretAge accepts Go durations plus whole-day values such as 90d.
// A blank value means the caller did not request an age filter.
func parseSecretAge(raw string) (time.Duration, error) {
	if raw == "" {
		return 0, nil
	}
	var age time.Duration
	var err error
	if strings.HasSuffix(raw, "d") {
		var days uint64
		days, err = strconv.ParseUint(strings.TrimSuffix(raw, "d"), 10, 64)
		const maxDays = uint64((1<<63 - 1) / int64(24*time.Hour))
		if err == nil && days <= maxDays {
			age = time.Duration(days) * 24 * time.Hour
		} else if err == nil {
			err = fmt.Errorf("day value is too large")
		}
	} else {
		age, err = time.ParseDuration(raw)
	}
	if err != nil || age <= 0 {
		return 0, fmt.Errorf("must be a positive duration such as 90d or 2160h")
	}
	return age, nil
}

func secretUpdatedBefore(updatedAt string, cutoff time.Time) bool {
	updated, ok := parseSecretUpdatedAt(updatedAt)
	return ok && !updated.After(cutoff)
}

func parseSecretUpdatedAt(updatedAt string) (time.Time, bool) {
	if updatedAt == "" {
		return time.Time{}, false
	}
	updated, err := time.Parse(time.RFC3339Nano, updatedAt)
	return updated, err == nil
}

func filterAppSecretListByAge(resp *api.AppSecretListResponse, cutoff time.Time) {
	resp.Count = 0
	if len(resp.SecretsByScope) > 0 {
		for scope, rows := range resp.SecretsByScope {
			filtered := rows[:0]
			for _, row := range rows {
				if secretUpdatedBefore(row.UpdatedAt, cutoff) {
					filtered = append(filtered, row)
				}
			}
			resp.SecretsByScope[scope] = filtered
			resp.Count += len(filtered)
		}
		return
	}
	filtered := resp.Secrets[:0]
	for _, secret := range resp.Secrets {
		if secretUpdatedBefore(secret.UpdatedAt, cutoff) {
			filtered = append(filtered, secret)
		}
	}
	resp.Secrets = filtered
	resp.Count = len(filtered)
}

func filterAccountSecretListByAge(resp *api.ListSecretsForAccountResponse, cutoff time.Time) {
	filtered := resp.Secrets[:0]
	for _, secret := range resp.Secrets {
		if secretUpdatedBefore(secret.UpdatedAt, cutoff) {
			filtered = append(filtered, secret)
		}
	}
	resp.Secrets = filtered
}

func validSecretClass(class string) bool {
	return class == "" || class == api.SecretClassPersistent || class == api.SecretClassEphemeral
}

func secretClassLabel(class string) string {
	if class == "" {
		return api.SecretClassPersistent
	}
	return class
}

func filterAppSecretListByClass(resp *api.AppSecretListResponse, class string) {
	if class == "" {
		return
	}
	resp.Count = 0
	if len(resp.SecretsByScope) > 0 {
		for scope, rows := range resp.SecretsByScope {
			filtered := rows[:0]
			for _, row := range rows {
				if secretClassLabel(row.SecretClass) == class {
					filtered = append(filtered, row)
				}
			}
			resp.SecretsByScope[scope] = filtered
			resp.Count += len(filtered)
		}
		return
	}
	filtered := resp.Secrets[:0]
	for _, secret := range resp.Secrets {
		if secretClassLabel(secret.SecretClass) == class {
			filtered = append(filtered, secret)
		}
	}
	resp.Secrets = filtered
	resp.Count = len(filtered)
}

func secretDeliveryLabel(status string) string {
	if status == "" {
		return "delivery unknown"
	}
	return "delivery " + status
}

func secretRuntimeReloadLabel(currentVersion, observedVersion int64, projection, signal, instanceID string, observations []api.SecretRuntimeReloadObservation, targetsComplete ...bool) string {
	if len(targetsComplete) > 0 && targetsComplete[0] {
		return secretRuntimeReloadTargetsLabel(currentVersion, observations)
	}
	if len(observations) > 0 {
		current, stale, sent, queued, unchanged, failed := 0, 0, 0, 0, 0, 0
		appApplied, appFailed, appAckStale := 0, 0, 0
		var failedInstances []string
		var appFailedInstances []string
		var staleAppAckInstances []string
		for _, observation := range observations {
			if observation.Version == currentVersion {
				current++
				switch {
				case observation.Projection == "unchanged":
					unchanged++
				case observation.Projection == "updated" && observation.Signal == "sent":
					sent++
				case observation.Projection == "updated" && observation.Signal == "queued":
					queued++
				}
			} else {
				stale++
			}
			if observation.Projection == "failed" || observation.Signal == "failed" {
				failed++
				failedInstances = append(failedInstances, observation.InstanceID)
			}
			if observation.ApplicationAckVersion > 0 {
				if observation.ApplicationAckVersion != currentVersion {
					appAckStale++
					staleAppAckInstances = append(staleAppAckInstances, observation.InstanceID)
				} else if observation.ApplicationAck == "applied" {
					appApplied++
				} else if observation.ApplicationAck == "failed" {
					appFailed++
					appFailedInstances = append(appFailedInstances, observation.InstanceID)
				}
			}
		}
		label := fmt.Sprintf("runtime status: %d active reports (%d current: %d sent, %d queued, %d unchanged; %d stale",
			len(observations), current, sent, queued, unchanged, stale)
		if failed > 0 {
			label += fmt.Sprintf(", %d failed: %s", failed, strings.Join(failedInstances, ","))
		}
		if appApplied+appFailed+appAckStale > 0 {
			label += fmt.Sprintf("; app ack: %d applied, %d failed, %d stale", appApplied, appFailed, appAckStale)
			if appAckStale > 0 {
				label += " (" + strings.Join(staleAppAckInstances, ",") + ")"
			}
			if appFailed > 0 {
				label += " (" + strings.Join(appFailedInstances, ",") + ")"
			}
		} else {
			label += "; app ack unknown"
		}
		return label + ")"
	}
	if observedVersion == 0 {
		return "runtime status unknown"
	}
	var label string
	if observedVersion != currentVersion {
		label = fmt.Sprintf("runtime status stale (v%d)", observedVersion)
	} else {
		switch {
		case projection == "failed":
			label = "runtime file update failed"
		case projection == "unchanged":
			label = "runtime file unchanged"
		case projection == "updated" && signal == "sent":
			label = "runtime file updated; signal sent"
		case projection == "updated" && signal == "queued":
			label = "runtime file updated; signal queued"
		case projection == "updated" && signal == "failed":
			label = "runtime file updated; signal failed"
		default:
			label = "runtime status unknown"
		}
	}
	if instanceID != "" {
		label += " (" + instanceID + ")"
	}
	return label
}

func secretRuntimeReloadTargetsLabel(currentVersion int64, targets []api.SecretRuntimeReloadObservation) string {
	eligible, disabled, unknown, reported, eligibleReported, current, appApplied, appFailed := 0, 0, 0, 0, 0, 0, 0, 0
	for _, target := range targets {
		switch target.ReloadSupport {
		case "enabled":
			eligible++
		case "disabled":
			disabled++
		case "unknown":
			unknown++
		}
		if !target.Reported {
			continue
		}
		reported++
		if target.ReloadSupport == "enabled" {
			eligibleReported++
		}
		if target.Version == currentVersion {
			current++
		}
		if target.ApplicationAckVersion == currentVersion && target.ApplicationAck == "applied" {
			appApplied++
		} else if target.ApplicationAckVersion == currentVersion && target.ApplicationAck == "failed" {
			appFailed++
		}
	}
	missing := eligible - eligibleReported
	label := fmt.Sprintf("runtime status: %d active authorized (%d reload-enabled, %d reported, %d missing; %d current", len(targets), eligible, reported, missing, current)
	if disabled > 0 || unknown > 0 {
		label += fmt.Sprintf("; reload support: %d disabled, %d unknown", disabled, unknown)
	}
	label += fmt.Sprintf("; app ack: %d applied, %d failed)", appApplied, appFailed)
	return label
}

// --- set -------------------------------------------------------------------

func secretsSet(args []string) int {
	fs := newFlagSet("secrets set", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	fromStdin := fs.Bool("from-stdin", false, "read KEY=VALUE pairs from stdin (one per line)")
	scope := fs.String(secretsCmdScopeFlag, "", "env scope to write into (defaults to linked project environment)")
	secretClass := fs.String("class", "", "snapshot retention (persistent or ephemeral; omitted updates preserve the class)")
	restart := fs.Bool("restart", false, "restart app with the updated secrets")
	orderedArgs, err := reorderSecretsSetArgs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "secret set:", err)
		return 1
	}
	if err := fs.Parse(orderedArgs); err != nil {
		return 1
	}
	if *app == "" {
		PrintUsage(os.Stderr, "usage: gregale secrets set --app <slug> KEY=VALUE [...] [--from-stdin] [--scope <name>] [--class <persistent|ephemeral>] [--restart]", "secrets")
		return 1
	}
	if *secretClass != "" && *secretClass != api.SecretClassPersistent && *secretClass != api.SecretClassEphemeral {
		fmt.Fprintln(os.Stderr, "secret set: --class must be persistent or ephemeral")
		return 1
	}
	resolvedScope, resolveErr := resolveEnvironmentFlagOrContext(*scope)
	if resolveErr != nil {
		return printErr("Could not read local project context", resolveErr)
	}
	*scope = resolvedScope

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

	keys := make([]string, 0, len(pairs))
	for _, p := range pairs {
		if err := client.SetSecretWithScopeAndClass(context.Background(), *app, p.Key, p.Value, *scope, *secretClass); err != nil {
			return printErr("Set "+p.Key+" failed", err)
		}
		keys = append(keys, p.Key)
		if !jsonOutput {
			PrintOK(osStdout, "%s set (scope=%s)", p.Key, scopeOrDefault(*scope))
		}
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
	if !jsonOutput {
		printSecretsQuotaStamp(client, *app, *scope)
	}
	if *restart {
		out, err := client.RestartAppFresh(context.Background(), *app)
		if err != nil {
			return printErr("Restart failed", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(secretsSetReceipt{
				App: *app, Status: "updated", Scope: scopeOrDefault(*scope), Keys: keys,
				RestartRequested: true, WakeID: out.WakeID,
			}))
		}
		PrintOK(osStdout, "Restart requested after secret update (wake_id=%s)", out.WakeID)
		return 0
	}
	if jsonOutput {
		return jsonOut(writeJSON(secretsSetReceipt{
			App: *app, Status: "updated", Scope: scopeOrDefault(*scope), Keys: keys,
			Warnings: []string{"Updated secrets apply on the next cold wake; running instances keep their current environment. Use --restart to apply now."},
		}))
	}
	PrintWarn(osStdout, "Updated secrets apply on the next cold wake; running instances keep their current environment. Use --restart to apply now.")
	return 0
}

type secretsSetReceipt struct {
	App              string   `json:"app"`
	Status           string   `json:"status"`
	Scope            string   `json:"scope"`
	Keys             []string `json:"keys"`
	RestartRequested bool     `json:"restart_requested"`
	WakeID           string   `json:"wake_id,omitempty"`
	Warnings         []string `json:"warnings,omitempty"`
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
		case a == "--app" || a == "-app" || a == "--scope" || a == "-scope" || a == "--class" || a == "-class":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("%s requires a value", a)
			}
			flags = append(flags, a, args[i+1])
			i++
		case a == "--timeout" || a == "-timeout":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("%s requires a value", a)
			}
			flags = append(flags, a, args[i+1])
			i++
		case strings.HasPrefix(a, "--app=") || strings.HasPrefix(a, "-app=") ||
			strings.HasPrefix(a, "--scope=") || strings.HasPrefix(a, "-scope=") ||
			strings.HasPrefix(a, "--class=") || strings.HasPrefix(a, "-class=") ||
			strings.HasPrefix(a, "--timeout=") || strings.HasPrefix(a, "-timeout=") ||
			a == "--from-stdin" || a == "-from-stdin" ||
			strings.HasPrefix(a, "--from-stdin=") || strings.HasPrefix(a, "-from-stdin=") ||
			a == "--restart" || a == "-restart" ||
			strings.HasPrefix(a, "--restart=") || strings.HasPrefix(a, "-restart=") ||
			a == "--wait-for-ack" || a == "-wait-for-ack" ||
			strings.HasPrefix(a, "--wait-for-ack=") || strings.HasPrefix(a, "-wait-for-ack="):
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
func setDeploySecretsWithScope(ctx context.Context, client *Client, app string, pairs []secretsPair, scope string) error {
	for _, pair := range pairs {
		if err := client.SetSecretWithScope(ctx, app, pair.Key, pair.Value, scope); err != nil {
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
	return setProjectDeploySecretsWithScope(ctx, client, workloads, pairs, "")
}

func setProjectDeploySecretsWithScope(ctx context.Context, client *Client, workloads []api.PlanWorkload, pairs []secretsPair, scope string) (int, error) {
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
		if err := setDeploySecretsWithScope(ctx, client, app, pairs, scope); err != nil {
			return configured, fmt.Errorf("workload %s: %w", app, err)
		}
		configured++
	}
	return configured, nil
}

// --- unset -----------------------------------------------------------------

func secretsUnset(args []string) int {
	fs := newFlagSet("secrets unset", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	scope := fs.String(secretsCmdScopeFlag, "", "env scope to delete from (defaults to linked project environment)")
	waitForAck := fs.Bool("wait-for-ack", false, "wait until every active authorized runtime confirms it removed the secret")
	timeout := fs.Duration("timeout", 2*time.Minute, "maximum time to wait for runtime acknowledgements")
	restart := fs.Bool("restart", false, "restart the app so running instances drop the removed secret now")
	orderedArgs, err := reorderSecretsSetArgs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "secret unset:", err)
		return 1
	}
	if err := fs.Parse(orderedArgs); err != nil {
		return 1
	}
	if *app == "" || fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale secrets unset --app <slug> KEY [--scope <name>] [--restart] [--wait-for-ack [--timeout 2m]]", "secrets")
		return 1
	}
	if *timeout <= 0 {
		fmt.Fprintln(os.Stderr, "secret unset: --timeout must be greater than zero")
		return 1
	}
	timeoutSpecified := false
	fs.Visit(func(value *flag.Flag) {
		if value.Name == "timeout" {
			timeoutSpecified = true
		}
	})
	if timeoutSpecified && !*waitForAck {
		fmt.Fprintln(os.Stderr, "secret unset: --timeout requires --wait-for-ack")
		return 1
	}
	resolvedScope, resolveErr := resolveEnvironmentFlagOrContext(*scope)
	if resolveErr != nil {
		return printErr("Could not read local project context", resolveErr)
	}
	*scope = resolvedScope
	key := fs.Arg(0)
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	revocation, err := client.UnsetSecretWithScopeAndStatus(context.Background(), *app, key, *scope)
	if err != nil {
		return printErr("Unset failed", err)
	}
	// Running instances keep the environment they booted with; like
	// `secrets set --restart`, a restart applies the removal now.
	wakeID := ""
	if *restart {
		out, err := client.RestartAppFresh(context.Background(), *app)
		if err != nil {
			return printErr("Restart failed", err)
		}
		wakeID = out.WakeID
	}
	if jsonOutput {
		receipt := map[string]any{
			"app": *app, "status": "deleted", "scope": scopeOrDefault(*scope), "key": key,
			"deleted": true, "revocation_id": revocation.ID,
			"revocation_status":  revocation.Status,
			"target_count":       revocation.TargetCount,
			"acknowledged_count": revocation.AcknowledgedCount,
			"pending_count":      revocation.PendingCount,
		}
		if *restart {
			receipt["restart_requested"] = true
			receipt["wake_id"] = wakeID
		}
		if *waitForAck {
			ctx, cancel := context.WithTimeout(context.Background(), *timeout)
			defer cancel()
			progress, err := waitForSecretRevocationAck(ctx, client, *app, revocation.ID)
			if err != nil {
				return printErr("Secret revocation acknowledgement incomplete", err)
			}
			receipt["revocation_status"] = progress.Status
			receipt["target_count"] = progress.TargetCount
			receipt["acknowledged_count"] = progress.AcknowledgedCount
			receipt["pending_count"] = progress.PendingCount
		}
		return jsonOut(writeJSON(receipt))
	}
	PrintOK(osStdout, "%s unset (scope=%s, revocation=%s)", key, scopeOrDefault(*scope), revocation.ID)
	if *restart {
		PrintOK(osStdout, "Restart requested after secret removal (wake_id=%s)", wakeID)
	} else {
		PrintWarn(osStdout, "Running instances keep the removed secret until their next cold wake. Use --restart to apply now.")
	}
	if *waitForAck {
		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		defer cancel()
		progress, err := waitForSecretRevocationAck(ctx, client, *app, revocation.ID)
		if err != nil {
			return printErr("Secret revocation acknowledgement incomplete", err)
		}
		PrintOK(osStdout, "All %d authorized runtime(s) acknowledged secret removal", progress.AcknowledgedCount)
	}
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
	fs := newFlagSet("secrets list-all", flag.ContinueOnError)
	before := fs.String("before", "", "pagination cursor from a previous call's next_before (slug|key)")
	limit := fs.Int("limit", api.SecretsListPageMax, "page size (1..100; server caps at 100)")
	secretClass := fs.String("class", "", "filter this page by snapshot-retention class (persistent or ephemeral)")
	olderThan := fs.String("older-than", "", "filter this page to secrets not updated within a duration (for example 90d or 2160h)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if !validSecretClass(*secretClass) {
		return printErr("Invalid --class", fmt.Errorf("must be %q or %q; got %q", api.SecretClassPersistent, api.SecretClassEphemeral, *secretClass))
	}
	age, err := parseSecretAge(*olderThan)
	if err != nil {
		return printErr("Invalid --older-than", err)
	}
	if *limit < 1 || *limit > api.SecretsListPageMax {
		return printErr("Invalid --limit", fmt.Errorf("must be in [1,%d]; got %d", api.SecretsListPageMax, *limit))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.GetSecrets(context.Background(), *before, *limit)
	if err != nil {
		return printErr("List-all failed", err)
	}
	if *secretClass != "" {
		filtered := resp.Secrets[:0]
		for _, secret := range resp.Secrets {
			if secretClassLabel(secret.SecretClass) == *secretClass {
				filtered = append(filtered, secret)
			}
		}
		resp.Secrets = filtered
	}
	if age > 0 {
		filterAccountSecretListByAge(&resp, time.Now().Add(-age))
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	if len(resp.Secrets) == 0 {
		if *secretClass != "" && age > 0 {
			_, _ = fmt.Fprintf(osStdout, "(no %s secrets older than %s on this page)\n", *secretClass, *olderThan)
		} else if *secretClass != "" {
			_, _ = fmt.Fprintf(osStdout, "(no %s secrets on this page)\n", *secretClass)
		} else if age > 0 {
			_, _ = fmt.Fprintf(osStdout, "(no secrets older than %s on this page)\n", *olderThan)
		} else {
			_, _ = fmt.Fprintln(osStdout, "(no secrets)")
		}
		if resp.NextBefore != "" {
			_, _ = fmt.Fprintf(osStderr, "next page: --before %s\n", resp.NextBefore)
		}
		return 0
	}
	for _, s := range resp.Secrets {
		_, _ = fmt.Fprintf(osStdout, "%-32s %-32s %-10s %s\n", s.AppSlug, s.Key, secretClassLabel(s.SecretClass), s.UpdatedAt)
	}
	if resp.NextBefore != "" {
		_, _ = fmt.Fprintf(osStderr, "next page: --before %s\n", resp.NextBefore)
	}
	return 0
}

type secretAuditFinding struct {
	AppSlug   string `json:"app_slug"`
	Scope     string `json:"scope"`
	Key       string `json:"key"`
	Class     string `json:"secret_class"`
	UpdatedAt string `json:"updated_at"`
	Age       string `json:"age"`
}

type secretAuditReport struct {
	AuditedAt             string               `json:"audited_at"`
	OlderThan             string               `json:"older_than"`
	Scanned               int                  `json:"scanned"`
	UnknownUpdatedAtCount int                  `json:"unknown_updated_at_count"`
	StaleCount            int                  `json:"stale_count"`
	StaleSecrets          []secretAuditFinding `json:"stale_secrets"`
}

func secretsAudit(args []string) int {
	fs := newFlagSet("secrets audit", flag.ContinueOnError)
	olderThan := fs.String("older-than", "", "required age threshold (for example 90d or 2160h)")
	failOnStale := fs.Bool("fail-on-stale", false, "exit non-zero when any secret exceeds the age threshold")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	age, err := parseSecretAge(*olderThan)
	if err != nil {
		return printErr("Invalid --older-than", err)
	}
	if age == 0 {
		return printErr("Invalid --older-than", fmt.Errorf("required; provide a positive duration such as 90d"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	report, err := collectSecretAudit(context.Background(), client, age, time.Now().UTC())
	if err != nil {
		return printErr("Audit failed", err)
	}
	report.OlderThan = *olderThan
	if jsonOutput {
		if code := jsonOut(writeJSON(report)); code != 0 {
			return code
		}
	} else {
		renderSecretAudit(osStdout, report)
	}
	if *failOnStale && report.StaleCount > 0 {
		return printErr("Stale secrets found", fmt.Errorf("%d secret(s) have not been updated within %s", report.StaleCount, *olderThan))
	}
	return 0
}

func collectSecretAudit(ctx context.Context, client *api.Client, age time.Duration, now time.Time) (secretAuditReport, error) {
	report := secretAuditReport{AuditedAt: now.Format(time.RFC3339Nano), StaleSecrets: make([]secretAuditFinding, 0)}
	cutoff := now.Add(-age)
	seenCursors := make(map[string]struct{})
	before := ""
	for {
		page, err := client.GetSecrets(ctx, before, api.SecretsListPageMax)
		if err != nil {
			return secretAuditReport{}, err
		}
		report.Scanned += len(page.Secrets)
		for _, secret := range page.Secrets {
			updated, ok := parseSecretUpdatedAt(secret.UpdatedAt)
			if !ok {
				report.UnknownUpdatedAtCount++
				continue
			}
			if updated.After(cutoff) {
				continue
			}
			secretAge := now.Sub(updated)
			report.StaleSecrets = append(report.StaleSecrets, secretAuditFinding{
				AppSlug: secret.AppSlug, Scope: scopeOrDefault(secret.Scope), Key: secret.Key,
				Class: secretClassLabel(secret.SecretClass), UpdatedAt: secret.UpdatedAt, Age: secretAge.String(),
			})
		}
		if page.NextBefore == "" {
			break
		}
		if _, exists := seenCursors[page.NextBefore]; exists {
			return secretAuditReport{}, fmt.Errorf("account secret pagination repeated a cursor")
		}
		seenCursors[page.NextBefore] = struct{}{}
		before = page.NextBefore
	}
	report.StaleCount = len(report.StaleSecrets)
	return report, nil
}

func renderSecretAudit(w io.Writer, report secretAuditReport) {
	if report.StaleCount == 0 {
		_, _ = fmt.Fprintf(w, "No known timestamps exceed %s (scanned %d; %d unknown updated_at value(s) not classified).\n", report.OlderThan, report.Scanned, report.UnknownUpdatedAtCount)
		return
	}
	_, _ = fmt.Fprintf(w, "%d secret(s) have remained unchanged in Gregale for at least %s (scanned %d; %d unknown updated_at value(s) not classified):\n", report.StaleCount, report.OlderThan, report.Scanned, report.UnknownUpdatedAtCount)
	for _, finding := range report.StaleSecrets {
		_, _ = fmt.Fprintf(w, "  %s/%s/%s · %s · age %s · updated %s\n", finding.AppSlug, finding.Scope, finding.Key, finding.Class, finding.Age, finding.UpdatedAt)
	}
}

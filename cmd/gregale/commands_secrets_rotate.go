// commands_secrets_rotate.go — `gregale secrets rotate` subcommand
// (ADR-089 PR-B).
//
// Distinct verb from `secrets set` so the server emits
// secret.rotated (vs secret.set on PUT). Single key per
// invocation — rotations are deliberate, single-credential
// operations; bulk rotation belongs in the rotation runbook.
//
//   gregale secrets rotate --app <slug> KEY=VALUE [--from-stdin]
//
// `--from-stdin` reads KEY=VALUE from one stdin line so the
// plaintext never lands in shell history. Mirrors `secrets set`'s
// stdin flag for consistency.
//
// Trust model matches `secrets set`: plaintext flows over TLS to
// apid, the seal happens server-side, ciphertext never re-enters
// the CLI. The server response includes the rotated_at timestamp
// and the kid of the host identity that sealed the new envelope —
// rendered as "rotated <ts> under <kid-short>".

package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// secretsRotate handles `gregale secrets rotate --app <slug>
// KEY=VALUE [--from-stdin] [--scope <name>]`. Single pair (vs
// `secrets set` which takes a list) — rotation is a deliberate,
// atomic operation. The handler reads from stdin when --from-stdin
// is set so the plaintext never touches argv. Failure surfaces
// apid's Problem envelope as-is (already mapped by client.do).
//
// --scope (ADR-092 PR-B) selects which env-scope the rotation
// targets; the rotation hint now reads "rotating %s in scope=%q"
// so the customer knows which (scope, key) pair they're rotating.
func secretsRotate(args []string) int {
	fs := newFlagSet("secrets rotate", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "validate input and preview the operation without API requests or changes")
	app := fs.String("app", "", "app slug (defaults to linked project app)")
	fromStdin := fs.Bool("from-stdin", false, "read KEY=VALUE from stdin (one pair)")
	scope := fs.String(secretsCmdScopeFlag, "", "env scope to rotate (defaults to linked project environment)")
	restart := fs.Bool("restart", false, "restart app with the rotated secret")
	waitForAck := fs.Bool("wait-for-ack", false, "wait until every active authorized runtime confirms it applied the rotated secret")
	timeout := fs.Duration("timeout", 2*time.Minute, "maximum time to wait for application acknowledgements")
	orderedArgs, err := reorderSecretsSetArgs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "secret rotate:", err)
		return 1
	}
	if err := fs.Parse(orderedArgs); err != nil {
		return 1
	}
	resolvedApp, appErr := resolveSecretApp(*app)
	if appErr != nil {
		return printErr("Could not resolve app", appErr)
	}
	appSource := targetValueSource(*app, resolvedApp)
	*app = resolvedApp

	if *timeout <= 0 {
		fmt.Fprintln(os.Stderr, "secret rotate: --timeout must be greater than zero")
		return 1
	}
	timeoutSpecified := false
	fs.Visit(func(value *flag.Flag) {
		if value.Name == "timeout" {
			timeoutSpecified = true
		}
	})
	if timeoutSpecified && !*waitForAck {
		fmt.Fprintln(os.Stderr, "secret rotate: --timeout requires --wait-for-ack")
		return 1
	}
	resolvedScope, resolveErr := resolveEnvironmentFlagOrContext(*scope)
	if resolveErr != nil {
		return printErr("Could not read local project context", resolveErr)
	}
	scopeSource := targetValueSource(*scope, resolvedScope)
	*scope = resolvedScope

	var pair secretsPair
	if *fromStdin {
		if fs.NArg() != 0 {
			fmt.Fprintln(os.Stderr, "secret rotate: --from-stdin takes no positional pair")
			return 1
		}
		scanner := newKeyValueScanner(bufio.NewScanner(osStdin))
		// Read the first non-empty, non-comment line; ignore
		// everything after (rotation is single-key by design).
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
			pair = p
			break
		}
		if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
			fmt.Fprintln(os.Stderr, "read stdin:", keyValueScanErr(err))
			return 1
		}
		if pair.Key == "" {
			fmt.Fprintln(os.Stderr, "secret rotate: no KEY=VALUE pair found on stdin")
			return 1
		}
	} else {
		if fs.NArg() != 1 {
			fmt.Fprintln(os.Stderr,
				"secret rotate: exactly one KEY=VALUE pair is required (got "+
					fmt.Sprint(fs.NArg())+")")
			return 1
		}
		p, err := parseSecretsPair(fs.Arg(0))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		pair = p
	}

	if *dryRun {
		return previewSecrets("rotate", *app, *scope, appSource, scopeSource, []string{pair.Key}, "", *restart, *waitForAck, *timeout)
	}

	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}

	printMutationTarget(*app, *scope, appSource, scopeSource)
	resp, err := client.RotateSecretWithScope(context.Background(), *app, pair.Key, pair.Value, *scope)
	if err != nil {
		return printErr("Rotate "+pair.Key+" failed", err)
	}
	receipt := secretsRotateReceipt{
		App: *app, Status: "rotated", Scope: scopeOrDefault(*scope), Key: resp.Key,
		RotatedAt: resp.RotatedAt, Kid: resp.Kid, RestartRequested: *restart,
	}
	writeReceipt := func() int {
		if jsonOutput {
			return jsonOut(writeJSON(receipt))
		}
		return 0
	}
	// Short-form kid for the human-friendly line. Full kid is in
	// the JSON response shape (RotateAppSecretResponse.Kid). The
	// first 12 chars are enough to disambiguate "rotated under
	// age-1abc..." from "rotated under age-1def..." without
	// dumping 60+ chars on every line.
	shortKid := resp.Kid
	if len(shortKid) > 12 {
		shortKid = shortKid[:12] + "…"
	}
	if !jsonOutput {
		PrintOK(osStdout, "%s rotated at %s (kid %s)",
			resp.Key, resp.RotatedAt, shortKid)
	}
	if *restart {
		wakeID, parked, err := restartAfterSecretChange(context.Background(), client, *app)
		if err != nil {
			return printConfigurationPartialError("Restart failed", err, *app, *scope, "Secret rotation was saved; restart was not confirmed.", []string{pair.Key}, "")
		}
		if parked {
			if jsonOutput {
				return writeReceipt()
			}
			PrintOK(osStdout, "%s", parkedSecretChangeNote)
			return 0
		}
		receipt.WakeID = wakeID
		if !*waitForAck {
			if jsonOutput {
				return writeReceipt()
			}
			PrintOK(osStdout, "Restart requested after secret rotation (wake_id=%s)", wakeID)
			printSecretUpdateNextSteps(*app, *scope, wakeID, pair.Key)
			return 0
		}
		if strings.TrimSpace(wakeID) == "" {
			return printConfigurationPartialError("Restart failed", errors.New("server accepted the restart without returning a wake_id"), *app, *scope, "Secret rotation was saved; no restart identifier was confirmed.", []string{pair.Key}, "")
		}
		waitCtx, cancel := context.WithTimeout(context.Background(), *timeout)
		defer cancel()
		instance, err := waitForAppWake(waitCtx, client, *app, wakeID, *timeout, 250*time.Millisecond)
		if err != nil {
			return printConfigurationPartialError("Waiting for restarted app failed", err, *app, *scope, "Secret rotation was saved and a restart was accepted; its completion was not confirmed.", []string{pair.Key}, wakeID)
		}
		count, err := waitForSecretApplicationAckAfterRestart(waitCtx, client, *app, pair.Key, *scope, instance.ID)
		if err != nil {
			return printConfigurationPartialError("Waiting for application acknowledgement failed", err, *app, *scope, "Secret rotation was saved; application acknowledgement is incomplete.", []string{pair.Key}, receipt.WakeID)
		}
		receipt.InstanceID = instance.ID
		receipt.AcknowledgedRuntimes = &count
		if jsonOutput {
			return writeReceipt()
		}
		PrintOK(osStdout, "Restart completed (wake_id=%s, instance_id=%s)", wakeID, instance.ID)
		PrintOK(osStdout, "All %d active authorized runtime(s) confirmed they applied %s", count, pair.Key)
		return 0
	}
	if *waitForAck {
		waitCtx, cancel := context.WithTimeout(context.Background(), *timeout)
		defer cancel()
		count, err := waitForSecretApplicationAck(waitCtx, client, *app, pair.Key, *scope)
		if err != nil {
			return printConfigurationPartialError("Waiting for application acknowledgement failed", err, *app, *scope, "Secret rotation was saved; application acknowledgement is incomplete.", []string{pair.Key}, receipt.WakeID)
		}
		receipt.AcknowledgedRuntimes = &count
		if count == 0 {
			receipt.Warnings = []string{"No active authorized runtimes; this rotation will apply on their next cold wake."}
			if jsonOutput {
				return writeReceipt()
			}
			PrintOK(osStdout, "No active authorized runtimes; %s rotation will apply on their next cold wake", pair.Key)
			return 0
		}
		if jsonOutput {
			return writeReceipt()
		}
		PrintOK(osStdout, "All %d active authorized runtime(s) confirmed they applied %s", count, pair.Key)
		return 0
	}
	if jsonOutput {
		receipt.Warnings = []string{"The rotated secret applies on the next cold wake; running instances keep their current environment. Use --restart to apply now."}
		return writeReceipt()
	}
	PrintWarn(osStdout, "The rotated secret applies on the next cold wake; running instances keep their current environment. Use --restart to apply now.")
	printSecretUpdateNextSteps(*app, *scope, "", pair.Key)
	return 0
}

type secretsRotateReceipt struct {
	App                  string   `json:"app"`
	Status               string   `json:"status"`
	Scope                string   `json:"scope"`
	Key                  string   `json:"key"`
	RotatedAt            string   `json:"rotated_at"`
	Kid                  string   `json:"kid,omitempty"`
	RestartRequested     bool     `json:"restart_requested"`
	WakeID               string   `json:"wake_id,omitempty"`
	InstanceID           string   `json:"instance_id,omitempty"`
	AcknowledgedRuntimes *int     `json:"acknowledged_runtimes,omitempty"`
	Warnings             []string `json:"warnings,omitempty"`
}

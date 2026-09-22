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
	app := fs.String("app", "", "app slug")
	fromStdin := fs.Bool("from-stdin", false, "read KEY=VALUE from stdin (one pair)")
	scope := fs.String(secretsCmdScopeFlag, "", "env scope to rotate (defaults to linked project environment)")
	restart := fs.Bool("restart", false, "restart app with the rotated secret")
	orderedArgs, err := reorderSecretsSetArgs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "secret rotate:", err)
		return 1
	}
	if err := fs.Parse(orderedArgs); err != nil {
		return 1
	}
	if *app == "" {
		PrintUsage(os.Stderr,
			"usage: gregale secrets rotate --app <slug> KEY=VALUE [--from-stdin] [--scope <name>] [--restart]", "secrets")
		return 1
	}
	resolvedScope, resolveErr := resolveEnvironmentFlagOrContext(*scope)
	if resolveErr != nil {
		return printErr("Could not read local project context", resolveErr)
	}
	*scope = resolvedScope

	var pair secretsPair
	if *fromStdin {
		if fs.NArg() != 0 {
			fmt.Fprintln(os.Stderr, "secret rotate: --from-stdin takes no positional pair")
			return 1
		}
		scanner := bufio.NewScanner(osStdin)
		scanner.Buffer(make([]byte, 0, 64*1024), 64*1024)
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
			fmt.Fprintln(os.Stderr, "read stdin:", err)
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

	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}

	resp, err := client.RotateSecretWithScope(context.Background(), *app, pair.Key, pair.Value, *scope)
	if err != nil {
		return printErr("Rotate "+pair.Key+" failed", err)
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
	PrintOK(osStdout, "%s rotated at %s (kid %s)",
		resp.Key, resp.RotatedAt, shortKid)
	if *restart {
		out, err := client.RestartAppFresh(context.Background(), *app)
		if err != nil {
			return printErr("Restart failed", err)
		}
		PrintOK(osStdout, "Restart requested after secret rotation (wake_id=%s)", out.WakeID)
		return 0
	}
	PrintWarn(osStdout, "The rotated secret applies on the next cold wake; running instances keep their current environment. Use --restart to apply now.")
	return 0
}

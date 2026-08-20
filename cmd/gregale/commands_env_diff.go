// commands_env_diff.go — `gregale env diff` CLI
// (ADR-117 PR-C, GET /v1/apps/{slug}/env-diff).
//
// Renders the env-diff matrix as a text table. Cells use
// three symbols:
//
//   - `-` (missing)    — the (row, scope) pair is absent.
//   - `==` (same)      — secret cells: value_hash matches
//                        a peer cell. env cells: value
//                        equals a peer cell.
//   - `≠`  (different) — secret cells: value_hash differs.
//                        env cells: value differs.
//   - literal          — env cells: the value itself (env is
//                        public; the load-bearing security
//                        property of the endpoint). Secret
//                        cells NEVER emit a literal.
//
// `--json` reuses the global jsonOutput flag and dumps the
// raw EnvDiffResponse for shell pipelines.

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"github.com/onebox-faas/faas/pkg/api"
)

// envDiff handles `gregale env diff --app <slug>`. The route
// is the same wire surface as the JSON endpoint; the renderer
// is text-only and the security rules (no plaintext on
// secret cells) are enforced by the EnvDiffCell DTO shape
// upstream (omitempty on Value for secret cells).
func envDiff(args []string) int {
	fs := flag.NewFlagSet("env diff", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *app == "" {
		PrintUsage(os.Stderr, "usage: gregale env diff --app <slug>", "env")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.GetAppEnvDiff(context.Background(), *app)
	if err != nil {
		return printErr("Diff failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	renderEnvDiffTable(osStdout, *app, &resp)
	return 0
}

// renderEnvDiffTable emits the matrix as a tabwriter-formatted
// text table. Column 1 is the key; column 2 is the kind
// (secret|env); subsequent columns are the scope cells in
// the response's Scopes order.
//
// Symbols:
//   - "-"     missing
//   - "=="    matches another cell in the same row
//   - "≠"     differs from at least one peer cell
//   - literal env cells only (secrets never reveal values)
//
// Pre-PR-C rows (value_hash = ”) render as "-" for secret
// cells — the renderer treats the absent value_hash as
// "unknown, cannot compare".
func renderEnvDiffTable(w io.Writer, app string, resp *api.EnvDiffResponse) {
	if len(resp.Rows) == 0 {
		_, _ = fmt.Fprintf(w, "%s: no env vars or secrets (0 rows)\n", app)
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	// Header.
	_, _ = fmt.Fprintf(tw, "KEY\tKIND")
	for _, sc := range resp.Scopes {
		_, _ = fmt.Fprintf(tw, "\t%s", sc)
	}
	_, _ = fmt.Fprintln(tw)
	// Body.
	for _, row := range resp.Rows {
		_, _ = fmt.Fprintf(tw, "%s\t%s", row.Key, row.Kind)
		for _, sc := range resp.Scopes {
			cell := row.Cells[sc]
			_, _ = fmt.Fprintf(tw, "\t%s", renderEnvDiffCell(row.Kind, cell, row.Cells, resp.Scopes))
		}
		_, _ = fmt.Fprintln(tw)
	}
	_ = tw.Flush()
}

// renderEnvDiffCell renders one cell's display string based
// on the row's kind + the cell + the peer cells (for "=="
// detection). The peer-aware logic is the load-bearing UX
// piece: a row with three identical cells should render as
// "==" across all three, not "≠" between every pair.
//
// Peer comparison semantics:
//   - secret rows: peers are equal iff value_hash matches
//     (the cell.Present check filters out missing peers).
//   - env rows:    peers are equal iff Value matches.
//   - Missing cells (Present: false) NEVER participate in
//     the peer set — a cell with no peer to compare against
//     renders as "-" (the "single cell" case).
//
// A pre-PR-C row (value_hash = ”) renders as "-" for every
// scope because we cannot make an equality claim about an
// unknown value. This is the conservative posture — better
// to render "unknown" than to assert a (potentially false)
// "==".
func renderEnvDiffCell(kind api.EnvDiffKind, cell api.EnvDiffCell, peers map[string]api.EnvDiffCell, scopes []string) string {
	if !cell.Present {
		return "-"
	}
	switch kind {
	case api.EnvDiffKindSecret:
		if cell.ValueHash == "" {
			// Pre-PR-C: unknown value_hash → "unknown",
			// NOT a "==" assertion.
			return "-"
		}
		// Walk peers: are there any PRESENT peers with
		// the same value_hash? If so, this cell matches
		// at least one peer → "==". If peers are
		// PRESENT but DIFFERENT, → "≠". If no PRESENT
		// peers, single cell → "==" (vacuously, the
		// row has only one stamped scope).
		hasPresentPeer := false
		for _, sc := range scopes {
			p, ok := peers[sc]
			if !ok || !p.Present {
				continue
			}
			hasPresentPeer = true
			if p.ValueHash != cell.ValueHash {
				return "≠"
			}
		}
		if !hasPresentPeer {
			// Single stamped scope; nothing to compare
			// against. Render as "==" (no peer
			// disagreement is a vacuous "match").
			return "=="
		}
		return "=="
	case api.EnvDiffKindEnv:
		// Env cells expose the value (env is public).
		// Peer comparison: if any peer has a different
		// value, render this one as the literal (the
		// customer wants to see WHICH value is
		// different, not just "≠"). Peers that match
		// render as "==".
		hasDiffPeer := false
		for _, sc := range scopes {
			p, ok := peers[sc]
			if !ok || !p.Present {
				continue
			}
			if p.Value != cell.Value {
				hasDiffPeer = true
				break
			}
		}
		if !hasDiffPeer {
			return "=="
		}
		return cell.Value
	}
	return "-"
}

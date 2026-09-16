// output.go — operator-side CLI output helpers (issue #911 / ADR-110 PR-6.5).
//
// Duplicates cmd/gregale/output.go so the operator binary is
// self-contained. The UX §3.2 conventions (glyph stripping on
// non-TTY, NO_COLOR handling, PrintOK/PrintFail/PrintProgress/PrintWarn
// shape) are identical between the two binaries — operators
// debugging a `gregalectl host-age rotate` want the same visual
// feedback as customers running `gregale apps list`. PR-7 may
// extract a shared cmd/internal/cliutil/output package; for
// PR-6.5 duplication is intentional.
package main

import (
	"fmt"
	"io"
	"os"
	"sync/atomic"

	"github.com/onebox-faas/faas/pkg/wire"
)

// stdoutIsTTY is defined per-platform: isatty_unix.go calls
// term.IsTerminal under the hood; isatty_windows.go always returns
// false (stub). The test seam (testOnlyTTY below) overrides both.
var noColorCached atomic.Bool
var noColorVal bool

func noColorSet() bool {
	if noColorCached.Load() {
		return noColorVal
	}
	if raw, ok := os.LookupEnv("NO_COLOR"); ok && raw != "" {
		noColorVal = true
	}
	noColorCached.Store(true)
	return noColorVal
}

// Enabled reports whether human-readable glyphs and colour are
// appropriate for the current stdout. Single global gate — both
// success and failure renderers call into writeStatus which checks
// this exactly once per line.
func Enabled() bool {
	if jsonOutput {
		return false
	}
	if noColorSet() {
		return false
	}
	return stdoutIsTTY()
}

// PrintOK emits a "done" line. Glyph `✓` when Enabled, plain otherwise.
func PrintOK(w io.Writer, format string, a ...any) {
	writeStatus(w, "✓", format, a...)
}

// PrintFail emits a "failed" line. Glyph `✗` when Enabled.
func PrintFail(w io.Writer, format string, a ...any) {
	writeStatus(w, "✗", format, a...)
}

// PrintProgress emits an "in-progress" line. Glyph `→` when Enabled.
func PrintProgress(w io.Writer, format string, a ...any) {
	writeStatus(w, "→", format, a...)
}

// PrintWarn emits a "warning" line. Glyph `!` when Enabled.
func PrintWarn(w io.Writer, format string, a ...any) {
	writeStatus(w, "!", format, a...)
}

// GlyphOK is the leading character for a "done" line. Same rationale
// as cmd/gregale/output.go — single allow-listed file for leading
// glyph literals.
const GlyphOK = "✓"

// GlyphFail is the leading character for a "failed" line.
const GlyphFail = "✗"

// GlyphProgress is the leading character for an "in progress" line.
const GlyphProgress = "→"

// GlyphEmDash is the placeholder character used when a tabular column
// has no value to render. Same rationale as GlyphOK.
const GlyphEmDash = "—"

// writeStatus centralises the "leading glyph + space + content + newline"
// rule. Fprintf errors intentionally discarded (same shape as
// cmd/gregale/output.go).
func writeStatus(w io.Writer, glyph, format string, a ...any) {
	prefix := ""
	if Enabled() {
		prefix = glyph + " "
	}
	_, _ = fmt.Fprintf(w, prefix+format+"\n", a...)
}

// testOnlyTTY is the package-private test seam. nil in production.
// Mirrors cmd/gregale/output.go:129 — the same race-safety caveat
// (no t.Parallel today).
var testOnlyTTY *bool

// docsURLBase is the canonical root for the operator-side CLI docs.
// Mirrors cmd/gregale/output.go:139. Operator topics
// (manifest, release, host-age, pki, ...) land at
// gregale.dev/docs/cli/<topic> until the docs site splits
// into /cli/ vs /operator/.
const docsURLBase = wire.DocsBaseURL + "/cli/"

// PrintUsage emits a one-line "usage:" hint followed by a "Docs:" line
// pointing at gregale.dev/docs/cli/<topic>. Mirrors cmd/gregale/output.go:177
// byte-for-byte.
func PrintUsage(w io.Writer, usage, topic string) {
	_, _ = fmt.Fprintf(w, "%s\n", usage)
	_, _ = fmt.Fprintf(w, "  Docs: %s%s\n", docsURLBase, topic)
}

// printErr emits a one-line error to stderr and returns exit code 1.
// The operator binary's error path is intentionally simpler than the
// customer's (cmd/gregale/commands.go:366): operator commands don't
// surface RFC 7807 envelopes or strict-secret-scan chains, so a
// minimal "title: err" line is the right shape. Mirrors UX §3.2's
// "PrintFail" line shape — uses the same glyph gate (dropped when
// not a TTY / NO_COLOR is set).
func printErr(title string, err error) int {
	if err == nil {
		_, _ = fmt.Fprintf(os.Stderr, "%s\n", title)
		return 1
	}
	_, _ = fmt.Fprintf(os.Stderr, "%s: %v\n", title, err)
	return 1
}

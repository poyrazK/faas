// UX spec §3.2 output conventions — the writer-based gate that strips
// `✓/✗/→/!` glyphs and any future colour escapes when the customer's
// stdout is not a TTY or when `NO_COLOR` is set non-empty (per
// no-color.org). Every existing `fmt.Fprintf(osStdout, "✗ …", …)` call
// site should migrate to PrintOK / PrintFail / PrintProgress / PrintWarn
// so the same line shape survives a pipe. Reuses the package-level
// jsonOutput flag (json_flag.go:18) so the JSON path is always plain.
//
// This is the §3.2 follow-up to PR #101. The plan is at
// /Users/poyrazk/.claude/plans/lets-create-imp-plan-majestic-hanrahan.md.
//
// Cross-platform: every renderer + the gate live here so this file
// compiles on every GOOS. The platform-specific TTY probe lives in
// isatty_unix.go (term.IsTerminal on unix) and isatty_windows.go
// (always returns false — cmd/faas does not officially target Windows
// today; the stub keeps `go build ./...` happy on a contributor's
// Windows box).

package main

import (
	"fmt"
	"io"
	"os"
	"sync/atomic"
)

// stdoutIsTTY is defined per-platform: isatty_unix.go calls
// term.IsTerminal under the hood; isatty_windows.go always returns
// false (stub). The test seam (testOnlyTTY below) overrides both.

// noColorSet reads NO_COLOR via os.LookupEnv once and caches the result.
// Per no-color.org, any non-empty value (including "0", "false", "no")
// disables colour and glyphs.
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
// appropriate for the current stdout. The single global gate — both
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

// writeStatus centralises the "leading glyph + space + content + newline"
// rule. The four Print* functions are one-liners above. The error from
// Fprintf is intentionally discarded: writer failures (closed pipe,
// broken TTY) are unrecoverable here, and we never want a status line
// to crash the CLI on its way out. This is the same shape the rest of
// the package uses for stdout/stderr printers (commands*.go).
func writeStatus(w io.Writer, glyph, format string, a ...any) {
	prefix := ""
	if Enabled() {
		prefix = glyph + " "
	}
	_, _ = fmt.Fprintf(w, prefix+format+"\n", a...)
}

// testOnlyTTY is the package-private test seam. nil in production
// (stdoutIsTTY returns the real platform result). output_test.go sets it
// to &true for the common case where a captured buffer should still
// carry the glyph, and to &false for explicit no-TTY assertions.
//
// The pointer is read under no concurrency — `cmd/faas/` tests don't
// use t.Parallel today. If that ever changes, swap to
// `atomic.Pointer[bool]` (Go 1.22+). Documented here so a future
// contributor doesn't silently introduce a race.
var testOnlyTTY *bool

// docsURLBase is the canonical root for the customer-facing CLI docs.
// Used by PrintUsage so every `usage:` line carries a stable, namespaced
// link to the docs site. Mirrors how the systemd unit files use
// `https://docs.DOMAIN/ops/<daemon>` — same host, same convention.
const docsURLBase = "https://docs.DOMAIN/cli/"

// RenderTitle emits the title row of an APIError render. When Enabled()
// the leading `✗ ` glyph prefixes the title; otherwise the row is just
// the title. The `Detail` row and the `Docs` row are written separately
// by the caller. The split is here (not inside one big function) so the
// glyph tripwire in lint_tripwires_test.go has a single allow-listed
// file (output.go) for every ✓/✗/→ string literal in the package. The
// Fprintf errors are intentionally discarded (same convention as
// writeStatus — see that comment).
func RenderTitle(w io.Writer, title string) {
	if Enabled() {
		_, _ = fmt.Fprintf(w, "✗ %s\n", title)
		return
	}
	_, _ = fmt.Fprintf(w, "%s\n", title)
}

// RenderDocsRow emits the third line of an APIError render — the row
// pointing at the docs URL. When Enabled() the row carries `  → `
// (UX §3.3's three-line contract); otherwise it carries `  ` so the
// line count is preserved for script consumers that split on "\n".
// Fprintf errors intentionally discarded — same convention as
// writeStatus / RenderTitle.
func RenderDocsRow(w io.Writer, url string) {
	if Enabled() {
		_, _ = fmt.Fprintf(w, "  → %s\n", url)
		return
	}
	_, _ = fmt.Fprintf(w, "  %s\n", url)
}

// PrintUsage emits a one-line "usage:" hint followed by a "Docs:" line
// pointing at docs.DOMAIN/cli/<topic>. Always plain (no glyphs) —
// usage lines go to stderr on bad argv and customers grep them; the
// glyph would just be noise there. Topic is the slug from the table in
// the §3.2 plan (cli/apps, cli/ps, cli/logs, etc.). Fprintf errors
// intentionally discarded — same convention as writeStatus / RenderTitle.
func PrintUsage(w io.Writer, usage, topic string) {
	_, _ = fmt.Fprintf(w, "%s\n", usage)
	_, _ = fmt.Fprintf(w, "  Docs: %s%s\n", docsURLBase, topic)
}

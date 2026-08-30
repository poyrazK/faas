// commands/man.go — Tier A8 / ADR-083.
//
// `gregale man` (full gregale(1) page) and `gregale man <command>`
// (per-command gregale-<command>(1)) emit roff source to stdout.
//
// Output format is groff man (the same flavour `man` renders on
// Linux + macOS). The script can pipe the result to `man -l -`
// for immediate rendering, or redirect to a file under
// /usr/local/share/man/man1/ for permanent installation.
//
// The roff emits NAME / SYNOPSIS / DESCRIPTION / COMMANDS /
// EXAMPLES / SEE ALSO sections per the Linux man-pages(7)
// convention. Headers are bracketed in .SH; the synopsis uses
// .B for the literal tokens; URLs are .UR + .UE.
//
// Output is human-only (never takes --json) — the roff itself
// IS the structured format. A `--json` flag would require
// defining an alternate schema just for this command, which is
// not worth the surface.

package main

import (
	"fmt"
	"io"
	"os"
	"strings"
)

const manDocsTopic = "man"

// manSourceBrand is the .TH source field for every page. We don't
// vary it per-command because the brand is what `whatis` and
// `man -k` index on — operators expect to grep `gregale` (lower-
// case) across every page in the section.
const manSourceBrand = "gregale"

// gregaleVersion is read from wire.Version at startup via
// initGregaleVersion() (set in main.go via a tiny init() or
// assigned at process boot — wire.Version is a string constant
// already, so this is just an indirection so tests can swap it).
var gregaleVersion = "dev"

// cmdMan dispatches `gregale man [command]`. With no arg, prints
// the top-level gregale(1) page; with one arg, prints the
// per-command page gregale-<command>(1). On an unknown command
// the dispatcher tries to suggest the nearest match by
// Levenshtein distance (suggestCommand, below) before failing —
// users typo'ing `gregale man aps` get "did you mean 'apps'?"
// instead of a bare "unknown command".
func cmdMan(args []string) int {
	switch len(args) {
	case 0:
		renderManTop(osStdout)
		return 0
	case 1:
		cmd, ok := lookupCliCommand(args[0])
		if !ok {
			_, _ = fmt.Fprintf(os.Stderr, "gregale man: unknown command %q\n", args[0])
			if sug, has := suggestCommand(args[0]); has {
				_, _ = fmt.Fprintf(os.Stderr, "  Did you mean %q?\n", sug)
			}
			return 1
		}
		renderManCommand(osStdout, cmd)
		return 0
	default:
		PrintUsage(os.Stderr, "usage: gregale man [<command>]", manDocsTopic)
		return 1
	}
}

// lookupCliCommand returns the cliCommand for name. Linear scan
// is fine — the manifest has ~50 entries.
func lookupCliCommand(name string) (cliCommand, bool) {
	for _, c := range cliCommands {
		if c.Name == name {
			return c, true
		}
	}
	return cliCommand{}, false
}

// renderManTop writes the gregale(1) top-level page.
func renderManTop(w io.Writer) {
	manHeader(w, "GREGALE(1)", "gregale Manual", manSourceBrand)
	manSection(w, "NAME", func(w io.Writer) {
		_, _ = fmt.Fprintln(w, ".B gregale")
		_, _ = fmt.Fprintln(w, "\\- deploy apps and functions that scale to zero")
	})
	manSection(w, "SYNOPSIS", func(w io.Writer) {
		_, _ = fmt.Fprintln(w, ".B gregale")
		_, _ = fmt.Fprintln(w, ".RI [ command ]")
		_, _ = fmt.Fprintln(w, ".RI [ flags ]")
	})
	manSection(w, "DESCRIPTION", func(w io.Writer) {
		_, _ = fmt.Fprintln(w, `.PP`)
		_, _ = fmt.Fprintln(w, `gregale is the customer-facing CLI for the Gregale FaaS platform.`)
		_, _ = fmt.Fprintln(w, `It is the primary interface to the platform; every action the platform`)
		_, _ = fmt.Fprintln(w, `supports is reachable from this single binary.`)
	})
	manSection(w, "COMMANDS", func(w io.Writer) {
		_, _ = fmt.Fprintln(w, `.PP`)
		_, _ = fmt.Fprintln(w, `Run \fBgregale help\fP for the full command list. The most common verbs:`)
		_, _ = fmt.Fprintln(w, ".TP")
		_, _ = fmt.Fprintln(w, ".BR apps ,", " \\fIalerts\\fP,")
		_, _ = fmt.Fprintln(w, ".BR deployments ,", " \\fIregistry\\fP,")
		_, _ = fmt.Fprintln(w, ".BR webhooks ,", " \\fIinvocations\\fP,")
		_, _ = fmt.Fprintln(w, ".BR crons ,", " \\fIdelayed-task\\fP,")
		_, _ = fmt.Fprintln(w, ".BR orgs ,", " \\fIkeys\\fP,")
		_, _ = fmt.Fprintln(w, ".BR mfa")
	})
	manSection(w, "GLOBAL FLAGS", func(w io.Writer) {
		_, _ = fmt.Fprintln(w, ".TP")
		_, _ = fmt.Fprintln(w, ".BR \\-\\-json")
		_, _ = fmt.Fprintln(w, `Machine-readable output. Equivalent to`)
		_, _ = fmt.Fprintln(w, `.B FAAS_JSON=1`)
		_, _ = fmt.Fprintln(w, `in the environment.`)
	})
	manSection(w, "EXAMPLES", func(w io.Writer) {
		_, _ = fmt.Fprintln(w, `.PP`)
		_, _ = fmt.Fprintln(w, `List your apps:`)
		_, _ = fmt.Fprintln(w, `.PP`)
		_, _ = fmt.Fprintln(w, ".RS 4")
		_, _ = fmt.Fprintln(w, `.nf`)
		_, _ = fmt.Fprintln(w, `gregale apps`)
		_, _ = fmt.Fprintln(w, `.fi`)
		_, _ = fmt.Fprintln(w, ".RE")
		_, _ = fmt.Fprintln(w, `.PP`)
		_, _ = fmt.Fprintln(w, `Deploy from a tarball:`)
		_, _ = fmt.Fprintln(w, ".RS 4")
		_, _ = fmt.Fprintln(w, ".nf")
		_, _ = fmt.Fprintln(w, "gregale deploy --tarball ./app.tar.gz --app my-app")
		_, _ = fmt.Fprintln(w, ".fi")
		_, _ = fmt.Fprintln(w, ".RE")
	})
	manSection(w, "SEE ALSO", func(w io.Writer) {
		_, _ = fmt.Fprintf(w, ".UR %s\n", cliDocsURL)
		_, _ = fmt.Fprintln(w, "gregale completion (bash|zsh|fish|powershell)")
		_, _ = fmt.Fprintln(w, ".UE")
		_, _ = fmt.Fprintln(w, ".PP")
		_, _ = fmt.Fprintf(w, ".UR %s\n", docsSiteURL)
		_, _ = fmt.Fprintln(w, "gregale docs")
		_, _ = fmt.Fprintln(w, ".UE")
	})
	manFooter(w)
}

// renderManCommand writes the gregale-<command>(1) per-command page.
func renderManCommand(w io.Writer, c cliCommand) {
	// The .TH source field is the brand ("gregale"), not the page
	// slug — whatis/man -k index on the source label, and operators
	// expect to grep `gregale` (lowercase) across every page.
	manHeader(w, "GREGALE-"+strings.ToUpper(c.Name)+"(1)", "gregale "+c.Name+" Manual", manSourceBrand)
	manSection(w, "NAME", func(w io.Writer) {
		_, _ = fmt.Fprintf(w, ".B gregale-%s\n", c.Name)
		_, _ = fmt.Fprintf(w, "\\- %s\n", escapeRoff(c.Short))
	})
	manSection(w, "SYNOPSIS", func(w io.Writer) {
		_, _ = fmt.Fprintf(w, ".B gregale %s\n", c.Name)
		for _, s := range c.Subcommands {
			_, _ = fmt.Fprintf(w, ".RI [ %s ]\n", s.Name)
		}
		for _, p := range c.Positionals {
			_, _ = fmt.Fprintf(w, ".RI %s\n", p)
		}
		for _, f := range c.Flags {
			// Required flags lose the surrounding brackets so the
			// reader can distinguish them from optional flags at a
			// glance — the conventional groff marker for "no brackets
			// means required".
			if f.Req {
				_, _ = fmt.Fprintf(w, ".RI --%s ", f.Name)
				_, _ = fmt.Fprint(w, ".IR value\n")
			} else {
				_, _ = fmt.Fprintf(w, ".RI [ --%s ", f.Name)
				_, _ = fmt.Fprint(w, ".IR value ")
				_, _ = fmt.Fprint(w, "]\n")
			}
		}
	})
	manSection(w, "DESCRIPTION", func(w io.Writer) {
		_, _ = fmt.Fprintf(w, ".PP\n%s\n", escapeRoff(c.Short))
	})
	if len(c.Subcommands) > 0 {
		manSection(w, "SUBCOMMANDS", func(w io.Writer) {
			_, _ = fmt.Fprintln(w, ".TP")
			for _, s := range c.Subcommands {
				_, _ = fmt.Fprintf(w, ".BR %s\n", s.Name)
				_, _ = fmt.Fprintf(w, "%s\n", escapeRoff(s.Short))
				_, _ = fmt.Fprintln(w, ".TP")
			}
		})
	}
	if len(c.Flags) > 0 {
		manSection(w, "FLAGS", func(w io.Writer) {
			_, _ = fmt.Fprintln(w, ".TP")
			for _, f := range c.Flags {
				// Required flags get a "(required)" suffix in the
				// FLAGS section so a reader scanning for the marker
				// finds it without cross-referencing the SYNOPSIS.
				flagHeader := fmt.Sprintf("--%s", f.Name)
				if f.Req {
					_, _ = fmt.Fprintf(w, ".BR %s\n(required)\n", flagHeader)
				} else {
					_, _ = fmt.Fprintf(w, ".BR %s\n", flagHeader)
				}
				_, _ = fmt.Fprintf(w, "%s\n", escapeRoff(f.Short))
				if len(f.ClosedSet) > 0 {
					_, _ = fmt.Fprintf(w, "Allowed values: %s.\n", strings.Join(f.ClosedSet, ", "))
				}
				_, _ = fmt.Fprintln(w, ".TP")
			}
		})
	}
	manSection(w, "SEE ALSO", func(w io.Writer) {
		_, _ = fmt.Fprintf(w, ".UR %s\n", cliDocsURL)
		_, _ = fmt.Fprintf(w, "gregale %s (docs)\n", c.Name)
		_, _ = fmt.Fprintln(w, ".UE")
		_, _ = fmt.Fprintln(w, ".PP")
		_, _ = fmt.Fprintf(w, ".UR %s\n", docsSiteURL)
		_, _ = fmt.Fprintln(w, "gregale(1) top-level manual")
		_, _ = fmt.Fprintln(w, ".UE")
	})
	manFooter(w)
}

// manHeader writes the page preamble only: .TH title section date source.
// The source field is the brand (`gregale`) for every page — the
// per-command title (GREGALE-ALERTS) lives in the title slot, so
// grepping the source label stays consistent across the whole
// man section. The NAME section is rendered by renderManTop /
// renderManCommand via manSection("NAME", ...) — keeping all .SH
// openings in one place.
func manHeader(w io.Writer, title, subtitle, source string) {
	_, _ = fmt.Fprintf(w, ".TH %s 1 \"%s\" \"%s\"\n", title, gregaleVersion, source)
	_ = subtitle // subtitle is rendered inside the NAME section body
}

// manSection writes a section header followed by the body callback.
// The body callback receives w and emits the roff for the section.
func manSection(w io.Writer, name string, body func(w io.Writer)) {
	_, _ = fmt.Fprintf(w, ".SH %s\n", strings.ToUpper(name))
	body(w)
}

// manFooter writes the trailing blank line + end-of-file marker.
// Most renderers add a final newline so the file ends cleanly
// regardless of how the user pipes the output.
func manFooter(w io.Writer) {
	_, _ = fmt.Fprintln(w)
}

// escapeRoff backslash-escapes roff-significant characters. The
// common ones are backslash itself and the period at the start
// of a line (which would otherwise be interpreted as a macro).
// Inside .SH and .TP sections the period is harmless; we only
// escape when text is part of a paragraph.
func escapeRoff(s string) string {
	if s == "" {
		return ""
	}
	s = strings.ReplaceAll(s, "\\", "\\\\")
	// Period-only escaping: only the first character of each line.
	// For our use (single-line summaries), we just protect a leading
	// period if any.
	if strings.HasPrefix(s, ".") {
		s = "\\&" + s
	}
	return s
}

// suggestCommand returns the closest cliCommand.Name to query by
// Levenshtein distance, but only when exactly one candidate is at
// the minimum distance AND that distance is ≤2. Ties and distances
// above the threshold return ("", false) so the caller falls
// through to a plain "unknown command" message — ambiguous
// suggestions are worse than none (a user might pick the wrong
// one of two equally-close candidates). The threshold of 2 covers
// the common typos (`aps`, `appss`, `appsss`) without suggesting
// across unrelated commands (`apps` → `mfa` is distance 4).
func suggestCommand(query string) (string, bool) {
	const maxDist = 2
	bestName := ""
	bestDist := maxDist + 1
	tied := false
	for _, c := range cliCommands {
		d := levenshtein(query, c.Name)
		switch {
		case d < bestDist:
			bestName, bestDist, tied = c.Name, d, false
		case d == bestDist:
			// Tie: another command at the same distance makes the
			// suggestion ambiguous, so we suppress it.
			tied = true
		}
	}
	if tied || bestDist > maxDist || bestName == "" {
		return "", false
	}
	return bestName, true
}

// suggestSubcommand is the per-top-level twin of suggestCommand:
// walks c.Subcommands and returns the closest-by-Levenshtein name
// when exactly one candidate is at the minimum distance ≤2. Ties
// and over-threshold distances return ("", false) so the caller
// falls through to a plain "unknown subcommand" message — same
// ambiguity policy as suggestCommand. The threshold covers the
// common typos (alerts `lst`/`lsit`, registry `st`) without
// suggesting across unrelated verbs (registry `set` is distance
// 3 from `rm`).
func suggestSubcommand(query string, c cliCommand) (string, bool) {
	const maxDist = 2
	bestName := ""
	bestDist := maxDist + 1
	tied := false
	for _, s := range c.Subcommands {
		d := levenshtein(query, s.Name)
		switch {
		case d < bestDist:
			bestName, bestDist, tied = s.Name, d, false
		case d == bestDist:
			// Tie: another subcommand at the same distance makes
			// the suggestion ambiguous, so we suppress it.
			tied = true
		}
	}
	if tied || bestDist > maxDist || bestName == "" {
		return "", false
	}
	return bestName, true
}

// maybeSuggestSub writes a one-line "Did you mean X?" hint to
// stderr when sug is non-empty. Mirrors the wording in cmdMan
// (man.go:60-62) so the failure surface is uniform across every
// top-level dispatcher. No-op when sug == "" (ambiguous or
// over-threshold — see suggestSubcommand).
func maybeSuggestSub(sug string) {
	if sug == "" {
		return
	}
	_, _ = fmt.Fprintf(os.Stderr, "  Did you mean %q?\n", sug)
}

// levenshtein computes the edit distance between a and b using
// the classic dynamic-programming algorithm: O(len(a)*len(b))
// time and space. We use a single-row rolling buffer (size
// len(b)+1) to keep allocations small — the caller is the man
// dispatcher, which fires at most once per `gregale man` invocation,
// so the work is tiny. No third-party dependency: a real dep
// would be 5x the code and one more thing to vet.
func levenshtein(a, b string) int {
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	// prev is the row for i-1; cur is the row for i. We allocate
	// once and reuse across iterations.
	prev := make([]int, lb+1)
	cur := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		cur[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			del := prev[j] + 1
			ins := cur[j-1] + 1
			sub := prev[j-1] + cost
			cur[j] = min3(del, ins, sub)
		}
		prev, cur = cur, prev
	}
	return prev[lb]
}

// min3 returns the smallest of three ints. Inlined because Go 1.23
// doesn't have a stdlib helper, and the function is hot (one call
// per cell of the DP table).
func min3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}

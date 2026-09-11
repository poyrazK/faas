// commands/completion.go — Tier A8 / ADR-083.
//
// Dispatcher for `gregale completion <shell>`. Mirrors cmdAlerts
// (commands_alerts.go:40-61) for the dispatch shape: single arg,
// switch over the recognised shell names, error on unknown.
//
// The actual script rendering lives in four sibling files:
//
//   - completion_bash.go        — bash 3.2+ compatible (uses compgen)
//   - completion_zsh.go         — zsh with #compdef + _arguments
//   - completion_fish.go        — fish complete -c registrations
//   - completion_powershell.go  — powershell Register-ArgumentCompleter
//
// All four are pure-string emitters driven by the cliCommands
// manifest (cli_meta.go). Adding a new command requires NO touch
// on these files — the new cliCommand entry in cli_meta.go shows
// up in every backend at compile time.
//
// Slug completion for the per-account positional paths (e.g.
// `<slug>` in `gregale app <slug> ...`) is sourced from the
// completion cache file written by the c.do middleware on every
// successful list response (pkg/api/completion_cache.go). The
// path is computed lazily inside CompletionCache.Path(); the
// script embeds a runtime expression that calls back into the
// binary for the cache reader so the install path stays static.

package main

import (
	"fmt"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
)

const completionDocsTopic = "completion"

// cmdCompletion dispatches `gregale completion <shell>` to one of
// the four per-shell renderers. Mirrors cmdAlerts for shape.
//
// Two hidden subcommands are also dispatched:
//
//   - completion-cache-path: prints the absolute path of the
//     completion cache file (read by every shell's completion
//     function at TAB time).
//   - completion-cache-list <kind>: prints the cached slugs of
//     <kind> ("apps" or "orgs"), one per line, to stdout.
//
// These two are internal — they don't show in the usage block
// and never take --json. Operators don't call them by hand; the
// shell functions invoke them at TAB time. Surfacing them via
// the dispatch switch keeps the helpers in the same binary so
// the install footprint stays "one binary, no extra scripts".
func cmdCompletion(args []string) int {
	parent, _ := lookupCliCommand("completion")
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale completion <bash|zsh|fish|powershell>", completionDocsTopic)
		return 1
	}
	switch args[0] {
	case "bash":
		return cmdCompletionBash()
	case "zsh":
		return cmdCompletionZsh()
	case "fish":
		return cmdCompletionFish()
	case "powershell":
		return cmdCompletionPowershell()
	case "completion-cache-path":
		_, _ = fmt.Fprintln(osStdout, cachePathForScripts())
		return 0
	case "completion-cache-list":
		if len(args) < 2 {
			_, _ = fmt.Fprintln(os.Stderr, "gregale completion completion-cache-list: missing <kind>")
			return 1
		}
		return cmdCompletionCacheList(args[1])
	}
	_, _ = fmt.Fprintf(os.Stderr, "gregale completion: unknown subcommand %q (want bash|zsh|fish|powershell)\n", args[0])
	sug, _ := suggestSubcommand(args[0], parent)
	maybeSuggestSub(sug)
	return 1
}

// cmdCompletionCacheList prints the cached slugs of kind one per
// line to stdout. Empty cache (or any error) prints nothing —
// completion scripts must never see "no slugs" as a non-zero exit
// because the caller offers "no completions" via a missing cache
// file or an empty stream.
//
// The operator binary has no apps/orgs surface, so this is a
// no-op stub: `kind` is ignored and no slugs are emitted. The
// completion generators in completion_{bash,zsh,fish,powershell}.go
// reference cmdCompletionCacheList for symmetry with the customer
// binary's CLI surface; the operator-side scripts always have an
// empty cache, so the call short-circuits to 0 with no output.
func cmdCompletionCacheList(kind string) int {
	return 0
}

// cachePathForScripts returns the absolute path the completion
// scripts should read for the slug cache. The path matches the
// one CompletionCache writes to; computed via NewCompletionCache
// so a SetPath override (tests, env var) propagates here.
//
// The completion scripts embed a shell expression that invokes
// `gregale completion completion-cache-path` to recover this same path at
// TAB time. This avoids embedding the UserConfigDir computation
// in four different shell dialects.
func cachePathForScripts() string {
	c := api.NewCompletionCache()
	return c.Path()
}

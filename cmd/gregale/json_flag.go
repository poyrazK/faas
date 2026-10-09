package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// jsonOutput is set by run() from --json or FAAS_JSON=1; read by
// every command's printer (and by printErr) to switch from human
// to machine rendering. Default false so the human output is
// unchanged when neither signal is present.
//
// Issue #64 D1: every command accepts --json; scripts and agents
// depend on the stable shape (UX §3.2 "agents depend on it").
//
// Deliberately non-JSON commands (the rationale lives here; the
// audit allow-list mirror in nonJSONAllowList in json_parity_test.go
// must move with these). When a new command legitimately emits
// no body, add it here AND to nonJSONAllowList — the audit test
// fails CI otherwise.
//
//   - cmdLogin (commands.go)             — interactive paste-code prompt
//   - cmdLogout (commands.go)            — emits a small status object
//   - cmdInit / runCmdInit* (commands_init.go) — successful scaffolding
//     emits a machine-readable receipt; --list has a machine-readable
//     template slice. The optional --deploy composite emits one receipt.
//   - cmdBackup / cmdBackupUnsealRclone  — operator fs writes; no body
//   - cmdMfa enroll                      — QR PNG is written to disk
//     (JSON shape is the path)
//   - cmdMfa (confirm/verify/recover/disable) — write-only no-body
//   - cmdHostAge init/rotate/status/prune — operator fs writes
//   - cmdPKI init/status/rotate          — operator fs writes
//   - cmdSignKeys init/rotate/status     — operator fs writes
//   - cmdTrustedPublishers add/remove/list — operator fs writes
//   - cmdOverageCap (set/clear)          — side-effect only
//   - cmdApp --concurrency fast path     — explicitly rejects --json
//     (commands2.go)
var jsonOutput bool

// newFlagSet gives every leaf parser the same machine-readable failure path.
// flag.FlagSet writes parse errors before returning them, so callers cannot
// reliably translate those errors after Parse. This writer emits the first
// diagnostic as one Problem object and discards FlagSet's follow-on usage
// fragments; human mode retains the standard flag package output.
func newFlagSet(name string, handling flag.ErrorHandling) *flag.FlagSet {
	fs := flag.NewFlagSet(name, handling)
	setFlagOutput(fs, os.Stderr)
	return fs
}

// parseInterspersed parses args with flags allowed before, between, and after
// positionals; the standard flag package stops at the first positional, so
// `gregale x <id> --flag v` would otherwise be a usage error. Whether a flag
// takes a value comes from fs itself, so a bool flag never swallows the
// positional that follows it. Positionals keep their order and are returned
// through fs.Args(); everything after a literal "--" stays positional.
func parseInterspersed(fs *flag.FlagSet, args []string) error {
	flags := make([]string, 0, len(args))
	positionals := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i] //nolint:gosec // G602: i starts at zero and the loop condition bounds it by len(args).
		if arg == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if len(arg) < 2 || arg[0] != '-' {
			positionals = append(positionals, arg)
			continue
		}
		flags = append(flags, arg)
		name := strings.TrimLeft(arg, "-")
		if strings.Contains(name, "=") {
			continue
		}
		f := fs.Lookup(name)
		if f == nil {
			continue
		}
		if b, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && b.IsBoolFlag() {
			continue
		}
		if i+1 < len(args) {
			flags = append(flags, args[i+1])
			i++
		}
	}
	return fs.Parse(append(append(flags, "--"), positionals...))
}

// rejectUnexpectedFlagArgs closes the standard flag package's permissive
// trailing-token behavior for flag-only leaves. Call it immediately after a
// successful Parse and before authentication or any API request.
func rejectUnexpectedFlagArgs(fs *flag.FlagSet) bool {
	if fs.NArg() == 0 {
		return false
	}
	topic := fs.Name()
	if fields := strings.Fields(topic); len(fields) > 0 {
		topic = fields[0]
	}
	printUsage(osStderr, "usage: gregale "+fs.Name()+" [flags]; unexpected positional argument(s): "+strings.Join(fs.Args(), " "), topic)
	return true
}

func setFlagOutput(fs *flag.FlagSet, human io.Writer) {
	if jsonOutput && !jsonUsageHelp {
		fs.SetOutput(&jsonFlagErrorWriter{name: fs.Name(), dst: human})
		return
	}
	fs.SetOutput(&humanFlagErrorWriter{name: fs.Name(), dst: human})
}

// invokedCommandPath is the public command path of the current run(), for
// example "invocations list". FlagSet names are internal ("usage-list",
// "jobs-list"), so flag errors must not print them as a command to run.
var invokedCommandPath string

// publicCommandPath resolves the manifest command and verb the user typed.
func publicCommandPath(args []string) string {
	if len(args) == 0 {
		return ""
	}
	command, ok := lookupCliCommand(args[0])
	if !ok {
		return args[0]
	}
	path := command.Name
	if command.SubcommandsAfterPositionals || len(args) < 2 {
		return path
	}
	sub, ok := findCliSubcommand(command.Subcommands, args[1])
	if !ok {
		return path
	}
	path += " " + sub.Name
	if len(args) > 2 {
		if leaf, ok := findCliSubcommand(sub.Subcommands, args[2]); ok {
			path += " " + leaf.Name
		}
	}
	return path
}

// flagDiagnosticDash matches the single-dash flag names the flag package
// prints ("-app"); the CLI documents and accepts double-dash flags.
var flagDiagnosticDash = regexp.MustCompile(`(^|flag |for |defined: |argument: )-([A-Za-z0-9])`)

// normalizeFlagDiagnostic rewrites a flag package parse error in the CLI's
// own spelling, e.g. "flag provided but not defined: -app" becomes
// "unknown flag --app".
func normalizeFlagDiagnostic(message string) string {
	message = flagDiagnosticDash.ReplaceAllString(message, "${1}--${2}")
	if rest, ok := strings.CutPrefix(message, "flag provided but not defined: "); ok {
		return "unknown flag " + rest
	}
	if rest, ok := strings.CutPrefix(message, "flag needs an argument: "); ok {
		return "flag " + rest + " needs a value"
	}
	return message
}

// humanFlagErrorWriter turns a flag parse failure into one diagnostic plus
// a pointer to the command's help. production-us hunt #4: 30 leaves dumped
// the flag package's raw output instead, with single-dash flag names, every
// default, and internal FlagSet names ("Usage of usage-list:"). Usage text a
// leaf prints itself still passes through; only the generic "Usage of"
// header and PrintDefaults lines are dropped.
type humanFlagErrorWriter struct {
	name  string
	dst   io.Writer
	wrote bool
}

func (w *humanFlagErrorWriter) Write(p []byte) (int, error) {
	text := string(p)
	if strings.TrimSpace(text) == "" {
		return len(p), nil
	}
	if !w.wrote {
		w.wrote = true
		path := invokedCommandPath
		if path == "" {
			if fields := strings.Fields(w.name); len(fields) > 0 {
				path = fields[0]
			}
		}
		_, err := fmt.Fprintf(w.dst, "Invalid command flags\n  %s\n  run 'gregale %s --help' for usage\n",
			normalizeFlagDiagnostic(strings.TrimSpace(text)), path)
		return len(p), err
	}
	if strings.HasPrefix(text, "Usage of ") || strings.HasPrefix(text, "  -") {
		return len(p), nil
	}
	_, err := io.WriteString(w.dst, text)
	return len(p), err
}

type jsonFlagErrorWriter struct {
	name  string
	dst   io.Writer
	wrote bool
}

func (w *jsonFlagErrorWriter) Write(p []byte) (int, error) {
	if w.wrote || strings.TrimSpace(string(p)) == "" {
		return len(p), nil
	}
	w.wrote = true
	err := writeJSONProblemWithExit(w.dst, api.Problem{
		Type:    docsSiteURL + "/errors/invalid-request",
		Title:   "Invalid command flags",
		Status:  400,
		Code:    api.CodeValidation,
		Detail:  normalizeFlagDiagnostic(strings.TrimSpace(string(p))),
		DocsURL: cliDocsURL,
	}, 1)
	return len(p), err
}

// applyJSONFlag consumes --json (or -j / --json=BOOL) before "--" from
// args and sets jsonOutput. Honors FAAS_JSON first, then the persistent
// non-secret config preference, unless --json=false is explicit on the
// command line. Returns the args with the flag
// stripped so downstream dispatch sees only its own flags. If the flag is
// repeated, the last explicit value wins. Idempotent on a second call — safe
// if a subcommand happens to call it.
//
// Recognised boolean spellings (case-insensitive):
//
//	true / yes / on / 1   → enable JSON
//	false / no / off / 0  → disable JSON
//	empty                 → enable JSON (same as bare --json)
//
// run validates the explicit suffix before calling this helper so typos
// cannot silently change output mode. The permissive fallback remains for
// persisted preferences that may have been written by older CLI versions.
func applyJSONFlag(args []string) []string {
	if configured, ok := configuredJSONPreference(); ok {
		jsonOutput = configured
	}
	filtered := make([]string, 0, len(args))
	for i, a := range args {
		if a == "--" {
			filtered = append(filtered, args[i:]...)
			break
		}
		switch {
		case a == "--json" || a == "-j":
			jsonOutput = true
		case strings.HasPrefix(a, "--json="):
			jsonOutput = jsonBoolTrue(a[len("--json="):])
		default:
			filtered = append(filtered, a)
		}
	}
	return filtered
}

func invalidJSONFlagValue(args []string) string {
	invalid := ""
	for _, arg := range args {
		if arg == "--" {
			break
		}
		if !strings.HasPrefix(arg, "--json=") {
			continue
		}
		value := strings.ToLower(strings.TrimPrefix(arg, "--json="))
		switch value {
		case "", requireSignedTrue, "yes", "on", "1", requireSignedFalse, "no", "off", "0":
		default:
			if invalid == "" {
				invalid = value
			}
		}
	}
	return invalid
}

// jsonBoolTrue maps a --json= suffix to a boolean. Falsy spellings
// (false / no / off / 0) disable JSON; everything else (including
// typos and the empty string) enables it. Case-insensitive.
//
// The literal tokens are referenced via the requireSignedTrue /
// requireSignedFalse consts declared in commands_app_security.go:56-59
// (the same ones parseSecretScanFlag in pack.go uses) so goconst
// (golangci-lint v2.4.0) counts the closed-enum literals once across
// the binary instead of separately per switch.
func jsonBoolTrue(s string) bool {
	switch strings.ToLower(s) {
	case requireSignedFalse, "no", "off", "0":
		return false
	}
	return true
}

// writeJSON emits v as one indented JSON object on osStdout. Use
// for scalar DTOs (api.AppResponse, api.AccountResponse, etc.).
// The DTO's JSON tags in pkg/api/dto.go are the single source of
// truth — no risk of drift between the human pretty-printer and
// the wire shape.
func writeJSON(v any) error {
	enc := json.NewEncoder(osStdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// writeJSONTo is the writer-parametrized companion used by commands
// whose pure helpers already accept an output stream. Keeping the
// encoder policy here prevents those commands from silently falling
// back to human output under --json.
func writeJSONTo(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// writeNDJSON emits one JSON object per line on osStdout. Use for
// slices (`[]api.AppResponse`, `[]api.InstanceResponse`, etc.).
// NDJSON is `jq -c '.'`-friendly and streams — a single array at
// end would require buffering the whole response and force scripts
// to know the slice shape (UX §3.2).
func writeNDJSON[T any](items []T) error {
	enc := json.NewEncoder(osStdout)
	for _, it := range items {
		if err := enc.Encode(it); err != nil {
			return err
		}
	}
	return nil
}

// writeJSONProblem marshals p as one JSON line on stderr. printErr
// calls this when jsonOutput is set so `jq .code` works directly
// against the error stream. The single line shape matches the
// RFC 7807 body the server already emits — we don't re-shape it.
func writeJSONProblem(p api.Problem) error {
	return writeJSONProblemTo(os.Stderr, p)
}

func writeJSONProblemTo(w io.Writer, p api.Problem) error {
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

// Client error metadata supplements the server's stable Problem code.
func writeJSONProblemWithExit(w io.Writer, p api.Problem, code int) error {
	b, err := json.Marshal(struct {
		api.Problem
		Category string `json:"category"`
		ExitCode int    `json:"exit_code"`
	}{p, exitCategory(code), code})
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

// resetJSONOutput is for tests only. Production code never calls it.
func resetJSONOutput() { jsonOutput = false }

// jsonOut converts an encoder error from writeJSON / writeNDJSON into
// a printErr call so every JSON branch has the same exit-code mapping.
// Returns 0 on nil (success); otherwise the printErr exit code.
func jsonOut(err error) int {
	if err == nil {
		return 0
	}
	return printErr("JSON encode failed", err)
}

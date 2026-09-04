package main

import (
	"encoding/json"
	"io"
	"os"
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
//   - cmdInit / runCmdInit* (commands_init.go) — file writes and the
//     --deploy composite retain progress-oriented output; --list has a
//     machine-readable template slice
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

// applyJSONFlag consumes a leading --json (or -j / --json=BOOL) from
// args and sets jsonOutput. Honors FAAS_JSON=1 env unless --json=false
// is explicit on the command line. Returns the args with the flag
// stripped so downstream dispatch sees only its own flags. Idempotent
// on a second call — safe if a subcommand happens to call it.
//
// Recognised boolean spellings (case-insensitive):
//
//	true / yes / on / 1   → enable JSON
//	false / no / off / 0  → disable JSON
//	empty                 → enable JSON (same as bare --json)
//
// Anything else (e.g. an agent typo'd `tru`) defaults to enable,
// matching the previous behaviour and the UX §3.2 "agents depend on
// it" intent: every obvious truthy spelling Just Works.
func applyJSONFlag(args []string) []string {
	if os.Getenv("FAAS_JSON") == "1" {
		jsonOutput = true
	}
	for i, a := range args {
		switch {
		case a == "--json" || a == "-j":
			jsonOutput = true
			return append(args[:i], args[i+1:]...)
		case strings.HasPrefix(a, "--json="):
			jsonOutput = jsonBoolTrue(a[len("--json="):])
			return append(args[:i], args[i+1:]...)
		}
	}
	return args
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
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = os.Stderr.Write(append(b, '\n'))
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

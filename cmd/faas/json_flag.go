package main

import (
	"encoding/json"
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
var jsonOutput bool

// applyJSONFlag consumes a leading --json (or -j / --json=BOOL) from
// args and sets jsonOutput. Honors FAAS_JSON=1 env unless --json=false
// is explicit on the command line. Returns the args with the flag
// stripped so downstream dispatch sees only its own flags. Idempotent
// on a second call — safe if a subcommand happens to call it.
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
			jsonOutput = a[len("--json="):] != "false"
			return append(args[:i], args[i+1:]...)
		}
	}
	return args
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

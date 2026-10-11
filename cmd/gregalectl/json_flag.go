// json_flag.go — operator-side --json flag wiring (issue #911 / ADR-110 PR-6.5).
//
// Mirrors cmd/gregale/json_flag.go's top portion (jsonOutput var +
// applyJSONFlag). The operator binary uses jsonEnabled() from
// commands_manifest.go:211 to gate --json output for `gregalectl release
// bundle|install` and `gregalectl manifest validate|render`. The
// customer-side writeJSON / writeNDJSON / writeJSONProblem / jsonOut
// helpers are NOT duplicated here because the operator binary doesn't
// emit pkg/api DTO shapes — operator reports are operator-local
// structs (releaseBundleReport, releaseInstallReport) marshalled via
// jsonEmit in commands_manifest.go:197.
//
// PR-7 may extract a shared cmd/internal/cliutil/json package; for
// PR-6.5 duplication is intentional.
package main

import (
	"os"
	"strings"
)

// jsonOutput is set by run() from --json or FAAS_JSON=1; read by
// jsonEnabled() in commands_manifest.go. Mirrors cmd/gregale/json_flag.go:41.
var jsonOutput bool

// applyJSONFlag consumes a leading --json (or -j / --json=BOOL) from
// args and sets jsonOutput. Honors FAAS_JSON=1 env unless --json=false
// is explicit on the command line. Returns the args with the flag
// stripped so downstream dispatch sees only its own flags. Idempotent
// on a second call. Mirrors cmd/gregale/json_flag.go:58.
func applyJSONFlag(args []string) []string {
	if os.Getenv("FAAS_JSON") == "1" {
		jsonOutput = true
	}
	for i, a := range args {
		if a == "--" {
			break
		}
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

// jsonBoolTrue maps a --json= suffix to a boolean. Mirrors
// cmd/gregale/json_flag.go:84. Operator commands don't use the
// requireSignedTrue/requireSignedFalse closed enum, so this is a
// simplified version.
func jsonBoolTrue(s string) bool {
	switch strings.ToLower(s) {
	case "false", "no", "off", "0":
		return false
	}
	return true
}

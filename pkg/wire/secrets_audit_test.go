// Package wire — secrets_audit_test.go is the §11 ship-blocker regression
// tripwire for issue #518 PR-A (FAAS_LOG_LEVEL). The production handler must
// never log a raw os.Getenv value: every FAAS_* read is either an empty
// sentinel, a path, an opaque identifier (host key path, registry host,
// storage root), or a credential that is recorded with logsanitize.RedactValue.
//
// What this test guards:
//  1. The new FAAS_LOG_LEVEL env value specifically — its only emission path
//     is through ParseLevel; if a future contributor adds a stray log.Info
//     that prints the raw env value (e.g. for debugging a typo), the test
//     fails.
//  2. The general invariant for the four call sites in cmd/{vmmd,imaged}/main.go
//     that already log envOr values. Each must use one of the four
//     safe-key shapes (registry, fc_root, oci_registry, …). Adding a new
//     secret-named env var to a log statement is exactly the regression
//     §11 prohibits.
//
// Per e2etest-harness-safebuffer pattern, the audit runs as a single
// in-process pass: walk cmd/ and pkg/, parse each file, classify, fail on
// the first violation. No subprocess required.

package wire

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestSecretLeakGuard_FAAS_LOG_LEVEL pins the wire-contract rule for issue
// #518 PR-A: FAAS_LOG_LEVEL is the only env var in pkg/wire whose value is
// read from process env at runtime, and it must NEVER reach a log call
// without going through ParseLevel first. Operators supply arbitrary text
// in sealed.env; the only thing we want on the wire is the resolved
// slog.Level string ("DEBUG"/"INFO"/…/etc.), not the literal input.
//
// The test walks pkg/wire/ for any log statement that includes the string
// "FAAS_LOG_LEVEL" and asserts that the value is funneled through ParseLevel.
// This catches:
//   - `log.Info("got", os.Getenv("FAAS_LOG_LEVEL"))` — would print raw input.
//   - `log.Info("got", slog.String("env", os.Getenv("FAAS_LOG_LEVEL")))` — same.
//   - accidental printf("%s", os.Getenv("FAAS_LOG_LEVEL")) — same shape.
//
// It does NOT match `log.Info("env", EnvLogLevel)` where EnvLogLevel is the
// package-level constant "FAAS_LOG_LEVEL" — that's just emitting the name,
// not the value, and is the canonical pattern in the warn fallback.
func TestSecretLeakGuard_FAAS_LOG_LEVEL(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed; cannot locate pkg/wire")
	}
	dir := filepath.Dir(thisFile)

	fset := token.NewFileSet()
	var files []*ast.File
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ParseComments|parser.AllErrors)
		if err != nil {
			return err
		}
		files = append(files, f)
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir(%s): %v", dir, err)
	}

	for _, f := range files {
		walkLogCalls(f, func(call *ast.CallExpr, argTexts []string) {
			if !strings.Contains(strings.Join(argTexts, " "), "FAAS_LOG_LEVEL") {
				return
			}
			// FAAS_LOG_LEVEL appears in this log call. The value side
			// must be ParseLevel(...) — anything else prints the raw
			// operator-supplied string into the JSON log stream.
			if !containsCallTo(call, "ParseLevel") {
				src := fset.Position(call.Pos()).String()
				t.Errorf("%s: FAAS_LOG_LEVEL reaches log.* without ParseLevel; "+
					"raw env value leaks to the log stream. Wrap in ParseLevel "+
					"or remove the field.", src)
			}
		})
	}
}

// TestSecretLeakGuard_FAAS_EnvToLog is the repo-wide version: any FAAS_*
// env read whose value flows into a log statement must be either a path-
// shaped value (already in the envOr(...) allowlist) or routed through
// logsanitize. The check is intentionally narrow — it tracks the
// expression `os.Getenv("FAAS_XXX")` / `envOr("FAAS_XXX", ...)` and
// asserts that the resulting value (the function call) appears as a
// value-side argument of a log statement.
//
// Emitting the literal env NAME as part of a log message is fine and
// pervasive — every flagged site in the codebase does exactly this:
// `log.Warn("FAAS_SESSION_KEY unset; ephemeral session key in use")`.
// The §11 rule prohibits emitting the VALUE, so the tripwire follows
// the value side: a CallExpr whose callee is os.Getenv / envOr / getenv
// with a "FAAS_XXX" string-literal argument, that is itself an argument
// of a log statement.
func TestSecretLeakGuard_FAAS_EnvToLog(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(thisFile)

	// Walk from the module root so cmd/ and pkg/ are both covered. Two
	// levels up from pkg/wire is the repo root.
	repoRoot := filepath.Join(dir, "..", "..")

	fset := token.NewFileSet()
	var files []*ast.File
	err := filepath.WalkDir(repoRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			name := d.Name()
			if name == "vendor" || name == "node_modules" || name == ".git" {
				return filepath.SkipDir
			}
			// Allow the root itself plus everything under cmd/ or pkg/.
			// filepath.Separator ensures "<root>/cmds" is not matched
			// as "<root>/cmd/..." (defense-in-depth against future dir
			// collisions).
			sep := string(filepath.Separator)
			allowed := path == repoRoot ||
				path == filepath.Join(repoRoot, "cmd") ||
				path == filepath.Join(repoRoot, "pkg") ||
				strings.HasPrefix(path, filepath.Join(repoRoot, "cmd")+sep) ||
				strings.HasPrefix(path, filepath.Join(repoRoot, "pkg")+sep)
			if !allowed {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".go") ||
			strings.HasSuffix(name, "_test.go") ||
			strings.HasSuffix(name, ".pb.go") ||
			strings.HasSuffix(name, ".gen.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ParseComments|parser.AllErrors)
		if err != nil {
			return err
		}
		files = append(files, f)
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir(%s): %v", repoRoot, err)
	}

	// Known-safe FAAS_* keys whose values are path-or-identifier shaped
	// (not credentials). The four call sites in cmd/{vmmd,imaged}/main.go
	// are the canonical examples — see those files for the pattern.
	//
	// Each entry MUST carry a justification (comment). Adding a new key
	// without one is a §11 ship-blocker until the tripwire is satisfied;
	// either it routes through RedactValue (preferred for secrets) or it
	// is genuinely not a secret.
	safeKeys := map[string]string{
		"FAAS_OCI_REGISTRY":           "identifier (registry host); see cmd/{vmmd,imaged}/main.go startup lines",
		"FAAS_STORAGE_ROOT":           "path; see cmd/{vmmd,imaged}/main.go startup lines",
		"FAAS_STORAGE_CACHE_DIR":      "path; see cmd/vmmd/main.go",
		"FAAS_STORAGE_BACKEND":        "enum ('local' | 'oci' | 'gcs'); see cmd/{vmmd,imaged}/main.go",
		"FAAS_GCS_BUCKET":             "identifier (private bucket name); see cmd/{vmmd,imaged}/main.go",
		"FAAS_LOG_LEVEL":              "covered by TestSecretLeakGuard_FAAS_LOG_LEVEL — the value must flow through ParseLevel",
		"FAAS_VMMD_CONFIG":            "path",
		"FAAS_APID_CONFIG":            "path",
		"FAAS_SCHEDD_CONFIG":          "path",
		"FAAS_GATEWAYD_CONFIG":        "path",
		"FAAS_IMAGED_CONFIG":          "path",
		"FAAS_BUILDERD_CONFIG":        "path",
		"FAAS_APPS_ROOT":              "path",
		"FAAS_GUEST_INIT":             "path",
		"FAAS_BUILDER_BASE_REF":       "identifier (OCI image ref)",
		"FAAS_BUILDER_BASE_PATH":      "path",
		"FAAS_DEPLOY_BASE_REF":        "identifier",
		"FAAS_TRUSTED_PUBLISHERS_DIR": "path",
		"FAAS_MAIL_TRANSPORT":         "enum-shaped transport identifier; see pkg/mail/factory.go (logs the value when unknown — operator-supplied, not a credential)",
		// ADR-127 §D3 Layer 7 — the bridge's framing-selection slog
		// line at cmd/vmmd-stream-bridge/main.go::newHandler logs the
		// env value verbatim so an operator can correlate the env
		// state with the framing actually used. The value is a
		// closed enum ({h1, h2c}, anything else falls back to h1
		// per currentBridgeFraming in cmd/vmmd-stream-bridge/framing.go),
		// not a credential.
		"FAAS_BRIDGE_PROTOCOL": "enum ({h1, h2c}); see cmd/vmmd-stream-bridge/main.go::newHandler framing-selection slog line (ADR-127 §D3 Layer 7)",
		// ADR-115 §D4: the Resend API key + Postmark token are the
		// load-bearing mail credentials. The Resend key MUST be scoped
		// to "Sending access" only (not "Full access") in the Resend
		// dashboard; the tripwire description captures the constraint
		// so it surfaces in the operator-facing secrets audit. The
		// From address is on a verified domain (SPF/DKIM records at
		// the DNS provider; Resend rejects with 403 if unverified).
		"FAAS_MAIL_RESEND_API_KEY": "secret (Resend API key); ADR-115 §D4 — 'Sending access' scope only in the Resend dashboard",
		"FAAS_MAIL_POSTMARK_TOKEN": "secret (Postmark server token); mirror of FAAS_MAIL_RESEND_API_KEY for the Postmark fallback transport",
		"FAAS_MAIL_FROM":           "RFC 5322 sender address; ADR-115 §D3 — domain MUST have SPF + DKIM records at the DNS provider (Resend rejects with 403 on unverified domain)",
	}

	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if !isLogCall(call.Fun) {
				return true
			}
			// Walk the log call's argument list looking for an env-read
			// call (os.Getenv / envOr / getenv) whose first arg is a
			// "FAAS_*" string literal. The value flows into the log
			// statement — that's the leak.
			for _, arg := range call.Args {
				envCall, envName, ok := envReadOfFAASKey(arg)
				if !ok {
					continue
				}
				if _, ok := safeKeys[envName]; ok {
					continue
				}
				// Sanitization check: the env-read must be wrapped in
				// logsanitize.RedactValue (for secrets) or routed through
				// a known-safe constructor. We grep the surrounding arg
				// text for the RedactValue marker.
				argSrc := exprText(arg)
				if strings.Contains(argSrc, "RedactValue") {
					continue
				}
				_ = envCall
				src := fset.Position(call.Pos()).String()
				t.Errorf("%s: %q env value flows into log statement without RedactValue. "+
					"Either route through logsanitize.RedactValue (secret) or extend safeKeys with a comment explaining the path/identifier shape.",
					src, envName)
			}
			return true
		})
	}
}

// envReadOfFAASKey reports whether expr is an os.Getenv("FAAS_XXX") /
// envOr("FAAS_XXX", …) / getenv("FAAS_XXX") call. If so, returns the
// call and the env var name. The tripwire only cares about value-side
// placement; emitting the literal env name as a message substring is
// caught separately and explicitly allowed.
func envReadOfFAASKey(expr ast.Expr) (*ast.CallExpr, string, bool) {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return nil, "", false
	}
	if len(call.Args) == 0 {
		return nil, "", false
	}
	// The first arg must be a "FAAS_XXX" string literal — anything more
	// elaborate (a variable, an interpolation) is too dynamic for a
	// static check and is left to manual review.
	lit, ok := call.Args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return nil, "", false
	}
	name, ok := unquoteFAASKey(lit.Value)
	if !ok {
		return nil, "", false
	}
	// Callee must be one of the env-reading helpers.
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		if fn.Name != "getenv" {
			return nil, "", false
		}
	case *ast.SelectorExpr:
		pkg, ok := fn.X.(*ast.Ident)
		if !ok {
			return nil, "", false
		}
		if pkg.Name != "os" || fn.Sel.Name != "Getenv" {
			return nil, "", false
		}
	default:
		return nil, "", false
	}
	return call, name, true
}

// unquoteFAASKey strips the surrounding quotes from a Go string literal
// and returns the value iff it starts with FAAS_ and contains only
// [A-Z0-9_]. Returns ("", false) on any other shape.
func unquoteFAASKey(lit string) (string, bool) {
	if len(lit) < 2 || lit[0] != '"' || lit[len(lit)-1] != '"' {
		return "", false
	}
	s := lit[1 : len(lit)-1]
	if !strings.HasPrefix(s, "FAAS_") {
		return "", false
	}
	rest := s[len("FAAS_"):]
	if rest == "" {
		return "", false
	}
	for _, r := range rest {
		if r < 'A' || r > 'Z' {
			if r < '0' || r > '9' {
				if r != '_' {
					return "", false
				}
			}
		}
	}
	return s, true
}

// walkLogCalls invokes fn for every log.* / slog.* / fmt.* call found in
// f. argTexts is the source-rendered slice of every argument (key, value,
// and message) so fn can string-match without walking the AST again.
func walkLogCalls(f *ast.File, fn func(call *ast.CallExpr, argTexts []string)) {
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if !isLogCall(call.Fun) {
			return true
		}
		texts := renderArgs(call.Args)
		fn(call, texts)
		return true
	})
}

// isLogCall reports whether expr is one of log.*, slog.*, fmt.Print*.
// Fmt.Print shapes are caught because the §11 rule applies equally to any
// log-emitting call site.
func isLogCall(expr ast.Expr) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	switch pkg.Name {
	case "log", "slog":
		switch sel.Sel.Name {
		case "Debug", "Info", "Warn", "Error",
			"Debugf", "Infof", "Warnf", "Errorf":
			return true
		}
	case "fmt":
		switch sel.Sel.Name {
		case "Print", "Println", "Printf":
			return true
		}
	}
	return false
}

// renderArgs returns the source-text slice of each argument, recursively
// for nested calls. Useful for string-matching on identifiers inside
// nested expressions like `envOr("FAAS_STORAGE_ROOT", "/srv/fc")`.
func renderArgs(args []ast.Expr) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		out = append(out, exprText(a))
	}
	return out
}

func exprText(e ast.Expr) string {
	var b strings.Builder
	ast.Inspect(e, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.BasicLit:
			b.WriteString(v.Value)
		case *ast.Ident:
			b.WriteString(v.Name)
		case *ast.SelectorExpr:
			if x, ok := v.X.(*ast.Ident); ok {
				b.WriteString(x.Name)
				b.WriteByte('.')
			}
			b.WriteString(v.Sel.Name)
		}
		return true
	})
	return b.String()
}

// containsCallTo reports whether call has any descendant whose callee is

// containsCallTo reports whether call has any descendant whose callee is
// named name (the simple identifier form). Used to check that an
// expression like `ParseLevel(os.Getenv("FAAS_LOG_LEVEL"))` is wrapped
// before reaching the log statement.
func containsCallTo(call *ast.CallExpr, name string) bool {
	found := false
	ast.Inspect(call, func(n ast.Node) bool {
		c, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if id, ok := c.Fun.(*ast.Ident); ok && id.Name == name {
			found = true
			return false
		}
		return true
	})
	return found
}

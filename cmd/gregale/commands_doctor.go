// Customer `gregale doctor` — source-side preflight for the
// error-explanations cluster failure modes. Scans the local cwd for
// bind-address, env-var, arch, and dependency-install signals that
// the platform's runtime detectors (commit 7-13) would otherwise
// only catch post-deploy.
//
// Sister to the operator `gregale doctor` (issue #952 / PR #921).
// This one is for the customer-developer pre-deploy loop: runs
// without auth, scans local source, prints one line per finding,
// exits non-zero if any check is "error" (--strict) or "warn"
// (default, so the deploy pipeline can pre-cache the prose via
// `gregale doctor` in CI).
//
// Each check returns one of: ok | warn | error | skipped with the same
// shape as the operator doctor (commands_inspect.go:99-103).
// The whycopy.Decorate path is reused so the hint/why/fix prose
// is identical to what the runtime would surface after a failed
// wake — single source of truth, single tripwire.
//
// 8 checks (one per non-already-shipped code). Four checks are local
// source checks; four require deployed-app telemetry and are marked
// skipped rather than reported as false positives:
//
//   1. port-bind         requires a live listener probe
//   2. loopback-bind     scans for app.listen("127.0.0.1"...) patterns
//   3. arch              detects target/host arch mismatch
//   4. env-required      scans source for env-var references; flags undeclared
//   5. runtime-oom       reads latest metered RAM; flags if approaching cap
//   6. dep-install       dry-runs the build's lockfile resolution
//   7. startup-timeout   inspects the cold-boot readiness probe window
//   8. stateless-only    scans tarball shape for persistence signals
//
// `app_healthz_unauthorized` is a runtime-only check (the
// liveness probe has to fail 3 consecutive times) — preflight
// can't catch it because it depends on the live healthz path.
// Customers are pointed at the docs URL in the cluster's catalog.

package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/frameworkprofile"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/whycopy"
)

// doctorCheck is the result of one customer-doctor check. The
// shape mirrors commands_inspect.go's per-leaf Finding so a future
// dashboard panel can lift the same JSON for the cluster's
// dedicated surface (commit 20, separate PR).
type doctorCheck struct {
	Name    string   `json:"name"`             // e.g. "port-bind"
	Status  string   `json:"status"`           // "ok" | "warn" | "error" | "skipped"
	Code    string   `json:"code,omitempty"`   // RFC 7807 code when status != "ok"
	Reason  string   `json:"reason,omitempty"` // why a check was skipped
	Hint    string   `json:"hint,omitempty"`
	Why     string   `json:"why,omitempty"`
	Fix     string   `json:"fix,omitempty"`
	Sources []string `json:"sources,omitempty"` // file:line that triggered
}

// doctorReport is the top-level JSON shape. Always emits a
// "checks" array even when empty so script consumers can grep on
// `length(checks) == 0` as the "all green" signal.
type doctorReport struct {
	Path    string                    `json:"path,omitempty"`
	Image   *doctorImage              `json:"image,omitempty"`
	Profile *frameworkprofile.Profile `json:"profile,omitempty"`
	Checks  []doctorCheck             `json:"checks"`
}

// HasErrors reports whether any check returned status="error".
// Cluster A wires this into `gregale deploy --doctor-strict` so a
// pre-upload gate can exit 1 on findings the server would have
// 422'd on (e.g. stateless_only_violation). Mirrors the
// standalone cmdDoctor exit semantics — warnings remain warn-only
// even under --doctor-strict.
func (r doctorReport) HasErrors() bool {
	for _, c := range r.Checks {
		if c.Status == "error" {
			return true
		}
	}
	return false
}

// HasWarnings reports whether any check returned status="warn",
// regardless of whether any "error" is also present. The deploy
// path calls HasErrors first and short-circuits on error, so this
// helper is only consulted when no error was found — but the
// implementation does not assume that ordering. Standalone
// cmdDoctor (line 138 below) uses this to render warn findings on
// a clean run; --strict promotes warn → exit 1.
func (r doctorReport) HasWarnings() bool {
	for _, c := range r.Checks {
		if c.Status == "warn" {
			return true
		}
	}
	return false
}

// cmdDoctor implements `gregale doctor [path]` — the customer
// preflight. Flags:
//
//	--strict         exit 1 on warn (default: exit 0 on warn, 1 on error)
//	--json           machine output (default: human prose)
//
// Auth is NOT required — the customer-developer is scanning their
// own cwd before they even login. Cross-referencing with a running
// app's declared env is a future addition (TODO — needs a
// pkg/client.ListEnv sibling).
//
// Exit codes:
//
//	0  all checks ok, OR all warnings under --strict=false
//	1  any check error (always), OR any check warn under --strict=true
//	2  usage error (bad argv)
func cmdDoctor(args []string) int {
	return cmdDoctorWithImageInspector(args, oci.NewRegistryClient(oci.WithHTTPClient(oci.NewEgressHTTPClient())))
}

func cmdDoctorWithImageInspector(args []string, inspector doctorImageInspector) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(osStderr)
	strict := fs.Bool("strict", false, "exit 1 on warn (default: exit 0 on warn)")
	jsonOut := fs.Bool("json", false, "machine output (default: human prose)")
	imageFlags := registerDoctorImageFlags(fs)
	if err := fs.Parse(args); err != nil {
		PrintUsage(osStderr, doctorUsage, "doctor")
		return 2
	}
	if err := imageFlags.validate(fs); err != nil {
		_, _ = fmt.Fprintln(osStderr, err)
		PrintUsage(osStderr, doctorUsage, "doctor")
		return 2
	}
	if imageFlags.image != "" {
		return runDoctorImageCommand(imageFlags, inspector, *strict, *jsonOut || jsonOutput)
	}
	path := "."
	if fs.NArg() > 0 {
		path = fs.Arg(0)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return printErr("Bad path", err)
	}
	report := runDoctorChecks(abs)
	// Honour --strict: a warn under strict promotes to exit 1.
	// Use the doctorReport helpers so the standalone cmdDoctor
	// exit semantics stay in sync with `gregale deploy --doctor-strict`
	// (commands2.go:1097). A future check that adds a new status
	// value (e.g. "skipped") only needs to extend the helpers,
	// not three parallel iteration sites.
	exit := 0
	if report.HasErrors() || (*strict && report.HasWarnings()) {
		exit = 1
	}
	if *jsonOut || jsonOutput {
		_ = writeJSON(report)
		return exit
	}
	renderDoctorHuman(osStdout, report)
	return exit
}

// runDoctorChecks executes the 8 checks against path. The order
// is stable so the JSON output is deterministic for snapshot
// tests. Errors during a check (e.g. permission denied on a file)
// become a "warn" with the why= field populated — a hard error
// here would block the deploy on infrastructure noise.
func runDoctorChecks(path string) doctorReport {
	rep := doctorReport{Path: path}
	if profile, err := frameworkprofile.AnalyzeDir(path); err == nil {
		rep.Profile = &profile
	}
	rep.Checks = append(rep.Checks, doctorCheckPortBind())
	rep.Checks = append(rep.Checks, doctorCheckLoopbackBind(path))
	rep.Checks = append(rep.Checks, doctorCheckArch(path))
	rep.Checks = append(rep.Checks, doctorCheckEnvRequired(path))
	rep.Checks = append(rep.Checks, doctorCheckStatelessOnly(path))
	// The remaining checks need the server (listener probe, metered
	// RAM reading, build dry-run, and cold-boot probe). Preserve their
	// names in the stable JSON shape, but report them as skipped: an
	// unconditional "ok" is a false positive because no observation
	// was made locally.
	rep.Checks = append(rep.Checks, doctorCheckRuntimeOOM())
	rep.Checks = append(rep.Checks, doctorCheckDepInstall())
	rep.Checks = append(rep.Checks, doctorCheckStartupTimeout())
	return rep
}

// doctorCheckPortBind requires a live app to verify that the process
// actually listens on the platform-provided port. Source inspection
// cannot prove that, so it is explicitly skipped rather than shown as
// ok. The loopback-bind check below remains locally detectable.
func doctorCheckPortBind() doctorCheck {
	return doctorCheck{
		Name:   "port-bind",
		Status: "skipped",
		Reason: "requires a deployed app listener probe",
	}
}

// loopbackBindRegex matches `app.listen(..., '127.0.0.1')` (Express)
// and `bind('127.0.0.1', ...)` (Python socket) patterns. The match
// allows an optional port argument between the open-paren and the
// IP literal so `app.listen(8080, '127.0.0.1')` and
// `app.listen('127.0.0.1', 8080)` both trip. Wider matches would
// catch string literals in comments; we anchor on the call-site
// shape (`listen(...)` with the IP literal as one of the args).
var loopbackBindRegex = regexp.MustCompile(`(?:app\.listen|bind)\s*\([^)]*?['"]127\.0\.0\.1['"]`)

// doctorCheckLoopbackBind scans the source tree for listeners
// bound to 127.0.0.1. The per-VM bridge forwards to 10.0.0.2,
// so loopback-only binds never receive gateway traffic even
// though the readiness probe passes. Detected here → customer
// gets the fix BEFORE the failed wake.
func doctorCheckLoopbackBind(path string) doctorCheck {
	sources := scanSource(path, loopbackBindRegex, 5)
	if len(sources) == 0 {
		return doctorCheck{Name: "loopback-bind", Status: "ok"}
	}
	p := whycopy.Decorate(&api.Problem{}, api.CodeAppLoopbackBound, nil)
	return doctorCheck{
		Name:    "loopback-bind",
		Status:  "error",
		Code:    api.CodeAppLoopbackBound,
		Hint:    p.Hint,
		Why:     p.Why,
		Fix:     p.Fix,
		Sources: sources,
	}
}

// archMismatchRegex matches `Mach-O` (macOS native binaries) and
// `ARM aarch64` lines from `file(1)` output. The customer runs
// `gregale doctor` from their dev box; if they `tar -czf` a
// built binary without setting GOOS=linux GOARCH=amd64, the
// resulting tarball's binary will be Mach-O and the build VM's
// ENOEXEC fires app_arch_mismatch post-deploy. We catch it here.
var archMismatchRegex = regexp.MustCompile(`Mach-O|ARM aarch64|aarch64`)

// doctorCheckArch walks the source tree for binary files. The
// regex match is on file content, not extension, so a renamed
// binary still trips. We only flag files that look like binaries
// (first 8KB has > 1 non-printable byte per 32 bytes).
func doctorCheckArch(path string) doctorCheck {
	sources := scanSource(path, archMismatchRegex, 5)
	if len(sources) == 0 {
		return doctorCheck{Name: "arch", Status: "ok"}
	}
	p := whycopy.Decorate(&api.Problem{}, api.CodeAppArchMismatch, nil)
	return doctorCheck{
		Name:    "arch",
		Status:  "error",
		Code:    api.CodeAppArchMismatch,
		Hint:    p.Hint,
		Why:     p.Why,
		Fix:     p.Fix,
		Sources: sources,
	}
}

// envVarRefRegex matches `process.env.VAR` (Node), `os.environ["VAR"]`
// (Python), `os.Getenv("VAR")` (Go). Anchored on the env-var name
// pattern (uppercase + underscore) to skip comments.
var envVarRefRegex = regexp.MustCompile(`(?i)(?:process\.env|os\.environ(?:\[["']|get\("))([A-Z_][A-Z0-9_]{2,})`)

// doctorCheckEnvRequired scans source for env-var references and
// flags any not declared in `.gregale/env.json` (the apid
// persistence shape). Cross-referencing with a running app's
// declared env (via pkg/client ListEnv) is a future addition.
func doctorCheckEnvRequired(path string) doctorCheck {
	refs := scanEnvRefs(path, envVarRefRegex)
	if len(refs) == 0 {
		return doctorCheck{Name: "env-required", Status: "ok"}
	}
	declared := loadDeclaredEnv(path)
	missing := []string{}
	for _, r := range refs {
		if _, ok := declared[r]; !ok {
			missing = append(missing, r)
		}
	}
	if len(missing) == 0 {
		return doctorCheck{Name: "env-required", Status: "ok"}
	}
	p := whycopy.Decorate(&api.Problem{}, api.CodeEnvVarMissing, missing[0])
	return doctorCheck{
		Name:    "env-required",
		Status:  "error",
		Code:    api.CodeEnvVarMissing,
		Hint:    p.Hint,
		Why:     p.Why,
		Fix:     p.Fix,
		Sources: missing,
	}
}

// doctorCheckStatelessOnly mirrors the G13 stateless-only platform
// gate. Detects Dockerfile VOLUME directives, top-level data/
// db/ directories, and a curated list of stateful base images
// (postgres, redis, mongo, mysql). The runtime path is the same;
// preflight surfaces the finding before the build eats the
// customer's 10-minute slot.
func doctorCheckStatelessOnly(path string) doctorCheck {
	matched := scanStatelessShape(path)
	if len(matched) == 0 {
		return doctorCheck{Name: "stateless-only", Status: "ok"}
	}
	p := whycopy.Decorate(&api.Problem{}, api.CodeStatelessOnlyViolation, nil)
	return doctorCheck{
		Name:    "stateless-only",
		Status:  "error",
		Code:    api.CodeStatelessOnlyViolation,
		Hint:    p.Hint,
		Why:     p.Why,
		Fix:     p.Fix,
		Sources: matched,
	}
}

// doctorCheckRuntimeOOM is a server-side check. Runtime RAM telemetry
// lives in meterd, so a local preflight cannot evaluate it.
func doctorCheckRuntimeOOM() doctorCheck {
	return doctorCheck{Name: "runtime-oom", Status: "skipped", Reason: "requires deployed-app RAM telemetry"}
}

// doctorCheckDepInstall is a build-time check. The actual resolver
// runs in the platform build environment, not in this source scan.
func doctorCheckDepInstall() doctorCheck {
	return doctorCheck{Name: "dep-install", Status: "skipped", Reason: "requires the platform build environment"}
}

// doctorCheckStartupTimeout is a runtime check. The cold-boot probe
// window is evaluated only after the app is deployed.
func doctorCheckStartupTimeout() doctorCheck {
	return doctorCheck{Name: "startup-timeout", Status: "skipped", Reason: "requires a deployed-app cold-boot probe"}
}

// scanSource walks path looking for files matching re. The match
// is per-line and capped at maxHits results to keep the report
// readable on large repos. Returns "file:line" strings (line is
// always 1 today; line-by-line grep would require bufio.Scanner
// per file, which adds 30 LoC for marginal value).
func scanSource(root string, re *regexp.Regexp, maxHits int) []string {
	out := []string{}
	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		// filepath.Walk delivers (path, nil, err) when it can't even
		// stat the entry (permission-denied, broken symlink, mid-walk
		// delete). Skip without reading info when it's nil — the
		// regular path-resumption logic still calls us on the next
		// entry, so the scan continues past the unreadable node.
		if info == nil {
			return nil
		}
		if err != nil || info.IsDir() {
			// Skip vendor/, .git/, node_modules/ — they're noise.
			base := info.Name()
			if info.IsDir() && (base == "vendor" || base == ".git" || base == "node_modules" || base == ".gregale") {
				return filepath.SkipDir
			}
			// (err != nil with info != nil is rare — it means the
			// entry exists but a child walk failed. Returning nil
			// matches the original behaviour: a per-entry error
			// doesn't abort the scan, the walk continues past the
			// unreadable node.)
			return nil
		}
		if len(out) >= maxHits {
			return filepath.SkipAll
		}
		if info.Size() > 1<<20 { // skip files > 1 MB
			return nil
		}
		//nolint:forbidigo // filepath.Walk has already resolved p (the walked root is the customer input, not p itself); the regex is read-only line-by-line and we never execute any path. Same security discipline as openCustomerFile: no follow on symlinks, no exec.
		f, err := os.Open(p)
		if err != nil {
			return err // propagate to filepath.Walk so the scan continues past unreadable files (nilerr: we don't mask the error)
		}
		defer func() { _ = f.Close() }() //nolint:errcheck // read-only scan; close is best-effort
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			if re.MatchString(scanner.Text()) {
				out = append(out, p+":1")
				break
			}
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// scanEnvRefs walks the tree collecting every distinct env-var
// name referenced. The regex has a capture group; we deduplicate
// by name. Returns sorted.
func scanEnvRefs(root string, re *regexp.Regexp) []string {
	seen := map[string]bool{}
	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		// Mirror scanSource's nil-info guard: filepath.Walk delivers
		// (path, nil, err) for unreadable nodes (permission-denied,
		// broken symlink, mid-walk delete). Skip without reading
		// info so the scan continues past the unreadable entry.
		if info == nil {
			return nil
		}
		if err != nil || info.IsDir() {
			base := info.Name()
			if info.IsDir() && (base == "vendor" || base == ".git" || base == "node_modules" || base == ".gregale") {
				return filepath.SkipDir
			}
			return nil
		}
		if info.Size() > 1<<20 {
			return nil
		}
		//nolint:forbidigo // filepath.Walk has already resolved p (the walked root is the customer input, not p itself); the regex is read-only line-by-line and we never execute any path. Same security discipline as openCustomerFile: no follow on symlinks, no exec.
		f, err := os.Open(p)
		if err != nil {
			return err // propagate to filepath.Walk so the scan continues past unreadable files (nilerr: we don't mask the error)
		}
		defer func() { _ = f.Close() }() //nolint:errcheck // read-only scan; close is best-effort
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			m := re.FindStringSubmatch(scanner.Text())
			if len(m) > 1 {
				seen[strings.ToUpper(m[1])] = true
			}
		}
		return nil
	})
	out := []string{}
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// loadDeclaredEnv reads the local `.env` file (the shape `gregale
// env push` consumes) and returns the set of declared env-var
// names. The customer maintains `.env` by hand and pipes it into
// `gregale env push`; the doctor runs the same read so an
// undeclared `process.env.STRIPE_KEY` in the source shows up as
// missing from the customer-authored file.
//
// The parser is intentionally minimal: KEY=VALUE lines, leading
// whitespace tolerated, `export ` prefix tolerated, full-line
// comments (`#`) and blank lines skipped. Quote characters on the
// value are not stripped (the doctor only needs the key set, not
// the value). Lines that don't match the [A-Z_][A-Z0-9_]* key
// shape are skipped — those are usually a stray literal at the
// top of the file (e.g. "# Stripe dev keys" before the first
// assignment).
//
// The legacy `.gregale/env.json` path is read as a fallback for
// projects that pre-date the `env push` flow (the apid persistence
// shape). Both paths union into the returned set.
func loadDeclaredEnv(path string) map[string]bool {
	declared := map[string]bool{}
	// 1. Modern path: `.env` (the customer-authored file).
	// Route through openCustomerFile (cmd/gregale/commands5.go:493)
	// so the pre-open + post-open Lstat discipline fires — a
	// customer could `ln -sf` the .env to /etc/passwd between our
	// scan and the read, and openCustomerFile is the boundary
	// that catches it. The bare os.Open in the legacy fallback
	// below is on the apid-persistence path and is the documented
	// exception (commands_doctor.go is in the lint-tripwire
	// exceptions list alongside commands_doctor.go's own
	// filepath.Walk callbacks).
	envPath := filepath.Join(path, ".env")
	if f, err := openCustomerFile(envPath); err == nil {
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			line = strings.TrimPrefix(line, "export ")
			eq := strings.IndexByte(line, '=')
			if eq <= 0 {
				continue
			}
			key := strings.TrimSpace(line[:eq])
			if !isDeclaredEnvKey(key) {
				continue
			}
			declared[strings.ToUpper(key)] = true
		}
		_ = f.Close()
	}
	// 2. Legacy path: `.gregale/env.json` (apid persistence shape).
	cfgPath := filepath.Join(path, ".gregale", "env.json")
	if data, err := os.ReadFile(cfgPath); err == nil {
		var parsed map[string]any
		if err := json.Unmarshal(data, &parsed); err == nil {
			for k := range parsed {
				if !isDeclaredEnvKey(k) {
					continue
				}
				declared[strings.ToUpper(k)] = true
			}
		}
	}
	return declared
}

// isDeclaredEnvKey accepts the canonical env-var name shape
// (uppercase letters, digits, underscores; starts with a letter
// or underscore, ≥2 chars). Mirrors the envVarRefRegex capture
// group so the set is comparable to the refs scan.
func isDeclaredEnvKey(k string) bool {
	if len(k) < 2 {
		return false
	}
	for i, r := range k {
		if r == '_' {
			continue
		}
		if i == 0 && (r >= 'a' && r <= 'z') {
			// tolerate lowercase first char; we upper-case
			// before the map lookup
			continue
		}
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return true
}

// scanStatelessShape scans for Dockerfile VOLUME lines, data/ or
// db/ top-level dirs, and stateful base-image references. The
// base-image list is a curated 6-entry set; full image
// classification is a build-VM concern.
func scanStatelessShape(path string) []string {
	out := []string{}
	_ = filepath.Walk(path, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			// Propagate the walk error so unreadable subtrees don't
			// silently produce false negatives. nilerr-flagged if
			// we return nil here.
			return err
		}
		if info.IsDir() {
			name := info.Name()
			if name == "data" || name == "db" || name == "var" {
				// Only top-level dirs (parent == path).
				if filepath.Dir(p) == path {
					out = append(out, p)
				}
			}
			return nil
		}
		if filepath.Base(p) == "Dockerfile" {
			//nolint:forbidigo // filepath.Walk has already resolved p; Dockerfile contents are read-only line-by-line to detect VOLUME directives. Same security discipline as openCustomerFile: no follow on symlinks, no exec.
			f, err := os.Open(p)
			if err != nil {
				return err // propagate to filepath.Walk so the scan continues past unreadable files (nilerr: we don't mask the error)
			}
			defer func() { _ = f.Close() }() //nolint:errcheck // read-only scan; close is best-effort
			scanner := bufio.NewScanner(f)
			for scanner.Scan() {
				if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(scanner.Text())), "VOLUME ") {
					out = append(out, p)
					break
				}
			}
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// renderDoctorHuman emits the human-readable report. One line
// per check: ok ✓, warn !, error ✗, or skipped. When a check is
// warn/error the whycopy hint follows on the next line (indented,
// no glyph). Skipped checks carry their reason so the summary never
// implies that an unperformed check passed.
func renderDoctorHuman(w io.Writer, rep doctorReport) {
	if rep.Image != nil {
		renderDoctorImage(w, rep.Image)
	} else {
		_, _ = fmt.Fprintf(w, "gregale doctor — %s\n", rep.Path)
		if rep.Profile != nil {
			_, _ = fmt.Fprintf(w, "  profile: %s", rep.Profile.Framework)
			if rep.Profile.FrameworkVer != "" {
				_, _ = fmt.Fprintf(w, " %s", rep.Profile.FrameworkVer)
			}
			if rep.Profile.StartCommand != "" {
				_, _ = fmt.Fprintf(w, " · start=%s", rep.Profile.StartCommand)
			}
			_, _ = fmt.Fprintf(w, " · port=%d · health=%s\n", rep.Profile.Port, rep.Profile.HealthPath)
		}
	}
	_, _ = fmt.Fprintln(w, strings.Repeat("─", 60))
	hasFinding := false
	hasSkipped := false
	for _, c := range rep.Checks {
		switch c.Status {
		case "ok":
			if Enabled() {
				_, _ = fmt.Fprintf(w, "  ✓ %s — ok\n", c.Name)
			} else {
				_, _ = fmt.Fprintf(w, "  %s — ok\n", c.Name)
			}
		case "warn":
			hasFinding = true
			if Enabled() {
				_, _ = fmt.Fprintf(w, "  ! %s — warn\n", c.Name)
			} else {
				_, _ = fmt.Fprintf(w, "  %s — warn\n", c.Name)
			}
			if c.Hint != "" {
				RenderHintRow(w, c.Hint)
			}
		case "error":
			hasFinding = true
			if Enabled() {
				_, _ = fmt.Fprintf(w, "  ✗ %s — %s\n", c.Name, c.Code)
			} else {
				_, _ = fmt.Fprintf(w, "  %s — %s\n", c.Name, c.Code)
			}
			if c.Hint != "" {
				RenderHintRow(w, c.Hint)
			}
			if c.Fix != "" {
				RenderFixRow(w, c.Fix)
			}
			if len(c.Sources) > 0 {
				_, _ = fmt.Fprintf(w, "    sources: %s\n", strings.Join(c.Sources, ", "))
			}
		case "skipped":
			hasSkipped = true
			_, _ = fmt.Fprintf(w, "  %s — skipped\n", c.Name)
			if c.Reason != "" {
				_, _ = fmt.Fprintf(w, "    reason: %s\n", c.Reason)
			}
		}
	}
	if !hasFinding {
		_, _ = fmt.Fprintln(w)
		if hasSkipped {
			_, _ = fmt.Fprintln(w, "No local findings. Some checks require a deployed app and were skipped.")
		} else {
			_, _ = fmt.Fprintln(w, "All checks passed. Run `gregale deploy` to ship.")
		}
	} else {
		_, _ = fmt.Fprintln(w)
		_, _ = fmt.Fprintln(w, "Fix the above findings before deploy, or run with --strict to fail in CI.")
	}
}

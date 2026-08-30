package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/browser"
)

// authedClient builds a client using the stored token, or errors (exit 2) if the
// user isn't logged in.
func authedClient() (*Client, error) {
	tok := loadToken()
	if tok == "" {
		return nil, errAuth(errors.New("not logged in — run 'gregale login'"))
	}
	return NewClient(apiBase(), tok), nil
}

// authedClientWithDeployTimeout is the deploy-only variant of authedClient.
// It uses a longer HTTP timeout so the tarball upload leg doesn't get
// cut off at 30s when the source is large. Issue #64 D4.
func authedClientWithDeployTimeout(timeout time.Duration) (*Client, error) {
	tok := loadToken()
	if tok == "" {
		return nil, errAuth(errors.New("not logged in — run 'gregale login'"))
	}
	return NewClientWithDeployTimeout(apiBase(), tok, timeout), nil
}

// cmdLogin implements `gregale login [--token T]` (UX §2.2). The
// browser-paste flow is the default UX; --token is the CI path
// (gap G5). On success writes the API key to the config file at
// 0600 perms (config.go::saveToken) so subsequent commands can use
// the bearer token without re-authenticating.
func cmdLogin(args []string) int {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	fs.Usage = func() {
		PrintUsage(os.Stderr, "usage: gregale login [--token T]", "auth")
	}
	token := fs.String("token", "", "API token (CI/non-interactive)")
	if err := fs.Parse(args); err != nil {
		return 1
	}

	// CI path — unchanged behavior. Keep --token working so build
	// servers + scripts aren't broken by this change. Routes through
	// finalizeLogin so the UX §8 first-run quickstart fires for the
	// --token flag too (issue #65 D4 — signup ≡ login via API key
	// still needs the deploy-pointer nudge for fresh accounts).
	if *token != "" {
		client := NewClient(apiBase(), *token)
		acct, err := client.Whoami(context.Background())
		if err != nil {
			return printErr("Login failed", err)
		}
		if err := saveToken(*token); err != nil {
			return printErr("Could not save token", err)
		}
		// Use a fresh ctx for the quickstart probe; the --token
		// path is CI-shaped (no caller-supplied cancellation).
		probeCtx, cancel := context.WithCancel(context.Background())
		defer cancel()
		return finalizeLogin(probeCtx, client, *token, acct)
	}

	// Interactive flow (spec §2.2 device-code pair).
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := NewClient(apiBase(), "")

	codeResp, err := c.MintCliAuthCode(ctx)
	if err != nil {
		return printErr("Could not start login", err)
	}

	_, _ = fmt.Fprintf(osStdout, "Opening %s in your browser...\n", codeResp.URL)
	_, _ = fmt.Fprintln(osStdout, "  (or visit that URL and paste the code below)")

	// Best-effort browser open. On a sandboxed CI box the helper
	// returns an error (no DISPLAY); we surface it but stay in
	// paste mode — the user can either paste the code into this
	// terminal or open the URL in a real browser on another box.
	if err := browser.Open(codeResp.URL); err != nil {
		PrintFail(os.Stderr, "Could not open browser: %v", err)
		fmt.Fprintf(os.Stderr, "  Open this URL manually:\n  %s\n", codeResp.URL)
	}

	// Two-mode wait: prompt for a pasted code, OR fall through to
	// polling if the user just hits Enter. Read with a short
	// timeout so the poll loop can take over.
	_, _ = fmt.Fprint(osStdout, "Paste code (or press Enter to wait for browser): ")
	var pasted string
	if v, ok := readLineWithTimeout(osStdin, 3*time.Second); ok {
		pasted = strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(v), "-", ""))
	}

	if pasted == "" {
		return waitForApproval(ctx, c, codeResp)
	}
	if len(pasted) != 8 {
		PrintFail(os.Stderr, "Code should be 8 characters (XXXX-NNNN), got %d", len(pasted))
		return 1
	}
	return exchangeOnce(ctx, c, pasted)
}

// waitForApproval polls /v1/cli-auth/exchange at 1s until the user
// approves the code in the browser or the server-stated expiry
// passes. Stops early on consumed (someone else exchanged this code
// on a different machine — race; tell the user).
//
// Spec §2.2: the CLI is the source of truth for the polling cadence;
// the server-side limit is 5 min (cliAuthCodeTTL in handlers_cli_auth.go).
func waitForApproval(ctx context.Context, c *Client, codeResp api.CliAuthCodeResponse) int {
	expiry, _ := time.Parse(time.RFC3339, codeResp.ExpiresAt)
	backoff := 1 * time.Second
	for {
		if !expiry.IsZero() && time.Now().After(expiry.Add(2*time.Second)) {
			PrintFail(os.Stderr, "Code expired. Run 'gregale login' again.")
			return 1
		}
		select {
		case <-ctx.Done():
			return 1
		case <-time.After(backoff):
		}
		// Strip the dash so the server's normalizeCliAuthCode
		// doesn't have to. The server is case-insensitive so
		// uppercase is purely cosmetic on the wire.
		normalized := strings.ReplaceAll(codeResp.Code, "-", "")
		resp, err := c.ExchangeCliAuthCode(ctx, normalized)
		if err == nil {
			return finalizeLogin(ctx, c, resp.Plaintext, resp.Account)
		}
		var ae *APIError
		if errors.As(err, &ae) {
			switch ae.Problem.Code {
			case api.CodeCliAuthPending:
				continue // keep polling
			case api.CodeCliAuthUnavailable:
				renderAPIError(os.Stderr, ae)
				return 1
			default:
				return printErr("Login failed", err)
			}
		}
		// Network error — keep polling until the deadline so a
		// flaky TCP doesn't break the flow. Stops naturally when
		// expiry passes.
		continue
	}
}

// exchangeOnce is the paste-the-code single-shot path. Same exchange
// endpoint, but no polling — used after a successful browser-open
// that the user decided to mirror by pasting the code back into the
// same terminal.
func exchangeOnce(ctx context.Context, c *Client, normalized string) int {
	resp, err := c.ExchangeCliAuthCode(ctx, normalized)
	if err != nil {
		return printErr("Login failed", err)
	}
	return finalizeLogin(ctx, c, resp.Plaintext, resp.Account)
}

// finalizeLogin writes the freshly-minted plaintext API key to disk
// and prints the success line. Splits the path so the paste +
// browser-open flows can share it without duplicating the printer
// or saveToken call.
func finalizeLogin(ctx context.Context, c *Client, plaintext string, acct api.AccountResponse) int {
	if err := saveToken(plaintext); err != nil {
		return printErr("Could not save token", err)
	}
	PrintOK(osStdout, "Logged in as %s (%s plan)", acct.Email, acct.Plan)

	// First-run quickstart (UX §8, issue #65 D4). If the account has
	// no apps yet, drop a 3-line pointer to the two deploy paths.
	// A failing ListApps is silent — login must not be blocked by
	// transient API issues.
	if apps, err := c.ListApps(ctx); err == nil && len(apps) == 0 {
		_, _ = fmt.Fprintln(osStdout, "")
		_, _ = fmt.Fprintln(osStdout, "You're in. Next step — deploy your first app:")
		_, _ = fmt.Fprintln(osStdout, "  cd my-project && gregale deploy         # auto-detect & ship the current directory")
		_, _ = fmt.Fprintln(osStdout, "  gregale deploy --template hello-node    # or start from an embedded template")
		_, _ = fmt.Fprintln(osStdout, "  gregale deploy --tarball <path.tar.gz>  # or ship a prebuilt archive")
	}
	return 0
}

// readLineWithTimeout reads a single line from r. Returns
// (trimmed-line, true) if a newline arrives within d, or
// ("", false) on timeout. Used by cmdLogin to multiplex the
// "paste code" prompt with the "press Enter to wait" fallback.
//
// Implementation note: we run a single goroutine that reads until
// newline; the timeout is the only way to abort it (the goroutine
// is intentionally orphaned — when the caller drops the result the
// goroutine's slice can no longer affect anything because r is
// typically osStdin, which blocks anyway after the line arrives).
func readLineWithTimeout(r io.Reader, d time.Duration) (string, bool) {
	type result struct {
		line string
	}
	ch := make(chan result, 1)
	go func() {
		br := bufio.NewReader(r)
		line, _ := br.ReadString('\n')
		ch <- result{strings.TrimRight(line, "\r\n")}
	}()
	select {
	case res := <-ch:
		return res.line, true
	case <-time.After(d):
		return "", false
	}
}

func cmdLogout() int {
	// Clear both stores (OS keychain + legacy plaintext file).
	// Best-effort: a stuck keychain must not block logout, and a
	// missing file is not an error. Issue #293.
	deleteToken()
	PrintOK(osStdout, "Logged out")
	return 0
}

func cmdWhoami() int {
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	acct, err := client.Whoami(context.Background())
	if err != nil {
		return printErr("Request failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(acct))
	}
	fmt.Printf("%s (%s plan, %s)\n", acct.Email, acct.Plan, acct.Status)
	return 0
}

func cmdApps() int {
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	apps, err := client.ListApps(context.Background())
	if err != nil {
		return printErr("Request failed", err)
	}
	if jsonOutput {
		return jsonOut(writeNDJSON(apps))
	}
	if len(apps) == 0 {
		_, _ = fmt.Fprintln(osStdout, "No apps yet.")
		_, _ = fmt.Fprintln(osStdout, "Deploy one: `gregale deploy --template hello-node` (or `gregale deploy --tarball path/to/source.tar.gz`).")
		return 0
	}
	// Header row + data rows. Format code:
	//   SLUG (24) — STATUS (10) — URL (32) — AUTH (40)
	// AUTH column (issue #695 / ADR-080) shows the app's
	// require_authn + public_auth_mode state in human-readable
	// form. The "since YYYY-MM-DD" suffix renders only when
	// auth_default_flipped_at is non-null — pre-flip apps
	// that have been grand-fathered by migration 00156.
	_, _ = fmt.Fprintf(osStdout, "%-24s %-10s %-32s %s\n", "SLUG", "STATUS", "URL", "AUTH")
	for _, a := range apps {
		_, _ = fmt.Fprintf(osStdout, "%-24s %-10s %-32s %s\n", a.Slug, a.Status, a.URL, formatAppAuth(a))
	}
	return 0
}

// formatAppAuth (issue #695 / ADR-080) renders the AUTH column for
// the `gregale app list` table. Prefix matches the customer's
// observable auth state:
//
//	"AUTH: open"               — public, anonymous traffic allowed
//	                              (require_authn=false).
//	"AUTH: required"           — require_authn=true + public_auth_mode='open'.
//	                              Gateway demands a Bearer token but
//	                              accepts any — the bare pre-#477
//	                              chain (kept for Hobby compat).
//	"AUTH: bearer"             — require_authn=true + public_auth_mode='bearer'.
//	                              Pro/Scale default post-#695; gateway
//	                              validates the Bearer token against
//	                              the per-account API key table.
//	"AUTH: required + basic"   — require_authn=true + public_auth_mode='basic'
//	                              + a sealed basic-cred pair is set
//	                              (HasBasicCreds).
//
// The mode-aware branch is required: a Hobby bearer-shaped fixture
// (require_authn=true, public_auth_mode='open') and a Pro fresh
// default (require_authn=true, public_auth_mode='bearer') used to
// both print "AUTH: required" — the review caught the missing
// distinction. The order of cases matters: HasBasicCreds wins over
// Mode (a basic-mode app with no creds is misconfigured and the
// operator-facing surface still calls it out as basic).
//
// Suffix renders "since YYYY-MM-DD" only when auth_default_flipped_at
// is non-null (pre-flip apps grand-fathered by migration 00156).
func formatAppAuth(a api.AppResponse) string {
	var prefix string
	switch {
	case a.RequireAuthn && a.PublicAuth.Mode == api.AppPublicAuthModeBasic && a.PublicAuth.HasBasicCreds:
		prefix = "AUTH: required + basic"
	case a.RequireAuthn && a.PublicAuth.Mode == api.AppPublicAuthModeBearer:
		prefix = "AUTH: bearer"
	case a.RequireAuthn:
		prefix = "AUTH: required"
	default:
		prefix = "AUTH: open"
	}
	if a.AuthDefaultFlippedAt == nil {
		return prefix
	}
	return fmt.Sprintf("%s · since %s", prefix, a.AuthDefaultFlippedAt.UTC().Format("2006-01-02"))
}

// deriveName uses the current directory as the default app slug (UX §2.3).
func deriveName() string {
	wd, err := os.Getwd()
	if err != nil {
		return appSlugFallback
	}
	return sanitizeSlug(filepath.Base(wd))
}

// sanitizeSlug lowercases and strips a directory name into a valid slug shape.
func sanitizeSlug(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == ' ' || r == '.':
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) < 3 {
		out = "app-" + out
	}
	if len(out) > 40 {
		out = out[:40]
	}
	return strings.Trim(out, "-")
}

// printErr renders an error in the CLI's shape and returns the exit code (UX §3).
// Under --json (issue #64 D1), it dumps the raw RFC 7807 Problem body to
// stderr instead of the three-line render so scripts can `jq .code`.
// Otherwise (UX §3.2), the leading `✗` glyph is dropped when stdout is
// not a TTY or NO_COLOR is set; the body of each line is unchanged.
func printErr(title string, err error) int {
	// Strict secret-scan dispatch (PR-A v2): extract the typed error
	// BEFORE the nested-marker-hint branch so the JSON envelope carries
	// the full findings array (under `extra.findings`) plus a
	// `extra.hint` line for programmatic consumers. Text mode emits
	// one line per finding under the title. Both modes return exit
	// code 1; callers that want to distinguish strict-scan-rejected
	// from a 4xx API rejection can route on the JSON envelope's
	// `code` ("secret_scan_strict").
	var strictErr *StrictSecretScanError
	hasStrict := errors.As(err, &strictErr)
	// Issue #744 / ADR-086: extract the nested-marker workspace hint
	// from the error chain BEFORE the jsonOutput branch so both modes
	// can route it to stderr. The hint must NEVER appear on stdout (it
	// would corrupt `gregale deploy --json | jq`), so JSON mode prints
	// only the envelope and writes the hint via osStderr; text mode
	// appends the hint to the existing PrintFail line.
	var hintErr *NestedMarkerHintError
	hasHint := errors.As(err, &hintErr)
	if hasStrict {
		return renderStrictSecretScanError(title, strictErr)
	}
	if jsonOutput {
		var ae *APIError
		if errors.As(err, &ae) {
			_ = writeJSONProblem(ae.Problem)
			if hasHint {
				PrintWarn(osStderr, "%s", hintErr.Hint)
			}
			return exitCodeForStatus(ae.Problem.Status)
		}
		// Non-API errors (network, etc.) — synthesise a 500 Problem so
		// scripts still see a parseable JSON line on stderr.
		_ = writeJSONProblem(api.Problem{
			Status: 500, Code: "internal", Title: title, Detail: err.Error(),
		})
		if hasHint {
			PrintWarn(osStderr, "%s", hintErr.Hint)
		}
		return 1
	}
	var ae *APIError
	if errors.As(err, &ae) {
		renderAPIError(osStderr, ae)
		if hasHint {
			PrintWarn(osStderr, "%s", hintErr.Hint)
		}
		return exitCodeForStatus(ae.Problem.Status)
	}
	var ec *exitErr
	if errors.As(err, &ec) {
		PrintFail(osStderr, "%s\n  %s", title, ec.msg)
		if hasHint {
			PrintWarn(osStderr, "%s", hintErr.Hint)
		}
		return ec.code
	}
	if hasHint {
		// The hint replaces the title — the bare error message already
		// encodes the cwd + reasons (e.g. "no deployable source found in
		// <dir>: expected package.json, ..."), and the title is the
		// same string the caller passed in. Rendering both would
		// duplicate the cwd in the customer-visible output (issue #744
		// review finding). The hint is the actionable next step; the
		// error text is the context; the title is dropped.
		PrintFail(osStderr, "%s\n  %s", err.Error(), hintErr.Hint)
		return 1
	}
	PrintFail(osStderr, "%s\n  %s", title, err.Error())
	return 1
}

// renderStrictSecretScanError is the dispatch target for *StrictSecretScanError.
// Two output shapes:
//
//   - Text mode: prints the title, then one line per finding
//     (file:line [provider] snippet), then the hint line. Goes to
//     stderr; returns 1.
//
//   - JSON mode: synthesises a 422 Problem with
//     code=secret_scan_strict, the full findings array under
//     `secret_findings`, and the hint under `secret_hint`, then
//     writeJSONProblem. Returns 1 so CI pipelines see a non-zero
//     exit and can route on the envelope code.
//
// Both modes share the Problem-shape contract so a CI script can
// `jq -r '.code'` regardless of which side fired.
func renderStrictSecretScanError(title string, e *StrictSecretScanError) int {
	if e == nil {
		PrintFail(osStderr, "%s\n  unknown strict-scan error", title)
		return 1
	}
	if jsonOutput {
		findings := make([]api.SecretFinding, 0, len(e.Findings))
		for _, f := range e.Findings {
			findings = append(findings, api.SecretFinding{
				File:     f.File,
				Line:     f.Line,
				Key:      f.Key,
				Provider: f.Provider,
				Severity: f.Severity.String(),
				Snippet:  f.Snippet,
			})
		}
		_ = writeJSONProblem(api.Problem{
			Status:         422,
			Code:           api.CodeSecretScanStrict,
			Title:          title,
			Detail:         fmt.Sprintf("%d secret-shaped value(s) found", len(findings)),
			SecretFindings: findings,
			SecretHint:     e.Hint,
			DocsURL:        docsURLForCode(api.CodeSecretScanStrict),
		})
		return 1
	}
	PrintFail(osStderr, "%s", title)
	for _, f := range e.Findings {
		// text/lint tripwire-safe (no glyph literals): format is
		// `file:line [provider] snippet` mirroring the .env warning
		// shape from renderSecretScanWarnings so customers see a
		// consistent format across both modes.
		PrintWarn(osStderr, "  %s:%d [%s] %s", f.File, f.Line, f.Provider, f.Snippet)
	}
	if e.Hint != "" {
		PrintWarn(osStderr, "%s", e.Hint)
	}
	return 1
}

// renderAPIError is the single presentation path for *APIError.
// Delegates to RenderTitle/RenderDocsRow in output.go so the gate
// (output.go) owns every literal ✓/✗/→ string in the package —
// lint_tripwires_test.go::TestLintTripwire_NoGlyphLiteralOutsideOutput
// then has a single allow-listed file to audit.
//
// When Problem.DocsURL is empty but Problem.Code is non-empty, the
// docs row is synthesised from docsURLForCode(Code) so UX §3.3 holds
// even for codes the server didn't decorate with WithDocs. Skipping
// the row entirely (as the older renderer did) breaks the three-line
// contract on its most common paths.
func renderAPIError(w io.Writer, e *APIError) {
	if e == nil || e.Problem.Title == "" {
		return
	}
	p := e.Problem
	if p.DocsURL != "" {
		p.DocsURL = normalizeDocsURL(p.DocsURL)
	}
	if p.DocsURL == "" && p.Code != "" {
		p.DocsURL = docsURLForCode(p.Code)
	}
	RenderTitle(w, p.Title)
	// Detail row has no glyph — direct write is intentional. Routing
	// through a no-op RenderDetailRow would only mirror the formatter,
	// not add behaviour; the asymmetry keeps the gate's job small.
	// Fprintf error intentionally discarded (same convention as the
	// rest of cmd/gregale — see output.go::writeStatus).
	if p.Detail != "" {
		_, _ = fmt.Fprintf(w, "  %s\n", p.Detail)
	}
	// Error-explanations cluster (spec §6.4 amendment 1): surface
	// the customer-facing hint / why / fix / relevant_logs block
	// in the same order the whycopy catalog row lists them. Each
	// new row is gated on a non-empty value so the legacy 3-line
	// shape (Title / Detail / DocsURL) is unchanged for codes that
	// the cluster did not catalog yet. The renderers are the
	// glyph-discipline-aware helpers in output.go (the central
	// glyph table tripwire at lint_tripwires_test.go:138 pins the
	// glyphs to a single declaration site).
	if p.Hint != "" {
		RenderHintRow(w, p.Hint)
	}
	if p.Why != "" {
		RenderWhyRow(w, p.Why)
	}
	if p.Fix != "" {
		RenderFixRow(w, p.Fix)
	}
	if len(p.RelevantLogs) > 0 {
		RenderRelevantLogs(w, p.RelevantLogs)
	}
	if p.DocsURL != "" {
		RenderDocsRow(w, p.DocsURL)
	}
}

func exitCodeForStatus(status int) int {
	switch {
	case status == 401 || status == 402:
		return 2
	case status >= 500:
		return 3
	default:
		return 1
	}
}

// exitErr carries an explicit exit code for non-API errors.
type exitErr struct {
	msg  string
	code int
}

func (e *exitErr) Error() string { return e.msg }

func errAuth(err error) error { return &exitErr{msg: err.Error(), code: 2} }

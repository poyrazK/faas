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
		return nil, errAuth(errors.New("not logged in — run 'faas login'"))
	}
	return NewClient(apiBase(), tok), nil
}

// authedClientWithDeployTimeout is the deploy-only variant of authedClient.
// It uses a longer HTTP timeout so the tarball upload leg doesn't get
// cut off at 30s when the source is large. Issue #64 D4.
func authedClientWithDeployTimeout(timeout time.Duration) (*Client, error) {
	tok := loadToken()
	if tok == "" {
		return nil, errAuth(errors.New("not logged in — run 'faas login'"))
	}
	return NewClientWithDeployTimeout(apiBase(), tok, timeout), nil
}

// cmdLogin implements `faas login [--token T]` (UX §2.2). The
// browser-paste flow is the default UX; --token is the CI path
// (gap G5). On success writes the API key to the config file at
// 0600 perms (config.go::saveToken) so subsequent commands can use
// the bearer token without re-authenticating.
func cmdLogin(args []string) int {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
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
		fmt.Fprintf(os.Stderr, "✗ Could not open browser: %v\n", err)
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
		fmt.Fprintf(os.Stderr, "✗ Code should be 8 characters (XXXX-NNNN), got %d\n", len(pasted))
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
			fmt.Fprintln(os.Stderr, "✗ Code expired. Run 'faas login' again.")
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
				fmt.Fprintln(os.Stderr, "✗ ", ae.Error())
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
	_, _ = fmt.Fprintf(osStdout, "✓ Logged in as %s (%s plan)\n", acct.Email, acct.Plan)

	// First-run quickstart (UX §8, issue #65 D4). If the account has
	// no apps yet, drop a 3-line pointer to the two deploy paths.
	// A failing ListApps is silent — login must not be blocked by
	// transient API issues.
	if apps, err := c.ListApps(ctx); err == nil && len(apps) == 0 {
		_, _ = fmt.Fprintln(osStdout, "")
		_, _ = fmt.Fprintln(osStdout, "You're in. Next step — deploy your first app:")
		_, _ = fmt.Fprintln(osStdout, "  faas deploy --template hello-node    # start from an embedded template")
		_, _ = fmt.Fprintln(osStdout, "  faas deploy --tarball <path.tar.gz>  # or ship your own source")
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
	if p, err := tokenPath(); err == nil {
		_ = os.Remove(p)
	}
	fmt.Println("✓ Logged out")
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
		_, _ = fmt.Fprintln(osStdout, "Deploy one: `faas deploy --template hello-node` (or `faas deploy --tarball path/to/source.tar.gz`).")
		return 0
	}
	for _, a := range apps {
		fmt.Printf("%-24s %-10s %s\n", a.Slug, a.Status, a.URL)
	}
	return 0
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
func printErr(title string, err error) int {
	if jsonOutput {
		var ae *APIError
		if errors.As(err, &ae) {
			_ = writeJSONProblem(ae.Problem)
			return exitCodeForStatus(ae.Problem.Status)
		}
		// Non-API errors (network, etc.) — synthesise a 500 Problem so
		// scripts still see a parseable JSON line on stderr.
		_ = writeJSONProblem(api.Problem{
			Status: 500, Code: "internal", Title: title, Detail: err.Error(),
		})
		return 1
	}
	var ae *APIError
	if errors.As(err, &ae) {
		fmt.Fprintf(os.Stderr, "✗ %s\n", ae.Error())
		return exitCodeForStatus(ae.Problem.Status)
	}
	var ec *exitErr
	if errors.As(err, &ec) {
		fmt.Fprintf(os.Stderr, "✗ %s\n  %s\n", title, ec.msg)
		return ec.code
	}
	fmt.Fprintf(os.Stderr, "✗ %s\n  %s\n", title, err.Error())
	return 1
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

// gregale signup [--email-only EMAIL] (issue #311, UX §2.2).
//
// Two modes:
//
//  1. Interactive (default): prompts for email + password + confirm,
//     calls POST /v1/auth/signup, saves the freshly-minted API key
//     via saveToken(), and prints the first-run quickstart. Same
//     exit-state as `gregale login`.
//
//  2. --email-only EMAIL: calls POST /v1/auth/signup/magic-link and
//     prints "Check your email". Symmetric with the web magic-link
//     flow; no token is minted on the CLI side.
//
// The interactive path is one round-trip — the server returns the
// api_key payload in the signup response, so the CLI never has to
// follow up with a `/login` call. Idempotent on (email, password):
// a re-signup with the same credentials returns a fresh key.

package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/auth"
	"golang.org/x/term"
)

const dispatchSignup = "signup"

// cmdSignup is the dispatch entry point. Mirrors the `dispatchLogin`
// pattern: a flag.NewFlagSet, positional-arg count check, then route
// to one of three helpers:
//
//   - signupInteractive (default): email + password + confirm.
//   - signupFromStdin  (--password-stdin): email + password, no
//     confirm prompt — single-shot read for CI.
//   - signupMagicLink  (--email-only EMAIL): passwordless, server
//     emits a magic link.
//
// --email-only and --password-stdin are mutually exclusive: the former
// is server-rendered (no password ever crosses the wire in the CLI),
// the latter is a CI-shape mirror of the interactive flow.
func cmdSignup(args []string) int {
	fs := newFlagSet("signup", flag.ContinueOnError)
	fs.Usage = func() {
		if jsonOutput && !jsonUsageHelp {
			return
		}
		PrintUsage(os.Stderr, "usage: gregale signup [--email-only EMAIL | --password-stdin]", "auth")
	}
	emailOnly := fs.String("email-only", "", "send a one-time signup link to this email (no password prompt)")
	passwordStdin := fs.Bool("password-stdin", false, "read password from stdin (CI; mutually exclusive with --email-only)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		PrintUsage(os.Stderr, "usage: gregale signup [--email-only EMAIL | --password-stdin]", "auth")
		return 1
	}
	if *emailOnly != "" && *passwordStdin {
		return printErr("Invalid flags", fmt.Errorf("--email-only and --password-stdin are mutually exclusive"))
	}
	if *emailOnly != "" {
		return signupMagicLink(*emailOnly)
	}
	if *passwordStdin {
		return signupFromStdin()
	}
	return signupInteractive()
}

// signupInteractive is the default path. Stdin-driven: email, password,
// confirm. Mis-matched passwords or a weak password short-circuit
// before any HTTP round-trip (kept local for honesty — the server
// would reject anyway with the same error).
//
// On a real terminal, the password prompt hides what the user types
// (golang.org/x/term::ReadPassword); on a pipe / redirect the prompt
// falls back to the existing line-read. The gate is on stdinIsTTY()
// so the silent branch is unreachable from the test suite (the
// testOnlyTTY seam forces false).
func signupInteractive() int {
	br := bufio.NewReader(osStdin)

	fmt.Fprint(os.Stderr, "Email: ")
	emailRaw, err := br.ReadString('\n')
	if err != nil {
		return printErr("Could not read email", err)
	}
	email := strings.ToLower(strings.TrimSpace(emailRaw))
	if !looksLikeCLIEmail(email) {
		return printErr("Invalid email", fmt.Errorf("email must look like local@domain.tld"))
	}

	pw1, err := readInteractivePassword(br, "Password: ")
	if err != nil {
		return printErr("Could not read password", err)
	}
	if err := auth.Validate(pw1); err != nil {
		return printErr("Password too weak", err)
	}

	pw2, err := readInteractivePassword(br, "Confirm:  ")
	if err != nil {
		return printErr("Could not read confirm", err)
	}
	if pw1 != pw2 {
		return printErr("Passwords do not match", fmt.Errorf("the two passwords differ"))
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := NewClient(apiBase(), "")
	resp, err := c.PostAuthSignup(ctx, email, pw1)
	if err != nil {
		return printErr("Signup failed", err)
	}
	return finalizeLogin(ctx, c, resp.APIKey.Plaintext, resp.APIKey.ID, api.AccountResponse{
		ID:    resp.AccountID,
		Email: resp.Email,
		Plan:  resp.Plan,
	})
}

// signupFromStdin is the CI / build-pipeline shape. Reads email +
// password from stdin, no confirm prompt — a single-shot read is the
// whole point. Skipping the confirm step is a deliberate trade: a CI
// job that wants to type the password twice would have to feed the
// same secret to two consecutive lines, doubling the surface for
// leaks and doubling the orchestration cost for no real benefit (the
// server-side validation already catches typos via the 401 path).
//
// The "drain remaining stdin" read uses io.ReadAll on the same
// bufio.Reader the email read already wrapped, then trims the trailing
// \r\n. We DO NOT consult stdinIsTTY() — a user running
// `gregale signup --password-stdin` on a real TTY is opting into
// CI-shape behaviour (e.g. piping from a secret store). Mirrors
// cmdMfaDisable's `--password` flag shape (commands_mfa.go:235-246)
// but strips the interactive prompt so the pipe contract is one
// line in, one line out.
func signupFromStdin() int {
	br := bufio.NewReader(osStdin)

	emailRaw, err := br.ReadString('\n')
	if err != nil {
		return printErr("Could not read email", err)
	}
	email := strings.ToLower(strings.TrimSpace(emailRaw))
	if !looksLikeCLIEmail(email) {
		return printErr("Invalid email", fmt.Errorf("email must look like local@domain.tld"))
	}

	// Drain the rest of stdin past the email's newline. io.ReadAll
	// returns io.EOF at clean stream end; that's not an error here.
	// errors.Is (NOT ==) because golangci-lint's errorlint flags
	// direct comparison against sentinels (the value can be wrapped).
	rest, err := io.ReadAll(br)
	if err != nil && !errors.Is(err, io.EOF) {
		return printErr("Could not read password", err)
	}
	pw := strings.TrimRight(string(rest), "\r\n")
	if pw == "" {
		return printErr("Could not read password", fmt.Errorf("stdin closed before password byte"))
	}
	if err := auth.Validate(pw); err != nil {
		return printErr("Password too weak", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := NewClient(apiBase(), "")
	resp, err := c.PostAuthSignup(ctx, email, pw)
	if err != nil {
		return printErr("Signup failed", err)
	}
	return finalizeLogin(ctx, c, resp.APIKey.Plaintext, resp.APIKey.ID, api.AccountResponse{
		ID:    resp.AccountID,
		Email: resp.Email,
		Plan:  resp.Plan,
	})
}

// signupMagicLink is the no-password path. The server always returns
// 200 with the same body regardless of whether the email is bound,
// unbound, malformed, or missing — so the CLI just prints the link
// guidance and exits 0. Anti-enumeration closure lives on the server.
func signupMagicLink(email string) int {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := NewClient(apiBase(), "")
	if err := c.PostAuthSignupMagicLink(ctx, email); err != nil {
		return printErr("Could not send magic link", err)
	}
	_, _ = fmt.Fprintln(osStdout, "Check your email — a one-time signup link is on the way.")
	return 0
}

// looksLikeCLIEmail is a deliberately frugal client-side check.
// `local@domain.tld` with at least one dot in the domain; this is
// NOT a substitute for the server's full validation, just a
// front-of-house guard so the CLI can render "Invalid email"
// before any HTTP round-trip on a typo.
func looksLikeCLIEmail(s string) bool {
	if len(s) < 3 || len(s) > 254 {
		return false
	}
	at := strings.IndexByte(s, '@')
	if at <= 0 || at == len(s)-1 {
		return false
	}
	return strings.IndexByte(s[at+1:], '.') >= 0
}

// readInteractivePassword reads one password from either a TTY (silent
// echo via golang.org/x/term::ReadPassword) or a pipe / redirect (plain
// bufio line-read).
//
// TTY gate: we check the package-level osStdin (the same Reader that
// the bufio.Reader is wrapping) rather than os.Stdin directly. The
// pipeStdin test seam swaps the package var, so a test run that pipes
// data through osStdin sees "not a TTY" even when the test process's
// real os.Stdin happens to be a terminal (CI runners / developer
// machines). When osStdin is not a *os.File (e.g. a pipe backed by
// os.Pipe()), the type assertion fails and we land in the line-read
// branch — exactly the right behaviour for the test suite.
//
// The caller prints the prompt BEFORE invoking this helper — silent-echo
// mode produces no visible feedback, so the user needs the "Password: "
// line on stderr to know what is being asked. After a TTY read we print
// a trailing newline so the next prompt lands on a fresh line.
func readInteractivePassword(br *bufio.Reader, prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	if f, ok := osStdin.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		pwBytes, err := term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", err
		}
		return strings.TrimRight(string(pwBytes), "\r\n"), nil
	}
	line, err := br.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

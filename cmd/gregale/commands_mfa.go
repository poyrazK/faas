// commands_mfa.go — `gregale mfa <subcommand>` customer CLI for
// IAM-2 (issue #186) MFA enrollment + step-up + recovery + disable.
//
// The five MFA endpoints under /v1/account/mfa/* are:
//   POST /enroll    — start enrollment, returns otpauth URL + QR +
//                     recovery codes ONCE
//   POST /confirm   — finish enrollment, body {totp}
//   POST /verify    — step up an mfa_pending session, body {totp}
//   POST /recover   — burn a recovery code, body {code}
//   POST /disable   — opt out, body {password} OR {recovery_code}
//
// The /enroll response is the one-shot plaintext surface: the customer
// sees Secret + RecoveryCodes exactly once. This CLI command mirrors
// the dashboard's three-pane enrollment view (QR + recovery list) on
// stdout, plus prints the otpauth URL for the customer's authenticator
// app to ingest. There is no QR rendering in the CLI itself (no new
// deps); the customer pipes the URL into their local `qrcode` tool.
//
// All five operations route through authedClient() — the SDK already
// has the matching methods (pkg/api/client.go:307-358). The CLI's
// job is the flag parsing + the one-line UX-spec §3.2 output, not
// new SDK surface.

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/onebox-faas/faas/pkg/api"
	"golang.org/x/term"
)

// cmdMfa dispatches `gregale mfa <subcommand>`. Subcommands mirror
// the API verbs (enroll / confirm / verify / recover / disable) so
// the customer can guess the shape from the docs without consulting
// `gregale help`. Unknown subcommands return 1 with a usage hint.
func cmdMfa(args []string) int {
	parent, _ := lookupCliCommand("mfa")
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale mfa <enroll|confirm|verify|recover|disable> [args]", "mfa")
		return 1
	}
	switch args[0] {
	case "enroll":
		return cmdMfaEnroll(args[1:])
	case "confirm":
		return cmdMfaConfirm(args[1:])
	case "verify":
		return cmdMfaVerify(args[1:])
	case "recover":
		return cmdMfaRecover(args[1:])
	case "disable":
		return cmdMfaDisable(args[1:])
	default:
		sug, _ := suggestSubcommand(args[0], parent)
		fmt.Fprintf(os.Stderr, "gregale mfa: unknown subcommand %q (known: enroll, confirm, verify, recover, disable)\n", args[0])
		maybeSuggestSub(sug)
		return 1
	}
}

// cmdMfaEnroll kicks off MFA enrollment. The response carries the
// one-shot plaintext (otpauth URL + base64 QR PNG + 10 recovery
// codes), so the printout is the customer's ONLY chance to capture
// them — re-calling /enroll issues a fresh set (per mfa.go:55-65).
//
// The QR PNG bytes are base64-encoded by the API (`qr_code_png_base64`)
// because the wire format is JSON-only. We decode and write to a
// caller-chosen path (default ./gregale-mfa-qr.png) so the customer
// can `open` it on macOS or pipe it elsewhere. The PNG is not
// printed to stdout (binary noise).
func cmdMfaEnroll(args []string) int {
	fs := flag.NewFlagSet("mfa enroll", flag.ContinueOnError)
	qrOut := fs.String("qr-out", "gregale-mfa-qr.png", "write the QR PNG to this path (binary; not printed to stdout)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		PrintUsage(os.Stderr, "usage: gregale mfa enroll [--qr-out <path>]", "mfa")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.PostAccountMfaEnroll(context.Background())
	if err != nil {
		return printErr("Could not start MFA enrollment", err)
	}
	// Write the QR PNG to the caller-chosen path. Mode 0600 because
	// the encoded secret is in there (no plaintext, but defence in
	// depth — same posture as ExportAccountFile in client.go:82-92).
	// resp.QRCodePNG is already raw PNG bytes — json's []byte tag
	// round-trips base64 automatically on marshal/unmarshal, so we
	// write the slice as-is.
	if err := os.WriteFile(*qrOut, resp.QRCodePNG, 0o600); err != nil {
		return printErr("Could not write QR PNG", err)
	}
	if jsonOutput {
		// --json: render the full response (otpauth + secret + qr path)
		// as indented JSON so a script can ingest the secret directly.
		// Note: QRCodePNG is []byte in the SDK; for the JSON output
		// we re-encode to base64 so the wire shape stays stable.
		return jsonOut(writeJSON(struct {
			OTPAuthURL    string   `json:"otpauth_url"`
			Secret        string   `json:"secret"`
			QRPNGPath     string   `json:"qr_png_path"`
			RecoveryCodes []string `json:"recovery_codes"`
		}{
			OTPAuthURL:    resp.OTPAuthURL,
			Secret:        resp.Secret,
			QRPNGPath:     *qrOut,
			RecoveryCodes: resp.RecoveryCodes,
		}))
	}
	PrintOK(osStdout, "MFA enrollment started. Save the recovery codes now — they will NOT be shown again.")
	PrintProgress(osStdout, "otpauth URL: %s", resp.OTPAuthURL)
	PrintProgress(osStdout, "secret:      %s", resp.Secret)
	PrintProgress(osStdout, "QR PNG:      %s", *qrOut)
	PrintProgress(osStdout, "recovery codes (one-time use; each saves a fresh login when the device is lost):")
	for _, code := range resp.RecoveryCodes {
		PrintProgress(osStdout, "  %s", code)
	}
	PrintProgress(osStdout, "Confirm with: gregale mfa confirm <6-digit-code>")
	return 0
}

// cmdMfaConfirm finishes enrollment by submitting the first TOTP
// from the customer's authenticator. Server stamps mfa_enrolled_at
// + clears mfa_pending. Re-running is safe (idempotent).
func cmdMfaConfirm(args []string) int {
	fs := flag.NewFlagSet("mfa confirm", flag.ContinueOnError)
	code := fs.String("code", "", "6-digit TOTP from the authenticator (required)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *code == "" && fs.NArg() == 1 {
		*code = fs.Arg(0)
	}
	if *code == "" {
		PrintUsage(os.Stderr, "usage: gregale mfa confirm <6-digit-code>", "mfa")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if _, err := client.PostAccountMfaConfirm(context.Background(), api.MFAConfirmRequest{Totp: *code}); err != nil {
		return printErr("Could not confirm MFA enrollment", err)
	}
	PrintOK(osStdout, "MFA confirmed. mfa_required is now false on this account.")
	return 0
}

// cmdMfaVerify steps up an mfa_pending session. Same wire shape as
// confirm but does NOT stamp mfa_enrolled_at. Used by the dashboard
// after a paid-plan upgrade; the CLI twin exists for the rare
// CI/script case where a script needs an mfa_pending session
// completed.
func cmdMfaVerify(args []string) int {
	fs := flag.NewFlagSet("mfa verify", flag.ContinueOnError)
	code := fs.String("code", "", "6-digit TOTP from the authenticator (required)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *code == "" && fs.NArg() == 1 {
		*code = fs.Arg(0)
	}
	if *code == "" {
		PrintUsage(os.Stderr, "usage: gregale mfa verify <6-digit-code>", "mfa")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if _, err := client.PostAccountMfaVerify(context.Background(), api.MFAVerifyRequest{Totp: *code}); err != nil {
		return printErr("MFA verify failed", err)
	}
	PrintOK(osStdout, "MFA verified. Session is now step-up cleared.")
	return 0
}

// cmdMfaRecover burns a recovery code to regain access when the
// TOTP device is lost. Each code is single-use; after 10 burns the
// customer must re-enroll (server-side: /disable + /enroll cycle).
// UX spec §7.1 promises the recovery-code screen here.
func cmdMfaRecover(args []string) int {
	fs := flag.NewFlagSet("mfa recover", flag.ContinueOnError)
	code := fs.String("code", "", "10-char base32 recovery code (required)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *code == "" && fs.NArg() == 1 {
		*code = fs.Arg(0)
	}
	if *code == "" {
		PrintUsage(os.Stderr, "usage: gregale mfa recover <recovery-code>", "mfa")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if _, err := client.PostAccountMfaRecover(context.Background(), api.MFARecoverRequest{Code: *code}); err != nil {
		return printErr("MFA recovery failed", err)
	}
	PrintOK(osStdout, "Recovery code accepted. You are signed in.")
	return 0
}

// cmdMfaDisable opts out of MFA. Server requires either Password OR
// RecoveryCode (one of the two, never both). Interactive prompt
// reads the password from /dev/tty so it doesn't leak into shell
// history; --password sets it explicitly for CI use.
//
// Plan-gated: only Pro/Scale accounts can disable MFA — the server
// returns 402 plan_mfa_disable_not_allowed for Free/Hobby. The CLI
// surfaces that as-is (no special-casing).
func cmdMfaDisable(args []string) int {
	fs := flag.NewFlagSet("mfa disable", flag.ContinueOnError)
	password := fs.String("password", "", "account password (CI use; will prompt interactively if empty)")
	recovery := fs.String("recovery-code", "", "single-use recovery code (alternative to --password)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *password != "" && *recovery != "" {
		return printErr("Invalid flags", fmt.Errorf("--password and --recovery-code are mutually exclusive"))
	}
	if *password == "" && *recovery == "" {
		// Interactive prompt: read the password from /dev/tty so
		// it does not land in shell history or in `ps` output.
		// Echo is suppressed via golang.org/x/term.
		fmt.Fprint(os.Stderr, "Account password: ")
		pwBytes, err := term.ReadPassword(int(syscall.Stdin))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return printErr("Could not read password", err)
		}
		*password = strings.TrimRight(string(pwBytes), "\r\n")
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	req := api.MFADisableRequest{Password: *password, RecoveryCode: *recovery}
	if _, err := client.PostAccountMfaDisable(context.Background(), req); err != nil {
		return printErr("MFA disable failed", err)
	}
	PrintOK(osStdout, "MFA disabled. mfa_required remains set; future triggers will re-arm it.")
	return 0
}

package main

import (
	"flag"
	"fmt"
	"net/url"
	"os"
)

// Account MFA belongs to a browser session. A CLI key must never become a
// substitute for the second factor it is meant to protect. These commands
// open the corresponding session-authenticated form, including for customers
// who cannot complete CLI login until they recover their authenticator.
func cmdMfa(args []string) int {
	parent, _ := lookupCliCommand("mfa")
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale account mfa <enroll|confirm|verify|recover|disable|rotate>", "mfa")
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
	case "rotate":
		// New recovery codes; the dashboard action keeps its name.
		if len(args) > 1 {
			PrintUsage(os.Stderr, "usage: gregale account mfa rotate", "mfa")
			return 1
		}
		return openMFAForm("recovery-codes")
	default:
		sug, _ := suggestSubcommand(args[0], parent)
		printCommandValidation(os.Stderr, "gregale account mfa: unknown subcommand %q (known: enroll, confirm, verify, recover, disable, rotate)\n", args[0])
		maybeSuggestSub(sug)
		return 1
	}
}

func cmdMfaEnroll(args []string) int {
	fs := newFlagSet("mfa enroll", flag.ContinueOnError)
	// Retain parsing compatibility; setup material stays in the browser.
	fs.String("qr-out", "", "legacy option; the browser displays the QR code")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	return openMFAForm("enroll")
}

func cmdMfaConfirm(args []string) int { return cmdMfaCodeForm("confirm", args) }
func cmdMfaVerify(args []string) int  { return cmdMfaCodeForm("verify", args) }
func cmdMfaRecover(args []string) int { return cmdMfaCodeForm("recover", args) }

func cmdMfaCodeForm(action string, args []string) int {
	fs := newFlagSet("mfa "+action, flag.ContinueOnError)
	code := fs.String("code", "", "legacy option; enter the code in the browser")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *code != "" && rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if fs.NArg() > 1 {
		PrintUsage(os.Stderr, "usage: gregale account mfa "+action, "mfa")
		return 1
	}
	return openMFAForm(action)
}

func cmdMfaDisable(args []string) int {
	fs := newFlagSet("mfa disable", flag.ContinueOnError)
	password := fs.String("password", "", "legacy option; enter the password in the browser")
	recovery := fs.String("recovery-code", "", "legacy option; enter the recovery code in the browser")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if *password != "" && *recovery != "" {
		return printErr("Invalid flags", fmt.Errorf("--password and --recovery-code are mutually exclusive"))
	}
	return openMFAForm("disable")
}

func openMFAForm(action string) int {
	target := dashboardAccountURL(apiBase()) + "/mfa?action=" + url.QueryEscape(action)
	if jsonOutput {
		return jsonOut(writeJSON(struct {
			URL             string `json:"url"`
			Action          string `json:"action"`
			RequiresBrowser bool   `json:"requires_browser"`
		}{target, action, true}))
	}
	PrintProgress(osStdout, "Complete MFA %s in your signed-in browser. Enter codes and passwords there.", action)
	PrintProgress(osStdout, "%s", target)
	if !nonInteractive {
		if err := openBrowser(target); err != nil {
			PrintProgress(osStderr, "Open the URL above in your browser: %v", err)
		}
	}
	return 0
}

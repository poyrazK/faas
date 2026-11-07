package main

import (
	"context"
	"flag"
	"fmt"
	"os"
)

// Account security and profile commands (ADR 20261107103000). Sessions,
// verification, display name and providers use the API key; password and
// email changes need the proof a browser session gives, so they open the
// account page like `gregale account mfa`.

func cmdAccountSessions(args []string) int {
	sub := subList
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case subList, "ls":
		return cmdAccountSessionsList(args)
	case "revoke":
		return cmdAccountSessionsRevoke(args)
	default:
		parent, _ := lookupCliCommand("account")
		return printUnknownSubcommand(os.Stderr, "account sessions", parent, sub)
	}
}

func cmdAccountSessionsList(args []string) int {
	if noArgs("account sessions list", args) != 0 {
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := client.ListSessions(context.Background())
	if err != nil {
		return printErr("Could not list sessions", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	if len(out.Sessions) == 0 {
		PrintProgress(osStdout, "No browser is signed in.")
		return 0
	}
	for _, s := range out.Sessions {
		current := ""
		if s.CurrentSession {
			current = " (this browser)"
		}
		_, _ = fmt.Fprintf(osStdout, "%s  %s  %s  %s%s\n", s.ID, s.IssuedAt, s.IssuedIP, s.IssuedUA, current)
	}
	return 0
}

// cmdAccountSessionsRevoke signs out one browser (SESSION_ID) or, with
// --all, every browser. API keys keep working either way.
func cmdAccountSessionsRevoke(args []string) int {
	fs := newFlagSet("account sessions revoke", flag.ContinueOnError)
	all := fs.Bool("all", false, "sign out every browser")
	yes := fs.Bool("yes", false, "with --all, sign out without prompting")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if (*all && fs.NArg() != 0) || (!*all && fs.NArg() != 1) {
		PrintUsage(os.Stderr, "usage: gregale account sessions revoke {SESSION_ID | --all [--yes]}", "account")
		return 1
	}
	if *all {
		if code := requireConfirmationFlag(*yes, "--yes", "signing out every browser"); code != 0 {
			return code
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if !*all {
		if err := client.RevokeSession(context.Background(), fs.Arg(0)); err != nil {
			return printErr("Could not revoke session", err)
		}
		PrintOK(osStdout, "Session revoked")
		return 0
	}
	out, err := client.RevokeAllSessions(context.Background())
	if err != nil {
		return printErr("Could not revoke sessions", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	PrintOK(osStdout, "Signed out %d browser session(s). API keys still work.", out.Revoked)
	return 0
}

// cmdAccountEmail: `verify` mails a new verification link, `update` opens
// the change-email form in the browser.
func cmdAccountEmail(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale account email {verify|update}", "account")
		return 1
	}
	switch args[0] {
	case "verify":
		return cmdAccountEmailVerify(args[1:])
	case subUpdate:
		return cmdAccountBrowserForm("account email update", "sign-in email", args[1:])
	default:
		parent, _ := lookupCliCommand("account")
		return printUnknownSubcommand(os.Stderr, "account email", parent, args[0])
	}
}

func cmdAccountEmailVerify(args []string) int {
	if noArgs("account email verify", args) != 0 {
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	status, err := client.ResendEmailVerification(context.Background())
	if err != nil {
		return printErr("Could not send a verification link", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]string{"status": status}))
	}
	if status == "already_verified" {
		PrintOK(osStdout, "Your email address is already verified")
		return 0
	}
	PrintOK(osStdout, "Verification link sent; it is valid for 24 hours")
	return 0
}

func cmdAccountPassword(args []string) int {
	if len(args) == 0 || args[0] != subUpdate {
		PrintUsage(os.Stderr, "usage: gregale account password update", "account")
		return 1
	}
	return cmdAccountBrowserForm("account password update", "password", args[1:])
}

// cmdAccountUpdate sets account profile fields.
func cmdAccountUpdate(args []string) int {
	fs := newFlagSet("account update", flag.ContinueOnError)
	name := fs.String("name", "", "display name shown to org members")
	clearName := fs.Bool("clear-name", false, "remove the display name")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if fs.NArg() != 0 || (*name == "") == !*clearName {
		PrintUsage(os.Stderr, "usage: gregale account update {--name NAME | --clear-name}", "account")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if err := client.SetDisplayName(context.Background(), *name); err != nil {
		return printErr("Could not update the account", err)
	}
	PrintOK(osStdout, "Account updated")
	return 0
}

func cmdAccountProvider(args []string) int {
	if len(args) == 0 || args[0] != subList {
		PrintUsage(os.Stderr, "usage: gregale account provider list", "account")
		return 1
	}
	if noArgs("account provider list", args[1:]) != 0 {
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := client.SignInMethods(context.Background())
	if err != nil {
		return printErr("Could not list sign-in methods", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	_, _ = fmt.Fprintf(osStdout, "password:   %s\n", onOff(out.Password))
	_, _ = fmt.Fprintf(osStdout, "two-factor: %s\n", onOff(out.MFA))
	for _, p := range out.Providers {
		_, _ = fmt.Fprintf(osStdout, "%-10s  %s  linked %s\n", p.Provider, p.Email, p.LinkedAt)
	}
	PrintProgress(osStdout, "Remove a linked provider from the account page: %s", dashboardAccountURL(apiBase()))
	return 0
}

// cmdAccountBrowserForm opens the account page for changes that need a
// browser session's proof (current password or authenticator).
func cmdAccountBrowserForm(command, what string, args []string) int {
	if noArgs(command, args) != 0 {
		return 1
	}
	target := dashboardAccountURL(apiBase())
	if jsonOutput {
		return jsonOut(writeJSON(map[string]any{"url": target, "action": what, "requires_browser": true}))
	}
	PrintProgress(osStdout, "Change your %s in your signed-in browser:", what)
	PrintProgress(osStdout, "%s", target)
	if !nonInteractive {
		if err := openBrowser(target); err != nil {
			PrintProgress(osStderr, "Open the URL above in your browser: %v", err)
		}
	}
	return 0
}

func cmdOrgsLeave(args []string) int {
	fs := newFlagSet("orgs leave", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "leave without prompting")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale org leave ORG_SLUG [--yes]", "orgs")
		return 1
	}
	if code := requireConfirmationFlag(*yes, "--yes", "leaving the org"); code != 0 {
		return code
	}
	slug := fs.Arg(0)
	if !*yes && !requireTyped(slug) {
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if err := client.LeaveOrg(context.Background(), slug); err != nil {
		return printErr("Could not leave the org", err)
	}
	PrintOK(osStdout, "You left %s", slug)
	return 0
}

func noArgs(name string, args []string) int {
	fs := newFlagSet(name, flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		PrintUsage(os.Stderr, "usage: gregale "+name, "account")
		return 1
	}
	return 0
}

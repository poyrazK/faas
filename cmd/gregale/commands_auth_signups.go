package main

import (
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

const authSignupsUsage = "usage: gregale auth signups <list|approve|remove> NAME [EMAIL...] [--status pending|approved|all] [--search TEXT] [--page N] [--limit N] [--no-email] [--admin-key-stdin]"

type authSignup struct {
	Email       string  `json:"email"`
	RequestedAt *string `json:"requested_at"`
	ApprovedAt  *string `json:"approved_at"`
	NotifiedAt  *string `json:"notified_at"`
	SignedUp    bool    `json:"signed_up"`
}

// cmdAuthSignups manages who may sign up while sign-up is by invitation or
// waitlist (--signup-access): the waitlist, approving addresses (emailed
// that they may sign up, unless --no-email) and removing them.
func cmdAuthSignups(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, authSignupsUsage, "auth")
		return 1
	}
	fs := newFlagSet("auth signups "+args[0], flag.ContinueOnError)
	list := addAuthListFlags(fs, "email address")
	status := fs.String("status", "all", "pending (waiting for approval), approved or all")
	noEmail := fs.Bool("no-email", false, "approve without emailing the addresses")
	keyStdin := fs.Bool("admin-key-stdin", false, "read the admin key from stdin (prompted on a terminal)")
	if err := parseInterspersed(fs, args[1:]); err != nil {
		return 1
	}
	allowed, known := map[string][]string{"list": {"search", "page", "limit", "status"}, "approve": {"no-email"}, "remove": nil}[args[0]]
	if !known || fs.NArg() < 1 || !api.ValidAppSlug(fs.Arg(0)) || (args[0] == "list") != (fs.NArg() == 1) || (args[0] == "remove" && fs.NArg() != 2) {
		PrintUsage(os.Stderr, authSignupsUsage, "auth")
		return 1
	}
	if err := authOnlyFlags(fs, allowed); err != nil {
		return printErr("Invalid flags", err)
	}
	emails := fs.Args()[1:]
	if len(emails) > api.AuthSignupApproveBatch {
		return printErr("Too many addresses", fmt.Errorf("approve at most %d addresses at a time", api.AuthSignupApproveBatch))
	}
	for _, email := range emails {
		if !strings.Contains(email, "@") || len(email) > 320 || strings.ContainsAny(email, " \t<>\"") {
			return printErr("Invalid address", fmt.Errorf("%q is not an email address", email))
		}
	}
	query := ""
	if args[0] == "list" {
		if *status != "pending" && *status != "approved" && *status != "all" {
			return printErr("Invalid flags", errors.New("--status is pending, approved or all"))
		}
		var err error
		if query, err = list.query(); err != nil {
			return printErr("Invalid flags", err)
		}
		query += "&status=" + url.QueryEscape(*status)
	}
	origin, key, code := authAdminTarget(fs.Arg(0), *keyStdin)
	if code != 0 {
		return code
	}
	switch args[0] {
	case "list":
		return authSignupsList(origin, key, query)
	case "approve":
		var approved struct {
			Approved int `json:"approved"`
			Notified int `json:"notified"`
		}
		if err := authAdminCall(http.MethodPost, origin, key, "/admin/v1/signups/approve", map[string]any{"emails": emails, "notify": !*noEmail}, &approved); err != nil {
			return printErr("Could not approve", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(approved))
		}
		PrintOK(osStdout, "Approved %d address(es); %d emailed that they may sign up", approved.Approved, approved.Notified)
		return 0
	default:
		status, raw, err := authAdminDo(http.MethodDelete, origin, key, "/admin/v1/signups/"+url.PathEscape(strings.ToLower(emails[0])), nil)
		if err == nil && status != http.StatusNoContent {
			err = authAdminError(status, raw)
		}
		if err != nil {
			return printErr("Could not remove the address", err)
		}
		PrintOK(osStdout, "Removed %s; it may no longer sign up unless invited", emails[0])
		return 0
	}
}

func authSignupsList(origin, key, query string) int {
	var page struct {
		Signups []authSignup `json:"signups"`
		Total   int          `json:"total"`
		Page    int          `json:"page"`
		PerPage int          `json:"per_page"`
	}
	if err := authAdminCall(http.MethodGet, origin, key, "/admin/v1/signups?"+query, nil, &page); err != nil {
		return printErr("Could not list sign-ups", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(page))
	}
	if len(page.Signups) == 0 {
		_, _ = fmt.Fprintln(osStdout, "No addresses match.")
		return 0
	}
	for _, signup := range page.Signups {
		state := "waiting since " + authOr(signup.RequestedAt, "-")
		switch {
		case signup.SignedUp:
			state = "signed up"
		case signup.ApprovedAt != nil:
			state = "approved " + *signup.ApprovedAt
		}
		_, _ = fmt.Fprintf(osStdout, "%-40s  %s\n", signup.Email, state)
	}
	if page.Page*page.PerPage < page.Total {
		_, _ = fmt.Fprintf(osStdout, "Page %d of %s addresses; next: --page %d\n", page.Page, strconv.Itoa(page.Total), page.Page+1)
	}
	return 0
}

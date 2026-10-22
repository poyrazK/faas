package main

import (
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
)

const authImpersonationsUsage = "usage: gregale auth impersonations <list|end> NAME [ID] [--user USER] [--admin-key-stdin]"

// cmdAuthImpersonations lists the sessions support staff opened as users
// (newest first, with the key and reason) and ends one early.
func cmdAuthImpersonations(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, authImpersonationsUsage, "auth")
		return 1
	}
	fs := newFlagSet("auth impersonations "+args[0], flag.ContinueOnError)
	userRef := fs.String("user", "", "only this user's (ID or email)")
	keyStdin := fs.Bool("admin-key-stdin", false, "read the admin key from stdin (prompted on a terminal)")
	if err := parseInterspersed(fs, args[1:]); err != nil {
		return 1
	}
	want := map[string]int{"list": 1, "end": 2}[args[0]]
	if want == 0 || fs.NArg() != want || !api.ValidAppSlug(fs.Arg(0)) || (want == 2 && !authImportUUID.MatchString(fs.Arg(1))) {
		PrintUsage(os.Stderr, authImpersonationsUsage, "auth")
		return 1
	}
	if args[0] == "end" {
		if err := authOnlyFlags(fs, nil); err != nil {
			return printErr("Invalid flags", err)
		}
	}
	origin, key, code := authAdminTarget(fs.Arg(0), *keyStdin)
	if code != 0 {
		return code
	}
	if args[0] == "end" {
		status, raw, err := authAdminDo(http.MethodDelete, origin, key, "/admin/v1/impersonations/"+fs.Arg(1), nil)
		if err == nil && status != http.StatusNoContent {
			err = authAdminError(status, raw)
		}
		if err != nil {
			return printErr("Could not end the session", err)
		}
		PrintOK(osStdout, "Ended session %s", fs.Arg(1))
		return 0
	}
	path := "/admin/v1/impersonations"
	if *userRef != "" {
		if err := authUserRefValid(*userRef); err != nil {
			return printErr("Invalid user", err)
		}
		user, err := authResolveUser(origin, key, *userRef)
		if err != nil {
			return printErr("Could not find the user", err)
		}
		path += "?user=" + url.QueryEscape(user.ID)
	}
	var page struct {
		Impersonations []struct {
			ID        string  `json:"id"`
			Email     *string `json:"email"`
			Actor     string  `json:"actor"`
			Reason    string  `json:"reason"`
			StartedAt string  `json:"started_at"`
			ExpiresAt string  `json:"expires_at"`
			EndedAt   *string `json:"ended_at"`
			Active    bool    `json:"active"`
		} `json:"impersonations"`
	}
	if err := authAdminCall(http.MethodGet, origin, key, path, nil, &page); err != nil {
		return printErr("Could not list sessions opened as users", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(page))
	}
	if len(page.Impersonations) == 0 {
		_, _ = fmt.Fprintln(osStdout, "No sessions were opened as users.")
		return 0
	}
	for _, i := range page.Impersonations {
		state := "ended"
		if i.Active {
			state = "active until " + i.ExpiresAt
		}
		_, _ = fmt.Fprintf(osStdout, "%s  %s  %s by %s, %s: %s\n", i.ID, authOr(i.Email, "-"), i.StartedAt, i.Actor, state, i.Reason)
	}
	return 0
}

package main

// adr: 1190 — service accounts: an organization's machine principals, which
// get access tokens with the client-credentials grant. The owner manages
// them in any organization through the Auth app's /admin/v1/service-accounts
// with an admin key; members holding members:manage manage their own
// organization's through the organization API.

import (
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

const authServiceAccountsUsage = "usage: gregale auth service-accounts <list|add|update|remove|add-secret|revoke-secret> NAME [ID] [SECRET_ID] [--org ORG] [--name NAME] [--role ROLE] [--expires-in DAYS] [--disable|--enable] [--yes] [--admin-key-stdin]"

// authServiceAccountRole is a role name as gregale_auth.roles allows it.
var authServiceAccountRole = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

type authServiceAccountSecret struct {
	ID         string  `json:"id"`
	Hint       string  `json:"hint"`
	CreatedAt  string  `json:"created_at"`
	ExpiresAt  *string `json:"expires_at"`
	LastUsedAt *string `json:"last_used_at"`
}

type authServiceAccount struct {
	ID        string                     `json:"id"`
	OrgID     string                     `json:"org_id"`
	Name      string                     `json:"name"`
	Role      string                     `json:"role"`
	Disabled  bool                       `json:"disabled"`
	CreatedAt string                     `json:"created_at"`
	Secrets   []authServiceAccountSecret `json:"secrets"`
}

// authServiceAccountFlags lists the flags each subcommand takes besides
// --admin-key-stdin, and how many positionals it needs.
var authServiceAccountFlags = map[string]struct {
	flags []string
	args  int
}{
	"list": {[]string{"org"}, 1}, "add": {[]string{"org", "name", "role", "expires-in"}, 1}, "update": {[]string{"name", "role", "disable", "enable"}, 2},
	"remove": {[]string{"yes"}, 2}, "add-secret": {[]string{"expires-in"}, 2}, "revoke-secret": {nil, 3},
}

func cmdAuthServiceAccounts(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, authServiceAccountsUsage, "auth")
		return 1
	}
	fs := newFlagSet("auth service-accounts "+args[0], flag.ContinueOnError)
	org := fs.String("org", "", "the organization (ID or slug)")
	name := fs.String("name", "", "the service account's name, shown in lists and the audit log")
	role := fs.String("role", "", "one of the organization's roles, as for a member")
	expires := fs.Int("expires-in", 0, "days until the secret expires (1-"+strconv.Itoa(api.AuthServiceAccountSecretMaxDays)+"); without it the secret lasts until revoked")
	disable := fs.Bool("disable", false, "stop the account: its tokens fail every authorize() check at once")
	enable := fs.Bool("enable", false, "start a disabled account again")
	yes := fs.Bool("yes", false, "remove without asking")
	keyStdin := fs.Bool("admin-key-stdin", false, "read the admin key from stdin (prompted on a terminal)")
	if err := parseInterspersed(fs, args[1:]); err != nil {
		return 1
	}
	spec, known := authServiceAccountFlags[args[0]]
	if !known || fs.NArg() != spec.args || !api.ValidAppSlug(fs.Arg(0)) || (spec.args >= 2 && !authImportUUID.MatchString(fs.Arg(1))) || (spec.args == 3 && !authImportUUID.MatchString(fs.Arg(2))) {
		PrintUsage(os.Stderr, authServiceAccountsUsage, "auth")
		return 1
	}
	if err := authOnlyFlags(fs, spec.flags); err != nil {
		return printErr("Invalid flags", err)
	}
	given := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { given[f.Name] = true })
	body, err := authServiceAccountBody(args[0], given, *name, *role, *expires, *disable, *enable)
	if err != nil {
		return printErr("Invalid service account", err)
	}
	if args[0] == "add" && *org == "" {
		return printErr("Invalid service account", errors.New("add needs --org"))
	}
	if args[0] == "remove" {
		if code := requireAutomationConfirmation(*yes, "--yes"); code != 0 {
			return code
		}
		if !*yes && *keyStdin {
			return printErr("Confirmation required", errors.New("--admin-key-stdin uses stdin for the key; confirm the removal with --yes"))
		}
	}
	origin, key, code := authAdminTarget(fs.Arg(0), *keyStdin)
	if code != 0 {
		return code
	}
	if args[0] == "remove" && !*yes {
		_, _ = fmt.Fprintf(osStderr, "About to remove service account %s of %s with its secrets; its tokens stop working at once.\n", fs.Arg(1), fs.Arg(0))
		if !requireTypedOr("remove service account", "--yes") {
			return 1
		}
	}
	if *org != "" {
		id, err := authResolveOrg(origin, key, *org)
		if err != nil {
			return printErr("Could not find the organization", err)
		}
		if args[0] == "add" {
			body["org"] = id
		}
		*org = id
	}
	return authServiceAccountsRun(fs.Arg(0), origin, key, args[0], fs.Arg(1), fs.Arg(2), *org, body)
}

// authServiceAccountBody builds the request for add, update and add-secret.
// The Auth app checks the same rules again; checking here reports a mistake
// before the key prompt.
func authServiceAccountBody(command string, given map[string]bool, name, role string, expires int, disable, enable bool) (map[string]any, error) {
	body := map[string]any{}
	if given["name"] {
		if trimmed := strings.TrimSpace(name); trimmed == "" || len([]rune(trimmed)) > 100 || strings.ContainsFunc(name, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
			return nil, errors.New("--name takes 1 to 100 characters")
		}
		body["name"] = name
	}
	if given["role"] {
		if !authServiceAccountRole.MatchString(role) {
			return nil, fmt.Errorf("--role %q is not a role name", role)
		}
		body["role"] = role
	}
	if given["expires-in"] {
		if expires < 1 || expires > api.AuthServiceAccountSecretMaxDays {
			return nil, fmt.Errorf("--expires-in must be 1 to %d days", api.AuthServiceAccountSecretMaxDays)
		}
		body["expires_in_days"] = expires
	}
	if disable && enable {
		return nil, errors.New("pass --disable or --enable, not both")
	}
	if disable || enable {
		body["disabled"] = disable
	}
	switch {
	case command == "add" && (!given["name"] || !given["role"]):
		return nil, errors.New("add needs --name and --role")
	case command == "update" && len(body) == 0:
		return nil, errors.New("update needs --name, --role, --disable or --enable")
	}
	return body, nil
}

func authServiceAccountsRun(slug, origin, key, command, id, secret, org string, body map[string]any) int {
	path := "/admin/v1/service-accounts"
	if id != "" {
		path += "/" + id
	}
	switch command {
	case "list":
		if org != "" {
			path += "?org=" + url.QueryEscape(org)
		}
		var out struct {
			ServiceAccounts []authServiceAccount `json:"service_accounts"`
		}
		if err := authAdminCall(http.MethodGet, origin, key, path, nil, &out); err != nil {
			return printErr("Could not list service accounts", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(out))
		}
		if len(out.ServiceAccounts) == 0 {
			_, _ = fmt.Fprintf(osStdout, "No service accounts. Add one with `gregale auth service-accounts add %s --org ORG --name NAME --role ROLE`.\n", slug)
		}
		for _, a := range out.ServiceAccounts {
			state := ""
			if a.Disabled {
				state = "  disabled"
			}
			hints := make([]string, 0, len(a.Secrets))
			for _, s := range a.Secrets {
				hints = append(hints, "…"+s.Hint)
			}
			_, _ = fmt.Fprintf(osStdout, "%s  org %s  %-12s  %s  secrets %s%s\n", a.ID, a.OrgID, a.Role, a.Name, authOr(strPtr(strings.Join(hints, ",")), "none"), state)
		}
		return 0
	case "remove", "revoke-secret":
		if command == "revoke-secret" {
			path += "/secrets/" + secret
		}
		status, raw, err := authAdminDo(http.MethodDelete, origin, key, path, nil)
		if err == nil && status != http.StatusNoContent {
			err = authAdminError(status, raw)
		}
		if err != nil {
			return printErr("Could not "+strings.Replace(command, "-", " the ", 1)+" the service account", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(map[string]any{"id": authOrID(secret, id), "deleted": true}))
		}
		if command == "remove" {
			PrintOK(osStdout, "Removed service account %s; its tokens stop working at once", id)
		} else {
			PrintOK(osStdout, "Revoked secret %s; tokens it got stop working at once", secret)
		}
		return 0
	case "update":
		var out authServiceAccount
		if err := authAdminCall(http.MethodPatch, origin, key, path, body, &out); err != nil {
			return printErr("Could not update the service account", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(out))
		}
		state := "enabled"
		if out.Disabled {
			state = "disabled"
		}
		PrintOK(osStdout, "Updated service account %s (%s, %s, %s)", out.ID, out.Name, out.Role, state)
		return 0
	}
	if command == "add-secret" {
		path += "/secrets"
	}
	var out struct {
		ID           string `json:"id"`
		OrgID        string `json:"org_id"`
		Name         string `json:"name"`
		Role         string `json:"role"`
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
		Secret       struct {
			ID        string  `json:"id"`
			ExpiresAt *string `json:"expires_at"`
		} `json:"secret"`
	}
	if err := authAdminCall(http.MethodPost, origin, key, path, body, &out); err != nil {
		return printErr("Could not save the service account", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	if command == "add" {
		PrintOK(osStdout, "Added service account %s (%s, %s) to organization %s", out.ClientID, out.Name, out.Role, out.OrgID)
	} else {
		PrintOK(osStdout, "Added secret %s to service account %s", out.Secret.ID, out.ClientID)
	}
	expiry := "until revoked"
	if out.Secret.ExpiresAt != nil {
		expiry = "until " + *out.Secret.ExpiresAt
	}
	_, _ = fmt.Fprintf(osStdout, "Client ID:     %s\nClient secret (shown only now; valid %s):\n  %s\n", out.ClientID, expiry, out.ClientSecret)
	_, _ = fmt.Fprintf(osStdout, "Get tokens with the client-credentials grant at %s/auth/v1/oauth/token.\n", origin)
	return 0
}

func strPtr(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func authOrID(first, second string) string {
	if first != "" {
		return first
	}
	return second
}

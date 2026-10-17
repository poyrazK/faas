package main

// adr: 1190 — organization single sign-on (SAML) for application Auth. The
// owner registers identity providers through the Auth app's /admin/v1/sso
// with an admin key; provider records stay in the customer's database.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const authSSOUsage = "usage: gregale auth sso <list|add|update|remove> NAME [PROVIDER_ID] [--metadata-url URL | --metadata-file FILE] [--domains DOMAINS] [--org ID|SLUG|none] [--role ROLE] [--admin-key-stdin]"

var authSSOProviderID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type authSSOProvider struct {
	ID           string   `json:"id"`
	EntityID     string   `json:"entity_id"`
	MetadataURL  *string  `json:"metadata_url"`
	Domains      []string `json:"domains"`
	Disabled     bool     `json:"disabled"`
	Organization *struct {
		ID   string `json:"id"`
		Slug string `json:"slug"`
		Role string `json:"role"`
	} `json:"organization"`
}

func cmdAuthSSO(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, authSSOUsage, "auth")
		return 1
	}
	fs := newFlagSet("auth sso "+args[0], flag.ContinueOnError)
	metadataURL := fs.String("metadata-url", "", "the identity provider's HTTPS SAML metadata URL")
	metadataFile := fs.String("metadata-file", "", "a file holding the identity provider's SAML metadata XML")
	domains := fs.String("domains", "", "comma-separated email domains whose users sign in through this provider")
	org := fs.String("org", "", "organization (ID or slug) its users join, or none")
	role := fs.String("role", "member", "role its users join the organization with (not owner)")
	keyStdin := fs.Bool("admin-key-stdin", false, "read the admin key from stdin (prompted on a terminal)")
	if err := parseInterspersed(fs, args[1:]); err != nil {
		return 1
	}
	want := map[string]int{"list": 1, "add": 1, "update": 2, "remove": 2}[args[0]]
	if want == 0 || fs.NArg() != want || !api.ValidAppSlug(fs.Arg(0)) || (want == 2 && !authSSOProviderID.MatchString(fs.Arg(1))) {
		PrintUsage(os.Stderr, authSSOUsage, "auth")
		return 1
	}
	given := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { given[f.Name] = true })
	body, err := authSSOBody(args[0], given, *metadataURL, *metadataFile, *domains, *org, *role)
	if err != nil {
		return printErr("Invalid SSO settings", err)
	}
	key, _, err := readRealtimeEndpointSecret("", *keyStdin, "admin-key", "Auth admin key: ", 128, true)
	if err != nil || !authAdminKeyFormat.MatchString(key) {
		return printErr("Invalid admin key", errors.New("expected a key from `gregale auth admin-key create`"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	app, err := client.GetApp(ctx, fs.Arg(0))
	if err != nil {
		return printErr("Could not find the Auth app", err)
	}
	origin, err := authExternalOrigin(app)
	if err != nil {
		return printErr("Could not resolve the Auth app", err)
	}
	return authSSORun(ctx, app, origin, key, args[0], fs.Arg(1), body)
}

// authSSOBody builds the request for add and update. The Auth app checks the
// same rules again; checking here reports a mistake before the key prompt.
func authSSOBody(command string, given map[string]bool, metadataURL, metadataFile, domains, org, role string) (map[string]any, error) {
	body := map[string]any{}
	if command == "list" || command == "remove" {
		if len(given) > 1 || (len(given) == 1 && !given["admin-key-stdin"]) {
			return nil, fmt.Errorf("%s takes no provider settings", command)
		}
		return body, nil
	}
	switch {
	case given["metadata-url"] && given["metadata-file"]:
		return nil, errors.New("use --metadata-url or --metadata-file, not both")
	case given["metadata-url"]:
		if !strings.HasPrefix(metadataURL, "https://") {
			return nil, errors.New("--metadata-url must be an HTTPS URL")
		}
		body["metadata_url"] = metadataURL
	case given["metadata-file"]:
		xml, err := readBoundedFile(metadataFile, api.AuthSSOMetadataBytes)
		if err != nil {
			return nil, fmt.Errorf("--metadata-file: %w", err)
		}
		body["metadata_xml"] = string(xml)
	case command == "add":
		return nil, errors.New("add needs --metadata-url or --metadata-file")
	}
	if given["domains"] {
		list := []string{}
		for _, domain := range strings.Split(domains, ",") {
			if domain = strings.TrimSpace(domain); domain != "" {
				list = append(list, strings.ToLower(domain))
			}
		}
		if len(list) == 0 || len(list) > api.AuthSSOMaxDomains {
			return nil, fmt.Errorf("--domains takes 1 to %d email domains", api.AuthSSOMaxDomains)
		}
		body["domains"] = list
	} else if command == "add" {
		return nil, errors.New("add needs --domains, the email domains whose users sign in through this provider")
	}
	switch {
	case given["org"] && org == "none":
		if given["role"] {
			return nil, errors.New("--org none takes no --role")
		}
		body["organization"] = nil
	case given["org"]:
		if role == "owner" {
			return nil, errors.New("an identity provider never grants the owner role")
		}
		body["organization"], body["role"] = org, role
	case given["role"]:
		return nil, errors.New("--role needs --org")
	}
	if command == "update" && len(body) == 0 {
		return nil, errors.New("update needs --metadata-url, --metadata-file, --domains or --org")
	}
	return body, nil
}

func authSSORun(ctx context.Context, app api.AppResponse, origin, key, command, id string, body map[string]any) int {
	path := "/admin/v1/sso"
	if id != "" {
		path += "/" + id
	}
	switch command {
	case "list":
		var out struct {
			Providers []authSSOProvider `json:"providers"`
		}
		if err := authAdminCall(http.MethodGet, origin, key, path, nil, &out); err != nil {
			return printErr("Could not list identity providers", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(out))
		}
		authSSOServiceProvider(ctx, app)
		if len(out.Providers) == 0 {
			_, _ = fmt.Fprintf(osStdout, "No identity providers. Add one with `gregale auth sso add %s --metadata-url URL --domains DOMAINS`.\n", app.Slug)
		}
		for _, provider := range out.Providers {
			_, _ = fmt.Fprintf(osStdout, "%s  %s  %s\n", provider.ID, strings.Join(provider.Domains, ","), authSSOOrganization(provider))
		}
		return 0
	case "remove":
		var out map[string]any
		if err := authAdminCall(http.MethodDelete, origin, key, path, nil, &out); err != nil {
			return printErr("Could not remove the identity provider", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(map[string]any{"id": id, "deleted": true}))
		}
		PrintOK(osStdout, "Removed identity provider %s; its users can no longer sign in through it and keep their memberships", id)
		return 0
	}
	method := http.MethodPost
	if command == "update" {
		method = http.MethodPatch
	}
	var out struct {
		Provider     authSSOProvider `json:"provider"`
		MembersAdded int             `json:"members_added"`
	}
	if err := authAdminCall(method, origin, key, path, body, &out); err != nil {
		return printErr("Could not save the identity provider", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	PrintOK(osStdout, "Saved identity provider %s for %s; %s", out.Provider.ID, strings.Join(out.Provider.Domains, ", "), authSSOOrganization(out.Provider))
	if out.MembersAdded > 0 {
		_, _ = fmt.Fprintf(osStdout, "%d existing users of this provider joined the organization.\n", out.MembersAdded)
	}
	if command == "add" {
		authSSOServiceProvider(ctx, app)
	}
	return 0
}

func authSSOOrganization(provider authSSOProvider) string {
	if provider.Organization == nil {
		return "users join no organization"
	}
	return "users join " + provider.Organization.Slug + " as " + provider.Organization.Role
}

// authSSOServiceProvider prints what to register with the identity provider.
func authSSOServiceProvider(ctx context.Context, app api.AppResponse) {
	settings, err := fetchAuthSettings(ctx, app)
	switch {
	case err != nil:
		PrintWarn(osStderr, "could not read the running Auth app (%v)", err)
	case !settings.SSO.Enabled:
		PrintWarn(osStderr, "SSO is off for %s; turn it on with `gregale auth config %s --sso on`", app.Slug, app.Slug)
	default:
		_, _ = fmt.Fprintf(osStdout, "Register %s with the identity provider:\n  entity ID (metadata URL)  %s\n  ACS URL                   %s\n", app.Slug, settings.SSO.EntityID, settings.SSO.ACSURL)
	}
}

func readBoundedFile(path string, limit int) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > int64(limit) {
		return nil, fmt.Errorf("%s exceeds %d bytes", path, limit)
	}
	return os.ReadFile(path)
}

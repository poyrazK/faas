package main

// adr: 1190 — the OAuth 2.1 authorization server of application Auth. The
// owner registers clients through the Auth app's /admin/v1/oauth/clients
// with an admin key; with open registration on, clients such as MCP clients
// also register themselves.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const authOAuthUsage = "usage: gregale auth oauth-clients <list|add|update|remove|rotate-secret> NAME [CLIENT_ID] [--name NAME] [--redirect-uri URI]... [--type public|confidential] [--client-uri URL] [--logo-uri URL] [--admin-key-stdin]"

type authOAuthClient struct {
	ID           string   `json:"id"`
	Name         *string  `json:"name"`
	Type         string   `json:"type"`
	Registration string   `json:"registration"`
	RedirectURIs []string `json:"redirect_uris"`
}

func cmdAuthOAuthClients(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, authOAuthUsage, "auth")
		return 1
	}
	fs := newFlagSet("auth oauth-clients "+args[0], flag.ContinueOnError)
	name := fs.String("name", "", "the client's name, shown on the consent page")
	var redirects multiFlag
	fs.Var(&redirects, "redirect-uri", "an exact redirect URI: HTTPS, HTTP on a loopback host, or an app's own scheme; repeat for more")
	kind := fs.String("type", "public", "public (no secret: apps and agents using PKCE) or confidential (a server that keeps a secret)")
	clientURI := fs.String("client-uri", "", "the client's HTTPS home page")
	logoURI := fs.String("logo-uri", "", "the client's HTTPS logo URL")
	keyStdin := fs.Bool("admin-key-stdin", false, "read the admin key from stdin (prompted on a terminal)")
	if err := parseInterspersed(fs, args[1:]); err != nil {
		return 1
	}
	want := map[string]int{"list": 1, "add": 1, "update": 2, "remove": 2, "rotate-secret": 2}[args[0]]
	if want == 0 || fs.NArg() != want || !api.ValidAppSlug(fs.Arg(0)) || (want == 2 && !authSSOProviderID.MatchString(fs.Arg(1))) {
		PrintUsage(os.Stderr, authOAuthUsage, "auth")
		return 1
	}
	given := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { given[f.Name] = true })
	body, err := authOAuthClientBody(args[0], given, *name, redirects, *kind, *clientURI, *logoURI)
	if err != nil {
		return printErr("Invalid OAuth client", err)
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
	return authOAuthRun(ctx, app, origin, key, args[0], fs.Arg(1), body)
}

// authOAuthClientBody builds the request for add and update. The Auth app
// and GoTrue check the same rules again; checking here reports a mistake
// before the key prompt.
func authOAuthClientBody(command string, given map[string]bool, name string, redirects []string, kind, clientURI, logoURI string) (map[string]any, error) {
	settings := 0
	for flagName := range given {
		if flagName != "admin-key-stdin" {
			settings++
		}
	}
	if command == "list" || command == "remove" || command == "rotate-secret" {
		if settings > 0 {
			return nil, fmt.Errorf("%s takes no client settings", command)
		}
		return nil, nil
	}
	body := map[string]any{}
	if given["name"] {
		if strings.TrimSpace(name) == "" || len(name) > 1024 {
			return nil, errors.New("--name takes 1 to 1024 characters")
		}
		body["client_name"] = name
	}
	if given["redirect-uri"] {
		if len(redirects) > 10 {
			return nil, errors.New("a client takes at most 10 redirect URIs")
		}
		for _, uri := range redirects {
			if err := authOAuthRedirectURI(uri); err != nil {
				return nil, fmt.Errorf("--redirect-uri %q: %w", uri, err)
			}
		}
		body["redirect_uris"] = []string(redirects)
	} else if command == "add" {
		return nil, errors.New("add needs at least one --redirect-uri")
	}
	switch {
	case given["type"] && command == "update":
		return nil, errors.New("a client keeps its type; remove it and add it again")
	case kind != "public" && kind != "confidential":
		return nil, errors.New("--type must be public or confidential")
	case command == "add":
		body["client_type"] = kind
	}
	for flagName, value := range map[string]string{"client-uri": clientURI, "logo-uri": logoURI} {
		if !given[flagName] {
			continue
		}
		u, err := url.Parse(value)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || len(value) > 2048 {
			return nil, fmt.Errorf("--%s must be an HTTPS URL", flagName)
		}
		body[strings.ReplaceAll(flagName, "-", "_")] = value
	}
	if command == "update" && len(body) == 0 {
		return nil, errors.New("update needs --name, --redirect-uri, --client-uri or --logo-uri")
	}
	return body, nil
}

// authOAuthRedirectURI applies GoTrue's rules: an absolute URI without a
// fragment, HTTP only on a loopback host, and no script or data schemes.
func authOAuthRedirectURI(uri string) error {
	u, err := url.Parse(uri)
	switch {
	case err != nil || u.Scheme == "" || u.Host == "" || len(uri) > 2048:
		return errors.New("must be an absolute URI with a host")
	case u.Fragment != "":
		return errors.New("must not have a fragment")
	case strings.Contains(",javascript,data,file,vbscript,about,blob,", ","+strings.ToLower(u.Scheme)+","):
		return fmt.Errorf("the %s scheme is not allowed", u.Scheme)
	case u.Scheme == "http" && u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1" && u.Hostname() != "::1":
		return errors.New("HTTP is allowed only on a loopback host")
	}
	return nil
}

func authOAuthRun(ctx context.Context, app api.AppResponse, origin, key, command, id string, body map[string]any) int {
	path := "/admin/v1/oauth/clients"
	if id != "" {
		path += "/" + id
	}
	switch command {
	case "list":
		var out struct {
			Clients []authOAuthClient `json:"clients"`
		}
		if err := authAdminCall(http.MethodGet, origin, key, path, nil, &out); err != nil {
			return printErr("Could not list OAuth clients", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(out))
		}
		authOAuthServer(ctx, app)
		if len(out.Clients) == 0 {
			_, _ = fmt.Fprintf(osStdout, "No OAuth clients. Add one with `gregale auth oauth-clients add %s --name NAME --redirect-uri URI`.\n", app.Slug)
		}
		for _, c := range out.Clients {
			_, _ = fmt.Fprintf(osStdout, "%s  %-12s  %-7s  %s  %s\n", c.ID, c.Type, c.Registration, authOAuthClientName(c), strings.Join(c.RedirectURIs, ","))
		}
		return 0
	case "remove":
		var out map[string]any
		if err := authAdminCall(http.MethodDelete, origin, key, path, nil, &out); err != nil {
			return printErr("Could not remove the OAuth client", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(map[string]any{"id": id, "deleted": true}))
		}
		PrintOK(osStdout, "Removed OAuth client %s; it can no longer get tokens", id)
		return 0
	}
	method := http.MethodPost
	switch command {
	case "update":
		method = http.MethodPatch
	case "rotate-secret":
		path += "/regenerate-secret"
		body = map[string]any{}
	}
	var out struct {
		Client       authOAuthClient `json:"client"`
		ClientSecret *string         `json:"client_secret"`
	}
	if err := authAdminCall(method, origin, key, path, body, &out); err != nil {
		return printErr("Could not save the OAuth client", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	PrintOK(osStdout, "Saved OAuth client %s (%s, %s)", out.Client.ID, out.Client.Type, authOAuthClientName(out.Client))
	if out.ClientSecret != nil && *out.ClientSecret != "" {
		_, _ = fmt.Fprintf(osStdout, "Client secret (shown only now; it replaces any previous one):\n  %s\n", *out.ClientSecret)
	}
	if command == "add" {
		authOAuthServer(ctx, app)
	}
	return 0
}

func authOAuthClientName(c authOAuthClient) string {
	if c.Name == nil || *c.Name == "" {
		return "unnamed"
	}
	return *c.Name
}

// authOAuthServer prints what a client needs to find the authorization
// server, or how to turn it on.
func authOAuthServer(ctx context.Context, app api.AppResponse) {
	settings, err := fetchAuthSettings(ctx, app)
	switch {
	case err != nil:
		PrintWarn(osStderr, "could not read the running Auth app (%v)", err)
	case !settings.OAuth.Enabled:
		PrintWarn(osStderr, "the OAuth server is off for %s; turn it on with `gregale auth config %s --oauth-server on`", app.Slug, app.Slug)
	default:
		_, _ = fmt.Fprintf(osStdout, "Authorization server for %s:\n  issuer        %s\n  metadata URL  %s\n", app.Slug, settings.OAuth.Issuer, settings.OAuth.MetadataURL)
	}
}

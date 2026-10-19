// commands_ai_tokens.go — `gregale ai tokens` (ADR-1431): access tokens that
// let local development, CI or a notebook call the AI gateway as an app.

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const aiTokensUsage = "usage: gregale ai tokens list --app <slug> | " +
	"gregale ai tokens create --app <slug> --name <name> [--expires-in-hours <1-2160>] [--integration <integration-id>]... | " +
	"gregale ai tokens revoke --app <slug> <token-id>"

func cmdAITokens(args []string) int {
	if len(args) == 0 {
		return printErr("Invalid ai tokens arguments", errors.New(aiTokensUsage))
	}
	fs := newFlagSet("ai tokens "+args[0], flag.ContinueOnError)
	slug := fs.String("app", "", "the app the token acts as (required)")
	name := fs.String("name", "", "token name for create: lowercase letters, digits and hyphens")
	expires := fs.Int("expires-in-hours", api.OutboundAIAccessTokenTTLDefaultHours, "token lifetime for create, 1 to 2160 hours")
	var integrations stringListFlag
	fs.Var(&integrations, "integration", "limit the token to this AI integration (repeat; default: every AI integration bound to the app)")
	if err := parseInterspersed(fs, args[1:]); err != nil || *slug == "" {
		return printErr("Invalid ai tokens arguments", errors.New(aiTokensUsage))
	}
	switch args[0] {
	case "list", "create":
		if rejectUnexpectedFlagArgs(fs) {
			return 1
		}
	case "revoke":
		if fs.NArg() != 1 {
			if fs.NArg() > 1 {
				rejectUnexpectedFlagArgs(fs)
			}
			return printErr("Invalid ai tokens arguments", errors.New(aiTokensUsage))
		}
	default:
		return printErr("Invalid ai tokens arguments", errors.New(aiTokensUsage))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	switch args[0] {
	case "list":
		return cmdAITokensList(client, *slug)
	case "create":
		req := api.CreateOutboundAIAccessTokenRequest{Name: *name, ExpiresInHours: expires, IntegrationIDs: integrations}
		if !req.Valid() {
			return printErr("Invalid token", errors.New(aiTokensUsage))
		}
		created, err := client.CreateOutboundAIAccessToken(context.Background(), *slug, req)
		if err != nil {
			return printErr("Could not create the access token", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(created))
		}
		_, _ = fmt.Fprintf(osStdout, "Access token %s for %s, expires %s:\n%s\n", created.AccessToken.Name, *slug,
			created.AccessToken.ExpiresAt.UTC().Format(time.RFC3339), created.Token)
		_, _ = fmt.Fprintln(osStderr, "Store it now; it is not shown again. Use it as your SDK's API key with the base URL "+
			"https://<AI gateway host>/i/<integration-id> (or /r/<route-id>).")
		return 0
	case "revoke":
		if err := client.RevokeOutboundAIAccessToken(context.Background(), *slug, fs.Arg(0)); err != nil {
			return printErr("Could not revoke the access token", err)
		}
		_, _ = fmt.Fprintf(osStdout, "Access token %s revoked\n", fs.Arg(0))
		return 0
	}
	return printErr("Invalid ai tokens arguments", errors.New(aiTokensUsage))
}

func cmdAITokensList(client *api.Client, slug string) int {
	tokens, err := client.ListOutboundAIAccessTokens(context.Background(), slug)
	if err != nil {
		return printErr("Could not list access tokens", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(tokens))
	}
	if len(tokens.Items) == 0 {
		_, _ = fmt.Fprintln(osStdout, "(no access tokens)")
		return 0
	}
	tw := tabwriter.NewWriter(osStdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ID\tNAME\tPREFIX\tSTATE\tEXPIRES\tLAST USED\tINTEGRATIONS")
	for _, token := range tokens.Items {
		state := "live"
		switch {
		case token.RevokedAt != nil:
			state = "revoked"
		case !token.ExpiresAt.After(time.Now()):
			state = "expired"
		}
		lastUsed := "-"
		if token.LastUsedAt != nil {
			lastUsed = token.LastUsedAt.UTC().Format(time.RFC3339)
		}
		scope := "all"
		if len(token.IntegrationIDs) > 0 {
			scope = strings.Join(token.IntegrationIDs, ",")
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s…\t%s\t%s\t%s\t%s\n", token.ID, token.Name, token.Prefix, state,
			token.ExpiresAt.UTC().Format(time.RFC3339), lastUsed, scope)
	}
	_ = tw.Flush()
	return 0
}

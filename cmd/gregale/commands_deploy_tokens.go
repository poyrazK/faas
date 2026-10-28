package main

// `gregale deploy-tokens` — per-app CI deploy tokens (GET/POST
// /v1/apps/{slug}/deploy-tokens, DELETE …/{id}, POST …/{id}/rotate). The
// capability was GA with no CLI command (#5709). Minting and rotating need a
// MFA step-up from the last 5 minutes (`gregale mfa verify`); the API's refusal says so.

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const deployTokensUsage = "usage: gregale deploy-tokens <list|add|rotate|rm> --app SLUG [args]"

func cmdDeployTokens(args []string) int {
	parent, _ := lookupCliCommand("deploy-tokens")
	if len(args) == 0 {
		PrintUsage(os.Stderr, deployTokensUsage, "deploy-tokens")
		return 1
	}
	switch args[0] {
	case subList:
		return cmdDeployTokensList(args[1:])
	case subAdd:
		return cmdDeployTokensAdd(args[1:])
	case subRotate:
		return cmdDeployTokensRotate(args[1:])
	case subRm:
		return cmdDeployTokensRm(args[1:])
	}
	printCommandValidation(os.Stderr, "unknown deploy-tokens subcommand %q\n", args[0])
	return unknownSubcommandChoices(os.Stderr, "deploy-tokens", parent, args[0])
}

// parseDeployTokenFlags parses --app plus the label/expiry flags a mint takes.
func parseDeployTokenFlags(name, usage string, args []string, minting bool, positionals int) (slug, label, expires string, rest []string, ok bool) {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet(name, flag.ContinueOnError)
	app := fs.String("app", "", "app the token deploys")
	var labelFlag, expiresFlag *string
	if minting {
		labelFlag = fs.String("label", "", "label shown in the list, for example the CI system")
		expiresFlag = fs.String("expires-at", "", "expiry as an RFC3339 time (default 90 days)")
	}
	if err := fs.Parse(flags); err != nil {
		return "", "", "", nil, false
	}
	if rejectUnexpectedFlagArgs(fs) || len(positional) != positionals || *app == "" {
		PrintUsage(os.Stderr, usage, "deploy-tokens")
		return "", "", "", nil, false
	}
	if minting {
		label, expires = *labelFlag, *expiresFlag
		if expires != "" {
			if _, err := time.Parse(time.RFC3339, expires); err != nil {
				printErr("Invalid --expires-at", fmt.Errorf("want an RFC3339 time such as 2027-01-31T00:00:00Z: %w", err))
				return "", "", "", nil, false
			}
		}
	}
	return *app, label, expires, positional, true
}

func cmdDeployTokensList(args []string) int {
	slug, _, _, _, ok := parseDeployTokenFlags("deploy-tokens list", "usage: gregale deploy-tokens list --app SLUG", args, false, 0)
	if !ok {
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := client.ListDeployTokens(context.Background(), slug)
	if err != nil {
		return printErr("Could not list deploy tokens", err)
	}
	if jsonOutput {
		return jsonOut(writeNDJSON(out.Tokens))
	}
	if len(out.Tokens) == 0 {
		printEmptyResult("deploy tokens for app "+slug, "", "gregale deploy-tokens add --app "+slug+" --label ci")
		return 0
	}
	for _, token := range out.Tokens {
		fmt.Fprintf(osStdout, "%-38s %-22s %-10s %-24s %s\n", token.ID, token.Prefix, token.Status, token.Label, deployTokenExpiry(token))
	}
	return 0
}

func cmdDeployTokensAdd(args []string) int {
	const usage = "usage: gregale deploy-tokens add --app SLUG [--label LABEL] [--expires-at RFC3339]"
	slug, label, expires, _, ok := parseDeployTokenFlags("deploy-tokens add", usage, args, true, 0)
	if !ok {
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	token, err := client.CreateDeployToken(context.Background(), slug, api.CreateDeployTokenRequest{Label: label, ExpiresAt: expires})
	if err != nil {
		return printErr("Could not create deploy token", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(token))
	}
	PrintOK(osStdout, "New deploy token for %s (shown ONCE; store it in your CI secrets):\n  %s", slug, token.Plaintext)
	PrintProgress(osStdout, "  id:      %s", token.ID)
	PrintProgress(osStdout, "  expires: %s", deployTokenExpiry(token))
	return 0
}

func cmdDeployTokensRotate(args []string) int {
	const usage = "usage: gregale deploy-tokens rotate <token-id> --app SLUG [--label LABEL] [--expires-at RFC3339]"
	slug, label, expires, positional, ok := parseDeployTokenFlags("deploy-tokens rotate", usage, args, true, 1)
	if !ok {
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.RotateDeployToken(context.Background(), slug, positional[0], api.RotateDeployTokenRequest{Label: label, ExpiresAt: expires})
	if err != nil {
		return printErr("Could not rotate deploy token", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	PrintOK(osStdout, "Rotated deploy token. New token (shown ONCE):\n  %s", resp.TokenPlaintext)
	PrintProgress(osStdout, "  new id:      %s", resp.Token.ID)
	PrintProgress(osStdout, "  old id:      %s", resp.OldTokenID)
	if resp.OldTokenExpires != "" {
		PrintProgress(osStdout, "  old expires: %s", resp.OldTokenExpires)
	}
	return 0
}

func cmdDeployTokensRm(args []string) int {
	slug, _, _, positional, ok := parseDeployTokenFlags("deploy-tokens rm", "usage: gregale deploy-tokens rm <token-id> --app SLUG", args, false, 1)
	if !ok {
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if err := client.RevokeDeployToken(context.Background(), slug, positional[0]); err != nil {
		return printErr("Could not revoke deploy token", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]any{"id": positional[0], "revoked": true}))
	}
	PrintOK(osStdout, "Revoked deploy token %s", positional[0])
	return 0
}

func deployTokenExpiry(token api.DeployTokenResponse) string {
	if token.ExpiresAt == "" {
		return "-"
	}
	return "expires " + token.ExpiresAt
}

package main

// adr: 20261127191645 — `gregale mcp server contract`: show the tool
// contract a listed server's production deploys are held to, and record the
// audited override that lets one breaking deploy ship.

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
)

const mcpContractUsage = "usage: gregale mcp server contract show|allow --app <app> [--reason TEXT] [--expires DURATION]"

func cmdMCPContract(args []string) int {
	if len(args) == 0 {
		return printErr("MCP contract", errors.New(mcpContractUsage))
	}
	verb := args[0]
	fs := newFlagSet("mcp-contract-"+verb, flag.ContinueOnError)
	slug := fs.String("app", "", "app slug")
	reason := fs.String("reason", "", "why paying customers' tools may break (allow)")
	expires := fs.Duration("expires", 0, "how long the override stays usable (allow; default 1h, at most 24h)")
	if err := fs.Parse(args[1:]); err != nil {
		return 1
	}
	if fs.NArg() != 0 || *slug == "" || verb != "show" && verb != "allow" {
		return printErr("MCP contract", errors.New(mcpContractUsage))
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if verb == "allow" {
		override, err := mcpContractAllowBreaking(ctx, c, *slug, *reason, *expires)
		if err != nil {
			return printErr("Could not record the override", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(override))
		}
		renderMCPContractOverride(osStdout, *slug, override)
		return 0
	}
	contract, err := c.GetMCPContract(ctx, *slug)
	if err != nil {
		return printErr("Could not read the tool contract", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(contract))
	}
	renderMCPContract(osStdout, contract)
	return 0
}

// mcpContractAllowBreaking records an override for the app's next breaking
// or unverified production deploy.
func mcpContractAllowBreaking(ctx context.Context, c *Client, slug, reason string, expires time.Duration) (api.MCPContractOverride, error) {
	if strings.TrimSpace(reason) == "" {
		return api.MCPContractOverride{}, errors.New("--reason is required: say why paying customers' tools may break")
	}
	if expires < 0 || expires > api.MCPContractOverrideMaxTTL || expires != 0 && expires < time.Minute {
		return api.MCPContractOverride{}, fmt.Errorf("--expires must be between 1m and %s", api.MCPContractOverrideMaxTTL)
	}
	return c.CreateMCPContractOverride(ctx, slug, api.CreateMCPContractOverrideRequest{Reason: reason, ExpiresInSeconds: int(expires / time.Second)})
}

// mcpListingContract is the discovered catalog as the tool contract the
// listing publishes, so production deploys are compared with what the
// seller saw.
func mcpListingContract(catalog mcphosting.Catalog) (json.RawMessage, error) {
	contract, err := mcphosting.NewCatalogContract(mcphosting.ProtocolVersion, catalog)
	if err != nil {
		return nil, err
	}
	raw, err := mcphosting.MarshalContract(contract)
	if err != nil {
		return nil, err
	}
	if len(raw) > api.MCPListingContractMaxBytes {
		return nil, fmt.Errorf("the server's catalog takes %d bytes; a listing's tool contract holds at most %d", len(raw), api.MCPListingContractMaxBytes)
	}
	return raw, nil
}

func renderMCPContract(w io.Writer, c api.MCPContractResponse) {
	from := fmt.Sprintf("listing version %d", c.ListingVersion)
	if c.BaselineDeploymentID != "" {
		from = "deployment " + c.BaselineDeploymentID
	}
	_, _ = fmt.Fprintf(w, "Contract:  %s (from %s)\nTools:     %d (%s)\n", shortSHA(c.BaselineSHA256), from, len(c.Tools), strings.Join(c.Tools, ", "))
	if c.ProbeSecret != "" {
		_, _ = fmt.Fprintf(w, "Probe:     bearer token from app secret %s\n", c.ProbeSecret)
	}
	if len(c.Overrides) == 0 {
		_, _ = fmt.Fprintln(w, "Overrides: none open")
	}
	for _, o := range c.Overrides {
		_, _ = fmt.Fprintf(w, "Override:  %s until %s by %s: %s\n", o.ID, o.ExpiresAt.Format(time.RFC3339), o.CreatedBy, o.Reason)
	}
	if len(c.Checks) == 0 {
		_, _ = fmt.Fprintln(w, "Checks:    none yet; the next production deploy is compared with this contract")
		return
	}
	_, _ = fmt.Fprintln(w, "Checks:")
	for _, ch := range c.Checks {
		state := "not live"
		if ch.AcceptedAt != nil {
			state = "live " + ch.AcceptedAt.Format(time.RFC3339)
		}
		if ch.Overridden {
			state += ", overridden"
		}
		_, _ = fmt.Fprintf(w, "  %s  %-10s %s (%s)\n", ch.CheckedAt.Format(time.RFC3339), ch.Verdict, ch.DeploymentID, state)
		for _, change := range ch.Changes {
			_, _ = fmt.Fprintf(w, "      %-13s %s %s\n", change.Severity, change.Kind, change.Path)
		}
	}
}

func renderMCPContractOverride(w io.Writer, slug string, o api.MCPContractOverride) {
	_, _ = fmt.Fprintf(w, "The next breaking production deploy of %s may ship until %s (override %s).\n", slug, o.ExpiresAt.Format(time.RFC3339), o.ID)
	_, _ = fmt.Fprintln(w, "Customers get the mcp.contract.changed webhook and see the change on the listing page once it is live.")
}

func shortSHA(sum string) string {
	if len(sum) > 12 {
		return sum[:12]
	}
	return sum
}

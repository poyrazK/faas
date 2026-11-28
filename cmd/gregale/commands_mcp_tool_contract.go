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

const mcpContractUsage = "usage: gregale mcp server contract show|allow --app <app> [--reason TEXT] [--expires DURATION] [--tool-scan]"

func cmdMCPContract(args []string) int {
	if len(args) == 0 {
		return printErr("MCP contract", errors.New(mcpContractUsage))
	}
	verb := args[0]
	fs := newFlagSet("mcp-contract-"+verb, flag.ContinueOnError)
	slug := fs.String("app", "", "app slug")
	reason := fs.String("reason", "", "why paying customers' tools may break, or with --tool-scan why the flagged tool text is safe (allow)")
	expires := fs.Duration("expires", 0, "how long the override stays usable (allow; default 1h, at most 24h)")
	toolScan := fs.Bool("tool-scan", false, "admit unreviewed high-severity tool safety scan findings instead of a breaking change (allow)")
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
		kind := api.MCPContractOverrideKindContract
		if *toolScan {
			kind = api.MCPContractOverrideKindToolScan
		}
		override, err := mcpContractAllow(ctx, c, *slug, kind, *reason, *expires)
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

// mcpContractAllow records an override for the app's next breaking or
// unverified production deploy (kind contract), or for its next one with
// unreviewed high tool safety scan findings (kind tool_scan).
func mcpContractAllow(ctx context.Context, c *Client, slug, kind, reason string, expires time.Duration) (api.MCPContractOverride, error) {
	if strings.TrimSpace(reason) == "" {
		if kind == api.MCPContractOverrideKindToolScan {
			return api.MCPContractOverride{}, errors.New("--reason is required: say why the flagged tool text is safe for customers' models")
		}
		return api.MCPContractOverride{}, errors.New("--reason is required: say why paying customers' tools may break")
	}
	if expires < 0 || expires > api.MCPContractOverrideMaxTTL || expires != 0 && expires < api.MCPContractOverrideMinTTL {
		return api.MCPContractOverride{}, fmt.Errorf("--expires must be between %s and %s", api.MCPContractOverrideMinTTL, api.MCPContractOverrideMaxTTL)
	}
	return c.CreateMCPContractOverride(ctx, slug, api.CreateMCPContractOverrideRequest{Reason: reason, ExpiresInSeconds: int(expires / time.Second), Kind: kind})
}

// mcpDeployOverrides checks that the app has a tool contract and returns the
// hook that binds an override of each kind to each candidate deployment the
// deploy queues (ADR-20261127191645, ADR-20261128135329), so it cannot admit
// another deploy. The candidate's build outlasts the request, so the
// overrides are in place before the gate runs. When an
// override cannot be recorded, the hook cancels that deployment rather than
// let it reach the gate without one.
func mcpDeployOverrides(ctx context.Context, c *Client, slug, reason string, kinds []string) (func(api.DeploymentResponse), error) {
	if _, err := c.GetMCPContract(ctx, slug); err != nil {
		return nil, err
	}
	return func(dep api.DeploymentResponse) {
		for _, kind := range kinds {
			override, err := c.CreateMCPContractOverride(ctx, slug, api.CreateMCPContractOverrideRequest{Reason: reason, DeploymentID: dep.ID, Kind: kind})
			if err != nil {
				_, cancelErr := c.CancelDeployment(ctx, slug, dep.ID, "tool contract override not recorded")
				if cancelErr != nil {
					_, _ = fmt.Fprintf(osStderr, "error: could not record the %s override for deployment %s (%v) nor cancel it (%v); the gate will refuse what it would have admitted\n", kind, dep.ID, err, cancelErr)
					return
				}
				_, _ = fmt.Fprintf(osStderr, "error: could not record the %s override for deployment %s, so it was cancelled: %v\n", kind, dep.ID, err)
				return
			}
			if !jsonOutput {
				PrintProgress(osStdout, "Recorded %s override %s for deployment %s", overrideKindLabel(kind), override.ID, dep.ID)
			}
		}
	}, nil
}

func overrideKindLabel(kind string) string {
	if kind == api.MCPContractOverrideKindToolScan {
		return "tool scan"
	}
	return "tool contract"
}

// mcpListingContractCheckable refuses to publish a contract the deploy gate
// could never check: a server that checks credentials itself needs a probe
// secret for the gate to list its tools.
func mcpListingContractCheckable(mode, probeSecret string) error {
	if mode == mcphosting.AuthExternalOAuth && probeSecret == "" {
		return errors.New("this server checks credentials itself, so production deploys can only be checked with a token: store one as an app secret and pass --probe-secret <NAME>")
	}
	return nil
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
	if c.BaselineToolScan != nil {
		_, _ = fmt.Fprintf(w, "Scan:      %s\n", toolScanLine(c.BaselineToolScan))
	}
	if len(c.Overrides) == 0 {
		_, _ = fmt.Fprintln(w, "Overrides: none open")
	}
	for _, o := range c.Overrides {
		_, _ = fmt.Fprintf(w, "Override:  %s (%s) until %s by %s: %s\n", o.ID, overrideKindLabel(o.Kind), o.ExpiresAt.Format(time.RFC3339), o.CreatedBy, o.Reason)
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
		if ch.ToolScanNewHigh > 0 {
			state += fmt.Sprintf(", %d new high scan finding(s)", ch.ToolScanNewHigh)
		}
		if ch.ToolScanOverridden {
			state += ", scan overridden"
		}
		_, _ = fmt.Fprintf(w, "  %s  %-10s %s (%s)\n", ch.CheckedAt.Format(time.RFC3339), ch.Verdict, ch.DeploymentID, state)
		for _, change := range ch.Changes {
			_, _ = fmt.Fprintf(w, "      %-13s %s %s\n", change.Severity, change.Kind, change.Path)
		}
		if ch.ToolScan != nil {
			for _, f := range ch.ToolScan.Findings {
				if f.Severity == "high" {
					_, _ = fmt.Fprintf(w, "      scan          %s\n", toolScanFindingLine(f))
				}
			}
		}
	}
}

func renderMCPContractOverride(w io.Writer, slug string, o api.MCPContractOverride) {
	if o.Kind == api.MCPContractOverrideKindToolScan {
		_, _ = fmt.Fprintf(w, "The next production deploy of %s with unreviewed high-severity tool scan findings may ship until %s (override %s).\n", slug, o.ExpiresAt.Format(time.RFC3339), o.ID)
		_, _ = fmt.Fprintln(w, "The listing page says the findings were reviewed by the seller.")
		return
	}
	_, _ = fmt.Fprintf(w, "The next breaking production deploy of %s may ship until %s (override %s).\n", slug, o.ExpiresAt.Format(time.RFC3339), o.ID)
	_, _ = fmt.Fprintln(w, "Customers get the mcp.contract.changed webhook and see the change on the listing page once it is live.")
}

// toolScanLine summarises a tool safety scan (ADR-20261128135329).
func toolScanLine(s *api.MCPToolScan) string {
	line := fmt.Sprintf("rules v%d, %s: %d high, %d medium, %d low", s.RuleSet, s.ScannedAt.Format(time.RFC3339), s.High, s.Medium, s.Low)
	if s.Truncated {
		line += " (findings truncated)"
	}
	if s.Review != "" {
		line += "; reviewed: " + s.Review
	}
	return line
}

func toolScanFindingLine(f api.MCPToolScanFinding) string {
	line := fmt.Sprintf("%s %s: %s at %s", f.Kind, f.Name, f.Rule, f.Path)
	if f.Excerpt != "" {
		line += " \u2014 " + f.Excerpt
	}
	return line
}

func shortSHA(sum string) string {
	if len(sum) > 12 {
		return sum[:12]
	}
	return sum
}

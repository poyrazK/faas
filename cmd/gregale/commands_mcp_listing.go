package main

// adr: 20261126175800 — `gregale mcp server listing`: publish, get and
// delete a sold MCP server's public listing, and export its MCP registry
// server.json.

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
)

const mcpListingUsage = "usage: gregale mcp server listing publish|get|delete|export --app <app>"

func cmdMCPListing(args []string) int {
	if len(args) == 0 {
		return printErr("MCP listing", errors.New(mcpListingUsage))
	}
	verb := args[0]
	fs := newFlagSet("mcp-listing-"+verb, flag.ContinueOnError)
	slug := fs.String("app", "", "app slug")
	path := fs.String("path", ".", "MCP source directory with gregale-mcp.json (publish)")
	tokenEnv := fs.String("token-env", "", "environment variable holding a client credential for a gateway-auth server (publish)")
	title := fs.String("title", "", "listing title (publish; default the app slug)")
	summary := fs.String("summary", "", "one or two sentences on what the server does (publish)")
	registry := fs.String("registry-name", "", "MCP registry name, such as io.github.<user>/<server> (publish)")
	showUsage := fs.Bool("show-usage", false, "show the open customer count on the page (publish)")
	probeSecret := fs.String("probe-secret", "", "app secret holding a bearer token the deploy gate sends to list a candidate's tools, for a server that checks credentials itself (publish)")
	if err := fs.Parse(args[1:]); err != nil {
		return 1
	}
	if fs.NArg() != 0 || *slug == "" {
		return printErr("MCP listing", errors.New(mcpListingUsage))
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	switch verb {
	case "publish":
		set := map[string]bool{}
		fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
		req := api.PutMCPListingRequest{Title: *title, Summary: *summary, RegistryName: *registry, ShowUsage: *showUsage, ProbeSecret: *probeSecret}
		// Republishing refreshes the tools and keeps what the flags leave out.
		current, err := c.GetMCPListing(ctx, *slug)
		switch {
		case err == nil:
			req = mcpListingKeep(req, current, set)
		case !isNotFound(err):
			return printErr("Could not read the listing", err)
		}
		if req.Title == "" {
			req.Title = *slug
		}
		return mcpListingPublish(ctx, c, *slug, *path, *tokenEnv, req)
	case "get":
		listing, err := c.GetMCPListing(ctx, *slug)
		if err != nil {
			return printErr("Could not read the listing", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(listing))
		}
		renderMCPListing(osStdout, listing)
		return 0
	case "delete":
		if err := c.DeleteMCPListing(ctx, *slug); err != nil {
			return printErr("Could not unpublish the listing", err)
		}
		_, _ = fmt.Fprintf(osStdout, "Unpublished the listing of %s.\n", *slug)
		return 0
	case "export":
		doc, err := c.GetMCPListingServerJSON(ctx, *slug)
		if err != nil {
			return printErr("Could not read server.json", err)
		}
		_, _ = fmt.Fprintln(osStdout, string(doc))
		return 0
	}
	return printErr("MCP listing", errors.New(mcpListingUsage))
}

// mcpListingPublish lists the live server's tools as the caller and
// publishes them with the listing.
func mcpListingPublish(ctx context.Context, c *Client, slug, path, tokenEnv string, req api.PutMCPListingRequest) int {
	dir, err := filepath.Abs(path)
	if err != nil {
		return printErr("MCP source", err)
	}
	cfg, err := mcphosting.Load(dir)
	if err != nil {
		return printErr("MCP configuration", err)
	}
	if err := mcpListingContractCheckable(cfg.Auth.Mode, req.ProbeSecret); err != nil {
		return printErr("MCP tool contract", err)
	}
	token, err := mcpToken(tokenEnv)
	if err != nil {
		return printErr("MCP client token", err)
	}
	app, err := c.GetApp(ctx, slug)
	if err != nil {
		return printErr("Could not read the app", err)
	}
	endpoint, err := cfg.URL(canonicalAppURL(app))
	if err != nil {
		return printErr("MCP endpoint", err)
	}
	client, err := mcphosting.NewClient(endpoint, token, mcphosting.ProtocolVersion)
	if err != nil {
		return printErr("MCP endpoint", err)
	}
	catalog, exchange, err := client.DiscoverCatalog(ctx)
	if err != nil {
		return printErr("Could not list the server's tools (gateway-auth servers need --token-env)", err)
	}
	for _, rejected := range exchange.RejectedTools {
		// Left out of the listing and its contract, as the deploy gate leaves
		// them out of each candidate's.
		PrintWarn(osStderr, "Tool %s is not listed: %s", rejected.Name, rejected.Reason)
	}
	req.Endpoint, req.Tools = endpoint, mcpListingTools(catalog.Tools)
	if req.Contract, err = mcpListingContract(catalog); err != nil {
		return printErr("MCP tool contract", err)
	}
	listing, err := c.PutMCPListing(ctx, slug, req)
	if err != nil {
		return printErr("Could not publish the listing", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(listing))
	}
	renderMCPListing(osStdout, listing)
	return 0
}

// mcpListingKeep fills the fields whose flags were not given from the
// published listing, so a republish after a tool change keeps the title,
// summary, registry name and usage badge.
func mcpListingKeep(req api.PutMCPListingRequest, current api.MCPListingResponse, set map[string]bool) api.PutMCPListingRequest {
	if !set["title"] {
		req.Title = current.Title
	}
	if !set["summary"] {
		req.Summary = current.Summary
	}
	if !set["registry-name"] {
		req.RegistryName = current.RegistryName
	}
	if !set["show-usage"] {
		req.ShowUsage = current.ShowUsage
	}
	if !set["probe-secret"] {
		req.ProbeSecret = current.ProbeSecret
	}
	return req
}

// mcpListingTools keeps each tool's name, description and annotation title.
func mcpListingTools(tools []mcphosting.Tool) []api.MCPListingTool {
	out := make([]api.MCPListingTool, 0, len(tools))
	for _, t := range tools {
		var annotations struct {
			Title string `json:"title"`
		}
		if len(t.Annotations) > 0 {
			_ = json.Unmarshal(t.Annotations, &annotations)
		}
		out = append(out, api.MCPListingTool{Name: t.Name, Title: annotations.Title, Description: t.Description})
	}
	return out
}

func renderMCPListing(w io.Writer, l api.MCPListingResponse) {
	_, _ = fmt.Fprintf(w, "Listing:   %s (version %d, published %s)\nPage:      %s\nEndpoint:  %s\n", l.Title, l.Version,
		l.PublishedAt.Format(time.RFC3339), l.PageURL, l.Endpoint)
	if l.ServerJSONURL != "" {
		_, _ = fmt.Fprintf(w, "Registry:  %s (%s)\n", l.RegistryName, l.ServerJSONURL)
	}
	names := make([]string, 0, len(l.Tools))
	for _, t := range l.Tools {
		names = append(names, t.Name)
	}
	_, _ = fmt.Fprintf(w, "Tools:     %d (%s)\n", len(l.Tools), strings.Join(names, ", "))
	if l.ShowUsage {
		_, _ = fmt.Fprintln(w, "Usage:     the page shows the open customer count")
	}
	if l.ContractSHA256 != "" {
		_, _ = fmt.Fprintf(w, "Contract:  %s; production deploys that break these tools need gregale mcp server deploy --allow-breaking\n", shortSHA(l.ContractSHA256))
	}
	if l.ProbeSecret != "" {
		_, _ = fmt.Fprintf(w, "Probe:     bearer token from app secret %s\n", l.ProbeSecret)
	}
}

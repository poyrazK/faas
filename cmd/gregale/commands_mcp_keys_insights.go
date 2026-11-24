package main

// adr: 20261123110137 — per-tool insights in the CLI and self-serve MCP
// consumer keys. Both stay behind FAAS_MCP_GATEWAY_PREVIEW_ENABLED with the
// rest of the gateway preview.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
)

// cmdMCPInsights is `gregale mcp server insights`: per-tool calls, failures,
// argument mismatches and estimated compute over the last hour.
func cmdMCPInsights(args []string) int {
	fs := newFlagSet("mcp-insights", flag.ContinueOnError)
	slug := fs.String("app", "", "app slug")
	timeout := fs.Duration("timeout", 30*time.Second, "API deadline")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 || *slug == "" {
		return printErr("MCP insights", errors.New("--app is required"))
	}
	if *timeout <= 0 {
		return printErr("MCP insights", errors.New("--timeout must be positive"))
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	resp, err := c.GetMCPToolInsights(ctx, *slug)
	if err != nil {
		return printErr("Could not fetch MCP tool insights", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	renderMCPInsights(osStdout, resp)
	return 0
}

func renderMCPInsights(w io.Writer, r api.MCPToolInsightsResponse) {
	_, _ = fmt.Fprintf(w, "App:     %s (last %s, compute at %d MB + 8 MB)\n", r.App, r.Window, r.MemoryMB)
	if r.Telemetry == api.MCPToolInsightsTelemetryNotObserved {
		_, _ = fmt.Fprintln(w, "No MCP telemetry in the window: no tool calls, or the server does not report per-tool telemetry.")
		return
	}
	if r.LastEvent != nil {
		_, _ = fmt.Fprintf(w, "Last:    %s\n", r.LastEvent.Format(time.RFC3339))
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "TOOL\tCALLS\tERRORS\tMISMATCHES\tP95\tGB-S\tCOST ≤")
	var total int64
	for _, t := range r.Tools {
		p95 := "-"
		if t.P95MS != nil {
			p95 = fmt.Sprintf("%.0f ms", *t.P95MS)
		}
		total += t.EstimatedMillicents
		_, _ = fmt.Fprintf(tw, "%s\t%.1f\t%.1f\t%.1f\t%s\t%.1f\t%s\n", t.Tool, t.Calls, t.Errors, t.Mismatches, p95, t.GBSeconds, formatMillicentsUSD(t.EstimatedMillicents))
	}
	_ = tw.Flush()
	_, _ = fmt.Fprintf(w, "Total:   ≤ %s at the overage rate; usage inside the plan's included GB-hours costs nothing extra.\n", formatMillicentsUSD(total))
	_, _ = fmt.Fprintln(w, "Counts are Prometheus estimates and may be fractional. Mismatches are calls whose arguments did not match the tool's input schema.")
}

// formatMillicentsUSD prints integer millicents (1/100,000 USD) as dollars.
func formatMillicentsUSD(m int64) string {
	return fmt.Sprintf("$%d.%05d", m/100_000, m%100_000)
}

const mcpKeysUsage = "usage: gregale mcp server keys <create|list|revoke> --app SLUG [flags]"

// cmdMCPKeys is `gregale mcp server keys`: consumer keys for an api-key MCP
// server. Scopes are given without the mcp: namespace and default to the
// endpoint scopes in gregale-mcp.json.
func cmdMCPKeys(args []string) int {
	if len(args) == 0 {
		return printErr("MCP keys", errors.New(mcpKeysUsage))
	}
	verb := args[0]
	fs := newFlagSet("mcp-keys-"+verb, flag.ContinueOnError)
	slug := fs.String("app", "", "app slug")
	path := fs.String("path", ".", "MCP source directory (create: default scopes)")
	consumer := fs.String("consumer", "", "existing API consumer ID")
	newConsumer := fs.String("new-consumer", "", "create an API consumer with this name and give it the key (create)")
	name := fs.String("name", "", "key name (create)")
	scopes := fs.String("scopes", "", "comma-separated MCP scopes without the mcp: prefix (create; default the endpoint scopes)")
	tools := fs.String("tools", "", "comma-separated tools the key may call; grants the endpoint scopes plus their scopes from gregale-mcp.json and refuses if those unlock other scoped tools (create)")
	expires := fs.String("expires", "", "key expiry, RFC3339 (create)")
	key := fs.String("key", "", "key ID (revoke)")
	timeout := fs.Duration("timeout", 30*time.Second, "API deadline")
	if err := fs.Parse(args[1:]); err != nil {
		return 1
	}
	if fs.NArg() != 0 || *slug == "" {
		return printErr("MCP keys", errors.New(mcpKeysUsage))
	}
	if *timeout <= 0 {
		return printErr("MCP keys", errors.New("--timeout must be positive"))
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	switch verb {
	case "create":
		return mcpKeyCreate(ctx, c, mcpKeyCreateOptions{slug: *slug, path: *path, consumer: *consumer, newConsumer: *newConsumer, name: *name, scopes: *scopes, tools: *tools, expires: *expires})
	case "list":
		if *consumer == "" {
			return printErr("MCP keys", errors.New("--consumer is required"))
		}
		list, err := c.ListConsumerKeys(ctx, *slug, *consumer)
		if err != nil {
			return printErr("Could not list keys", err)
		}
		list.Keys = mcpScopedKeys(list.Keys)
		if jsonOutput {
			return jsonOut(writeJSON(list))
		}
		renderMCPKeys(osStdout, list.Keys)
		return 0
	case "revoke":
		if *consumer == "" || *key == "" {
			return printErr("MCP keys", errors.New("--consumer and --key are required"))
		}
		if err := requireMCPKey(ctx, c, *slug, *consumer, *key); err != nil {
			return printErr("MCP keys", err)
		}
		revoked, err := c.RevokeConsumerKey(ctx, *slug, *consumer, *key)
		if err != nil {
			return printErr("Could not revoke key", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(revoked))
		}
		PrintOK(osStdout, "Key %s revoked; the gateway refuses it within the policy convergence window", revoked.Prefix)
		return 0
	}
	return printErr("MCP keys", errors.New(mcpKeysUsage))
}

type mcpKeyCreateOptions struct {
	slug, path, consumer, newConsumer, name, scopes, tools, expires string
}

func mcpKeyCreate(ctx context.Context, c *Client, o mcpKeyCreateOptions) int {
	if (o.consumer == "") == (o.newConsumer == "") {
		return printErr("MCP keys", errors.New("give exactly one of --consumer or --new-consumer"))
	}
	req, err := mcpKeyRequest(o)
	if err != nil {
		return printErr("MCP keys", err)
	}
	consumerID := o.consumer
	if o.newConsumer != "" {
		created, err := c.CreateAPIConsumer(ctx, o.slug, api.CreateAPIConsumerRequest{ExternalRef: o.newConsumer, Name: o.newConsumer})
		if err != nil {
			return printErr("Could not create API consumer", err)
		}
		consumerID = created.ID
	}
	created, err := c.CreateConsumerKey(ctx, o.slug, consumerID, req)
	if err != nil {
		if o.newConsumer != "" {
			PrintWarn(osStderr, "Consumer %s (%s) was created; retry with --consumer %s", o.newConsumer, consumerID, consumerID)
		}
		return printErr("Could not create key", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(created))
	}
	PrintOK(osStdout, "Key %s for consumer %s with %s", created.Prefix, consumerID, strings.Join(created.Scopes, ","))
	_, _ = fmt.Fprintf(osStdout, "  %s\n", created.Key)
	_, _ = fmt.Fprintln(osStdout, "  Shown once. Callers send it as Authorization: Bearer <key>.")
	return 0
}

// mcpKeyRequest builds the key request: names become mcp:<name>, and with no
// --scopes the endpoint scopes from an api-key gregale-mcp.json are used.
func mcpKeyRequest(o mcpKeyCreateOptions) (api.CreateConsumerKeyRequest, error) {
	req := api.CreateConsumerKeyRequest{Name: strings.TrimSpace(o.name)}
	if req.Name == "" || len(req.Name) > api.ConsumerKeyNameMaxBytes {
		return req, fmt.Errorf("--name is required and at most %d bytes", api.ConsumerKeyNameMaxBytes)
	}
	names := splitMCPScopes(o.scopes)
	if o.tools != "" {
		if len(names) > 0 {
			return req, errors.New("give --scopes or --tools, not both")
		}
		toolScopes, err := mcpToolKeyScopes(o.path, splitMCPScopes(o.tools))
		if err != nil {
			return req, err
		}
		names = toolScopes
	}
	if len(names) == 0 {
		dir, err := filepath.Abs(o.path)
		if err != nil {
			return req, err
		}
		cfg, err := mcphosting.Load(dir)
		if err != nil {
			return req, fmt.Errorf("--scopes not given and %s unreadable: %w", mcphosting.ConfigFile, err)
		}
		if cfg.Auth.Mode != mcphosting.AuthAPIKey {
			return req, fmt.Errorf("%s auth mode is %q, not api-key; run `gregale mcp server auth enable --mode api-key` or pass --scopes", mcphosting.ConfigFile, cfg.Auth.Mode)
		}
		names = cfg.Auth.Scopes
	}
	if len(names) == 0 {
		return req, fmt.Errorf("no MCP scopes: pass --scopes or set auth.scopes in %s", mcphosting.ConfigFile)
	}
	for _, n := range names {
		scope := api.ConsumerKeyMCPScopePrefix + strings.TrimPrefix(n, api.ConsumerKeyMCPScopePrefix)
		if !api.ValidConsumerKeyMCPScope(scope) {
			return req, fmt.Errorf("invalid MCP scope %q", n)
		}
		req.Scopes = append(req.Scopes, scope)
	}
	if o.expires != "" {
		at, err := time.Parse(time.RFC3339, o.expires)
		if err != nil {
			return req, fmt.Errorf("--expires: %w", err)
		}
		req.ExpiresAt = &at
	}
	return req, nil
}

func mcpScopedKeys(keys []api.ConsumerKeyResponse) []api.ConsumerKeyResponse {
	out := []api.ConsumerKeyResponse{}
	for _, k := range keys {
		if len(api.ConsumerKeyMCPScopes(k.Scopes)) > 0 {
			out = append(out, k)
		}
	}
	return out
}

func renderMCPKeys(w io.Writer, keys []api.ConsumerKeyResponse) {
	if len(keys) == 0 {
		_, _ = fmt.Fprintln(w, "No MCP keys for this consumer.")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ID\tPREFIX\tNAME\tSCOPES\tLAST USED\tSTATE")
	for _, k := range keys {
		used, state := "-", "active"
		if k.LastUsedAt != nil {
			used = k.LastUsedAt.Format(time.RFC3339)
		}
		if k.RevokedAt != nil {
			state = "revoked"
		} else if k.ExpiresAt != nil && k.ExpiresAt.Before(time.Now()) {
			state = "expired"
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", k.ID, k.Prefix, k.Name, strings.Join(k.Scopes, ","), used, state)
	}
	_ = tw.Flush()
}

// requireMCPKey refuses to revoke a key that grants no mcp: scope, so the MCP
// command cannot revoke an app's billing or admin key by mistake.
func requireMCPKey(ctx context.Context, c *Client, slug, consumer, keyID string) error {
	list, err := c.ListConsumerKeys(ctx, slug, consumer)
	if err != nil {
		return err
	}
	for _, k := range list.Keys {
		if k.ID != keyID {
			continue
		}
		if len(api.ConsumerKeyMCPScopes(k.Scopes)) == 0 {
			return fmt.Errorf("key %s grants no mcp: scope; revoke it with gregale api consumer key revoke", keyID)
		}
		return nil
	}
	return fmt.Errorf("key %s is not a key of consumer %s", keyID, consumer)
}

// mcpToolKeyScopes returns the scopes a key needs to call tools on an
// api-key server: the endpoint scopes and each tool's scopes in
// gregale-mcp.json (ADR-20261123192254). Keys carry scopes, not tool
// lists, so it refuses when those scopes would also unlock a scoped tool
// that was not asked for. Tools that need no scope stay callable by every
// key.
func mcpToolKeyScopes(path string, tools []string) ([]string, error) {
	dir, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	cfg, err := mcphosting.Load(dir)
	if err != nil {
		return nil, fmt.Errorf("--tools needs %s: %w", mcphosting.ConfigFile, err)
	}
	if cfg.Auth.Mode != mcphosting.AuthAPIKey {
		return nil, fmt.Errorf("%s auth mode is %q, not api-key", mcphosting.ConfigFile, cfg.Auth.Mode)
	}
	scopes := slices.Clone(cfg.Auth.Scopes)
	for _, tool := range tools {
		required, ok := cfg.Auth.ToolScopes[tool]
		if !ok {
			return nil, fmt.Errorf("tool %q is not in auth.tool_scopes of %s", tool, mcphosting.ConfigFile)
		}
		scopes = append(scopes, required...)
	}
	slices.Sort(scopes)
	scopes = slices.Compact(scopes)
	var extra []string
	for tool, required := range cfg.Auth.ToolScopes {
		if len(required) == 0 || slices.Contains(tools, tool) {
			continue
		}
		if !slices.ContainsFunc(required, func(s string) bool { return !slices.Contains(scopes, s) }) {
			extra = append(extra, tool)
		}
	}
	if len(extra) > 0 {
		slices.Sort(extra)
		return nil, fmt.Errorf("scopes %s would also let the key call %s; give those tools scopes of their own in %s or include them in --tools",
			strings.Join(scopes, ","), strings.Join(extra, ","), mcphosting.ConfigFile)
	}
	return scopes, nil
}

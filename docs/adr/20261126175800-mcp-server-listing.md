# ADR-20261126175800: Public listing page for sold MCP servers

- Status: Proposed (dark: API monetization routes)
- Date: 2026-11-26
- Related: ADR-20261124094745 (self-serve consumers), ADR-20261021143000
  (consumer sign-up page), ADR-20261126082046 (seller stats),
  ADR-20261123192254 (MCP per-tool billing)

## Context

A seller can sell an MCP server end to end: strangers sign in, use a free
plan, get an upgrade prompt and pay. Nobody can find it. The seller has to
write a page with the server URL, the tools, the prices and the client
configuration by hand, and keep it in step with the server. Gregale already
hosts the customer side of the sale (the sign-up and upgrade pages) and
knows the plans and prices; it does not store the server's tools.

## Decision

Owner decisions (2026-11-26): the tools are a snapshot taken when the seller
publishes; a listing exists only once the seller publishes it; the page shows
tools, plans, sign-up, install links, an MCP registry `server.json` and an
optional usage badge; Gregale hosts it next to the sign-up page.

1. **Publish.** `gregale mcp server listing publish --app <app>` reads the
   app's `gregale-mcp.json`, lists the live server's tools as the seller
   (`--token-env` for a gateway-auth server, as `mcp server replay` does)
   and sends `PUT /v1/apps/{slug}/mcp-listing` with the endpoint, a title,
   a summary, the tools (name, title, description), an optional registry
   name and whether to show the usage badge. Republishing replaces the
   snapshot; `DELETE` (`listing delete`) takes the page down. The owner
   routes take MFA, deploy-write scope (read scope for `GET`) and the
   app-security permission for organization members, like
   `monetization-funnel`. They are part of the API monetization surface.
2. **Endpoint.** The endpoint must be https on the app's own host (its
   platform URL or default custom domain): a listing never points elsewhere.
3. **Public routes** (no credential, the sign-up page's rate limit, only
   while the listing is published, the account has API monetization and
   the app has sign-up settings):
   - `GET /v1/consumer-signup/{slug}/listing`: JSON with the title,
     summary, endpoint, tools, the plans the sign-up page offers (prices
     from the rate cards, ADR-950–953), install links, the sign-up page
     URL and, when the seller opted in, the open customer count;
   - `GET /v1/consumer-signup/{slug}/listing/page`: the HTML page;
   - `GET /v1/consumer-signup/{slug}/server.json`: the server in the MCP
     registry format (`streamable-http` remote), when the seller set a
     registry name. Gregale does not submit it: registry namespaces prove
     ownership of a GitHub account or domain the seller holds.
4. **Install links.** Cursor
   (`https://cursor.com/en/install-mcp?name=…&config=<base64 {"url"}>`),
   VS Code (`https://vscode.dev/redirect/mcp/install?name=…&config=…`) and
   a JSON client configuration, built from the endpoint.
5. **Usage badge.** Off by default; when on, the page shows the number of
   open customers. Nothing else (revenue, units) is public.
6. **Storage.** One row per app in `mcp_listings` (snapshot as JSON),
   written by apid only; bounds in `pkg/api/limits.go`
   (`MCPListing*`: tools, field lengths, snapshot bytes).
7. **Ownership.** apid only; new logic in `pkg/apid/mcplisting`
   (ADR-982). The public routes live with the sign-up page in
   `pkg/apid/consumersignup`.

## Compatibility

Expand-only migration (new table). New routes, CLI commands and SDK
methods; older CLIs ignore them. Deploy order: migration, apid, then the
CLI.

## Consequences

- A sold MCP server gets a shareable page and one-click installs without
  the seller writing any.
- The tool list can go stale until the seller republishes; the page shows
  when it was published.
- Registry listing stays the seller's step, with the file ready.

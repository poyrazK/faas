# ADR-20261030065746: The hosted MCP server accepts Client ID Metadata Documents and names its issuer

Date: 2026-10-30
Status: Accepted
Amends: ADR-20261026195952 (hosted management MCP: who can connect)
Related: ADR-20261028205920 (sliding connections), ADR-1238 (delegated OAuth)

## Context

Clients of the hosted MCP server register themselves through Dynamic Client
Registration: an open endpoint, rate-limited to 20 registrations per address
per hour, that issues an opaque `client_id`. The 2026-07-28 MCP authorization
spec deprecates it in favor of Client ID Metadata Documents: the `client_id` is
an HTTPS URL, and the server fetches the client's metadata from it. The spec
also asks servers to send `iss` (RFC 9207) on authorization responses so a
client can tell which server answered. Cloudflare, Railway, Sentry and Linear
already accept documents.

While reviewing registration we found that a registered client expired after
24 hours, and a restart restored only grants whose client was still
registered. With connections now lasting up to 30 days (ADR-20261028205920),
a restart dropped every connection older than a day.

## Decision

### 1. Documents beside registration

The hosted server (`mcp-bridged`) advertises
`client_id_metadata_document_supported` and accepts a `client_id` that is an
HTTPS URL with a path and no credentials, fragment or dot segments. Dynamic
registration stays for clients that only do that; the spec keeps it for
backward compatibility.

On authorization the server fetches the document unless a fresh copy is
cached:

- only from public addresses, checked after DNS at every connection
  (`pkg/publicdest`, moved out of `pkg/outbound` so the daemon does not link
  that package's dependencies);
- HTTPS, no redirects, at most 5 KiB, a 5-second timeout;
- an uncached fetch counts against the address's registration budget;
- cached for its `max-age`, between 5 minutes and 24 hours (1 hour without
  one).

The document must name its own URL as `client_id`, list 1 to 16 redirect URIs,
and describe a public client (`token_endpoint_auth_method` none, code grants
only).

### 2. The operator's redirect allowlist still applies

Of the document's redirect URIs, only those the server already allows
(Claude's and ChatGPT's published callbacks and loopback, as for registered
clients) are kept; a document with none is refused. A document proves which
site a client is; it does not open the server to new web clients. The consent
page shows that site: "Published by claude.ai".

### 3. Every authorization response names its issuer

Approval and denial redirects carry `iss`, the server's issuer, and the
metadata advertises `authorization_response_iss_parameter_supported`.

Codex then registers a bare loopback `/callback` instead of adding a random
final path component, which was its defense against issuer mix-up. The
operator's `http://127.0.0.1/callback/*` loopback rule therefore accepts the
bare path as well as one plain component.

### 4. A client lasts as long as its grants

Approving or sliding a grant extends its client's registration to the grant's
expiry, and a restart restores a client for as long as any stored grant it
holds. Connections survive restarts for their whole lifetime.

## Consequences

- Clients that support documents connect without the registration endpoint;
  the endpoint stays for the rest.
- The control plane makes outbound HTTPS requests to client sites, bounded as
  above.
- A client can change its name or redirect URIs by updating its document; the
  change applies after the cache expires.

## Not included

- Removing Dynamic Client Registration.
- Accepting redirect URIs outside the operator's allowlist.
- Private-key-JWT client authentication for confidential clients.

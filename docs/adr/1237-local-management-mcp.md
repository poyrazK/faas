# ADR-1237: Local MCP access to the Gregale customer API

- Status: Accepted
- Date: 2026-10-11
- Related: ADR-426; API-hosting roadmap phase 6
- Amends: none
- Extended by: [ADR-1238](1238-management-mcp-releases-and-oauth.md)

## Context

Gregale can host customer MCP applications and its CLI can inspect their
catalogs. Coding agents also need to inspect Gregale itself: discover apps,
find deployment failures, and read logs and metrics through the customer's
existing account permissions.

## Decision

Add `gregale mcp serve --transport stdio`, using the official Go MCP SDK pinned
to the same version as the Go hosting starter. The CLI selects the connection
profile and credential through its existing login/configuration code, then
passes an authenticated `pkg/api.Client` to `pkg/mcpmanagement`.

Expose seven read tools: connection context, app list/detail, deployment
list/detail, bounded app log snapshots, and app metrics. All handlers call
existing GET APIs. Tool discovery performs no API request. `apid` retains
ownership, scope, and entitlement checks; MCP annotations do not authorize
calls. The management adapter imports no daemon or database implementation.

Project app, account, and deployment responses to the fields needed for these
workflows. Keep known-credential redaction, structured API error codes,
per-call deadlines, request cancellation, bounded output, and log truncation
metadata at the MCP boundary. The server never initiates an interactive login
and stdout carries protocol messages exclusively.

## Consequences

Local agents reuse the installed CLI and its connection profiles. Management
inspection requires no deployed MCP application or MCP-hosting plan. The server
has the same account-wide credential access as the selected connection, while
the exposed operations are read-only. Logs may contain unrecognized secrets
and untrusted application text; redaction is not a promise to detect all secrets.

Remote Streamable HTTP, delegated OAuth credentials, and deployment mutations
are later slices. Mutation handlers must reuse the HTTP/CLI release contract,
including idempotency, explicit target/source identity, and serving-revision
guards. They are not part of this read-only release.

## Validation

An official SDK client exercises all tools against an authenticated test API
under modern and legacy protocol versions. A stdio subprocess acceptance test
checks CLI dispatch, named profile selection, protocol-only stdout, and clean
EOF shutdown. Failure tests cover authorization, pagination, invalid paths,
timeouts, redaction, degraded logs, and response bounds.

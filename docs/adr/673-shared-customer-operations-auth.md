# ADR-673: Shared customer Operations browser auth

## Status

Implemented locally — 2026-10-07. Operations admission remains closed.

## Context

The HTTP, Job and workflow feature starters each provided a short-lived token
form and asked application developers to replace it with their own login
integration. Each starter needs the same behavior: resolve a current
tenant-bound credential for every API request and close the old feature session
when the customer signs out or changes identity.

## Decision

Add `CustomerOperationAuth` to the browser-safe Node SDK Operations entry point.
An application provider implements `getCredential(): string | Promise<string>`
and `onIdentityChange(listener): unsubscribe`. The helper validates the
credential on demand and wraps the unsubscribe function so it is safe to call
more than once. A fallback callback supports the development token forms; the
helper does not persist credentials.

Give all three starters the same `public/customer-auth.mjs` adapter file. When a
provider is configured, the UI hides and disables the token field, fetches a
credential before connecting, and passes the helper to `GregaleOperationClient`
for subsequent requests. Identity change closes the previous browser session,
clears its displayed operation data and requires a new connection. Disconnecting
the Operations feature does not sign the customer out of the host application.
The server continues to serve only its explicit browser asset allowlist.

No API, database, platform identity or admission contract changes. The host
application remains responsible for returning the correct customer-scoped token
and reporting identity changes. Account API keys remain on its backend.

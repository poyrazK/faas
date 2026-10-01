# ADR-393 · Durable TCP listener TLS intent

Status: proposed

## Context

A TCP listener needs durable TLS policy across edge restarts. A policy change must not leave an endpoint enabled under its previous negotiation settings. Existing listeners must continue using passthrough unless termination is explicitly selected.

## Decision

Add `tls_mode` and `tls_hostname` to TCP listener intent using an append-only migration. Normalize policy on creation and updates. Existing rows default to passthrough with an empty hostname. Database constraints independently reject unknown modes, malformed DNS hostnames, IP-shaped names, and passthrough policies carrying a hostname.

Update TLS intent and disable the listener in one store mutation. A separate authorized enable action must follow any TLS-policy change. Both PostgreSQL and memory stores return the new disabled intent. This store operation does not establish domain ownership; API validation and the public edge must enforce that before enabling or admitting traffic. Certificates and private keys are absent from intent.

Update the schema snapshot and regenerate sqlc models without changing unrelated schema objects. This persistence foundation does not enable TLS in the customer API or public listener runtime. Gregale retains Linux/amd64 hosts and stateless guest storage.

## Validation

Store tests cover legacy defaults, normalization, persistence round trips, invalid policy rejection, and atomic disabling on policy change. Real PostgreSQL validation uses a disposable local database with isolated test schemas; native VM qualification remains pending a designated KVM host.

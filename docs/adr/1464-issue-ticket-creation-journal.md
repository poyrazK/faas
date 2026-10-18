# ADR 1464: Explicit provider ticket creation with durable CLI recovery

Status: Accepted

## Decision

Add `gregale issues tickets create` for public GitHub repositories and Linear teams. Operators supply an explicit title, destination, stable request UUID and a credential environment-variable name. GitHub uses a token with repository Issues write permission; Linear uses a personal API key with issue creation permission. Credentials remain in the calling process and are never stored in the journal or Gregale. Native organization connection management, GitHub App RPC support, OAuth and bidirectional status synchronization remain separate increments.

Read the Gregale app and issue before creation and bind the operation to the API base, app UUID, requested issue UUID and complete normalized ticket inputs. Keep a private per-profile journal directory, with a cross-process lock per request UUID. Persist and sync the dispatch state before the external write; atomically persist and sync the confirmed provider identity before linking through the existing tracker-reference API. Identical retries reuse the result; changing inputs under the same request UUID conflicts. Missing provider credentials do not block resuming a confirmed ticket's Gregale attachment. A completed operation returns its recorded reference rather than dispatching effects again.

Provider clients use fixed HTTPS API origins, bounded response bodies, a 15-second timeout, no redirects and sanitized errors. Send only the explicit title/description and a stable operation marker, never automatic stacks, tags, request context or customer identities. GitHub responses must match the requested repository and carry the marker. Linear creation supplies the request UUID as the issue UUID and verifies it plus the team in the response. Its [IssueCreateInput contract](https://github.com/linear/linear/blob/master/packages/sdk/src/_generated_documents.ts) requires UUID v4; reject other request UUID versions before dispatch.

An explicit HTTP 400/401/403/404/422 rejection allows another attempt under the same request identity, including after credential rotation. A transport failure, unexpected response, rate limit, server failure or crash after dispatch leaves an uncertain outcome and never triggers another create. Recovery requires an explicit existing ticket URL: read the GitHub issue and verify repository, issue type and operation marker, or query the Linear issue by the stable UUID and verify the team and URL. Retain uncertain records even when no matching ticket can be found; there is no unsafe force-create escape hatch.

## Consequences

The CLI can create and link real provider tickets today, including resuming a failed Gregale attachment without duplicating tickets. Journals contain ticket input text and provider URLs, require private permissions and must survive retries. This guarantee applies to a retained journal and a reused request UUID, not to multiple machines with separate journals or a newly generated UUID. Local journal loss can lose recovery evidence. GitHub has no atomic idempotency key; a dispatch crash before sending can require manual investigation even when no ticket exists. The journal reports that uncertainty honestly. Completed journal records do not recreate manually removed tracker references; use the existing links commands for later reference edits.

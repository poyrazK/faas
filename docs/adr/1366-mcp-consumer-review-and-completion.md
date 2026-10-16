# ADR-1366: MCP consumer review and completion

Status: Accepted

## Context

The consumer get/watch/ACK/recovery/retirement tools are available, but MCP review
prompts and argument completion focus on deployment workflows. Agents need
current scoped review guidance and a way to discover existing consumer names
without treating prompt retrieval or suggestions as processing authorization.

## Decision

Register `gregale_activity_consumer_review` with four required arguments: app,
project, named environment and consumer. Reject unknown or malformed arguments.
Inspect the selected consumer using the same current identity, feed-access,
durable grant/storage, revision and generation checks as exact consumer gets.
Refactor that inspection from the get tool's metric sampling so prompt retrieval
does not create consumer-get lag observations.

Return a sanitized metadata snapshot and static status-specific review guidance.
Uninitialized progress requires choosing a baseline explicitly after processing
its complete contents. Ready progress resumes through the consumer-aware watch,
with explicit ACK only after processing a page. Expired progress requires complete
baseline recovery and baseline_rebuilt=true on the explicit ACK. Explain refresh
on revision/generation conflicts, restart of overtaken baseline discovery,
retained versus expired lag, point-in-time observations and replay/idempotency.
Do not include cursors, event contents or ACK drafts in the review snapshot.
Do not process events, advance progress, retire consumers or grant permission.
Treat all snapshot content as data, not instructions.

Extend completion for the new prompt with consumer-name suggestions. Require a
complete app/project/named-environment selector before reading consumer names.
Require project/environment before suggesting this prompt's app, retaining the
existing named-resource behavior. Reuse existing app/project/environment
completion; never infer default scope for a partial named selection. Validate
consumer context names and bound consumer prefixes to the existing name limit.

For complete selectors, verify current native read access at a fresh scoped head,
then inspect the bounded connection namespace under the journal mutex and current
durable grant checks. Filter every endpoint/account/immutable app/project/env/
scope binding. Inspect at most the existing 100 consumer records, then sort,
deduplicate and prefix-filter with the existing 100-result completion bound.
This finds matching names beyond the first 50-entry list page. Emit names only;
omit names changed by existing credential redaction rather than returning an
unusable redacted label. Expired consumers remain discoverable and suggestions do not claim readiness.
Incomplete selectors return empty values; failed permission/storage reads are
errors, never cached suggestions. Separate profiles or OAuth grants remain
isolated. Completion is a point-in-time hint, not a frozen list or permission.

## Consequences

The prompt catalog grows from four to five; tool and resource catalogs do not
change. No native API, journal-format change, database migration, background
poller, processing lease or new quota is needed. Prompts and completion use short
request admission, observation drain and existing authorization deadlines.
Retrieving guidance leaves encrypted state unchanged, even for a new name.
Existing get/watch/ACK revision and generation fences remain authoritative.

Standard MCP SDK tests exercise in-memory and Streamable HTTP retrieval with
modern and legacy protocol versions, all three progress states, expired-name
completion, bounded full-namespace prefix matching, immutable-binding and
connection isolation, incomplete/invalid selectors, permission loss, in-flight
grant revocation, target recreation, retirement conflicts and unavailable
storage. Pinned Codex prompt/completion UI exposure remains client-dependent;
these protocol checks do not claim model-driven processing or native/provider
qualification.

# ADR-734: Durable lifecycle successor compatibility approvals

Status: Accepted

## Context

ADR-733 conservatively blocks changed successor URLs in an enforced canary
advance. Advisory migration reviews cannot authorize these changes. Owners need
an authenticated, inspectable review tied to the evidence the server checks.

## Decision

Add owner/account-admin lifecycle approval and read endpoints and Gregale
prepare-approval, approve and receipt commands. The preparation command reads
server evidence and writes a request for review; it grants no authority.

Store append-only compatibility receipts with the authenticated approver,
server time, normalized explicit mappings, mapping digest and checker version.
Require exact authoritative capture hashes, canary gate revision, saved route
intent revision, removal policy revision and configured route-policy fingerprint.
The first checker supports inline rooted OpenAPI operations on the same app's
canonical HTTPS production host (default/prod/production scopes). Reject
external/custom hosts, query/fragment URLs, missing response contracts and any
inconclusive or breaking request, response, method, path-parameter or security
comparison. All successor changes for that baseline/candidate pair must be
mapped exactly. Resolve other lifecycle findings before approving.

Approval runs inside account/app/rule, deployment and capture locks. Receipt
persistence and input checks share that transaction. Receipts expire after one
hour. Replacing or deleting either capture permanently marks all affected
receipts invalid, including replacement with identical bytes and later restores.

Canary advances recompute current declaration and policy bindings inside their
existing transaction. Accept matching unexpired checker-version-1 receipts for
each applicable serving or retained baseline independently. Only successor
review findings are cleared; accepted receipt IDs are recorded in the decision
and traffic audit. Receipt reads retain expired/stale evidence for inspection;
readability does not establish current validity. All other gates remain active.

## Consequences

Owners can evolve same-app successor declarations in enforce mode without
switching to report mode. Policy/capture changes require a fresh pinned review;
prepare after selecting the intended gate mode. Changed scopes and new serving
baselines are never covered by another deployment's receipt. The result is
supported declared compatibility, not proof of runtime equivalence or absence
of clients. Cross-app/custom-host/environment successors and wider rollout
boundaries remain subsequent work. This approval cannot authorize removal.

# Typed outbound actions (SDK preview)

Node and Python can prepare the same typed external action, obtain human approval
for its exact input, dispatch through Gregale's credential relay and recover a
lost provider response through reads. Action intents, workflow attempts and
observations share an application-owned PostgreSQL protocol.

## Install and configure

Apply [outbound-actions.sql](../sdk/request-contracts/outbound-actions.sql) with
the application's migration role, alongside the existing pipeline tool and gate
schemas. `outboundActionSchema` / `OUTBOUND_ACTION_SCHEMA` exports the same DDL.
Constructors never migrate. Query adapters must use acknowledged autocommit
statements on one primary, without automatic mutation retries or ambient
transactions. Set driver statement and lock timeouts.

Use a managed [outbound integration](outbound-integrations.md) bound to the app.
The runtime reads `GREGALE_OUTBOUND_URL`, or accepts a backend-configured relay
URL for local development. Requests go to `/i/<integration-id>/<provider-path>`.
The relay adds the provider credential and enforces route policy and budgets.
SDK action inputs, receipts, MCP calls and workflow journals contain no provider
authorization headers. Keep relay URLs and integration configuration outside
model arguments.

The first built-in adapter supports Stripe API v1 refunds. Pin a Stripe account
and API version explicitly. Its integration needs `GET /v1/account`,
`GET /v1/refunds` (including refund retrieval) and `POST /v1/refunds`. Configure
the credential for the intended Stripe account and test/live environment. Connect
account switching and transfer/application-fee reversals are outside this adapter.

```ts
import {defineOutboundAction, stripeRefundContract, stripeRefundActionProvider,
  postgresOutboundActionStore, createOutboundActionMCP} from '@gregale/sdk-node/preview';

const refunds = defineOutboundAction(stripeRefundContract, {
  name: 'order_refund', revision: 'v1', scope, token: backendToken,
  integrationId,
  provider: stripeRefundActionProvider({accountId, apiVersion}),
  store: postgresOutboundActionStore(query), toolStore, gateStore,
  reviewBaseURL: 'https://app.example.test/refund-review', requiredRoles: ['reviewer'],
  authorize: authorizeCurrentRefundRequest,
  authorizeExecution: authorizeCurrentPaymentAndRecovery,
});
```

Python exposes `OutboundAction[Input, Output, Access]`,
`PostgresOutboundActionStore`, `StripeRefundActionProvider` and
`stripe_refund_contract` in `faas_sdk.preview`, with corresponding snake-case
arguments. See the complete [Node](../examples/sdk-csv-jobs/refund.mjs) and
[Python](../examples/sdk-csv-jobs/refund_backend.py) definitions.

The refund input requires `payment_intent`, positive integer `amount_minor`,
lowercase `currency` and `reason` (`requested_by_customer` or `duplicate`). Amount
is explicit even for full refunds. Bind it to a trusted payment/refund request;
provider metadata and a caller-supplied identifier grant no ownership.

## Prepare, approve and dispatch

`prepare(input, requestId, access, options)` / Python
`prepare(input, request_id, access, timeout=...)` saves an immutable intent and
returns `{action_id, binding_sha256, outcome}`. The request UUID belongs to a
durable application business record. Authorization must compare supplied input
with that record and check current access, including duplicate prepares.
Changing scope, owner, configuration, revision, input or encoded request conflicts.

```ts
const prepared = await refunds.prepare(input, savedRefundRequestId, session,
  {timeoutMs: 30_000});
const ref = {action_id: prepared.action_id, binding_sha256: prepared.binding_sha256};
await refunds.tool.start(ref, savedWorkflowAttemptId, session, {timeoutMs: 30_000});
const mcp = createOutboundActionMCP(refunds);
```

```python
prepared = await refunds.prepare(input, saved_refund_request_id, session, timeout=30)
ref = {k: prepared[k] for k in ("action_id", "binding_sha256")}
await refunds.tool.start(ref, saved_workflow_attempt_id, session, timeout=30)
mcp = OutboundActionMCP(refunds)
```

The business request UUID and workflow attempt UUID serve different purposes.
Retain both. Native starts use the existing [pipeline tool](sdk-pipeline-tools.md)
receipt protocol. An uncertain workflow start keeps its UUID and reconciles the
exact native run. A terminal failed workflow can use an explicitly chosen new
workflow attempt and fresh human approval, while retaining the original action.
Creating another business request can repeat an external effect; authorize that
as a new business decision.

Publish the returned `definition` as the native `action-<name>` code workflow
through the existing gateway-checked workflow handler. It opens the `review`
gate using `reviewContract` / `review_contract`, containing the exact business
input, revision and binding hash. The authenticated human review service must
display that input and enforce current reviewer roles. No MCP approval tool is
added. Public lifecycle methods reauthorize current access. Backend execution
also checks current application policy before verifying the provider and again
before dispatch.

`execute(actionId, runId, options)` / `execute(action_id, run_id, timeout=...)`
is the backend/Job entry. It requires an active native action workflow, its
retained exact envelope binding and the approved stored gate for that run, owner,
scope, contracts, input and policy. A bare run ID or model approval field cannot
authorize a write. Failed or cancelled native workflows cannot execute effects.

## Receipt and recovery behavior

One acknowledged compare-and-swap from `prepared` to `unknown` can issue one SDK
POST, with `Idempotency-Key: gregale-action:<action-id>`. The claim commits before
the request. Concurrent SDKs, lost claims and restarted workers cannot acquire
another dispatch claim. Provider and application records are separate
transactions; uncertainty remains explicit until provider evidence resolves it.

| State | Meaning |
| --- | --- |
| `prepared` | Intent retained; no dispatch claim has been consumed. |
| `unknown` | Dispatch/response cannot be established from retained evidence. |
| `pending` | Provider object is known and still processing. |
| `succeeded` | Validated provider evidence confirms success. |
| `failed` | Provider rejects the request or reports a failed/cancelled object. |
| `needs_review` | Evidence is conflicting, malformed, ambiguous or requires human action. |
| `cancelled` | Application cancelled the intent before dispatch. |

`inspect` reads the current receipt. `reconcile` uses provider GETs and updates
the observation with a versioned compare-and-swap. It never sends a POST.
Verified webhook handlers can call `reconcile` as a wakeup after authenticating
delivery through the existing Stripe webhook adapter and reauthorizing the
application action. Webhook payloads do not directly overwrite action outcomes.

Stripe reconciliation retrieves a known refund ID, or scans bounded refund pages
for the original PaymentIntent. Metadata must bind the action ID and exact input
digest; amount, currency and payment must also match. Missing evidence remains
unknown, multiple matches require review, and a read cannot regress a retained
terminal outcome. The adapter verifies the currently bound Stripe account before
dispatch and reconciliation.

There is no automatic replay of an uncertain write, including after a provider's
idempotency window. Stripe API v1 keys can be pruned after 24 hours; reusing a
pruned key creates a new request. See [Stripe idempotency](https://docs.stripe.com/api/idempotent_requests),
[refund creation](https://docs.stripe.com/api/refunds/create),
[refund listing](https://docs.stripe.com/api/refunds/list), and the
[official account client](https://github.com/stripe/stripe-node/blob/master/src/resources/Accounts.ts).

`cancel` / the MCP `_discard` operation only cancels prepared intents. Cancellation
after a claim cannot recall an external request. Deadlines or AbortSignal/asyncio
cancellation after dispatch return the retained unknown observation; callers can
subsequently inspect/reconcile it. A lost observation-write acknowledgement can
also return unknown even when a later read finds the saved terminal result.

The workflow result records what was observed during that workflow. Later
reconciliation updates the action receipt; use `inspect` or MCP `_observe` to
read that current outcome. A completed workflow may therefore retain an earlier
unknown result while its action has subsequently been confirmed.

## MCP, providers and bounds

Register `createOutboundActionMCP` / `OutboundActionMCP` with the existing
authenticated MCP host. It adds `_prepare`, `_observe`, `_refresh` and `_discard`
to the existing start/status/result/cancel/reconcile lifecycle. The host supplies
the authenticated principal and negotiated protocol version. Additional ownership,
credential or approval arguments are refused. `_prepare` retains data but performs
no provider write; `_refresh` reconciles through provider reads.

Custom `OutboundActionProvider<Input, Output>` implementations supply a pure
request encoder, connection verifier, response decoder and read-only reconciler.
The reconciler receives a GET-only transport; the runtime owns the single POST.
Callbacks must honor deadlines, validate provider ownership/business bindings
and return portable contract-valid results. Keep them free of hidden remote
effects. The initial request format supports JSON or form POSTs and an optional
Stripe API version header; arbitrary authorization headers are not accepted.

Business input is bounded to 8 KiB, encoded POST body to 16 KiB, provider response
to 1 MiB and retained observation to 32 KiB. A reconciliation has at most 20
reads (default 10); the Stripe adapter scans at most 10 pages (default 5) of 100
refunds, plus its account read. Approval is capped at 24 hours and 16 roles.
These are SDK protocol bounds; existing native plan, workflow, journal, relay
and MCP limits still apply. Node durations are milliseconds; Python uses seconds.

Retain private intents, attempt/run bindings and observations as long as requests
can be retried or reconciled. There is no automatic pruning, lease takeover or
reset of consumed dispatch claims. Bump `revision` when provider/authorization
semantics change, and keep matching old definitions available or drain their
plans before upgrading. A changed definition name creates another namespace and
requires an application business-idempotency migration strategy.

Shared fixtures verify contracts, intents, provider cases and MCP descriptors.
Unit tests cover concurrent claims, uncertain writes, current authorization,
approval binding, cancellation and deadlines. The
[paid CSV acceptance](../examples/sdk-csv-jobs/README.md#approved-refunds-after-paid-import-failure)
uses real PostgreSQL and Chrome with native/Stripe protocol fixtures. Live Stripe
and deployed gateway/VM qualification remain separate acceptance steps.

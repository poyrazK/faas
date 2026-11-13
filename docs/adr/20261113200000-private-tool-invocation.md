# ADR-20261113200000: Private delegated invocation with sealed credential bindings

Status: Accepted
Amends: ADR-20261113130000

## Decision

Owners may bind an existing ordinary `secret:MCP_*` application secret in the
publication's default environment to one exact invocation grant. APID freezes
its ciphertext digest, scope, required tool scopes and opaque binding digest.
No plaintext is accepted by grant APIs or exposed through descriptors. The grant
already fixes account, app, publication, tool, connection key and expiry. Rotation, deletion or resealing invalidates the old binding; a new grant requires owner review.
Managed infrastructure credentials are excluded. Provider expiry, audience and
OAuth scopes remain enforced by the authenticated MCP application; Gregale
restricts which exact tool can use the credential and never refreshes it.

The first preview uses owner-provisioned access tokens. It does not repurpose
catalog-monitor OAuth grants or add provider login/refresh. Hosted publishing
remains open-only; a private publication may be made through the existing local
verified publication workflow.

Private credentials remain in APID's custody. Add a bounded native execute route
that validates canonical transient arguments and creates the existing invocation
journal before starting one worker. Only a fresh created fact authorizes launch;
response loss, duplicate submissions and restarts never relaunch a queued fact.
APID reuses the registered invocation protocol runner with a narrowly scoped
native store adapter. It does not execute customer code, own VMs, or introduce
a durable argument queue. Open invocation retains the existing CLI worker.

The private worker resolves only its frozen sealed secret, discovers the exact
native deployment's HTTPS preview, checks the full published catalog and every
current replica's pin, and requests one native dispatch. State locks serialize
that dispatch with grant/key revocation and secret replacement/deletion. A
rotation or revocation that wins before dispatch prevents the call; an already
authorized remote effect cannot be rolled back. Native receipts and result writes
recheck current authority. Only sanitized complete results are retained; known
credential reflections are redacted before journaling. A malicious authorized
application can still disclose arbitrary private data or transformed credentials.

Public dispatch/outcome APIs reject private worker transitions. Only the native
worker adapter performs them. The owner keeps history and cancellation; agents
cannot read sibling tools or choose another credential, endpoint or environment.

## Consent, bounds and recovery

APID enables this preview with `FAAS_MCP_PRIVATE_TOOL_INVOCATION=1`.
Private hosted calls require `--allow-private-tool-invocation` in addition to
`--allow-tool-invocation`, and browser OAuth scope
`gregale:tools:invoke-private`, with explicit final consent in addition to normal
invocation consent. Existing OAuth grants gain no new authority. Headless keys
need an owner-created credential-bound invocation grant and operator enablement.
Native execution is also disabled by default until the API operator enables it.

Reuse the existing argument/result, account active-call, retained identity and
600-second worker limits. Limit native private workers to four per API process.
Shutdown cancels and drains them. No automatic retries, worker takeover or resume
are added. Abandoned requests preserve not_started/outcome_unknown distinctions.
Data/API updates precede bridge enablement; old open grants remain unchanged.
No product maturity or VM lifecycle changes are made.

## Validation

Portable tests cover authenticated TLS MCP invocation through both supported
protocol versions, independent OAuth consent, lost creation replies, redaction,
single dispatch and recovery. Memory and migrated PostgreSQL stores exercise
credential rotation, exact grant/key isolation and revocation. Native API tests
cover the default-off gate and replica pin rejection; CLI tests cover owner grant
creation. These checks do not establish deployed staging or real-provider
acceptance.

The opt-in `TestMCPPrivateToolLifecycle` journey now drives a deployed API and
bridge with a synthetic JWT-authenticated tool, including credential rotation,
result redaction and catalog drift. Its local harness checks validate source
preparation and JWT behavior only; a successful configured staging run is still
required for deployed evidence. External-provider login and refresh remain
separate acceptance work.

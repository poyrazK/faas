# ADR-1035: Managed object storage for developer sessions

Status: accepted

## Context

Managed compute bucket bindings and the Node storage starter work through the
gateway, but `gregale dev` requires manual bucket creation and binding. Developer
apps already have stable account/project/workspace ownership and leased teardown
that revokes compute credentials, claims deletion fences and journals provider
cleanup before tombstoning the app.

## Decision

Add explicit, repeatable `gregale dev --bucket NAME` and `dev.buckets` declarations.
The flag replaces the manifest's bucket declarations for that invocation; it
defaults to `read_write` and `GREGALE_S3_<NORMALIZED_NAME>`. The manifest supports
explicit names, regions, permissions and prefixes. `dev setup` validates and
reports the same plan without provisioning until `--start`. `dev --all` forwards
explicit flags and otherwise uses each member's own manifest.

Bucket declarations require `storage:manage` (or admin) in addition to the existing
`deploy:write` authorization. Session responses omit bucket binding metadata for
principals lacking storage management permission.

The API normalizes the complete batch before creating or renewing an app. Private
buckets are reserved in the developer app's default scope through the existing
placement/quota writer; existing placement remains authoritative on retries.
Binding creation shares the existing sealed credential and atomic six-secret
writer. The automatic binding ID is a deterministic UUID over bucket ID and
prefix. Exact identity lookup includes revoked rows, so a restart or retry cannot
silently replace a revoked grant. Permission changes conflict; rotation continues
through the existing binding rotation endpoint. Restart the developer session
with `--stop` to create a fresh grant after revocation.

Omitted declarations preserve resources. They do not mean deletion, detachment,
or credential resurrection. Changed declarations do not clean up older resources;
all workspace buckets remain charged and count toward the existing quotas until
explicit deletion or teardown. Names and prefixes within the requested batch are
unique, including collisions caused by prefix normalization/truncation.

Safe session receipts include active automatic bucket bindings and rotation state.
Read-only inspection does not renew leases or provision resources. No credential
secret, ciphertext, provider placement or local source identity is returned.
Revoked bindings are excluded from active receipts. A same-named existing private
bucket may be reused inside the dedicated developer app, but public settings and
unrelated secret/binding collisions are never overwritten.

Partial failures retain durable reservations and successful earlier resources for
retry, stop or lease cleanup. They are not compensated by deleting existing data.
Existing bucket leases/backoff serialize provider operations. A concurrent create
can require a retry while provisioning is leased; a concurrent successful binding
insert is adopted only after checking the exact stable identity and permissions.
Explicit stop does not parse start-time declarations, so a malformed manifest cannot
block teardown. Stop and expiry use the existing cleanup worker, including write
fences, protected bucket checks and persisted failures. No new provider path, database schema or VM
lifecycle writer is introduced.

## Validation

Memory and actual PostgreSQL handler contracts cover workspace isolation, repeated
upsert/omission, read-only inspection, fresh server recovery, permission mismatch,
revocation, failed provider deletion with durable retry and expiry cleanup. CLI
checks cover flag parsing/dispatch, manifest precedence, planning, multi-app
forwarding and safe receipt output. Native KVM and real-provider qualification
remain separate from these local contracts.

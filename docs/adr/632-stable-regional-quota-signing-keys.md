# ADR-632: Stable regional quota signing keys

Date: 2026-10-05
Status: Operator provisioning and schedd owner wiring implemented; automatic recovery and native acceptance pending.

## Context

The authenticated retirement protocol (ADR-631) needs a stable cell signing key.
Generating a key on daemon startup loses issuer continuity. A valid workload or
TLS certificate does not confer cell quota authority. Operator key rotation must
retain historical verification without replacing already accepted receipts.

## Decision

Use a separate Ed25519 PKCS#8 key and a bounded canonical public trust bundle.
Each public anchor pins the key fingerprint, authority role and immutable issuer
UUID. Reject duplicate keys, unknown/duplicate JSON fields, extra PEM objects,
other key algorithms, oversized files, unsafe modes, symlinks, hardlinks and
non-regular files. Private material supports read-only 0400 or 0440; public
material permits owner read and optional group/other read only. Operator file
loading and provisioning support native Linux and macOS; other platforms refuse.

`gregalectl runtime-quota-keys init` exclusively creates a new 0700 directory
containing `key.pem` (0400) and `trust.json` (0444). Never overwrite an existing
directory or retry a partial installation with a new key. File contents and
directories are synchronized before success. Status prints public identity and
paths only. Keys remain outside the repository; no real authority is provisioned
or deployed by this implementation.

Rotation creates another directory for the same issuer and role. The `trust`
command merges public bundles into a new file, refusing duplicates and overwrite.
Stage overlapping old/new trust on all receivers before restarting an owner with
the new private key. Retain old public anchors until all signed historical
messages still needing delivery have been consumed. Do not delete or re-sign
accepted retirement evidence. This slice has startup loading, no hot reload.

Schedd optionally reads `[regional_runtime_signing]` with `private_key_path` and
`trust_bundle_path`. Empty configuration keeps retirement unavailable; partial
configuration aborts startup. When configured, require vmmd mTLS and the node
identity resolver, then read the immutable cell identity with the existing sqlc
query. Construct the signer with that database cell UUID and require its exact
cell-role public anchor. No configured string or key file can replace the
database's identity. Unbound databases cannot host a configured cell signer.

Install the owner into the scheduler and expose an explicit operation requiring
the retained original physical permit and actual native lookup key. This uses
ADR-631 fencing, authenticated native stop and locked signing/release. It neither
looks up mutable app/instance rows nor falls back to generic Destroy. Missing
configuration or trust refuses retirement and preserves issued capacity.

Example staging workflow (substitute actual immutable IDs and absolute paths):

```sh
gregalectl runtime-quota-keys init --role cell --issuer "$CELL_ID" --dir /secure/cell-key-v1
gregalectl runtime-quota-keys init --role cell --issuer "$CELL_ID" --dir /secure/cell-key-v2
gregalectl runtime-quota-keys trust --input /secure/cell-key-v1/trust.json --input /secure/cell-key-v2/trust.json --out /secure/cell-trust-v2.json
```

```toml
[regional_runtime_signing]
private_key_path = "/secure/cell-key-v2/key.pem"
trust_bundle_path = "/secure/cell-trust-v2.json"
```

Operators install the private file read-only for schedd, with directory traversal
limited to its owner/group. The initial private directory is owner-only; an
operator must explicitly arrange daemon ownership/group access. Public trust
distribution, directory signer startup wiring and receivers remain to be added.
The directory's distinct immutable issuer identity is not inferred from a cell.

Automatic lifecycle/recovery dispatch still needs a source-bound inventory of
original attempts, including collected/orphaned producers, and reconciliation of
independent first-fence conflicts. This explicit operation does not establish
that inventory or an automatic sweeper. All quota heads remain frozen. Native
pause, migration recovery, complete global billing and cached ingress routing to
all selected healthy eligible regions remain required for the full objective.

## Validation

Check stable reload, overlapping key rotation, identity/role/key mismatches,
unsafe files, canonical bounds, exclusive operator writes, CLI metadata and
daemon startup configuration. Exercise the configured scheduler operation over
real mTLS after app deletion, retaining original signed evidence and refusing an
unconfigured owner. Portable tests do not replace dedicated native Linux x86_64
KVM test-metal, leakcheck and recovery acceptance.

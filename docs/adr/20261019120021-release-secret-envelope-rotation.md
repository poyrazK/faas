# ADR-20261019120021: Release secret envelope rotation and retirement

## Status

Implemented. Atomic secret admission remains disabled pending the strengthened
`project-release-secrets-only` native qualification lane. No native VM execution,
production key rotation or key retirement was performed in this workspace.

## Context

ADR-20261019120020 pins encrypted values to deployments and saved release captures. Walking
only `app_secrets` leaves retained releases dependent on an old encryption key.
Resealing a mutable row through the customer mutation path also conflicts with
the release snapshot fence. An elapsed overlap period does not prove that a key
can be removed.

## Decision

The existing APID background worker, enabled by `FAAS_REKEY_ENABLED=true`, walks
four private document kinds: live app secrets, staged secret bundles, deployment
secret snapshots and saved release configuration captures. Every physical copy
of `live_secrets`, `secret_snapshot` and `secret_target` is included. Expired
bundles and retained inactive deployments count until removed or rekeyed.

The worker opens an envelope with its loaded identities and reseals the same
values to the current recipient. It preserves immutable secret IDs, value HMACs,
numeric delivery versions, ownership, timestamps and public receipts. It preserves
missing historical value HMACs as well; filling those is separate maintenance.
Plaintext stays in process memory. Public progress contains counts and a document
cursor, never envelopes or decoder input.
The existing row-rate budget is charged per encryption envelope, including
multiple copies inside one capture; a large document cannot bypass throttling.

Each document commits through compare-and-swap against its exact previous sealed
configuration. The maintenance transaction can replace only recipient/ciphertext;
database triggers retain logical immutability. Runtime document updates take the
app mutation lock, and publication fingerprints continue to bind ciphertext so
an old in-flight publication must retry. Existing runtime delivery receipts remain
fresh because their logical inputs have not changed.

Outstanding envelopes are the durable work journal. Each run starts a fresh
ordered scan, skips copies already using the current key, and pages past failures
without starving later documents. Restart retries all outstanding copies, including
ones inserted behind a prior cursor and work from a subsequent key rotation.
The persisted progress cursor is diagnostic for this document walk.

Saved scan/apply proofs retain their original public identity. Before reusing an
environment proof, stores compare every original environment input while ignoring
only recipient and ciphertext. Actual variable changes, HMAC/version changes,
ownership changes, timestamp changes and eligibility changes still conflict.
This also supports receipts created before the maintenance implementation.

Retirement reads dependency counts under an exclusive database fence and refuses
while any matching or unlabelled envelope remains. Every encrypted write holds
the shared fence until commit. A generation row locked in share mode causes an
older repeatable-read transaction to fail serialization rather than publish using
a stale retirement snapshot. A durable retired-recipient record rejects future
writes under that key; schema downgrade refuses to discard retirement records.

`gregalectl host-age prune-previous` requires database verification, including
dry-run. `--force` skips only the overlap age check. Promotion retains the identity
and keeps its existing recovery semantics. The CLI rechecks the previous file's
fingerprint after verification, and JSON status reports pruning unavailable when
the database check cannot establish eligibility.

`gregalectl fleet-seal retire-release-key --kid RECIPIENT` provides the equivalent
release-storage barrier for fleet recipients. It requires the current fleet
identity and refuses to retire it. Its `--dry-run` reads dependencies without
recording retirement. Identity-file retirement and migrations of MFA, signing,
provider credentials and other encryption domains retain their own procedures.
This release-store inventory does not qualify those domains for key removal.

Fleet identity loading accepts optional `fleet.age.previous` and the corresponding
systemd credential alias before legacy host identities. Generated units deliver
that optional overlap credential. Operators distribute overlap identities and
restart readers before changing the active sealing recipient.

## Qualification

Storage tests exercise rekeying before/after candidate acceptance, replay,
concurrent replacement, immutable ID enforcement, unlabelled legacy input,
retirement versus concurrent writers in both orders, repeatable-read writers,
promotion and rollback using only the new identity. Recovery tests interrupt the
worker, fail an early document, insert behind its cursor and perform a second
rotation. CLI tests retain the identity on refusal and dry-run.

The native lane now rotates retained baseline/candidate envelopes through the
real APID worker at 25%, removes the old identity, restarts APID/schedd/vmmd,
requires cold boots under the new identity, and verifies rollback's original
artifact/configuration/secret combination. Its source-bound report requires
`rekey=true old_key_removed=true cold_boot=true` plus the existing observations,
final leakcheck and successful runner completion. Compilation and unit evidence
do not establish native lifecycle qualification.

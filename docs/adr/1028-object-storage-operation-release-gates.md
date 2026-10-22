# ADR-1028: Object storage operation release gates

Status: Accepted

## Context

Storage binding canaries can list buckets, perform write-only transfers, or
verify read/write transfers. A generic passing probe does not establish every
operation an application needs. Promotion already fences verification, catalog,
runtime, and policy observations at the atomic traffic write.

## Decision

Add a bounded map of managed storage environment prefixes to required modes in
each scope's release policy. Read accepts listing evidence; write requires upload
and cleanup; read_write also requires byte-for-byte download. Exact successful
stage sets are validated against the admitted mode; legacy evidence is read-only.
Read/write evidence covers all modes. Missing or ambiguous prefixes, insufficient
permissions, stale/failed/incomplete evidence, and existing independent rotation,
runtime, freshness, and application adoption blockers cannot be waived.

Operation requirements are opt-in. Read-only CLI checks evaluate explicit flags;
server traffic gates evaluate stored policies. Neither evaluates by performing
provider I/O or scheduling writes. Application ACKs retain their separate meaning.

Use a dedicated update route requiring an explicit map, including an empty object
for clearing. Old servers reject the route instead of silently weakening policy;
legacy routes reject updates that would remove stored requirements. Updates remain
revision-checked and audited. Memory and PostgreSQL compare the evaluated map and
policy revision at the locked traffic boundary, alongside existing catalog fences.
The new database CHECK validates bounded prefixes and modes. Migration rollback
locks policy writes before its guard and is refused while any configured
requirements remain, including concurrent updates committed while it waits.

## Consequences

Writable deployments must explicitly collect suitable canary evidence before
increasing traffic under a configured policy. Changes invalidate old fences.
Read requirements cover listing, not object byte reads; applications needing byte
transfer choose read_write and accept its explicit mutation. Write-only cleanup
acknowledges deletion and does not independently verify absence. Existing Object
Lock guards and bounded best-effort cleanup remain unchanged. Policy updates
replace the complete map; operators must retain every intended requirement or
explicitly clear it. Rolling back requires a reviewed explicit policy update.

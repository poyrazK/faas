# ADR-20261017190803: Read-only GitOps promotion proposals

- **Status:** accepted (implementation preview)
- **Date:** 2026-10-16
- **Related:** ADR-568, ADR-1155, ADR-20261017190801, ADR-20261017190802

## Context

Generic environment promotion protects active GitOps graphs from replacement.
Customers need a reviewable Git change that selects the image tested in another
environment while preserving the target's configuration and identities.

## Decision

1. Add a read-scoped promotion-plan endpoint and CLI. Verify the target manifest
   at an immutable commit through its bound provider. Read both bindings, the
   approved origin revision, current graph evidence, active release membership,
   selected deployment identities, and target ownership/intent on one
   repeatable-read, read-only PostgreSQL snapshot (one MemStore lock).
2. Require the same account, project and provider repository identity. Current
   source verification, qualification, activation and serving evidence must
   agree before export. Report missing or contradictory evidence as blockers.
   The selected live deployment must match each reviewed immutable image.
3. Propose only image source replacements for matching logical workloads.
   Preserve target app mappings, variables, secret references, routes, policies,
   runtime settings, queues, schedules, service bindings and extra workloads.
   Source/Dockerfile/function builds remain blocked because the manifest lacks
   a portable reference to their existing built artifact. Copying their source
   descriptor would rebuild from a different commit and cannot represent
   promotion of the tested bytes.
4. Return identities, source changes and value-redacted target field plans.
   A domain-separated observation hash binds release, selected deployments,
   revision/generation, target base/proposed digests and target plan. A repeated
   request with `expected_plan_hash` returns conflict when observations change.
   Wall-clock observation timestamps alone do not change that hash.
5. Export requires an explicitly reviewed hash and a fresh endpoint request.
   The patch envelope contains RFC 6902 test/replace operations and the required
   base definition digest. Manifest export validates a local YAML/JSON file
   with the strict environment parser, checks its complete canonical base
   digest, replaces only the proposed source contracts, and verifies the final
   digest. Output is canonical JSON (valid YAML); no local file or provider
   repository is written by the command.
6. Proposal hashes grant no adoption, approval, lease, activation or serving
   authority. The normal target Git review and fresh target qualification are
   required. Existing generic promotion fences, production gates and exclusive
   native acceptance requirements remain in force.

## Validation

Cover target configuration preservation, image/provenance matching, source
outages, stale graphs/releases, missing serving evidence, unsupported source
builds, response redaction, exact local-base checks, stable observation hashes,
changed-source conflicts, read-only store behavior and authenticated HTTP scope.
This feature neither runs VMs nor claims dedicated native acceptance.

# ADR-20261104015055: GitOps report redaction and release recovery guards

- **Status:** accepted
- **Date:** 2026-11-04
- **Related:** ADR-568, ADR-20261019071733, ADR-20261020085706, ADR-20261104005609

## Context

GitOps status and adoption previews returned original console environment values
through their plan fields, including unmanaged values. Their read scopes do not
grant application configuration access, and the ordinary environment API returns
only metadata. Empty retirement also skipped the production freeze and approval
guards. Finally, policy effect replay required fresh acknowledgements from
departed gateways even when their receipts were already durable.

## Decision

1. Redact every plan change's before/after values at the API and dashboard
   boundary. Preserve plan hashes, identities, actions and blockers. Retain full
   internal reports and observations for reconciliation and adoption validation.
   Malformed historical plans fail closed without returning their raw bytes.
2. Check release guards for both candidate deployments and planned retirements
   belonging to the current approval. Apply them before empty release cutover
   as well as candidate graph activation. Retirements already cut over continue
   cleanup. Preserve the existing production-scope and approval policy rules.
3. Policy recovery retains the complete durable minimum gateway roster. Require
   fresh replay acknowledgements from all current serving gateways, including
   newly registered ones. A departed gateway's saved acknowledgement counts;
   a missing acknowledgement remains a blocker. Preserve the original account,
   application, host and generation scope when replaying invalidation.

These changes add no stored columns, migrations or daemon protocols. Qualification
and production enforcement remain behind their existing native acceptance gates.

## Validation

Exercise scoped HTTP reads, guarded cutover and post-commit recovery with both
MemStore and PostgreSQL. Verify public receipts retain their usable plan hash
while internal values remain intact. Hold first retirement cutover during a
freeze or insufficient approval, resume after the hold ends, and allow already
committed cleanup. Recovery completes with a departed gateway's durable receipt,
but missing old receipts, absent fresh live acknowledgements and unacknowledged
new gateways continue to block completion.

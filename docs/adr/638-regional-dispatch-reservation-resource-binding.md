# ADR-638: Bind regional dispatch to its complete resource reservation

Date: 2026-10-06
Status: Implementation in progress; native reconciliation and acceptance pending.

## Context

ADR-635/637 retain original scheduler and native launch request footprints.
Dispatch currently binds only the instance/run/host identity to its ledger
reservation. Ordinary wake, prime and warm-pool callers omit allocated sidecar
RAM, despite the admission primitive supporting it. A mismatched or absent
reservation can therefore precede a correct durable claim. A timer can also
release a regional startup CPU peak without proving the physical attempt stopped.

## Decision

Before first regional issuance, require an existing reservation with the same
app, deployment, node, original run, plan, initial purpose, guest RAM including
sidecars, guest vCPU and conservative CPU peak. Capture the complete source and
footprint together only after those comparisons. Retries retain the same full
source and footprint. A recovered or repaired row shadow cannot acquire original physical
provenance through this dispatch path. A failed comparison before first issuance
leaves no new placement on the returned spec, so ordinary cleanup can release that unbound
reservation. A retry with an escaped placement retains its original identity.
Explicit Engine bookkeeping fixtures with no ledger remain separate; production
Engine construction always supplies a ledger.

Once regional source ownership is bound, deadline expiry and generic readiness
bookkeeping cannot release its CPU peak. Only accepted physical retirement or
subsequent independent native reconciliation can establish a different charge.
Ordinary unbound reservations retain their existing temporary-boost behavior.

Wake, prime, warm restore and warm row repair use the captured deployment's
allocated sidecar RAM. Migration uses the captured dispatch AppSpec. Startup
rebuilds sidecar RAM from the original deployment and retains known row charges
while blocking new admission on missing or malformed allocation provenance.
Warm creation also reserves the startup CPU peak and captures its original run.
These changes preserve independent row shadows and issued physical holds;
matching requested resources does not prove native presence or permit deduplication.

Native inventory still needs a barrier covering every producer, independent
machine/resource observation, first-fence reconciliation and current task/run
ownership. Capture/pause/migration integration, directory authority, global
billing and all-selected-region routing remain part of the full feature. Regional
heads stay frozen. Native Linux x86_64 KVM test-metal, leakcheck and recovery
acceptance remain required.

## Validation

Check missing/mismatched reservations refuse before issuance, immutable retries,
purpose/plan binding, retained CPU peaks and exact-source retirement. Exercise
sidecar RAM placement/admission and startup reconstruction, including allocation
read errors. Run affected race suites and pinned lint/policy gates.

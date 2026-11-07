# ADR-20261105103000 · Recover egress policy after uncertain writes and restarts

- **Status:** accepted
- **Date:** 2026-11-05
- **Amends:** ADR-031, ADR-033, ADR-361, ADR-373, ADR-20261017 (vmmd restart adoption), ADR-20261104170000 (atomic allowlists)

## Context

An nft transaction can commit before its caller receives an error. If a newer
revision returns to the cached ports or an empty allowlist, equality alone can
acknowledge that revision while the kernel still enforces the failed update.
Unknown handles identify some uncertain nonempty allowlists, but cannot express
uncertainty for an empty list or a named port set.

Restart adoption reconstructs a basic namespace configuration from the journal.
It loses the live port policy, DNS/database enablement and plan connection
limits. DNS grant RPCs then succeed without updating the surviving timed sets;
a later policy rebuild removes the missing controls. The kernel already holds
the effective port sets and the remaining lifetime of each timed permission.

IPv6 allowlist rules also lack the port-25 exclusion required by the strict
public-rule handle reader, so unchanged policy repeatedly reconstructs tables.

## Decision

Track uncertain public allowlist and port-set writes independently of cached
contents, under the manager mutex. Equality fast paths require known state.
After an uncertain complete replacement, both controls require reconciliation;
an acknowledged replacement restores confidence. A newer policy revision may
supersede failed intent, but must still physically establish its projection.

Before publishing an adopted guest, capture the IPv4 and IPv6 nft tables from
its proven, re-anchored namespace. Require matching canonical port policies and
the current DNS/database policy objects. Restore those ports, the DNS gate and
the current plan's connection limits, together with active DNS/database grants.
Compute each expiry from the start of its capture plus nft's remaining
milliseconds. Never substitute the original timeout. Discard subsecond grants;
unreadable, incomplete or inconsistent state refuses adoption and retains
quarantine. Explicitly disabled DNS gating remains supported. Guests being
registered solely to finish teardown do not depend on policy recovery.

The journal format remains unchanged. The first normal allowlist projection
after adoption reconstructs policy even when the journaled list is empty: its
contents cannot prove the result of a write interrupted by the restart.

Render the same canonical blocked TCP port set for IPv4 and IPv6 public
allowlists, preserving the current abuse-port restrictions and port-25 denial.
Read numeric nft listings and recognize both that exact set and the legacy
port-25-only rule during upgrades. Retain the strict handle reader so private
and database accepts cannot be confused with public exceptions.

## Validation and rollout

Regression tests cover lost commit acknowledgements followed by newer clearing
revisions and retries, real renderer-to-handle matching, adopted DNS renewal,
live extra ports, both address families, remaining grant lifetimes, complete
policy rebuilds and refusal of unverifiable kernel state. Dedicated native
hosts provide kernel namespace and restart acceptance.

Upgrade vmmd through the ordinary PR/release path. Adoption of a namespace
without the required policy objects is refused rather than reconstructed from
defaults. Existing scheduler recovery replaces refused guests. No schema,
quota, protobuf or journal migration is required.

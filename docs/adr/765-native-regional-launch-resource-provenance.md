# ADR-765: Original resources in native regional launch records

Date: 2026-10-05
Status: Implementation in progress; native inventory reconciliation and acceptance pending.

## Context

ADR-763 retains scheduler claims and ADR-764 charges them on restart. Native
version 2 records retain the original permit and physical lease, but the lease
does not retain guest vCPU count. The scheduler claim also omits sidecar RAM,
although vmmd includes that RAM in the actual guest and cgroup allocation.
Neither mutable plans nor current instance rows can repair historical provenance.

## Decision

Production native regional launches require a resource-capable producer. Before
network, artifact or child-process effects, it durably writes version 3 ownership
containing the complete original placement, requested guest RAM including
sidecars, requested guest vCPU count and conservative startup CPU peak. VM overhead
remains a separate per-VM charge. The footprint must be positive, bounded and consistent with the
physical lease's RAM and CPU policy. Scheduler issuance uses the same RAM and CPU
semantics, including the legacy zero CPU quota's plan-ceiling resolution. Reads
validate the retained footprint's internal lease binding without recalculating
the original CPU profile from current plan limits; only new launches resolve the
current profile. If today's CPU policy differs from a known original peak,
fallback refuses a new producer and retains the original ownership for retirement.

Version 3 uses an exact required resource object; missing, null, duplicate,
unknown or invalid fields refuse recovery. Native authorization, exit/resource
acknowledgement and generation archives retain and compare the footprint.
Restore-to-cold-boot fallback preserves it exactly. A completed physical generation
may be replaced with a new explicit footprint after retirement; a fallback cannot
alter its footprint, and a known footprint cannot be downgraded to unknown.

Version 1 and 2 records remain readable with unknown original resources. Their
guest vCPU is never inferred from today's plan. Historical fallback does not
backfill resources. A subsequent fully retired new launch may record its own
actual footprint. No exported Lease fields change the historical record schema.

This prerequisite does not establish a complete native inventory, authority to
deduplicate scheduler shadows, current task/run ownership or permission to open
regional heads. Retaining request provenance does not independently measure a
recovered guest or prove snapshot machine compatibility. All regional heads remain
frozen. Native Linux x86_64 KVM test-metal, leakcheck and recovery acceptance remain required.

## Validation

Test strict version compatibility, persistence before effects, actual sidecar
RAM and CPU policy, immutable fallback/archive resources, exact acknowledgement
and rejection before journal mutation. Run portable race suites and pinned lint
and policy gates. Portable fixtures do not establish native acceptance.

# ADR-1543: Managed HTTPS service probes

Worker caller eligibility is extended by [ADR-1544](1544-managed-worker-service-probes.md).
The original scope decision below records the initial HTTP-only caller support.

- **Status:** accepted
- **Date:** 2026-10-17
- **Decision:** Support deployment-selected HTTPS route verification for active managed request/service workloads. Resolve the logical binding, custom environment key, physical target app, exact target deployment and active release on the server. Publish `managed-https-v1` only for a complete supported context. Keep held candidates, workers, Jobs, HTTP edges and unknown contracts unsupported.
- **Authority:** Store a versioned managed context in the existing target-pinned task ledger. Guest dispatch uses a distinct versioned argument shape on the oldest reserved service-probe handler; old probe-aware guests reject it before customer command fallback. The durable command retains the existing target-probe shape. Recheck the current graph before VM preparation and immediately before dispatch. SQL admission checks active release membership and enforcement under share locks. A changed context can leave an obsolete task record but cannot qualify current evidence.
- **Guest:** Require the frozen custom environment key and staged caller deployment identity, use verified private TLS without ambient proxies, and send exact target and release assertions. Require unique matching gateway confirmations. Never wake the target or invoke application code. Old guests cannot produce the managed report contract.
- **Source identity:** Task VMs remain absent from serving-instance rows. On named nodes, the guest service listener resolves the source address against vmmd's current live map through its authenticated transport, then checks the managed task's running lease and pinned caller. No address-to-task result is cached. Reused, expired, cancelled and ambiguous identities fail closed. A distinct command-free restore RPC rejects old nodes before a managed probe boots; ordinary requests retain their existing rolling-upgrade compatibility.
- **Evidence:** Segregate managed and ordinary task receipts before latest-receipt selection. Include frozen caller/target inputs and the private service-policy revision in an opaque digest. Require the exact versioned report, successful task exit, scope, caller deployment and current context. Publish sanitized stage statuses. Refresh inventory after CLI completion; release, target, graph or policy changes invalidate reuse. An ordinary receipt can never qualify the managed edge.
- **Scope:** Expose this route observation through inventory, binding checks, verification planning and single/all CLI verification. It is not resident application adoption, application behavior, environment activation authority or native release qualification. Existing reviewed environment and release gates remain authoritative.
- **Acceptance:** Cover custom environment keys, malformed and conflicting gateway confirmations, stale and ordinary receipts, release/policy changes, dispatch fencing, API admission and CLI refresh. Native execution requires a dedicated Linux KVM acceptance host, actual guest execution and host leak checks; portable fixtures and compilation cannot establish that qualification.

The managed HTTPS native journey owns its isolated control plane, reviewed
caller/target graph, private CA and exact-source CLI. It is a required fourth
suite of the binding-native lane (aggregate version 2), including CLI coverage,
release/policy invalidation, cancellation, expiry and reviewed workload
retirement before daemon shutdown. Its non-secret hashes are evidence of guest
acceptance only; provider and expected-artifact approval remain independent.
See the [native runbook](../ops/managed-service-binding-native-acceptance.md).

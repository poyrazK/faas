# ADR-1542: Frozen service binding inventory

- **Status:** accepted
- **Date:** 2026-10-17
- **Decision:** Build managed service inventory from the selected deployment's frozen workload graph, using one caller selection for service metadata and verification lookup. Without an explicit selector, use the current live deployment. Preserve ordinary app-based inventory for unmanaged deployments and apps without a deployment. Add `gregale bindings APP --deployment ID|vN` and typed inventory context in Go, Node and Python.
- **Why:** Selecting deployment-specific evidence while deriving service names from app settings can omit logical environment bindings or associate them with the wrong physical target. Managed logical names must never resolve through a global app slug fallback.
- **Metadata:** Publish logical name, frozen environment key, scope, physical target app, resolved exact deployment/release and effective transport. Per-edge revisions bind reviewed graph identities and public route facts, excluding credentials, variable values, task output and collection timestamps. Include the context in durable review configuration digests. Failed reads and invalid frozen context make inventory incomplete; known denied or held routes remain inspectable without becoming connectivity evidence.
- **Verification boundary:** Ordinary task probes, including exact-target probes, cannot verify frozen environment service edges. Mark them unsupported, exclude app-scoped receipts, block them in checks even with the ordinary unsupported queue/outbound waiver, and block durable review planning and task admission. Batch previews report this gap without creating work. Managed target overrides cannot replace active reviewed graph authority. This change does not add a managed guest canary or adopt environment qualification reports as ordinary binding receipts.
- **Compatibility:** Existing app service namespaces, receipt revisions and ordinary probe behavior remain compatible. Empty or unreadable frozen service maps never fall back to app bindings. Missing caller/target ownership, held qualification contexts and stale routes never establish a passed check. Explicit CLI deployment selectors require confirmation from the server.
- **Validation:** Cover logical `billing` targeting physical `git-billing`, frozen/app disagreement, stable reads and changing target/release revisions, malformed/empty graphs without fallback, scope mismatch, sanitized failures, legacy receipt exclusion, unsupported probe admission, strict check/preview behavior and generated SDK metadata.

The initial verification boundary above is extended by
[ADR-1543](1543-managed-https-service-probes.md) for the explicitly versioned
managed HTTPS contract. Ordinary receipts and unsupported contexts remain
excluded.

# ADR-794: Use the patched Go 1.26 build toolchain

- **Status:** accepted
- **Date:** 2026-10-09

## Context

The release vulnerability gate reports reachable standard-library findings
with Go 1.25.13. The October 2026 advisories identify Go 1.26.9 as the fixed
supported release; the 1.25 line has no patched version. The same advisories
affect `golang.org/x/net` before v0.60.0.

## Decision

Pin the platform, CI, native build workflows, image toolchain installers, and
generated Go starter modules to Go 1.26.9. Keep installer checksums aligned
with the published archives. Upgrade `golang.org/x/net` to v0.60.0 and
`golang.org/x/crypto` to v0.57.0 (required by the networking update), and align the module and CI linter pins at
golangci-lint v2.10.0, which supports Go 1.26. Update the builder image's
compiler manifest digest and regenerate its vendored dependency graph after
applying the same networking floors.

Retain the existing `go124` runtime identifier and SDK minimum Go version.
The compiler pin changes; deployment ownership and the guest runtime
interface stay the same. Keep the vulnerability gate enabled and verify
the selected dependencies in both the platform and Terraform modules.

## Consequences

Build and lint caches must warm for the new toolchain. Release acceptance
must include vulnerability scans, lint, starter generation, and ordinary
repository CI before merging. This change does not deploy images or apps.
Use the existing apid compiler guard for the CLI as well: disable compiler
DWARF generation and serialize compiler workers for that package, retaining
all tests and race/coverage instrumentation. Bound PostgreSQL shard compilation
with the same GC and memory settings already used by the larger API shard.

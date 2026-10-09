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
generated Go starter modules to Go 1.26.9. Root and provider modules retain
the Go 1.26 language target and select Go 1.26.9 with a toolchain directive.
Keep installer checksums aligned
with the published archives. Upgrade `golang.org/x/net` to v0.60.0 and
`golang.org/x/crypto` to v0.57.0 (required by the networking update), and align the module and CI linter pins at
golangci-lint v2.14.0, which supports Go 1.26 and fixes analysis-cache handling. Update the builder image's
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

Full repository analysis with a 3 GiB soft memory limit exceeded the existing
60-minute CI job deadline. Partition the complete `go list ./...` inventory
deterministically across four lint jobs, and analyze one package at a time.
Retain every pre-upgrade configured rule and lint tests as before. Make the
gosec v2.22.7 registry (golangci-lint v2.4.0 on main) explicit so upgrading the
tool does not implicitly deploy additional security policies. A checked-in
inventory and CI guard verify that all 32 previously enabled security rules
remain enabled; the existing eight documented exclusions stay the same.
Additional rules introduced upstream require a separately qualified rollout.
Allow a 10 GiB analysis budget within each hosted runner. The
existing required lint/build job explicitly fails if any lint shard fails,
including cancellation or enumeration failure. Local `make lint` uses the same
executor with one shard.

The patched x/net release deprecates its HTTP/2 connection APIs in favor of
standard-library APIs with different dialing and connection ownership.
Retain the qualified connection pool with a deprecation exception confined to
those APIs and files; migration needs protocol and pool qualification. The supported legacy
proxy API also retains its existing forwarding contract during this compiler
patch. Neither exception changes runtime behavior or disables unrelated checks.

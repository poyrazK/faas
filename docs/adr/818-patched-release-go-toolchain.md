# ADR-818: Patched Go compiler and release dependency floors

Status: accepted · 2026-10-09

## Context

The continuous profiling release's mandatory vulnerability scan reports twelve
reachable standard-library findings on Go 1.25.13. The supported fixes are in
Go 1.26.9. The affected HTTP/2 dependency requires golang.org/x/net v0.60.0,
whose dependency graph requires Go 1.26 and golang.org/x/crypto v0.57.0.

## Decision

Pin platform and Terraform provider builds to Go 1.26.9. Align CI, native
acceptance, Packer, local metal provisioning and profiling fixture compilers.
Verify the official Linux amd64 archive checksum and retain digest-pinned
multi-architecture Go images for guest-init, runc and BuildKit builds.

Use x/net v0.60.0 and x/crypto v0.57.0 with their selected transitive versions.
Regenerate BuildKit's vendor tree from that graph so the compiled source and
reported module versions agree. Keep the existing source release and repository
patches. Use main’s pinned golangci-lint v2.14.0, which supports Go 1.26. Retain
its existing rules and baseline policy, and every vulnerability rule.

The independent Go SDK keeps its Go 1.23 language requirement. Historical
validation evidence retains the compiler versions actually used. Compiler
updates do not grant native runtime or snapshot qualification: the existing
exact-artifact native qualification and recovery requirements still apply.

## Consequences

Platform development and provider builds require the patched compiler. The
release must pass vulnerability scans, generation and contract checks, tests,
and image builds using the new pins before merging. This change does not deploy
hosts, replace running guests, or qualify native profiling snapshot restore.

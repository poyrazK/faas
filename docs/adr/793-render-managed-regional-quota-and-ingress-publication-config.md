# ADR-793: Render managed regional quota and ingress publication config

Date: 2026-10-11
Status: Implemented; focused portable validation passed. Operational acceptance pending.

## Context

APID loads quota transport and global ingress publisher tables, but the managed
manifest schema and renderer omit both. Re-rendering during a release therefore
loses a manually installed signing authority, receiver or route publisher.

## Decision

Carry both APID-owned tables through typed manifest fields and the existing TOML
placement catalog. Directory authority requires the explicit directory gate and
an unbound fleet. A regional receiver requires a bound cell and its canonical
HTTPS API origin; it cannot carry directory signing or delivery worker settings.
Ingress publication requires the directory authority and a distinct output file.
Signing material stays in separately installed host files; manifests contain
only paths and the issuer identity. Empty configuration keeps the runtime off.

The daemon still verifies database scope, immutable issuer, key ownership and
publication directory access at startup. Rendering cannot grant serving
eligibility or establish native recovery acceptance.

## Validation

Exercise directory and receiver scope, incomplete authority, path collisions,
wrong daemon/table placement, disabled defaults and typed TOML round trips,
including the delivery worker boolean. Managed rendering must preserve these
settings through successive releases. Full regional runtime and dedicated
native KVM acceptance remain outstanding.

The manifest and renderer race suites pass (237 test events), the pinned
golangci-lint 2.4.0 check reports no findings, and the seven repository policy
gates pass. These checks cover configuration preservation and refusal behavior;
they do not demonstrate positive native regional admission.

# ADR-392 · Edge-owned TCP listener certificate bundles

Status: proposed

## Context

TCP termination needs a private-key provider whose path handling and rotation preserve listener isolation. A malformed file must not hold public handshake capacity indefinitely.

## Decision

Anchor lookups to an open certificate-directory descriptor. Recheck directory permissions on each lookup and reject group or other write access. Normalize each hostname before deriving its bundle filename. Require a regular file no larger than 64 KiB; reject public access and group write permissions. Open nonblocking so FIFOs can be rejected without waiting for a writer. Root confinement prevents escaping symlinks and replacing the directory pathname does not redirect an already-open provider.

Operators provision a complete certificate-chain/private-key PEM bundle by atomic rename of `<hostname>.pem`. Each lookup returns an independent immutable certificate snapshot. Existing snapshots remain unchanged after rotation. Provider errors omit key material and storage paths, and lookup respects cancellation before and after its bounded read. Close the provider when the edge stops.

This supplies the provider only. Listener termination, provisioning automation, issuer integration, and customer API exposure are separate integrations. Bundles belong to public-edge infrastructure, not persistent guest disks.

## Validation

Portable race tests cover bundle rotation, old-snapshot preservation, unsafe permissions, oversized and missing bundles, cancellation, root escape, directory replacement, and nonblocking FIFO rejection. Native container acceptance remains pending a designated Linux/amd64 KVM host.

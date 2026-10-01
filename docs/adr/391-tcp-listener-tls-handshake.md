# ADR-391 · TCP listener TLS handshake boundary

Status: proposed

## Context

A public TCP listener can reserve connection capacity before it knows whether a client is valid. TLS negotiation must finish before workload admission, and certificate errors must not disclose key material or storage paths.

## Decision

Provide a TLS handshake primitive for the public edge. Its caller reserves session credits first and selects or admits a workload only after success. Require TLS 1.2 or newer, exact listener SNI, and a currently valid certificate matching the hostname and private key with server authentication usage. Certificate snapshots are immutable and their provider must honor cancellation.

Bound negotiation to ten seconds, preserving earlier caller deadlines. Every unsuccessful call closes the supplied connection; success transfers the secure connection to its caller. Sanitize provider errors. Normalize DNS hostnames and reject IP-shaped, malformed, and oversized names.

This change supplies configuration validation and the handshake primitive. It does not enable termination in listeners, add certificate storage, or assert domain ownership. Those integrations require their own checks before the primitive is used. Persistent guest disks and additional host architectures are outside this contract.

## Validation

Portable race tests cover trusted negotiation, SNI rejection, expiry, certificate/key mismatch, signature restrictions, extended key usage, cancellation, parent deadlines, connection closure, and provider error sanitization. API tests cover configuration normalization and malformed DNS names. Native VM lifecycle qualification remains pending a Linux/amd64 KVM acceptance host.

# Companion ephemeral scratch acceptance

Gregale provides disposable scratch, not persistent container disks. Durable
state belongs in object storage or an external database. The scratch tmpfs
capacity is a ceiling, not a promise that the workload can consume that much
memory: the workload and VM memory fences remain authoritative.

Run `make test-companion-scratch-contract` as root on an explicitly designated
Linux/x86_64 acceptance host. This mount contract requires mount namespaces and
mount privileges; it does not require KVM. Use the pinned Go toolchain and keep
the command output with the tested commit. The runner rejects unsupported hosts,
skips, missing test results and failures. A cross-compiled binary is not passing
acceptance evidence.

The test calls the production mount helper and verifies the configured tmpfs
capacity, ENOSPC at the ceiling, independent companion contents and capacity,
space reclamation after deletion, sticky permissions, nosuid/nodev, and teardown.
Mounts exist only in a subprocess's private namespace. The parent owns temporary
empty target directories; it verifies that payloads do not survive namespace
exit. It does not mount over application or host runtime directories.

This test complements native microVM OOM, park/restore, cold fallback and leak
acceptance; it cannot establish those behaviors. See
[ADR-158](../adr/158-ephemeral-disk-boundary.md) for the stateless disk contract.
Native mount execution is pending until a designated Linux acceptance host is
available.

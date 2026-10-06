# ADR 399: Safely configure and monitor operator-provisioned TCP TLS

Status: proposed

Ansible exposes an optional absolute certificate directory and quotes its path in the systemd environment file. Empty keeps termination unconfigured. Control characters, dot path components, and filesystem root are rejected before filesystem access. Existing directories are inspected without symlink following and must be root-owned real directories without group or other write access. Their ownership and permissions are preserved; only missing directories are created root:faas 0750.

Operators remain responsible for certificate issuance and atomic installation of complete bounded PEM bundles with appropriate service read access. The gateway provider and handshake continue to validate material at runtime. This introduces no persistent guest disks and no automated private-key distribution.

Per-edge aggregate certificate availability and expiry gauges drive warning alerts after five and ten minutes respectively. Expiry alerts require a ready listener, so disabled nodes remain quiet. Both alerts link to the TCP ingress runbook. Local Ansible fixtures execute path guards and template rendering without service changes; promtool tests cover unavailable, expiring, healthy, and disabled cases. Native Linux/amd64 host qualification remains separate.

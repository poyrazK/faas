# ADR-025 · Decoupled Control Plane and Compute Nodes

- **Status:** proposed
- **Date:** 2026-07-21
- **Decision:** Evolve the FaaS architecture from a strict single-box loopback deployment to a decoupled, location-transparent topology. Specifically:
  - Transition the internal service-to-service gRPC boundaries (e.g. `schedd` ➔ `vmmd`, `builderd` ➔ `vmmd`) from hardcoded UNIX domain sockets to support standard TCP/IP networking secured via **Mutual TLS (mTLS)**.
  - Abstract local filesystem writes for rootfs layers and VM snapshot storage behind a unified storage interface (`StorageBackend`). Support local disk storage for single-box mode, and an OCI registry or object-storage-backed driver for distributed deployments.
  - Abstract edge routing in `gatewayd` to optionally tunnel or route network traffic via system-level mesh overlays (such as WireGuard or Cilium/VxLAN) when tenant guest TAP interfaces run on remote physical hosts.
  - Maintain absolute backwards compatibility with the existing single-box deployment mode, ensuring local developer loops and integration test suites run without modifications using localhost/loopback sockets.
- **Why:** The current prototype (Milestones M0 to M8) is structurally hardcoupled to a single host. Compute-bound services (`vmmd` and Firecracker microVMs) require hardware virtualization (`/dev/kvm`), which is unavailable or expensive on standard cloud VPS offerings (e.g. regular DigitalOcean Droplets). Decoupling the compute nodes allows developers to run control-plane services (`apid`, `gatewayd`, `schedd`, etc.) on inexpensive, standard cloud servers while routing virtualization workloads to dedicated hardware hosts or cloud instances that support nested virtualization (such as Intel GCP N1/N2 VMs).
- **Consequences:**
  - **Location Transparency:** Services can be run anywhere. The same system can be deployed on a single physical host or distributed across multiple cloud providers.
  - **Security (mTLS):** Moving gRPC communication over TCP introduces a network boundary. Services MUST enforce certificate verification via mutual TLS (mTLS) to prevent unauthorized control-plane calls.
  - **Shared Registry/Storage:** Introducing a remote storage driver eliminates the local disk dependency. Compute nodes pull required app and base layers on-demand, making compute nodes stateless and easily scalable.
  - **Config Additions:** `schedd` and `vmmd` gain standard gRPC server/client parameters (such as `listen_network`, `cert_file`, `key_file`, `ca_file`).

---

## Technical Details

### 1. Dialing & Listening Abstraction

Currently, `pkg/scheddgrpc` dials a hardcoded UNIX path:
```go
// Current dialer code
conn, err := grpc.Dial("unix://" + socketPath, grpc.WithInsecure())
```

We will extend dialing to parse a target address URL scheme (`unix://`, `tcp://`, `dns://`):
```go
// Proposed Dial helper in pkg/wire/grpc.go
func Dial(target string, creds *tls.Config) (*grpc.ClientConn, error) {
    var opts []grpc.DialOption
    if creds != nil {
        opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(creds)))
    } else {
        opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
    }
    return grpc.Dial(target, opts...)
}
```

### 2. Config Schema Extensions

#### `vmmd.toml` Updates:
```toml
# Network bind options
listen_addr = "127.0.0.1:50051"     # or "unix:///run/faas/vmmd.sock"
tls_cert_path = ""                  # Path to server certificate (optional)
tls_key_path = ""                   # Path to server private key (optional)
tls_ca_path = ""                    # Path to client CA certificate (optional for mTLS)
```

#### `schedd.toml` Updates:
```toml
# Remote vmmd target
vmmd_target = "unix:///run/faas/vmmd.sock" # or "tcp://10.128.0.5:50051"
vmmd_tls_cert_path = ""                    # Client certificate (optional)
vmmd_tls_key_path = ""                     # Client private key (optional)
vmmd_tls_ca_path = ""                      # Server CA certificate (optional)
```

### 3. File System Decoupling

We introduce the `StorageBackend` interface in `pkg/storage`:
```go
type StorageBackend interface {
    Put(ctx context.Context, key string, r io.Reader) error
    Get(ctx context.Context, key string) (io.ReadCloser, error)
    Delete(ctx context.Context, key string) error
}
```

*   `LocalStorageBackend`: Mounts local directory and writes files directly (used for single-box mode).
*   `OCIRegistryStorageBackend`: Wraps pushing/pulling images as OCI layers.

---

## Future Scaling & Multi-Node Orchestration

This decoupling directly unlocks horizontal scaling of the compute layer:
1. **Multi-Node Scheduling:** `schedd` will be upgraded from a single-box allocator to a multi-node scheduler. It will track the capacity and resources (vCPU, memory headroom, slot allocations) of all registered compute nodes, dispatching wake and create commands to the node selected by its placement algorithm.
2. **Cross-Node Routing:** The routing registry will map active instances to their host compute node's private IP. When `gatewayd` receives a request, it resolves the destination to the correct remote compute node IP and routes traffic across the private overlay network.
3. **Stateless Compute Nodes:** Because the filesystem is decoupled via the `StorageBackend` abstraction, compute nodes do not hold persistent customer state. They act as stateless execution runtimes that pull image layers on-demand, allowing new dedicated servers to be added to the cluster instantly.

---

## Rejected Alternatives

- **Always TCP (remove UNIX socket support):**
  - Rejected because UNIX domain sockets are faster, simpler, and provide OS-level file permission boundaries on single-box setups. We must retain UNIX socket support.
- **Plain TCP (no mTLS):**
  - Rejected. Exposing `vmmd`'s control surface (which runs as root and boots VMs) over plain unauthenticated TCP creates a critical vulnerability. Strong certificate-based authorization is mandatory for distributed setups.
- **CA-only verification (chain with no hostname pinning):**
  - Rejected. The first iteration of `loadClientTLSConfig` did chain-only verification by suppressing stdlib's hostname check (`InsecureSkipVerify=true`) and re-running chain validation in a custom `VerifyPeerCertificate` hook. That posture was strictly weaker than letting stdlib's default verifier run, and CodeQL alert #58 flagged the literal `= true` regardless of any rationale. The current design (issue #95, slice 1) relies on stdlib's `verifyServerCertificate` (handshake_client.go / handshake_client_tls13.go), which performs chain trust against the operator's `RootCAs`, RFC 6125 SAN matching, and EKU enforcement in a single pass during the handshake. grpc-go's `tlsCreds.ClientHandshake` populates `ServerName` from the dial `:authority` before `tls.Client` is called, so no caller-side plumbing is required.
  - **Operational consequence:** distributed deployments must issue per-daemon SANs (`schedd.faas`, `vmmd.faas`, …) on every leaf certificate. The local-dev PKI continues to issue SANs for `127.0.0.1`, `::1`, and `localhost`, so single-box tests stay correct. A future slice that adds a production-ready dev PKI for distributed setups should make this automatic.

# FaasVMMDGuestReceiverDown

`vmmd_guest_vsock_receiver_up{receiver="events|workload_identity"}` is `0` for a required guest-initiated channel. vmmd also reports this failure through `/readyz`, so the compute node must not receive new traffic while the alert fires.

Check the bounded failure class first:

```promql
sum by (receiver, kind) (increase(vmmd_guest_vsock_receiver_errors_total[10m]))
```

`prepare` means vmmd could not create or permission the per-instance Firecracker endpoint at `<jail-root>/vsock.sock_<port>`. Inspect free space, inode availability, jail ownership, and recent `vmmd` logs. `accept`, `read`, or `write` means an established Unix bridge failed; correlate the timestamp with a Firecracker exit or jail teardown. `protocol` and `overload` are counted but do not independently lower readiness because malformed or excessive guest input must not hold the entire node out of service.

The event receiver uses port 1027 for framework-ready, sidecar init/restart, waitUntil tail, workload OOM, and disk telemetry frames. The workload-identity receiver uses port 1030. Both endpoints are guest-initiated Firecracker STREAM sockets exposed on the compute host as `<configured uds_path>_<destination port>`; do not add a host `AF_VSOCK` bind to CID 2. The compute host is itself a GCE guest and cannot own `VMADDR_CID_HOST`.

After correcting the host or release problem, start a fresh healthy instance and verify both gauges return to `1`. Confirm `/readyz` is HTTP 200, then verify an event frame and a workload-identity request succeed before returning the node to traffic.

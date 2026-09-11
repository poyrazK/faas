# ADR-175 · Sidecar scratch and disk-I/O policy

- **Status:** accepted
- **Date:** 2026-09-12
- **Decision:** Sidecars may request a bounded writable scratch ceiling with
  `scratch_mb` and a named disk-I/O policy with `disk_io_profile`.

`scratch_mb=0` preserves the existing safe default: the sidecar RAM profile
when it is explicit, otherwise 64 MiB. Explicit scratch values are limited to
16..512 MiB and apply only to the sidecar's private `/tmp` tmpfs; sidecar image
roots remain read-only and no durable volume is created.

`disk_io_profile` is a closed set: `low`, `standard`, or `high`. Guest-init
maps these names to cgroup v2 `io.weight` values 50, 100, and 200 and places
the sidecar process in the corresponding workload leaf before exec. The `io`
controller is delegated in the guest cgroup namespace alongside `cpu` and
`memory`. An omitted profile inherits the guest default and does not create an
extra I/O leaf.

The fields are carried through the deployment JSON, scheduler/vmmd protobuf,
workload roster, and sidecar manifest. Existing deployments omit both fields
and retain their current RAM-derived scratch default and unrestricted guest
I/O scheduling. Persistent volumes, host-port allocation, and public
multi-port routing remain separate follow-up work.

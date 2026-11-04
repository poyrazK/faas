# ADR-20261025111339 · Guests give workloads the environment a container runtime provides

- **Status:** accepted
- **Date:** 2026-10-25
- **Amends:** [ADR-158](158-ephemeral-disk-boundary.md) (`/tmp`), [ADR-1790](1790-image-run-docker-process-and-registry-auth.md) (shares its HOME, PATH and WORKDIR rules with apps)
- **Decision:** after `pivot_root`, guest-init prepares the process environment Docker and Kubernetes give a container, for every guest mode:
  - **`/dev/shm`**: a tmpfs, mode 1777, `nosuid,nodev`.
  - **`/dev/pts`**: a private devpts instance, mounted at boot. It used to be mounted only by interactive app tasks.
  - **Open files:** `RLIMIT_NOFILE` is raised from the kernel's 1024/4096 to 65,536 soft and 1,048,576 hard (`GuestOpenFilesSoftLimit`, `GuestOpenFilesHardLimit`). Every process inherits it.
  - **Hostname:** a guest the kernel left unnamed (`(none)`) is named `gregale`.
  - **`/etc/hosts`:** guest-init adds `127.0.0.1 localhost` and `10.0.0.2 <hostname>` when the image's hosts file lacks them, and creates the file when the image has none. The image's own entries are kept.
  - **`/tmp`:** stays a RAM-backed tmpfs. When the image ships files under `/tmp`, the tmpfs is the writable upper of an overlay whose lower is the image's `/tmp`, so those files stay visible.
  - **The main app and app tasks** now get the image-run rules of ADR-1790:
    - `HOME` is the user's passwd home when the user can write there. Otherwise it is `/tmp`, as before.
    - An image without `PATH` gets Docker's default.
    - A missing working directory is created, owned by the workload user.
- **Why:** each gap broke a common image.
  - Chromium, Puppeteer, Playwright, PostgreSQL and Python `multiprocessing` need `/dev/shm`.
  - `localhost` and the hostname did not resolve in images without a hosts file, so a Dockerfile `HEALTHCHECK` running `curl localhost:$PORT` failed. That check is now enforced (ADR-20261024100000). Java's `getLocalHost` and Erlang node names failed on `(none)`.
  - A Go or JVM server, which raises its soft descriptor limit to the hard one, topped out near 4,096 sockets.
  - Config baked into `/home/<user>` (an `.npmrc`, a `.config` directory) was invisible with `HOME=/tmp`.
  - Files an image put in `/tmp` disappeared under the tmpfs.
  - Programs that open a terminal (`script`, `expect`, an embedded shell) had no ptys.

## Details

- **Failures only log.** Each step logs and the boot continues, so a guest that boots today keeps booting. The one exception is a missing working directory that cannot be created: the app fails to start with that reason instead of a bare `chdir` error.
- **Hosts file.** No `::1 localhost` line is added. A client that tries `::1` first, as Node 17–19 does, would miss a server listening on `0.0.0.0`; images that ship the line keep it. A symlinked hosts file is replaced by a regular file, as a runtime's bind mount would hide it. The write goes to the guest's writable root, like the `/run/guest-init` sockets.
- **Hostname.** Every guest of an app has the same hostname. Snapshot-restored guests already share every other kernel state. A per-instance name would need the resume hook to rename the guest, and it is not part of this decision.
- **Descriptor limit.** The hard limit is the kernel's default `fs.nr_open`, as under Kubernetes. The soft limit stays below it, so programs that close or index every possible descriptor at startup do not slow down. The guest's `fs.file-max`, which scales with its RAM, still bounds the total.
- **Sizes.** `/dev/shm` and `/tmp` are each sized at the kernel default, half the guest's RAM. What they hold counts against the guest's memory: filling either ends in the memory limit, not `ENOSPC`. Docker's 64 MB `/dev/shm` default is not copied, because it is the usual reason Chromium crashes in containers.
- **`/tmp` overlay.** The overlay's upper tmpfs is staged under `/dev` and detached once mounted, so no staging path is visible. With the two-drive root, `/tmp` becomes the second overlay level, which is the kernel's stacking limit. If the overlay cannot be mounted, `/tmp` falls back to the empty tmpfs.
- **HOME.** A home owned by another user, missing, or `/` keeps the `/tmp` fallback that production-us hunt #8 introduced. A home on the root filesystem counts against the ephemeral disk allowance (ADR-158), as under Docker.
- **Not covered.** Sidecars run in their own read-only roots and are unchanged. Compose `shm_size`, `ulimits`, `hostname` and `extra_hosts` still warn.

## Consequences

- Images that need shared memory, ptys, many descriptors, `localhost`, or their passwd home run unchanged.
- Apps whose passwd home is writable now write `~` to disk rather than RAM. Their memory snapshots shrink by whatever they cached there.
- Guest-init is a runtime unit; the change takes effect as images are rebuilt with the new guest-init. Host daemons are unchanged, so there is no version-skew window.
- Evidence: unit tests cover the hosts merge, HOME selection and PATH default. A `metal` test covers the `/tmp` overlay. Native acceptance of a booted guest is pending: there is no native host.

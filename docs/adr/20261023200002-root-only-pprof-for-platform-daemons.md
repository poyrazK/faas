# ADR-20261023200002: Root-only pprof for the platform daemons

- **Status:** accepted (owner, 2026-10-23: "do the pprof for platform daemons next")
- **Date:** 2026-10-23
- **Related:**
  - [ADR-20261023124955](20261023124955-observability-on-the-control-plane.md), whose `FaasCgroupResources` runbook found that a leaking control-plane daemon could not be profiled.
  - [ADR-1155](1155-on-demand-cpu-and-heap-profiling.md), profiling for customer instances, not the platform.
  - [ADR-122](122-canonical-metrics-listener-shape.md), the metrics listener this does not touch.
- **Decision in one line:** every platform daemon serves Go's `net/http/pprof` on a Linux abstract unix socket, `@faas-debug/<daemon>`. The socket accepts only connections whose peer credentials are root or the daemon's own user. `gregalectl debug pprof` captures profiles from it.

## Context

The platform daemons (apid, schedd, vmmd, the gateways, meterd, imaged, builderd and the rest) expose Prometheus metrics but no profiling endpoint. When one of them grows in memory, burns CPU or stalls on a lock, the only evidence is the metrics trend and the journal. The cause, an allocation site or a stuck goroutine, needs a heap profile, a CPU profile or a goroutine dump from the live process. ADR-1155 covers only customer instances.

Two easy answers are wrong here:

- **pprof on the metrics listener.** The metrics listeners are reachable from the control plane's Prometheus over the private network (ADR-122, nftables). Anyone who reaches them could pull goroutine dumps or start 60-second CPU profiles on the 2-vCPU control plane.
- **A filesystem socket under `/run/faas`.** The daemons run as different users (`faas-apid`, `faas-schedd`, `faas`, root for vmmd). About half of them have no write access to `/run/faas` under `ProtectSystem=strict`. Every unit would need a `RuntimeDirectory` and a group, and a reload's second apid generation would collide on the same path.

## Decision

1. **Where.** `wire.BootStamps`, which every daemon already calls once at start, starts the endpoint, so no daemon needs its own wiring.
   - It listens on the abstract unix socket `@faas-debug/<daemon>`. Abstract sockets have no file, so no unit or sandbox setting changes.
   - When the name is taken, for example by the outgoing apid generation during a two-generation reload, it listens on `@faas-debug/<daemon>.<pid>` instead.
   - The endpoint is off on non-Linux builds.
2. **Who.**
   - **Peer-credential check.** Each accepted connection's `SO_PEERCRED` must be uid 0 or the daemon's own uid; anything else is closed before a byte is read and logged.
   - **Network namespace.** Abstract sockets are visible only inside the host network namespace. Tenant VMs and their jailed Firecracker processes run in per-VM namespaces under jailer uids (20000–29999), so they fail both checks.
   - **No TCP listener** is added.
3. **What.**
   - The standard pprof index and named profiles: heap, allocs, goroutine (`debug=1|2`), threadcreate, block and mutex.
   - `/debug/pprof/profile` (CPU) and `/debug/pprof/trace`, with `seconds` capped at 60 and the server's write timeout above that. The Go runtime already allows only one CPU profile at a time.
   - `/debug/pprof/symbol`.
   - `cmdline` is not served: argv is not needed, and keeping it out removes one place a misconfigured flag value could leak.
   - Block and mutex sampling stay off, the runtime default, so those two profiles are empty unless a later change turns sampling on.
4. **Cost.** Nothing until someone connects. A heap or goroutine profile is a snapshot; a CPU profile costs a few percent of one core for at most 60 seconds.
5. **Kill switch.** `FAAS_DEBUG_PROFILING=off` in a daemon's environment disables the endpoint.
6. **Operator command.** `gregalectl debug pprof`:
   - `--daemon schedd --profile heap|cpu|goroutine|allocs|trace|… [--seconds N] [--debug 0|1|2] [-o FILE]`, run as root on the daemon's host, writes the profile to a file for `go tool pprof`;
   - `--pid` picks the generation-specific socket;
   - `--list` shows the daemons that are serving on this host.

## Consequences

- A leaking or spinning daemon can be diagnosed in place:
  `sudo gregalectl debug pprof --daemon apid --profile heap`, then `go tool pprof`.
- Anyone who already has root, or the daemon's own uid, can read its stacks and allocation sites. Both could already attach a debugger or read the process's memory, so this adds no capability.
- The endpoint shares the daemon's lifetime and stops on shutdown with the rest of `BootStamps`'s goroutines.

## Rejected alternatives

- **pprof on the metrics listener:** reachable over the private network; see Context.
- **A filesystem socket per daemon:** needs every unit edited and does not survive a two-generation reload without per-pid paths anyway.
- **Profiles pushed to the profiled daemon or a continuous profiler:** a standing CPU and storage cost on the control plane for a question that is asked rarely.

# Worker stop signal metal evidence (Hunt 14 H14-59)

Production-us rc.294: scaling a worker in killed its VM about 140 ms after
`worker_drain_started`, and the worker never logged its SIGTERM handler.
vmmd signalled the host Firecracker process, which exits at once.

## Run

`gregale-internal-test-1` (nested KVM, Firecracker 1.17.0), under the shared
`/run/lock/faas-builder-acceptance.lock`, with a private tmpfs jail, at source
`d9f59f1c`, using `worker-stop-metal.sh` in this directory. Native CI hosts
cannot start (billing is disabled on the CI project), as for #5394.

| Check | Result |
|---|---|
| leakcheck before and after | clean |
| `TestMetalWorkerStopSignalReachesTheWorkload` (new) | PASS: stop took 2.17 s for a 2 s drain, `killSignalSent=false`, exit code 0 |
| `TestMetalWorker_NotReapedAfterIdleWindow` | PASS |
| `guest/init` `TestLivenessPortDeliversWorkerStopSignals`, `TestControlPortDeliversWorkerStopSignals` (Linux) | PASS |

`TestMetalParkDeadlineReleasesPausedVM` was left out: on this shared host the
tenant bridge it creates already exists (`RTNETLINK answers: File exists`).

## What the gate found

The fixture worker traps SIGTERM, drains for 2 s and exits 0. Earlier runs on
the same host failed, and each failure was a real bug:

1. **The stop frame reached the liveness listener.** A long-running guest's
   liveness listener owns vsock port 1028, the job-control port. The guest
   console showed `vsock liveness unknown msg type type=5`, and the liveness
   error reply starts with byte 0, the job-control OK. So the host believed the
   stop was delivered, waited `grace + 5 s` and killed the VM (15.1 s,
   `killSignalSent=true`). Now the liveness listener dispatches type 5 to the
   workload's signal loop. The host also requires the one-byte ack to end the
   reply, and an older guest-init falls back to the immediate hard stop.
2. **Nothing reaped a guest that stopped on its own.** After the drain,
   guest-init exits as PID 1 and Firecracker keeps running; only builders had
   a halt watcher. `SignalJob` now polls the console while it waits and reaps
   the guest once guest-init has exited ("Attempted to kill init!", with its
   exit code) or the guest has halted.

Native x86_64 acceptance is still owed once the native CI hosts are back.

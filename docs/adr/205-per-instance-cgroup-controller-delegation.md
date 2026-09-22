# ADR-205 · vmmd enables the controllers a per-VM cgroup needs

- **Status:** accepted
- **Date:** 2026-09-22
- **Decision:** Before writing a per-instance cgroup fence, vmmd ensures the
  parent slice lists `memory`, `cpu`, and `pids` in its
  `cgroup.subtree_control`. Enabling happens one controller per write, only
  for controllers the parent actually has in `cgroup.controllers`, and is
  idempotent.
- **Why:** On the production fleet, no per-VM cgroup had a `memory.max` at
  all:

```
faas.slice                     controllers=[cpuset cpu io memory pids] subtree_control=[cpuset cpu io memory pids]
faas.slice/faas-tenant.slice   controllers=[cpuset cpu io memory pids] subtree_control=[cpu memory pids]
…/faas-tenant-scale.slice      controllers=[cpu memory pids]           subtree_control=[]
```

  In cgroup v2 a child only receives a controller's interface files when its
  **parent** lists that controller in `cgroup.subtree_control`. The per-plan
  tenant slices listed nothing, so every per-instance cgroup contained only
  `cgroup.*` and `*.pressure`. Verified on a live instance scope:

```
$ ls …/faas-tenant-scale.slice/61bc1c8f-…/
cgroup.controllers cgroup.events … cpu.pressure cpu.stat io.pressure memory.pressure
```

  No `memory.max`, no `cpu.max`, no `pids.max`.

  Nothing was responsible for enabling them. The `systemd_slices` role
  deliberately sets only `CPUWeight` on `faas-tenant-<plan>.slice` (ADR-044,
  so the parent's fence stays the only memory ceiling), and systemd only
  propagates a controller into a slice's `subtree_control` when a child
  **unit** needs it. The per-VM scopes are created by jailer, not by systemd,
  so systemd never did. `pkg/renderer` writes `subtree_control` for exactly
  one manifest-named slice, not the per-plan tenant children.

- **Two consequences, both serious.**

  1. **The §11 per-VM memory fence did not exist.** "Every VM via jailer:
     unique uid/gid, chroot, seccomp, cgroup scope `memory.max = plan + 8 MB`"
     is a ship-blocking security rule, and it was silently unenforced. A guest
     was bounded only by the shared `faas-tenant.slice` ceiling, so one tenant
     could consume the whole slice and get its neighbours OOM-killed.

  2. **Rolling rollouts were impossible.** Migration Phase 1 widens the
     instance's `memory.max` before capturing a snapshot. The write failed
     with `permission denied`, which is what `open(2)` returns when asked to
     create a file in cgroupfs — a misleading error that reads like a
     privilege problem even though vmmd is root and the directory is
     `root:root` mode 755. Every migration therefore failed at
     `sched: migrate one: Phase 1 prepare failed`, a graceful drain never
     reached an empty maintenance hold, and cd-platform's compute stage
     aborted after 48 retries with "instances still on <node>". 2,197
     occurrences on one node since 2026-09-19 18:58.

- **Why vmmd and not ansible.** vmmd owns per-instance cgroups: it is the only
  component that touches jailer, and it already writes `memory.max` and
  `cpu.max` there. Putting the enable in the deploy layer would leave a
  window on every fresh slice and would not repair the running fleet.
  Enabling a controller immediately materialises its files in **existing**
  children, so doing it in vmmd also fixes instances jailer already created —
  which is why the widen path calls it too, rather than pinning a
  pre-existing VM to its node forever.

- **Why one controller per write.** The kernel applies a multi-token
  `subtree_control` body atomically. `+memory +cpu +pids` against a parent
  lacking `pids` rejects the whole write and leaves `memory` disabled, which
  is the failure this ADR exists to prevent.

- **Consequences.**
  - A non-cgroup2 parent (the pure-Go test tier, the Lima shim) is not an
    error; enabling nothing is correct there. The caller's own `memory.max`
    write still fails loudly and names the exact path, so a real host missing
    the fence is never silent.
  - The per-VM fence begins to be enforced on deploy. An app that was
    exceeding its plan RAM under the shared ceiling will now be OOM-killed at
    `plan + 8 MB`, which is the specified behaviour.

- **Follow-up not taken here.** There is no test that asserts a live VM's
  `memory.max` equals `plan + 8 MB` on real hardware; the metal tier should
  gain one, because a unit test cannot observe cgroupfs accumulate semantics.

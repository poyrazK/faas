# Debug tools for `app exec` on images without a shell

Status: accepted

Extends: ADR-1037 (interactive app task attach), ADR-147 (full-rootfs
arbitrary OCI images). Touches spec §4.6 (runtime bases) and §11 (guest
security); changes neither the two-drive layout nor any §11 rule.

## Context

`gregale app exec <app>` (ADR-1037) starts a fresh, single-use VM from the
app's deployment and runs one command in it; the command defaults to
`/bin/sh`. Images with no shell (`gcr.io/distroless/*`, `scratch`-based Go
and Rust images) cannot use it: the session ends at once with "command not
found". Those images deploy as full-rootfs artifacts (ADR-147), so guest-init
pivots straight into the image and nothing from the runtime base stays
reachable. Issue #11 asks for a way to inspect such an app (files, env,
config, DNS, outbound reachability) without rebuilding the image with a shell.

What already exists:

- guest-init boots from the runtime base (drive0) as `/`, assembles the app
  root and pivots into it (`guest/init/main_linux.go`). Until the pivot the
  base is mounted and readable.
- `base-minimal` ships a pinned static busybox (`images/base-minimal.Dockerfile`).
  The node, python and go runner bases do not, and a full-rootfs app boots on
  its runtime's default base (`pkg/imaged/runtime_default_base.go`).
- The guest kernel is 6.1, so the new mount API (`open_tree`, `move_mount`)
  is available.

A Kubernetes-style debug container does not fit. A tools image would need a
third drive or a hot-plugged one: Firecracker cannot add drives to a running
VM, and an extra drive changes the device set of every VM, the drive layout
of §4.6 and each snapshot. Downloading tools at session time would add a
fetch path into tenant VMs.

## Decision

1. **Command.** `gregale app exec --debug <app> [-- COMMAND...]` opens an
   interactive session (tty unless `--no-tty`) whose command is the reserved
   session built-in `__gregale_debug_v1__` followed by the command
   (default `sh`). apid accepts it on interactive requests, with or without a
   tty, never with `command_shell`, with at most 64 arguments. It goes
   through the same admission, auth chain (bearer/session, MFA, deploy-write
   scope, IDOR-safe app load), timeout and VM lifecycle as any `app exec`
   session. No new scope.

2. **Tools ship in every runtime base.** Every runtime base an app boots on
   (base-minimal and the go, node and python runner bases; not agent-base or
   the staging-only Debian parent) carries the same pinned static busybox at
   `/usr/lib/gregale/debug/busybox`, `root:root` mode `0555`, with
   `/usr/lib/gregale/debug/bin/<applet>` symlinks for its applets. It comes
   from the busybox stage `base-minimal` already pins, so all bases carry one
   digest. A base is shared per node, so this costs about 1 MB per base
   generation per node, nothing per app. The tools are not on any `PATH`.

3. **guest-init keeps a handle on the tools across the pivot.** Before
   pivoting, guest-init clones `/usr/lib/gregale/debug` from the base with
   `open_tree(OPEN_TREE_CLONE | OPEN_TREE_CLOEXEC)` and keeps the detached
   mount's fd. A base without the directory leaves the fd unset. Nothing is
   mounted into the app's root.

4. **Each debug session gets its own mount namespace.** For
   `__gregale_debug_v1__`, guest-init starts the process the way it starts any
   session command (deployment user, working directory, environment, secrets,
   loopback bindings, PTY), with these additions in the child before exec:
   `unshare(CLONE_NEWNS)`, `/` made recursively private, the mountpoint
   `/.gregale/debug` created as guest-init creates `/dev`, `/proc` and `/sys`
   in images that lack them, and the cloned tree attached there with
   `move_mount`, read-only, `nosuid`, `nodev`. `/.gregale/debug/bin` is
   **appended** to `PATH`, so the image's own tools win where they exist. The
   command is resolved against that `PATH`.

5. **Deployment user only.** The session runs as the deployment's configured
   user, exactly like `app exec`. There is no `--root`.

6. **Audit.** The task row already records who started the session, the
   command, timing, node and exit code (ADR-1037). The reserved first argument
   marks a debug session, and task listings show it as `debug`.

7. **Failure modes are explicit.**
   - A guest-init or base without debug support (deployment made before the
     release): the session fails with `debug_tools_unavailable`, and the CLI
     says to redeploy so the deployment picks up the current base.
   - An image that has no shell, run without `--debug`: the CLI keeps the
     "command not found" result and adds "this image has no shell; use
     `gregale app exec --debug`".

## Security

- **No new privilege.** `app exec` already runs arbitrary commands as the
  deployment user with the app's environment and secrets. Debug sessions add
  only tools, under the same identity, in a VM that serves no traffic and is
  destroyed when the session ends.
- **The app's own image is unchanged.** The tools exist only in the session's
  private mount namespace. They never enter the image artifact, the deployed
  instances, or the namespace of any other process.
- **Not a boundary.** Hiding the tools is defense in depth, not isolation:
  code running as the deployment user could carry its own binaries. A task VM
  runs only the session's own processes, so no other process of the
  deployment user exists to reach the session's mounts through
  `/proc/<pid>/root`. (An execute-only `0111` busybox would also make those
  processes non-dumpable, but the base build converts the image to ext4 with
  an unprivileged `mkfs.ext4 -d`, which cannot read such a file.)
- **Network.** busybox includes `wget` and `nc`. They run inside the app's
  network namespace, so the app's egress policy (§11: no 25/465/587, no
  RFC1918, link-local or metadata ranges) applies unchanged.
- **Supply chain.** busybox is pinned by digest in the base Dockerfiles and
  covered by the existing base provenance and grype scans.

## Consequences

- Distroless and scratch-based apps can be debugged with `ls`, `cat`, `env`,
  `ps`, `wget`, `nc`, `nslookup` and the rest of busybox, without rebuilding
  the image. Shell-carrying images gain the same tools behind their own.
- Every runtime base gets a new generation. The rollout follows
  ADR-20261116190000, so nodes need no drain. Existing deployments get debug
  support at their next deploy.
- No change to the drive layout, snapshots, admission, billing, apid
  ownership or the vmmd and gateway session path. The new pieces are
  guest-init, apid's built-in validation, the base images and the CLI.
- Acceptance: unit tests for apid validation and the CLI. On a native KVM
  host, `make test-metal` runs a debug session against a `distroless/static`
  image and a two-drive node app (`ls /`, `env`, `wget` to an allowed and a
  denied address, mounts invisible from a second same-user process), and
  `make leakcheck` passes.

## Not decided here

- **Attaching to a live instance** (its processes, `strace`, memory): ADR-1037
  keeps sessions in their own VM. This needs a design of its own.
- **Root in debug sessions.**
- **Tools beyond busybox** (`curl`, `strace`, `tcpdump`).

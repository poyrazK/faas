# ADR-20261105150000: A release keeps the previous guest-init while its code is the same, so bases and snapshots survive it

## Status
Proposed.

## Context

On rc.322 (production-us, 2026-10-29 to 2026-11-05) 14% of user wakes restored
a snapshot, were refused, and cold-booted (p50 3.9 s against 534 ms for a
restore), and 205 of the 207 restore fallbacks on the compute nodes were
ADR-510's `snapshot backing images changed`. The kernel never changed (the
kernel digest is the same in all 209 refusals read; `reuse-release-kernel.sh`
keeps it). The base did: each release replaced the digest of every runtime
base the apps use, so each app's first wake after a release was refused. One
example: captured `base=sha256:f81d6ee6…`, now `base=sha256:676da494…`.

A base is an ext4 image of the OCI layers plus guest-init at `/sbin/init`.
imaged refreshes a staged base when the digest sidecar's guest-init differs
from the release's: it patches `/sbin/init` in place with `debugfs` under
`E2FSPROGS_FAKE_TIME`, so two nodes patching the same bytes publish the same
bytes (`pkg/rootfs/patch_base.go`). When the guest-init digest is unchanged,
nothing is patched and the base keeps its bytes. The refusals line up with the
releases whose guest-init digest differed: each new base digest first appears
in a refusal minutes after the release's pre-stage (rc.322: pre-stage
19:11–19:15Z, first refusal 19:18Z), on both nodes with the same digest.

### The cause, with bytes

The producer catalogue on the control plane (`base_image_producers`,
read-only, 2026-10-24 to 2026-11-05) holds **26 distinct guest-init digests in
12 days** (a rollout window can show two while old and new nodes alternate),
each published into 7–13 bases.

rc.321 and rc.322 have no commit under `guest/init`. Built from each tag's
tree for linux/amd64 with the release recipe of the time (the Makefile's
`GO_BUILD_FLAGS`, `-trimpath -buildvcs=false`, no ldflags, no tags; the one
setting that differs from CI is this machine's `-dwarf=false`, the same for
both):

| | rc.321 | rc.322 |
|---|---|---|
| size | 33,938,382 B | 34,128,101 B |
| `go version -m` | identical apart from the file name | |
| Go build ID | `s5NzLQuJ…` | `qcZglwY4…` |

Three packages in guest-init's 60-package internal import closure changed
between the tags: `pkg/api` (16 files), `pkg/datarepair` (27 files, 1,068
lines added) and `pkg/productcap` (`catalog.json`). Per-package symbol size:
`pkg/datarepair` 26,462 → 49,487 B, `pkg/api` 172,098 → 172,528 B (the 430 B is
the new method `Plan.LogBytesPerInstanceHour`, linked into PID 1 though
guest-init has no use for it: see 3 below).

Four things make guest-init's bytes follow its closure instead of its code:

1. **The Go build ID.** It hashes every package the binary links, and the
   linker writes it into two ELF notes (`.note.go.buildid`,
   `.note.gnu.build-id`). Built with the release recipe, rc.320 and rc.321
   differ in **those two notes and nothing else**: identical `.text`, `.rodata`,
   `.data`, line table. A closure change that alters no code still changes
   the bytes.
2. **The line table.** `.gopclntab` carries file and line numbers, so a line
   inserted above a linked function in any file changes it. That alone leaves
   the code digest unchanged (a comment added to a package of a tiny test
   module changed nothing else).
3. **Method retention.** guest-init imports gRPC, which imports
   `golang.org/x/net/trace` and so `html/template`, whose execution reaches
   `reflect.Value.MethodByName`. The linker then keeps every exported method of
   every type the program converts to an interface, all of `pkg/api`'s. This
   is finding 4 of ADR-20261020160744, which fixed it for the daemons with the
   `grpcnotrace` tag and left guest-init, built without tags, behind. With the
   tag the rc.322 binary is 25.0 MB instead of 34.1 MB and
   `Plan.LogBytesPerInstanceHour` is gone: a change to the body of a method
   nothing in guest-init calls no longer changes its code. The method's name
   still sits in its type's descriptor, so adding a method to a type guest-init
   links still changes it, and so does declaring a new type with methods in a
   package it links, even if nothing uses the type (measured on a tiny module:
   a new function, a constant, a type without methods and a comment each leave
   the code digest alone; a type with one method changes `.rodata` and
   `.typelink`). Most pkg/api additions are not of that kind.
4. **Real code.** `.text` differs in rc.318 → rc.319, rc.319 → rc.320 and
   rc.321 → rc.322 (the 23 KB of `datarepair` above): the guest changed.

The closure changes constantly. Over the 34 release pairs rc.290 → rc.324,
`pkg/api` changed in 32, `pkg/productcap` in 25 and `guest/init` itself in 14;
in 31 of the 34 a file that holds code linked into the rc.322 binary changed
(it links code from 91 files, 26 of them in `pkg/api`).

### The base is not the other half

`mkfs.ext4 -d` is not reproducible either: two runs with the production
arguments over one tree differ in 634 bytes (filesystem UUID, directory hash
seed, created/written/checked times, and every checksum seeded by the UUID).
Pinning `-U`, `-E hash_seed` and `E2FSPROGS_FAKE_TIME` makes one tree
reproducible, but inode ctime and atime come from the staging tree and differ
between builds (408 bytes). e2fsprogs 1.47.4 clamps them with
`SOURCE_DATE_EPOCH`; the compute nodes run Ubuntu 24.04's 1.47.0, whose
`mke2fs`, `libext2fs` and `libe2p` do not mention it. A rebuilt base cannot be
made byte-identical with the tools on the nodes, and a rebuild is not what
happens on a release: the patch above is deterministic. It stays as it is.

## Decision

**1. The release ships the previous signed release's guest-init while the code
is the same.** `scripts/reuse-release-guest-init.sh` runs in
`scripts/build-canonical-tarball.sh` after the release binaries are built
(`REUSE_GUEST_INIT=true`, set by `release.yml`), as `reuse-release-kernel.sh`
does for `vmlinux`. It finds the newest signed release reachable from the
commit, verifies its cosign bundle and signed manifest, checks that the
manifest's `tool_hashes.init` is the digest of the `init` in its tarball, and
compares that binary with the one just built with
`scripts/ci/guest_init_reuse.py`. The comparison is `binary_code.py`'s code
digest from ADR-1650 ("Selection by code"): name, size and contents of every
loaded section except the line table and the module data, no addresses; plus
the two build ID notes. The same bytes, or the same code under another build
ID or line table, replace the new binary with the prior one. Different code,
no prior signed release, or a prior manifest without a guest-init digest ship
the new binary. A bundle that does not verify, a digest that does not match,
or a binary that is not an ELF file fails the release.

Equality of code is transitive, so a run of releases with the same code ships
the same bytes however many there are, and imaged sees an unchanged guest-init
digest: no patch, no new base generation, no refused snapshot.

**2. guest-init builds with `grpcnotrace`** (Makefile `GUEST_TAGS`, used by
`make build` and `make build-release-batch`), and `scripts/ci/daemon_deps.json`
forbids `golang.org/x/net/trace`, `html/template` and `text/template` in its
closure as the binary `init` (`make daemon-deps-check`, ci-light). Nothing
reachable goes away: guest-init never enables gRPC tracing. This is what makes
the comparison in 1 pass for an edit to the body of a method nothing in
guest-init calls.

**3. A nightly check keeps it that way.** `scripts/ci/guest_init_stability.py`
(`make guest-init-stability-check`, `guest-init-stability.yml`, nightly and on
changes to the recipe) builds guest-init with the release recipe, adds a file
to `pkg/api` that nothing references (a function, a constant, a type without
methods), builds it again, and fails if the code differs. It catches a new
stamp, a flag, or a dependency that ties PID 1 to code it does not run. It
was not run on the finished probe here (the disk was nearly full): the
first run is the pull request's own. On a tiny module each of the three
leaves the code alone.

ADR-510's check is unchanged: vmmd still compares the exact content identity
of the kernel and the base and refuses any difference. Nothing here makes a
restore possible onto bytes that differ; it keeps the bytes the same when the
code is.

## What this does not fix

A release that changes what guest-init runs still changes PID 1, every base
and every snapshot taken on one, once. That is correct: the restored guest
reads its own `/sbin/init` from the disk the snapshot was taken on.

The five release pairs rc.318 → rc.323, built from each tag with the release
recipe and `grpcnotrace` and compared with `guest_init_reuse.py`:

| pair | verdict | what changed in files that hold linked code |
|---|---|---|
| rc.318 → rc.319 | changed | `guest/init` (5 files), `pkg/api` (`errors.go`, `limits.go`, `profiles.go`), `pkg/cronexpr` |
| rc.319 → rc.320 | changed | `pkg/api` (`appmanifest.go`, `limits.go`); `.text` same size, 67 KB of it different |
| rc.320 → rc.321 | **lines** | nothing: the two binaries differ only in the build ID notes |
| rc.321 → rc.322 | changed | `pkg/api`, `pkg/datarepair` (11 files); `.text` +25 KB |
| rc.322 → rc.323 | changed | `guest/init` (5 files), `pkg/api`, `pkg/datarepair`; `.text` +5.9 KB |

This change keeps one of the five: the release after a closure change that
touched no linked file, which is what rc.321 was. The other four ran
different guest code, so their bases changed, and the check that refuses their
snapshots is right. `pkg/api/limits.go` and `errors.go` are in four of the
five, because guest-init links constants and error codes from them: a changed
limit is a changed instruction. What remains are the options below, each its
own decision:

- **Carve a small guest-protocol package out of `pkg/api`**, starting with
  `limits.go` and `errors.go`. guest-init needs 1,623 `pkg/api` declarations (type-checked reachability from the 31 closure
  packages that use it, methods of included types included): 249 of them in
  `limits.go`, which changes with every plan change, in 56 files. It would
  move them behind aliases and move every importer in the closure
  (`guest/init`, `guest/executor`, `pkg/datarepair`, `pkg/sharedruntime` and
  ten more). It cuts the closure-driven changes, not the 14 of 34 releases in
  which `guest/init` itself changed.
- **Re-snapshot recently active apps in the background after a base refresh**
  (snapshots are cache, ADR-005). Covers every release, including those that
  change the guest. It is a scheduler feature: it needs an unbilled
  platform-initiated wake, admission within the tenant budget, and a way to
  tell a stale snapshot before a user wakes it. Not started here.

## Rejected

- **Make the base reproducible instead.** The nodes' e2fsprogs cannot clamp
  inode times (see above), and it would not change that guest-init's bytes
  change.
- **Run guest-init from an initramfs.** The binary would sit in RAM, off the
  base, and the snapshot would hold it. Every VM would keep the whole 25 MB
  resident instead of the pages it touches, in a fleet whose snapshot average
  target is 130 MB, and the boot contract, the kernel configuration and every
  host test change. A restored guest would also run a guest-init of any age
  under the current vmmd, which release compatibility (N beside N-1) does not
  allow without a new gate.
- **Ship guest-init under a hand-bumped epoch.** Nothing would catch a change
  that needed a bump; guest and host would skew silently.
- **Restore when only `/sbin/init` differs.** ADR-510 rejected heuristics that
  cannot be verified: the restored guest's cached metadata and the old init's
  lazily paged text must match the disk, and a debugfs patch frees and
  reallocates blocks.
- **Blank the build ID in the release build.** It would stop the first
  difference but not the line table, and the comparison has to ignore both
  anyway to accept a binary built before the change.

## Consequences

- The first release with this change changes guest-init once: `grpcnotrace`
  changes its code, and the Makefile recipe change makes the next unit-lane
  range wait for a full release (ADR-1650), as when `grpcnotrace` reached the
  daemons.
- guest-init shrinks from 34.1 MB to 25.0 MB: a smaller base copy on every
  patch, and fewer pages for the guest to read.
- A reused guest-init carries the line numbers of the commit that built it:
  panics and `slog` source positions from guest-init can name older lines until
  the next release that changes its code. ADR-1650 accepts this for daemons.
  `.gopclntab` also holds function metadata (stack maps, inline trees); a
  change that alters only that, with the same instructions, data and type
  descriptors, is treated as the same code until the next code change.
- The release job needs `gh` and `cosign`, as the kernel step already does. A
  release from a repository with no earlier signed release builds as before.
- builder-base compiles its own guest-init in `images/builder-base.Dockerfile`
  and is not covered: builder microVMs are not restored from snapshots.
- Checked on a copy of the release flow with stubbed `gh` and `cosign`
  (`scripts/reuse-release-guest-init_test.sh`); the first real release with it
  is the proof. After it, compare `tool_hashes.init` of two consecutive signed
  releases and `journalctl -u faas-vmmd | grep 'backing images changed'` on a
  compute node: both should be quiet for a release that did not change the
  guest.

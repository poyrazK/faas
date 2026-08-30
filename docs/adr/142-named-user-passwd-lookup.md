# ADR-142 · Named-user `/etc/passwd` lookup

- **Status:** proposed
- **Date:** 2026-08-30
- **Deciders:** Gregale platform team
- **Forks:** [Epic #1186 — make Gregale a first-class OCI container platform](#1186), sub-task C.3 (named-user `/etc/passwd` lookup). M-3 of the five-Mega-PR plan.

## Context

Two layers of the platform's user-identity stack are **today
hard-coded to `app` / 1000**:

1. **Layer-apply chown** (`pkg/rootfs/layer.go::parseOwnership`,
   `preserveOwnership`): tar entries with `hdr.Uid==0 && hdr.Uname!=""`
   trip the `unparseable_uid` counter and fall through to the daemon
   uid. Numeric uids in `[0, 65534]` (ADR-136's existing clamp) work
   fine; named users (the distroless shape — `Uname="nonroot", Uid=0`)
   silently land on root-equivalent uid 0 inside the guest, which
   spec §11 forbids.

2. **guest-init UID resolution** (`guest/init/main_linux.go:1620-1625`):
   `lookupUID(user string) int` is hardcoded to
   `api.DefaultAppUID` (1000) regardless of input. The image's
   `User: "node"` directive flows through `oci.ManifestFromConfig →
   normalizeUser` (`pkg/oci/image.go:237-249`) but never reaches the
   guest's `SysProcAttr.Credential{Uid, Gid}` — the guest runs as
   1000, not as the image's declared uid.

ADR-136 §Decision 5 (numeric-only chown) and §Negative consequences
explicitly deferred both layers to M-3:

> Named users (`USER node`, `USER postgres`) are explicitly
> out-of-scope. Today's behaviour for them is "the customer image
> runs as the default user". Documented in the ADR; addressed in
> M-3 via a guest-side `/etc/passwd` map keyed by `(image_digest,
> user)`.

> Layer-owner policy is fixed at numeric-with-cap. A future ADR may
> widen to named users (M-3) …

M-3 closes the named-user gap so distroless (`USER nonroot` → uid
65532), alpine (`USER root` → uid 0 with the right path handling),
and customer-declared users (`USER postgres` → uid 999) all resolve
correctly at guest boot.

**User decision (2026-08-30):** the named-user Resolver is **full-
rootfs-only**. Two-drive customers continue to use the runner-* base's
hardcoded `app`/1000 user — even if a runner-* customer adds
`USER postgres` to their Dockerfile. Smaller blast radius; the
runner-* Dockerfiles already declare the right UID at the base level.

## Decision

### 1. `Resolver` interface in `pkg/rootfs`

```go
// pkg/rootfs/parse_passwd.go (commit 7)
type Resolver interface {
    Resolve(name string) (uid, gid int, ok bool)
}
```

`parseOwnership` consults the resolver when
`hdr.Uid==0 && hdr.Uname!=""`. On `ok=true`, the resolved uid/gid
replace the zero; the existing `inOwnershipRange` clamp still applies
(ADR-136 `[0, 65534]`). On `ok=false`, today's `unparseable_uid`
counter increments and fall-through applies.

The original `ApplyLayer` / `ApplyLayerGz` are kept as thin wrappers
(`resolver=nil → today's behavior`). New variants
`ApplyLayerWithResolver` / `ApplyLayerGzWithResolver` thread the
resolver into the per-entry chown path.

### 2. Image `/etc/passwd` parsed at full-rootfs build time

The full-rootfs builder (`BuildFullRootfs`, commit 5) walks the
staging tree after layer application, finds every `/etc/passwd`
entry (one per layer, top-most first — overlay semantics), parses
each entry as standard `name:password:uid:gid:gecos:home:shell`
format, and merges bottom-up. The final map feeds a
`PasswdResolver` struct that satisfies `Resolver`.

```go
// pkg/rootfs/parse_passwd.go (commit 7)
type PasswdEntry struct {
    Name string
    Uid  int
    Gid  int
}

func ParsePasswd(r io.Reader) (map[string]PasswdEntry, error)

// pkg/rootfs/passwd_resolver.go (commit 7)
type PasswdResolver struct {
    entries map[string]PasswdEntry
}

func NewPasswdResolver(entries map[string]PasswdEntry) *PasswdResolver

func (p *PasswdResolver) Resolve(name string) (int, int, bool) {
    e, ok := p.entries[name]
    if !ok {
        return 0, 0, false
    }
    return e.Uid, e.Gid, true
}
```

The resolver is held by `BuildFullRootfs`'s closure and threaded
into every `ApplyLayerWithResolver` call as layers are applied. The
cap `UserUIDOverrideMax` (commit 9, Hobby 16 / Pro 64 / Scale 256)
bounds the per-build map size; counter `imaged_passwd_entries_total`
exposes the per-build count for telemetry.

### 3. Binary `/etc/faas/app_passwd` table

The builder writes the resolved table to `/etc/faas/app_passwd`
inside the produced drive (binary format, max 256 entries, owned by
root, mode 0644):

```
+--------+--------+---+-----+--------+--------+---+-----+
| uid    | gid    | n | name| uid    | gid    | n | name|
| 4B BE  | 4B BE  |1B | NB  | 4B BE  | 4B BE  |1B | NB  |
+--------+--------+---+-----+--------+--------+---+-----+
```

Each entry: 4-byte big-endian uid, 4-byte big-endian gid, 1-byte
length, N-byte name (UTF-8, no newlines). Entries are **sorted by
name** at build time so the runtime can binary-search in O(log n).

`alpine:latest` (~30 MB uncompressed, 5 layers) produces ~5 entries
(root, bin, daemon, sshd, …). `distroless/static-debian12` produces
~3 (root, nonroot, _apt). The cap at 256 entries is ~16 KB worst-case,
fits trivially in drive1.

### 4. guest-init reads the table at boot

```go
// guest/init/main_linux.go:1620 (commit 8)
func lookupUID(user string) int {
    if user == api.DefaultAppUser {
        return api.DefaultAppUID
    }
    if u, ok := readPasswdTable(user); ok {
        return u
    }
    return api.DefaultAppUID
}

// guest/init/passwd_linux.go (commit 8)
func readPasswdTable(name string) (int, bool) {
    f, err := os.Open(api.AppPasswdPath)
    if err != nil {
        return 0, false  // missing file → fall through to DefaultAppUID
    }
    defer f.Close()
    return binarySearchPasswd(f, name)
}
```

Read at guest boot — no per-request overhead. The lookup sites
already in place (`runAppWithEnv` line 352-356, `healthcheck_linux.go`
line 215) call `lookupUID(m.EffectiveUser())`; both pick up the new
implementation transparently.

When the table is **missing entirely** (legacy two-drive image, or
a full-rootfs build with no `/etc/passwd` in any layer), `lookupUID`
returns `api.DefaultAppUID` and the existing behavior is preserved.
Counter `guest_init_user_lookup_miss_total` increments for telemetry
on miss paths (a warn-log line is also emitted).

### 5. Numeric USER directives short-circuit

`oci.ManifestFromConfig` (`pkg/oci/image.go:137-177`) joins
`Entrypoint+Cmd` and projects `User` onto `manifest.User`. When
`manifest.User` is a numeric string (`"1000"`), `effectiveUser()`
short-circuits to that numeric value at `pkg/api/appmanifest.go:153-158`;
guest-init's `lookupUID` is still called but the table read is
wasted work. Future optimisation (M-4): branch on numeric-vs-named
at `effectiveUser()` to skip the table read entirely. Not load-
bearing today; the binary search is < 1 µs.

### 6. Customer USER override is M-4

The customer-side override axis (`faas deploy --user postgres`)
lands in M-4 (ADR-053 fourth axis). M-3 honors whatever the image
declares — the operator override stays a M-4 follow-up.

## Consequences

### Positive

- **distroless, alpine, scratch all deploy with their declared USER.**
  `whoami` reports the right uid inside the guest. spec §11
  (no root-equivalent uid) is satisfied by construction for distroless.
- **`/etc/faas/app_passwd` is owned by root + read-only.** `USER node`
  cannot rewrite the table from inside the guest — defense in depth.
- **Resolver is a package-private interface** (commit 5 wrapper
  + commit 7 impl). The legacy two-drive path calls `ApplyLayer` /
  `ApplyLayerGz` (no resolver); the full-rootfs path calls the new
  variants. No cross-path contamination.
- **Counter `imaged_passwd_entries_total`** exposes the per-build
  count; operators can alert on `> 200` (Hobby) / `> 500` (Pro) /
  `> 1000` (Scale) to catch a malicious layer that bloats the
  passwd map.
- **Counter `guest_init_user_lookup_miss_total`** exposes the
  boot-time miss rate; operators can alert on a sudden spike
  (suggests an image where `USER` doesn't match any entry in the
  merged `/etc/passwd`).

### Negative

- **Full-rootfs-only scope means runner-* customers adding `USER
  postgres` to their Dockerfile still get uid 1000** at guest-init
  time. The two-drive path doesn't carry the resolver. M-3 surfaces
  this in the docs as a known limitation; M-4 follow-up extends
  the resolver to two-drive if telemetry shows demand.
- **Resolver interface add adds ~250 LOC to `pkg/rootfs`/`pkg/imaged`
  build chain.** Mitigated by reusing `ApplyLayer` /
  `ApplyLayerGz` as the no-op wrappers; the two-drive call sites
  don't reflow.
- **Per-build passwd merge is O(layers × entries).** For
  `alpine:latest` (5 layers × ~5 entries = 25 ops) the cost is
  negligible. For a hostile layer declaring 256 entries, the merge
  is bounded by `UserUIDOverrideMax` (256) — still ~3 µs.
- **Two-pass build** (apply layers → walk `/etc/passwd` → write
  table). The second pass is `os.ReadDir` + `os.ReadFile` per layer;
  ~10 ms for `alpine:latest`. Acceptable; full-rootfs build is
  already cold-boot path, not hot-path.

### Neutral

- `EffectiveUser()` at `pkg/api/appmanifest.go:153-158` is unchanged;
  the resolver lives entirely in the build + guest-init layers.
- `pkg/oci/image.go::normalizeUser` is unchanged; numeric-uids and
  named-users pass through verbatim, same as M-1.
- `pkg/fcvm/cgroup.go` is unchanged — the in-guest uid is set by
  `cmd.SysProcAttr.Credential{Uid, Gid}` at `runAppWithEnv` line
  352-356, which calls `lookupUID`. The cgroup scope is for memory
  / CPU enforcement, not uid/gid.

## Rejected alternatives

- **Looking up `/etc/passwd` from the host.** Rejected — the host's
  `/etc/passwd` is unrelated to the image's; the guest has no network
  at boot; the resolver must operate on the image's own data.
- **Embedding the full `/etc/passwd` text into the drive as a
  regular file.** Rejected — wastes ~50 KB of inodes per app and
  exposes `/etc/passwd` to guest userland (which `USER node` then
  has write access to). The binary `/etc/faas/app_passwd` is owned
  by root and read-only — defense in depth.
- **Pre-baking a `(image_digest, user) → uid` map at imaged build
  time** (ADR-136's deferred alternative). Rejected — per-image
  rather than per-deployment requires the platform to know the
  customer's image ref at every read site. The in-drive binary table
  is per-deployment and self-contained.
- **Resolver-on-two-drive (extending the resolver scope to two-drive
  customers).** Rejected (user decision 2026-08-30) — runner-*
  Dockerfiles already declare the right UID at base level; extending
  the resolver to two-drive is a wider surface for marginal benefit.
  Tracked as M-4 follow-up.
- **Looking up passwd via `/etc/passwd` mount on a tmpfs the
  guest-init overlays at boot.** Rejected — tmpfs is RAM-backed;
  the platform's 47,600 MB tenant budget (§6.2 invariant 2) doesn't
  have headroom for an in-RAM `/etc/passwd` per VM. The binary table
  on drive1 costs ~16 KB worst-case.
- **Using nsswitch / NSS modules inside the guest.** Rejected — guest-
  init is a static-Go PID 1 (spec §11); adding NSS modules would
  require pulling in `libnss_*` shared objects, breaking the
  static-binary posture.

## Cross-references

- **Forced by Mega-PR #3 (M-3) of issue #1186**:
  - `pkg/rootfs/parse_passwd.go` (commit 7, new) — `ParsePasswd`
  - `pkg/rootfs/passwd_resolver.go` (commit 7, new) —
    `PasswdResolver` + `NewPasswdResolver`
  - `pkg/rootfs/layer.go` (commit 5) — `ApplyLayerWithResolver` /
    `ApplyLayerGzWithResolver`; `preserveOwnership` consults
    `Resolver.Resolve`
  - `pkg/rootfs/build_base.go` (commit 5) — `buildPasswdTable` helper
  - `pkg/rootfs/build.go` (commit 5, 7) — `BuildFullRootfs` calls
    `buildPasswdTable` post-layer-apply; threaded Resolver feeds
    every `ApplyLayerWithResolver`
  - `pkg/api/limits.go` (commit 9) — `UserUIDOverrideMax` per plan
  - `guest/init/main_linux.go:1620` (commit 8) — real `lookupUID`
  - `guest/init/passwd_linux.go` (commit 8, new) — binary table reader

- **Loading constraints (existing ADRs this PR must not violate)**:
  - ADR-136 (M-1): the `[0, 65534]` cap on layer-side chown is
    preserved verbatim; the resolver's resolved uid/gid are clamped
    through the existing `inOwnershipRange` gate.
  - ADR-019 (jailer uid 20000-29999): host-side. The resolver
    operates in the `[0, 65534]` guest-side range (ADR-136); host-
    side uid allocation is plumbed by `pkg/fcvm/jailer.go` and is
    OUTSIDE M-3's scope. M-3 adds no host-side widening.
  - ADR-005 (cold boot must always work): `/etc/faas/app_passwd`
    is in the staging tree (drive1), not in drive0. Cold boot works
    identically when the file is missing (resolver returns ok=false,
    fall through to DefaultAppUID).
  - ADR-009 (identical inner network world 10.0.0.2/30): unaffected.
  - ADR-040 (OCI layer symlink policy): unrelated to passwd resolution.

- **Issue / PR relationships**:
  - **#1186** (parent epic) — M-3 of the five-Mega-PR plan. This ADR
    closes sub-task C.3 (named-user passwd lookup).
  - **#474** (guest-init supervisor split, closed inside M-2):
    M-3's `lookupUID` reuses the M-2 supervisor wiring unchanged;
    no new guest-init signal handling required.
  - **PR #1190 (M-1)**: ADR-136 §Forced follow-ups named this work;
    M-3 closes the named-user gap.
  - **PR #1202 (M-2)**: `lookupUID`'s call sites (M-2 commit 7
    supervisor + M-2 commit 8 healthcheck) carry on with the new
    implementation transparently.

- **Spec sections**:
  - §4.6 (two-drive rootfs) — load-bearing constraint preserved.
  - §6.2 (invariants) — invariants 1-5 preserved by construction.
    The full-rootfs image's `/etc/passwd` is the source of truth;
    no invariant is violated.
  - §11 (security hardening) — `cmd.SysProcAttr.Credential{Uid, Gid}`
    is sourced from the resolver-clamped passwd table; no uid
    outside `[0, 65534]` ever reaches the guest. The table is
    read-only and owned by root.
  - §14 (delivery plan) — M-3 ships as part of M8.

- **Tests pinning this ADR**:
  - `pkg/rootfs/parse_passwd_test.go::TestParsePasswd_AlpineShape`
    (commit 7) — fixture matches alpine's `/etc/passwd`; assert 3+
    entries
  - `pkg/rootfs/parse_passwd_test.go::TestParsePasswd_DistrolessShape`
    (commit 7) — fixture matches distroless; assert root + nonroot
  - `pkg/rootfs/passwd_resolver_test.go::TestPasswdResolver_HitMissClamp`
    (commit 7) — resolve hit, miss, out-of-range (uid=70000 →
    clamped to 65534)
  - `pkg/rootfs/build_test.go::TestBuildFullRootfs_PasswdTableBuilt`
    (commit 5) — synthetic 2-layer image → assert binary table
    contains all entries
  - `pkg/rootfs/build_test.go::TestBuildFullRootfs_ResolverPassesDistrolessUID`
    (commit 7) — synthetic distroless shape → assert chown applied
  - `pkg/rootfs/layer_ownership_test.go::TestApplyLayerWithResolver`
    (commit 5) — table-driven over named-user / numeric / out-of-
    range cases
  - `guest/init/passwd_linux_test.go::TestReadPasswdTable_HitMiss`
    (commit 8) — fixture binary; lookup each entry; assert
  - `guest/init/passwd_linux_test.go::TestReadPasswdTable_MalformedFileFallback`
    (commit 8) — corrupt binary; assert miss returns (0, false)
  - `guest/init/main_linux_test.go::TestLookupUID_DefaultUser`
    (commit 8) — empty / `app` → DefaultAppUID
  - `guest/init/main_linux_test.go::TestLookupUID_NamedUserResolved`
    (commit 8) — fixture binary containing `postgres → (999, 999)`;
    `lookupUID("postgres")` returns 999
  - `pkg/api/limits_test.go::TestUserUIDOverrideMax_PerPlan`
    (commit 9) — per-plan cap pinning
  - `pkg/imaged/full_rootfs_metal_test.go::TestMetalFullRootfs_NamedUserResolution`
    (commit 10) — synthetic image declaring `USER node`; assert
    `id` inside guest reports 999 not 1000 not 0
  - `pkg/fcvm/vmm_user_resolution_metal_test.go::TestMetalUserResolution_LookupUIDFallback`
    (commit 10) — image declaring `USER 1000` numeric short-circuit

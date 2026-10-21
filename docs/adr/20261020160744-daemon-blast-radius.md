# ADR 20261020160744 · What a fix restarts: daemon blast radius

- **Status:** proposed
- **Date:** 2026-10-20
- **Related:** ADR-1650 (per-unit releases), ADR 20261018084150 (expand-only
  migrations in per-unit releases), ADR 20261018133100 (host-configuration
  lane), ADR-982 (apid packages), ADR-053 and ADR-419 (signed node reports)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

Per-unit releases (ADR-1650) ship a daemon when a file in its import closure
changed since the release the hosts run. The goal is that a fix restarts
only the services whose code it touches, with vmmd the expensive one (each
compute node drains until restart adoption lands). Each of the three fixes
measured below shipped 12 to 18 of the 28 release binaries.

`scripts/ci/blast_radius.py` measures two things for a change: the binaries
whose closure it touches (what the release ships) and the binaries whose
code differs when built at both commits with the release flags, one version
stamp and an empty Go build ID. Three recent fixes:

| fix | closure (ships today) | code | code, `grpcnotrace` | closure after the leaf packages |
| --- | --- | --- | --- | --- |
| #5045 one function in `pkg/sched` | 12 | 1 (schedd) | 1 (schedd) | 9 |
| #5048 `pkg/apptaskproto`, `pkg/executionproto` | 12 | 1 (vmmd) | 1 (vmmd) | 12 |
| #5052 one sqlc query + a migration | 18 | 12, vmmd among them | 6 (+4 line tables only, vmmd among those) | 18 |

Counts are release binaries (26 daemons and 2 CLIs). The three fixes
restarted vmmd three times; with `grpcnotrace`, its code changed in one.

Six findings explain the table.

1. **Raw bytes equal the closure.** The Go build ID embedded in every binary
   hashes every package the binary links, so a release binary's bytes
   change whenever anything in its closure does, even when none of its
   code does. Comparing release binaries byte for byte measures the
   closure; blanking the build ID (`-ldflags=-buildid=`) measures the code.
2. **The closure is a coarse proxy for the code.** #5045 touched one
   function that only schedd runs; the eleven other binaries that import
   `pkg/sched` link none of it. Closure-based selection ships all twelve.
3. **Imports of the scheduler engine for small contracts.** vmmd, imaged
   and builderd imported `pkg/sched` only for storage keys, node-report
   signing and the lease contract. The leaf packages `pkg/storagekey`,
   `pkg/nodereport` and `pkg/leaser` remove `pkg/sched` from their closures
   (the last column); `scripts/ci/daemon_deps.json` keeps it out.
4. **Every exported store method is linked into daemons that link gRPC's
   trace package.** A
   query change altered vmmd's code although vmmd never runs that query:
   vmmd linked 5,922 `*PgStore` methods. The Go linker keeps a method of a
   type converted to an interface only when some call site invokes a method
   of that name and signature through an interface, unless the binary
   reaches `reflect.Value.MethodByName` with a non-constant name, which
   makes it keep every exported method of every such type. gRPC imports
   `golang.org/x/net/trace`, whose `init` registers HTML pages rendered
   with `html/template`, whose execution engine calls `MethodByName`. With
   gRPC's `grpcnotrace` build tag the trace package is gone and vmmd links
   146 `*PgStore` methods (meterd 191, imaged 222); its stripped binary
   shrinks from 112.4 MB to 47.0 MB.
5. **Interface width does not matter to the linker.** A program that holds
   the full store interface and calls one method links that one method;
   so does one that holds a one-method interface. Narrowing each daemon's
   store to an interface of the methods it calls (the plan this record was
   opened for) changes neither the closure (`pkg/state` stays in it) nor
   the linked code once finding 4 is fixed.
6. **Daemons that embed the migration set change with every migration.**
   `pkg/db`'s goose entry points read `migrations.FS`, the embedded SQL of
   every migration. imaged applies migrations at start, and the new
   migration file of #5052 is in meterd's and imaged's binaries, so both
   ran changed bytes for a query neither runs.

What stays: a line inserted above a function shifts the line numbers that
every binary linking later functions of the same file carries in
`.gopclntab`. Those binaries run the same code; the report lists them as
`lines`. #5052's vmmd under `grpcnotrace` is one: the inserted line sits in
the 70,000-line generated `queries.sql.go`, above queries vmmd runs.

## Decision

1. **Release builds give daemons the `grpcnotrace` tag.** The Makefile names
   the daemon tag set once (`DAEMON_TAGS := metal,grpcnotrace`) and every
   daemon build target uses it; `affected_units.py`, `blast_radius.py` and
   `check_daemon_deps.py` load closures with it, and a change to it is a
   release build recipe change. Nothing reachable goes away:
   `grpc.EnableTracing` is never set, so no RPC was traced, and the
   `/debug/requests` and `/debug/events` pages the trace package registers
   live on `http.DefaultServeMux`, which no daemon serves. Tests keep
   building without the tag.
2. **Cross-daemon contracts live in leaf packages.** A type, key layout or
   signature two daemons must agree on goes in a small package that imports
   neither the scheduler nor the store; the owning package re-exports it
   under its old names. `scripts/ci/daemon_deps.json` lists the packages a
   release binary must not link (`make daemon-deps-check`, ci-light), with
   `golang.org/x/net/trace` forbidden in every daemon so finding 4 cannot
   return through a new import.
3. **No blanket narrowing of `pkg/state` per daemon.** Finding 5 measured it
   at zero effect. The test-store rules in CLAUDE.md stand; new code may
   still take a small interface where that makes a fake easier.
4. **Every pull request reports its blast radius.** `blast-radius.yml`
   (report-only, poyrazK/faas#5056) comments the closure set and, of it,
   the binaries whose code or only line table changes.

## Next step: selection by code (decided 2026-10-21)

Decided and built in ADR-1650, "Selection by code": the release keeps the
closure as its candidate set, builds the candidates at base and head as
`blast_radius.py` does (the build and digest code now lives in
`scripts/ci/binary_code.py`, shared by both), and a host restarts a unit
only when the code it runs differs from the release's. `lines` binaries do
not restart; ADR-1650 gives the reasons. Unit versions keep naming the
commit a binary was built from, and a host keeps the version of a unit
whose code it already runs. The proposal as first written:

**Select per-unit release binaries by code, not by closure.** The release
lane would build each closure candidate at the base release and at head
(the blast-radius build) and ship only the ones whose code changed;
`same` and `lines` binaries stay at the base version, which runs identical
code. With the decisions above, the three fixes would ship 1, 1 and
6 binaries instead of 12, 12 and 18, and vmmd would ship only for #5048,
whose code it runs. Costs and open points:

- Two builds of the candidates in `release-units.yml`: about a minute each
  for a few binaries with a warm cache, several minutes when `pkg/state` or
  go.mod changes and every binary is a candidate.
- Unit versions: a shipped unit keeps `unit-<newest closure commit>`; a
  candidate whose code did not change keeps the base release's version, so
  the deploy plan and host inventory stay truthful about what runs.
- A `lines` binary's stack traces and `slog` source positions keep the old
  line numbers until the next release that ships it.

## Consequences

- vmmd, imaged and builderd no longer restart for scheduler-only fixes;
  vmmd no longer restarts for store query changes it does not run (with
  selection by code, ADR-1650).
- Daemon binaries shrink (vmmd 112.4 MB → 47.0 MB stripped), which lowers
  RSS on every node.
- `pkg/sched` re-exports the moved contracts; code outside `pkg/sched`
  should import the leaf packages.
- The first release with `grpcnotrace` changes every daemon once (a recipe
  change).
- Follow-up candidate (finding 6): a daemon that only needs to know whether
  the schema is current could compare against a generated version list
  instead of embedding every migration's SQL. Not decided here.

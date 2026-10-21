# ADR 20261021092138 · Keep the edge lane off the store type: edge store

- **Status:** proposed
- **Date:** 2026-10-21
- **Related:** ADR 20261020160744 (daemon blast radius; amends its
  decision 3 for the edge daemons), ADR-1650 (per-unit releases, selection
  by code), ADR 20261020214632 (automatic unit deploy safety rails)

## Context

A per-unit release restarts a daemon only where its code changed
(ADR-1650, "Selection by code"). The edge lane is the most expensive one to
restart: a canary node held on a 4-minute gate, then every other host one at
a time (about 7 of the 11 minutes of an edge-only deploy). On 2026-10-21 the
owner asked why almost every pull request's blast-radius comment lists many
daemons: are the pull requests too big, or does the code need more
separation?

**What the comments show.** Of the last 26 comments:

| pull requests | paths changed | binaries whose code changed |
| --- | --- | --- |
| small fixes outside `pkg/state` (#5060, #5084, #5085, #5086, #5091) | 2-4 | 1-2 |
| small fixes in `pkg/state` (#5062, #5073, #5082) | 4-10 | 12-17 |
| batch pull requests (#5038-#5040, #5049, #5065) | 25-81 | 15-27 |

An edge daemon's code changed in 18 of the 26. In 4 of those (#5062,
#5064, #5073, #5082) the only change the gateways could have cared about
was in `pkg/state`. Of the last 120 merges, 43 touched `pkg/state` and 22
added an exported `*PgStore` method.

**Why a store change reaches every daemon.** Built with the release recipe
and compared by code digest (`scripts/ci/binary_code.py`), on main
39ed0429b6:

| change to `pkg/state` | bridged (links 4 `*PgStore` methods) | meterd | gatewayd-public |
| --- | --- | --- | --- |
| add one exported `*PgStore` method no one calls | changed | changed | changed |
| edit the body of a `*PgStore` method they do not call | same | same | same |
| add one `*sqlc.Queries` method no one calls | same | changed | same |

Adding a method changes binaries that never call it. Every daemon links
reflection (`fmt`, `encoding/json`, `log/slog`). The Go linker then keeps
the method table (names and types) of every type reachable from an
interface value, so that reflection can list the methods. That includes a
type that is only a field of a struct converted to an interface. The
daemons hand `*PgStore` to their packages as `state.Store` or narrower
interfaces, so its type, with all of its method names, is in all 12
daemons that link `pkg/state`. A minimal program shows the rule:

| `*T` in the program (reflection linked) | add an unused method to `*T` |
| --- | --- |
| converted to an interface | changed |
| a field of a struct converted to an interface | changed |
| a field of a struct only called directly | same |
| only called directly | same |
| converted to an interface, no reflection in the binary | same |

ADR 20261020160744 found (finding 5) that a narrower interface does not
change the code a daemon links, and decided against narrowing `pkg/state`
per daemon (decision 3). That measurement holds, but it measured edits to
methods, not additions. Narrower interfaces do not help additions either:
the concrete type behind them still carries every method name. What helps
is a binary that never reaches the `*PgStore` type.

Finding 7 of that ADR is the other half. Two daemons, gatewayd-internal and
apid, executed an `html/template`. That made the linker keep the code of
every method, not only the names, so even edits rebuilt them.
poyrazK/faas#5098 removes it from gatewayd-internal, which then links 213
`*PgStore` methods instead of 4,169.

**Precedent.** The store already has small concrete types over the same
pool for narrow jobs: `state.NewPGRateLimitBackend` and
`state.NewPGConcurrencyQueueAdmission`, which gatewayd-internal uses.

## Decision (proposed)

1. **An edge store.** gatewayd-internal and gatewayd-public stop linking
   `*state.PgStore` and `state.Store`.
   - A concrete `state.EdgeStore`, built over the same pool, implements
     exactly the interfaces the gateway packages consume. That is about 250
     methods: 213 in gatewayd-internal and 50 in gatewayd-public, which
     overlap.
   - Each query has one implementation, a package-level function over
     `sqlc.DBTX`. The `*PgStore` method and the `EdgeStore` method are
     one-line wrappers over it, so the two cannot drift.
   - Plain functions have no method table: a binary carries only the ones
     it calls.
   - A generator writes the wrappers, and a test checks that every
     `EdgeStore` method has a `*PgStore` twin calling the same function.
2. **Gateway packages take narrow interfaces.** Files in `pkg/gateway` that
   take `state.Store` take the interface of the methods they call, as most
   of the package already does. Otherwise adding a method to `state.Store`
   still changes the edge daemons through the interface's own method list.
3. **A link-level guard.** A check links each edge daemon with
   `-ldflags=-dumpdep` and fails when `type:*…/pkg/state.PgStore` or
   `type:…/pkg/state.Store` is reachable. It runs where the blast-radius job
   already builds the binaries, on changes to `pkg/gateway`, `pkg/state`
   and `cmd/gatewayd-*`. `scripts/ci/daemon_deps.json` cannot express it,
   because the gateways still import `pkg/state`.
4. **Not decided here:** a per-domain split of `*PgStore` for the
   control-plane daemons. Their lane is cheaper (one host, one gate),
   apid links the whole store regardless (finding 7), and the edge store
   will show what a split costs. Revisit with its measurements.

## Options considered

- **Narrow interfaces only.** No effect: the concrete type behind them
  carries every method name.
- **Split all of `pkg/state` by domain now.** It is the long-term shape,
  but it means about 2,400 files and the `MemStore` contract tests. It is
  larger than the cost it removes today.
- **Hide `*PgStore` from reflection** (function-valued fields, `unsafe`).
  Brittle, and it breaks the next time someone stores the type in an
  interface.
- **Smaller pull requests.** Useful for review, but a 4-file store fix
  still changes 12-17 daemons.

## Validation before acceptance

- **V1 (gatewayd-public, 50 methods):** build the edge store for it alone.
  Adding a `*PgStore` method must leave gatewayd-public's code digest
  unchanged, and editing a query it calls must change it.
- **V2:** the `-dumpdep` guard finds neither type in gatewayd-public, and
  fails when one is reintroduced.
- **V3:** the same for gatewayd-internal, after poyrazK/faas#5098.
- **V4:** replay #5062, #5073 and #5082 against the result. The edge
  daemons' code must not change.

## Consequences

- A store method addition or edit no longer redeploys the edge lane unless
  it changes a query the gateways run. The lane runs only for gateway
  changes, and with the canary that is about 7 of the 11 minutes of an
  edge-only deploy.
- A new query the gateways need takes a wrapper on `EdgeStore` as well as
  on `*PgStore`, from the generator.
- Edge daemon binaries shrink further: they stop carrying the names and
  types of about 4,000 store methods.
- Gateway tests keep their fakes: they implement the same narrow
  interfaces. The shared query functions keep running under the existing
  `PgStore` contract tests.

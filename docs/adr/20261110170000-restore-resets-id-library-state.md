# ADR-20261110170000: A restore resets known ID libraries' per-process state

- Status: Accepted (owner decision 2026-11-10: patch known libraries, warn on
  the rest)
- Date: 2026-11-10
- Amends: ADR-680/687 (restore userspace RNG reseed),
  ADR-20261109140000 (report what the barrier missed)

## Context

The 2026-11-09 snapshot review listed ID libraries that keep random state in
module or process scope. A snapshot restores that state, so every instance
restored from one snapshot generates the same next IDs:

- npm `uuid`: a 256-byte pool behind `v4()` in versions that do not use
  `crypto.randomUUID` (and behind every call with options), plus the node,
  clock sequence and counter state of `v1`/`v6`/`v7`;
- `nanoid`: a pool refilled only when exhausted;
- `bson` (MongoDB drivers, Mongoose; Node and Python): a 5-byte per-process
  value and a counter, regenerated only when the PID changes, which a
  restore keeps;
- Go `rs/xid`, the MongoDB Go driver's `ObjectID`, and the OpenTelemetry Go
  SDK's trace and span ID generator, all seeded once per process.

Duplicate ObjectIds become duplicate-key errors; duplicate UUIDs and trace
IDs corrupt data and telemetry. The ADR-680 barrier reseeds the runtimes'
generators but cannot reach module-scope variables.

## Decision

1. **Node.** The reseed preload appends, when Node compiles a known file, a
   function that puts the module's state back the way the module first had
   it, and runs every such function on each restore after `Math.random` is
   reseeded (and on adoption in thread mode):
   - `uuid/dist/**/rng.js`: `poolPtr = rnds8Pool.length` (next call refills);
   - `uuid/dist/**/v1|v6|v7.js`: clear `_state`, or `_nodeId`/`_clockseq`;
   - `nanoid/index.(c)js`: drop the pool;
   - `bson/lib/*`: `PROCESS_UNIQUE = null` and a fresh `ObjectId.index`.
   CommonJS files are patched through `Module.prototype._compile`; ES
   modules through `module.registerHooks` (Node 22.15+). Bundled copies are
   not recognized.
2. **Python.** The reseed preload sets a fresh `bson.ObjectId` per-process
   value and counter when `bson.objectid` is loaded.
3. **Best effort.** A version whose variables differ is left alone: the
   appended function throws and is ignored. The barrier never fails over an
   ID library.
4. **Go: warn.** After each barrier pass guest-init logs a warning naming
   the known Go ID modules (from the build info of Go workload processes)
   that nothing outside the program can reset.

## Consequences

- Node and Python apps using these libraries stop repeating IDs across
  restored instances, without code changes.
- Each restore runs a few assignments per loaded library.
- Not covered: bundled copies, ESM on Node before 22.15, other libraries,
  and Go programs; the docs keep them on the "reseed these yourself" list.
- Validation: the preload against real packages (uuid 8 and 11, nanoid 3
  and 5, bson 6, CommonJS and ES modules; pymongo 4.19 bson), and a unit
  test for the Go module match.

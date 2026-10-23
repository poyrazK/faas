# Admitted runtimes survive a vmmd restart: append-only receipt re-binding

Date: 2026-10-18
Status: Proposed (slices 1–4 built on `feat/vmmd-no-drain`; version-skew runs
owed, see Slices)
Extends: ADR-595 (native boot contract), ADR-435 (native incarnation history)
Related: `20261017-vmmd-restart-adoption.md` (the adoption ADR: ownership
epoch, adoption report, "Runtime-admission incarnation: admitted guests are not
adopted"); spec §6, §11

## Summary

A guest booted under an ADR-595 runtime-admission receipt is today refused
when vmmd restarts (`admission_receipt`). Its row fails and the app is
re-admitted fresh: one re-admission per restart for every app under a managed
application standard, and a pre-restart check that drains the node whenever one
is running. The receipt cannot follow the guest because ADR-595 ties every
receipt to the vmmd process incarnation that issued it, and its history is
immutable by design.

This ADR keeps that history immutable and adds one append-only fact beside
it: "instance X's admission, issued under incarnation A, is held by
incarnation B since ownership epoch E", written by schedd from the successor's
adoption report under the epoch fence. The guards that compare a receipt's
incarnation with the node's current one resolve it through these rows
instead. Grants stay single-use and are always issued to the current
incarnation, and receipts and bindings never change. The successor checks the
parent receipt against its own adoption evidence, so vmmd and PostgreSQL agree
independently. When the row cannot be written, the outcome is today's: the
guest is refused and re-admitted.

## Context

### Where the issuing incarnation is checked after boot

Every ADR-595 grant names `node_id` and `incarnation`, and every receipt
embeds its grant's binding (`pkg/runtimeadmission/binding.go`). After the boot,
the receipt's incarnation is compared with the current process in:

| Where | Check |
|---|---|
| vmmd `PromoteAdmitted` (`pkg/fcvm/runtime_promotion.go`) | fresh binding's incarnation = this process |
| `runtimeadmission.Promotion.checkFreshPromotionBinding` | fresh binding = parent binding except token, payload hash and clock, so the parent's incarnation = the fresh one |
| vmmd `checkSnapshotGrantIdentity` (`pkg/fcvm/runtime_snapshot_admission.go`) | capture grant's parent incarnation = this process |
| schedd promotion builder (`pkg/sched/application_standard_native_promotion.go:58`) | node identity's incarnation = parent's |
| schedd observation roster (`pkg/state/application_standard_observation_artifacts.go:53`) | node's native incarnation = binding's |
| `application_standard_native_publication_guard` (trigger on `instances`) | the instance's receipt binding incarnation = `compute_nodes.vmmd_incarnation` on every write into running, warm or migrating |
| `application_standard_lock_native_promotion`, `application_standard_native_promotion_guard` | promotion binding incarnation = node's; parent equality as above |
| `application_standard_lock_snapshot_capture`, `application_standard_snapshot_resume_valid` | parent receipt incarnation = node's; resume parent equality |
| PostgreSQL current-receipt read (`GetCurrentApplicationStandardRuntimeReceipt`: schedd's measured-capture path, native-instance qualification); missed by the first version of this inventory | receipt binding incarnation = `compute_nodes.vmmd_incarnation` |

ADR-435 adds `application_standard_native_incarnations`: an append-only
history of each node's incarnations, and a trigger that refuses to
re-activate a superseded one.

So once vmmd restarts, `compute_nodes.vmmd_incarnation` moves to the new
process and an admitted guest loses everything above. Even if vmmd adopted it,
it could not be migrated, captured or promoted, and its row could not move
into running or warm again.

### What the adoption ADR already provides

- An ownership epoch claimed by compare-and-swap on `compute_nodes`
  (`vmmd_epoch`, `vmmd_epoch_incarnation`, `vmmd_predecessor_incarnation`).
  The claimant's incarnation is the same random value ADR-595 registers.
- A multi-factor proof that a surviving Firecracker process is the one the
  journal recorded: boot id, pidfd, cgroup, netns inode, jail, API identity
  and machine configuration.
- An adoption report schedd applies under the epoch. A row the new vmmd does
  not hold goes through ADR-419's repair (`failMissingRunning`).
- The owner decision of 2026-10-18: until an ADR-595 extension exists,
  admitted guests are refused at adoption.

## Decision

### 1. The re-bind record

A new append-only table, beside the immutable boot and promotion history:

```sql
CREATE TABLE instance_application_standard_rebinds (
 instance_id uuid NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
 node_id uuid NOT NULL,
 from_incarnation uuid NOT NULL,
 to_incarnation uuid NOT NULL CHECK (to_incarnation <> from_incarnation),
 vmmd_epoch bigint NOT NULL CHECK (vmmd_epoch > 0),
 observed jsonb NOT NULL CHECK (jsonb_typeof(observed) = 'object'),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (instance_id, from_incarnation),
 UNIQUE (instance_id, to_incarnation),
 FOREIGN KEY (node_id, from_incarnation) REFERENCES application_standard_native_incarnations (node_id, incarnation),
 FOREIGN KEY (node_id, to_incarnation) REFERENCES application_standard_native_incarnations (node_id, incarnation)
);
```

`observed` is what the successor saw of the adopted guest: `netns`,
`host_ip`, `lease_uid`, `paused` and the receipt's `token` and
`completed_at_unix_nano`.

A `BEFORE INSERT OR UPDATE OR DELETE` guard enforces:

- **Append-only.** UPDATE always raises. DELETE raises unless the instance
  row is gone, which is the cascade from owner erasure or instance deletion.
  This mirrors `application_standard_native_incarnation_guard`.
- **Current holder.** On INSERT, `compute_nodes` is locked `FOR SHARE NOWAIT`.
  `to_incarnation` must equal both `vmmd_incarnation` and
  `vmmd_epoch_incarnation`, and `vmmd_epoch` must equal the row's `vmmd_epoch`.
  A superseded or zombie process therefore cannot write one, and neither can a
  report from an earlier epoch.
- **Chain head.** `from_incarnation` must be the instance's effective
  incarnation (§2) at insert time. It must also have registered before
  `to_incarnation` in ADR-435's history. One that skips an intermediate
  incarnation (A→C after B died before its rebind was written) is allowed,
  because nothing under B could have used the receipt without an A→B row.
- **Live, unchanged residency.** The instance row must be on `node_id` in
  `running` or `warm`, with its boot or promotion token set. `observed` must
  equal the current receipt's `netns`, `host_ip`, `lease_uid`, `token` and
  `completed_at_unix_nano`, and `paused` must equal `state = 'warm'`. The row's
  own `netns`, `host_ip` and `guest_uid` must match too. History therefore
  cannot recreate residency after cleanup, as ADR-595 requires.

### 2. Effective incarnation

```sql
application_standard_effective_incarnation(instance uuid, issued uuid) RETURNS uuid
```

It follows `from_incarnation → to_incarnation` from `issued` and returns the
last hop. With no rows it returns `issued`. Each (instance, from) is unique and
rows never change, so the walk is deterministic and bounded by the node's
incarnation count.

A new migration redefines, with `CREATE OR REPLACE`, every guard in the
Context table to compare `application_standard_effective_incarnation(instance,
binding incarnation)` with the node's current incarnation, where it used to
compare the binding's literal incarnation:

- the publication guard;
- the promotion lock and guard;
- the snapshot-capture lock;
- the resume validation.

The Go checks on the schedd side (the promotion builder and the observation
roster in `pkg/state`) follow the same rule in slice 3.

The parent-equality comparisons also accept the parent's incarnation differing
from the fresh one exactly when the effective incarnation of the parent's equals
the fresh one. Nothing else in a binding may differ. Fresh grants still name the
current incarnation literally, and the boot guard's checks on new grants are
unchanged.

### 3. vmmd: the receipt joins the adoption record

- The v7 adoption block records the guest's current receipt in full
  (`runtimeadmission.Receipt`, which by ADR-595 carries no payload bytes or
  credentials). It also records `runtimeAdmissionEgress`: plan, allowlist and
  ports. This replaces today's `admitted` flag. The receipt is rewritten
  synchronously after a promotion, in the same commit discipline as
  `migration_paused`.
- The successor restores `runtimeAdmissionReceipt` and
  `runtimeAdmissionEgress`. It also marks the instance `admissionAdopted`,
  together with the issuing incarnation it adopted.
- Its identity checks accept a parent whose incarnation is not this process's
  only when two things hold: this instance was adopted carrying exactly that
  receipt (`Receipt.Equal`, already required), and the parent names the
  recorded issuing incarnation. This is vmmd's own evidence and does not
  depend on schedd.
- The checks are in `PromoteAdmitted`, `checkSnapshotGrantIdentity` and
  `consumeSnapshotGrant`. Fresh grants must still name this process.
- `runtimeadmission.Promotion` gains a hold, `Held(issuer)`, set by the
  caller and never serialized: `Validate` then lets the parent's incarnation
  be `issuer` while the fresh grant names another, which the caller checks
  is its current one. Each side vouches for the hold from its own evidence:
  vmmd from its adoption of this exact receipt (`AdmissionHeldFrom`), the
  stores from the promotion lock, which accepts such a parent only through
  the instance's re-binds. As built (2026-10-18) this replaces the design's
  `Rebound` wire field and `RebindVersion` capability: nothing new travels
  on the promotion request, so its payload hash and an N−1 vmmd are
  unaffected. Captures need no hold on the wire either: their grant's
  parent is the receipt itself, which vmmd checks the same way.
- The adoption report gains, per adopted admitted instance, the receipt's
  `token`, its issuing incarnation and the `observed` facts.
- The `admission_receipt` refusal remains for a vmmd without
  `adopt_admitted_runtimes` (default off) and for receipts from protocols this
  binary does not know.

### 4. schedd writes the row from the report

schedd is the only writer of runtime publications. Its adoption-report
reconciler handles each adopted instance whose row is managed (has a boot
token). It inserts the re-bind in the same fenced pass that today fails
missing rows, under the app lock, with a reread of `wake_id` and `started_at`.
The values:

- `from` = the instance's effective incarnation;
- `to` = `report.incarnation`;
- `vmmd_epoch` = `report.epoch`;
- `observed` = the report's facts.

If the guard refuses, schedd treats the instance as refused and sends it
through ADR-419's repair (row failed, app re-admitted). That is today's
behaviour, so no failure here is worse than the status quo.

Until the row exists, the guards in §2 refuse captures, promotions and
publications for that instance as stale, exactly as they do after every
restart today. The reconciler runs every 5 s
(`api.AdoptionReportIntervalSeconds`), so the window is one tick after the
successor serves. Slice 3 checks that each caller retries a stale refusal
rather than replacing the instance; where one does not, it must wait for the
report first.

### 5. The pre-restart check

`vmmd adoption-check` counts an admitted guest as adoptable when the
candidate has `adopt_admitted_runtimes` on and knows the receipt's protocol.
Otherwise it stays unadoptable and the node drains, as today.

## Security (§11) and ADR-595 guarantees

| ADR-595 guarantee | How it holds |
|---|---|
| Grants are single-use and bound to one process | Unchanged. Fresh grants name the current incarnation, and a successor still cannot consume a predecessor's tokens: the replay window is per process, and old tokens name the old incarnation. |
| Boot and promotion history is immutable | Unchanged. Bindings and receipts are never written. The re-bind is a separate append-only row. |
| History cannot recreate residency after cleanup | The re-bind requires a live row on the node whose netns, IP, uid and paused state equal the receipt's, and cascades away with the instance. |
| A superseded incarnation never regains authority (ADR-435) | `to` must be the node's current incarnation and epoch claimant. `from` must precede it in the history. |
| Owner erasure removes private history | The cascade from `instances` removes re-binds too. |
| Fresh approval and current inputs at promotion and capture | Unchanged. Promotion and capture still need fresh grants. The publication guard's captured-input comparison still runs, so changed inputs still force a replacement. |
| A forged or stale report | Reports are accepted only from the epoch claimant, over the verified daemon certificate. The guard re-checks the epoch and incarnation itself. |
| A guest swapped under the receipt | The adoption proof (pidfd, boot id, cgroup, netns inode, jail, API identity), plus the `observed` equality in the guard. |

What this ADR does not claim: a re-bind is not a new admission. It asserts
continuity of one already-admitted guest. Standard revisions, approvals and
scans are re-checked only where ADR-595 already re-checks them: promotion,
capture, publication.

## Migration and release (docs/release-compatibility.md)

- One expand migration (it starts with `SET LOCAL lock_timeout = '5s';`). It
  creates the table and function and redefines the guards. With no rows, the
  redefined guards behave exactly as before, so N−1 schedd and vmmd are
  unaffected. Down drops the table and restores the previous definitions.
- A second expand migration (`20261023084254597`) adds the nullable
  `producer_incarnation` column and redefines the capture guard to stamp it.
  N−1 writers do not name the column; the guard fills it for them.
- Proto: one new optional message on `AdoptionReportInstance`
  (`AdoptionReportAdmission`). An N−1 schedd ignores it.
- Deploy order: data (migration) → schedd → vmmd with
  `adopt_admitted_runtimes` per node. Turning the flag off restores the
  refusal.
- Version skew, checked against the previous release (v0.1.18-rc.272) on
  2026-10-19:
  - `make version-skew-check`: proto and guest protocol OK.
  - `make migration-lint`: the branch's four migrations are expand-only. No
    function is dropped; the five-argument resume validator is a new
    overload beside the four-argument one.
  - N−1 code on this schema: rc.272's own PostgreSQL standards tests pass
    against these migrations (215 pass).
  - vmmd rollback: rc.272 refuses a journal with a version-7 record, an
    unknown field or the `.epoch` file, and then does not start. Those are
    written only with `adoption_record` / `adopt_running_vms`, which stay
    off until every rollback target reads version 7 (adoption ADR, version
    rules). `TestJournalWithoutAdoptionRecordsIsReadableByThePreviousRelease`
    pins the default path, including the admitted-wake receipt commit.
  - Guest job exits: the frame and its acknowledgment are unchanged; the
    guest only retries for longer (60 s), so an N−1 host that acknowledges
    the first attempt sees no difference.
  - N−1 schedd after re-binds exist (a schedd rollback): its own Go
    readers compare incarnations strictly, so it treats re-bound guests as
    stale and refuses their captures and promotions (fail-closed; parks
    then cold-boot).

## Slices

1. **Storage:** the migration, MemStore mirror and conformance tests (insert
   guard cases, chain walk, cascade, the redefined guards with and without a
   row), `make migration-lint` and `schema-dump`.
   **Built** (2026-10-18, migration `20261023084252389`): the table, its
   guard, `application_standard_effective_incarnation`, and the publication
   guard, promotion lock and snapshot-capture lock redefined through it;
   `ApplicationStandardRebindStore` (`RecordApplicationStandardRebind` is
   idempotent, `ListApplicationStandardRebinds`) on both stores, with the
   MemStore mirroring the guard and the three checks; conformance tests for
   a restart, each refusal, a retried write, a second restart, a skipped
   incarnation and the publication guard. The promotion guard's
   fresh-versus-parent equality stays strict: relaxing it needs `Rebound` on
   the wire, so it moves to slice 2, and a re-bound warm spare cannot be
   promoted until then.
2. **vmmd:** receipt and egress in the block, restored on adoption; identity
   checks; the hold in `runtimeadmission` and the promotion guard's parent
   comparison; report fields; the `adopt_admitted_runtimes` flag; inventory
   test reclassification.
   **Built** (2026-10-18, migration `20261023084253491`): the block's
   `admission` (receipt, plan, allowlist, ports), validated against the
   lease and restored with the issuer held; `AdmissionHeldFrom` in
   `PromoteAdmitted`, the gRPC promotion path and the capture identity
   check; the block rewritten synchronously after a promotion; report
   `admission`; `adopt_admitted_runtimes` (requires `adopt_running_vms`,
   honoured by the pre-restart check); both stores and the SQL promotion
   guard (plus a five-argument resume validator) accept a fresh grant
   naming the current incarnation for a re-bound parent, and nothing else.
3. **schedd:** the report reconciler writes re-binds; the promotion builder
   and vmmd client set the hold; the observation roster uses the effective
   incarnation.
   **Built** (2026-10-18): the report parses `admission`; before applying a
   report, schedd re-binds each adopted admission from its current holder
   (walked from the receipt's issuer through the instance's re-binds) to the
   report's vmmd under its epoch, also for an incomplete report. A hop the
   store refuses moves the guest from adopted to refused
   (`admission_rebind_refused`), so its running row is repaired; a
   transient failure retries the epoch on the next tick. The promotion
   builder promotes a re-bound parent under the current incarnation with
   the hold, and the vmmd client holds a parent whose incarnation differs
   from its grant's (a structural check; the store and vmmd hold the
   authority). A re-bound warm row whose hop is refused is not repaired:
   the report lists running and frozen rows only, and its promotion stays
   stale until the warm pool replaces it. The observation roster
   follows the re-binds since open question 3 was answered (below).
4. **Acceptance:** a metal test in which an admitted guest is adopted, then
   captured and promoted under the successor. Version-skew and N−1 runs.
   **Passed** (2026-10-18 on gregale-internal-test-1 at `f040d80c38`,
   `TestMetalAdoptionKinds`, leakcheck clean): an admitted warm
   spare (protocol 1) is adopted under the hold and promoted with a grant
   naming the successor; a measured guest (protocol 2) is adopted under the
   hold and its capture is refused (open question 1). The first run on
   gregale-internal-test-1 found two vmmd bugs, both fixed:
   - a successor that had admitted nothing itself panicked on its first
     promotion or capture of an adopted guest, because only an admitted
     boot created the replay and flight maps;
   - an admitted wake attached its receipt after the readiness commit and
     never rewrote the block, so a slow wake (a measured boot observing its
     drives) left a record without the admission, and the successor
     adopted the guest as unadmitted. A block written while the admission
     is in flight now marks the guest admitted without a receipt, which a
     successor refuses, and the receipt is committed before `WakeAdmitted`
     returns it.
   **Not done:** version-skew and N−1 runs, and a protocol-2 resume
   re-bind.

## Open questions

1. **Protocol 2 capture state.** A measured capture checks the frozen private
   main drive against boot-time measurements. Is everything it needs in the
   receipt's `ArtifactConsumption`, or does vmmd hold other per-instance
   handoff state (`runtimeDriveHandoffs`) that the block would also have to
   carry? To verify in slice 2. If it doesn't fit, protocol-2 guests stay
   refused at first.
   **Answered (2026-10-18): it does not fit.** A measured capture and a
   measured promotion both need the launching process's
   `runtimeDriveHandoff`: the pinned approved drives and the launch-time
   observation (`ObservedRuntimeDrives`, `checkResumedSnapshotParent`).
   Pins are open file descriptors of the old process; the receipt carries
   the measurements but not the pins, and re-measuring the drives of a
   running guest would not prove what it booted from. The successor's
   handoff map starts empty, so:
   - a measured guest is still adopted and keeps serving across the
     restart (refusing it would kill it at the restart instead);
   - its capture is refused in `beginNativeSnapshot` before any pause, with
     `ErrUnavailable` and nothing published; `CaptureAdmitted` then tears
     the guest down, as it does for every failed admitted capture, and
     schedd's park sequence does the same on any capture error, so the
     app's next wake cold-boots its measured drives (ADR-005);
   - promoting an adopted measured warm spare is refused the same way, and
     the warm pool replaces it.
   The cost is one lost snapshot per adopted measured guest per restart,
   counted in `schedd_warm_snapshot_errors_total{reason="vmm_call"}` on its
   next park.
   Carrying the pins across the restart (passing the drive descriptors
   through the systemd fd store with the guest's other handoffs) is the
   follow-up that removes it; it needs its own design.
2. **`migrating` rows.** An adopted live-migration source keeps its pause
   (`migration_paused`). Should a re-bind be allowed in `migrating`, or should
   the migration be cancelled and re-tried after the re-bind? This design
   allows only `running` and `warm`.
3. **The observation roster.** Live observations compare the binding's
   incarnation with the node's. They would follow the effective incarnation;
   confirm with the ADR-595 owners that observed-standard reporting may count
   a re-bound guest as current.
   **Answered (owner-approved 2026-10-18): they may.** Two readers change:
   - PostgreSQL's current-receipt read (`GetCurrentApplicationStandardRuntimeReceipt`)
     resolves the receipt's incarnation through the re-binds, as MemStore's
     guard already did. Before this, PostgreSQL treated every re-bound
     receipt as stale, so schedd's measured-capture path refused the
     guest's snapshots and native-instance qualification failed it.
   - A snapshot's producer is the process its capture was acknowledged
     from, not its parent's issuer. The catalog now records it:
     `application_standard_snapshot_captures.producer_incarnation`
     (migration `20261023084254597`), which the capture guard stamps once at
     acknowledgment, where its lock has just required the source's
     effective incarnation to be the node's current one. Re-binds cannot
     answer this later: they are deleted with the instance, and parked
     instances are collected while the catalog keeps the capture. The
     observation compares the node's incarnation with the producer;
     captures acknowledged before the migration have none and read as their
     parent's.
   Unchanged: a snapshot whose producer has since restarted is unavailable
   until it is replaced, as ADR-595 requires for any process restart, and
   log targets are compared with the node's current identity, which the new
   process registers itself.

## Rejected alternatives

- **Updating the stored binding to the new incarnation.** This was the
  adoption ADR's first design. It would break ADR-595's immutability, which
  the boot and promotion guards enforce, and erase who issued the receipt.
- **A stable per-node incarnation that survives restarts.** It removes
  ADR-595's replay protection: a successor could accept its predecessor's
  unexpired grants.
- **A fresh admission for every adopted guest at restart.** This is today's
  re-admission with extra steps: a new grant needs a new boot to receipt.
- **vmmd writes the re-bind.** vmmd never writes customer runtime history,
  and schedd owns runtime publication. Keeping the writer in schedd also keeps
  the epoch fence in one place.

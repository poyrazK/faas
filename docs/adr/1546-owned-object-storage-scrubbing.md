# ADR-1546: Resumable checksum scrubbing on the owned object node

- Status: Accepted for the local prototype; no production enrollment.
- Date: 2026-10-16
- Related: [ADR-1542](1542-owned-object-storage-first-node.md), [ADR-1543](1543-owned-object-storage-write-receipts.md), [ADR-1544](1544-owned-object-storage-multipart.md), [ADR-1545](1545-owned-object-storage-inventory.md)

## Context

Inventory validates catalog references and file lengths. Same-size corruption
of committed objects can pass those checks. Both objects and native multipart
parts already carry a SHA256 calculated before their bytes were acknowledged.
The node needs to compare stored contents against those digests, resume large
checks after interruption, and retain findings without confusing an old blob
with a later value at the same key or part number.

## Decision

Add a single background scrub worker. Its durable state lives in `scrub/`:
`progress.json`, a UUID-named JSONL worklist for the active run, and sealed
`findings/<identity-hash>.json` records. These files are independent observations;
they never participate in object publication, write settlement or billing.
The object format remains version 3. Earlier binaries ignore this extra state;
they do not perform checksum scrubbing. Full-directory backups must include it.

### Worklist and read budgets

Build a streaming worklist while holding the catalog read lock, capturing exact
current object identities and selected parts of active/frozen multipart uploads.
Each target includes its bucket incarnation, key, blob UUID, expected size and
SHA256; parts additionally include their native session and part number. Do not
include staging files, obsolete blobs or historical receipt metadata.

Bound the worklist to 64 MiB, each target record to 8 KiB and construction to the
existing 45-second inventory work budget. Reserve its maximum space through the
node's ordinary write admission and require the existing 1 GiB free-space floor.
Fsync the finished worklist and its directory before publishing sealed progress.
Catalog publication is blocked during construction; content hashing releases
that lock. Scans above the manifest/time budget fail visibly instead of claiming
complete coverage. A metadata index is still a later scaling milestone.

The worker reads at most 8 MiB of blob contents per one-second tick by default,
tunable with `--scrub-bytes-per-second` from 1 byte up to 64 MiB. Each tick also
limits target completions to 32 and uses a 64 KiB copy buffer. Metadata reads,
worklist construction, checkpoint writes and startup worklist validation have
their own bounded budgets; they are outside the blob byte-rate setting. Files
are opened one at a time, without following the final symlink or blocking on a
FIFO, and must be regular files of the captured length.

Start immediately when there is no saved cycle. Resume active work after restart;
after completion, wait 24 hours before the next automatic worklist. A private
operator POST can start a new cycle sooner. `--scrub=false` disables the worker
while retaining checkpoints and findings. Operational failures retry after
30 seconds; shutdown cancellation preserves the last acknowledged checkpoint.

### Resumption and identity checks

Persist the worklist byte position, rolling worklist hash, current target's
offset and marshaled SHA256 state, and the source device/inode/size/mtime/ctime.
Check the catalog identity before opening and again after reading. Overwrite,
deletion, bucket reincarnation, part replacement, abort, expiry or completion
makes an old target stale. Stale targets are counted separately from verified
and failed targets and never create a finding against replacement data.

Resume a prefix only when the saved file identity/fingerprint still matches.
Changed fingerprints restart the hash at zero. Changes observed during a chunk
discard its partial hash. Empty objects are explicitly verified. Check the
rolling worklist hash before completing a cycle, and validate the entire bounded
worklist on startup. A broken worklist, checkpoint seal or SHA state fails closed.
The SHA serialization belongs to the pinned Go toolchain; incompatible future
encodings require an explicit checkpoint migration rather than assumed success.

Checkpoints do not advance the catalog revision, so background progress cannot
invalidate an accounting inventory. A pass describes bytes observed during that
scan against the saved worklist. New objects wait for a later cycle; it does not
prove the contents remained unchanged after their last read. Hardware corruption
of an already-read prefix that leaves its fingerprint unchanged can require the
next full pass to detect.

### Durable findings

Record checksum mismatches, missing blobs, wrong lengths and invalid file types
against the complete immutable target identity. Preserve the first detection,
last detection, observed digest, run UUID and verification history. Fsync the
finding before a target's progress can advance. A crash between finding and
checkpoint replays the same identity without duplicating or losing the finding.
Transient read/permission errors stop and retry the step; they do not establish
corruption or advance it as verified.

Retain findings indefinitely, including after overwrite/deletion. Listing adds
`current` and `current_known` by checking the live catalog at observation time;
unknown metadata is distinct from a known superseded identity. A subsequent
successful verification of the same identity marks it verified while preserving
its original detection history. Do not delete, rewrite, quarantine or repair
object bytes as a result of scrubbing. Historical write receipts remain valid
evidence of the original publication independently of later media damage.

Startup removes only recognizable unpublished scrub temporary files and unused
worklists. It validates retained findings. Malformed durable scrub state stops
startup with a corrupt-state error; it is never silently reset into a clean scan.

### Private operations and observability

Reuse the operator SigV4 query boundary with no tenant route:

- GET `/?gregale-inventory=scrub` returns durable cycle progress.
- POST on that path with a signed empty body builds a worklist; it returns a
  running status. An existing run returns `OperationAborted`.
- GET `/?gregale-inventory=scrub-findings` lists retained findings with bounded
  `max-keys` and a `cursor` continuation. It is an observation, not capacity proof.

Responses are `no-store`; credentials remain node-wide and private. Reject
bucket/object targets, mixed selectors, duplicate parameters and malformed
requests. The existing loopback metrics listener serves cached aggregate scrub
metrics with no key, bucket or blob labels:

- `faas_object_node_scrub_targets{outcome}`: total, processed, verified, failed,
  stale counts for the current or most recent manifest.
- `faas_object_node_scrub_bytes{kind}`: read bytes and current offset.
- `faas_object_node_scrub_timestamp_seconds{event}`: progress, completed and
  verified_manifest timestamps. The latter advances only when every pinned
  target was verified without failures or stale identities.
- `faas_object_node_scrub_running`, `faas_object_node_scrub_worker_ok` and
  `faas_object_node_scrub_steps_total{outcome}`: worker state and errors.

Counts are cycle history, not a live count of damaged current objects. Operators
inspect the finding's identity and current/known fields before acting. Alert on
failed targets, worker errors, stalled running progress and a completion age
beyond the deployment's expected full-scan duration. A completed scan with stale
targets does not advance the verified-manifest timestamp.

Ship an optional rule family at `pkg/promqlrules/object_node_scrub.yaml` and
synthetic Prometheus fixtures for absent metrics, healthy idle scans, failed
targets, operational errors and stalled progress. Deployment roles do not load
the family or enroll a scrape target automatically. Alert labels retain only
job/opaque node ID and static component/family/severity, without instance paths
or blob identities.

## Validation and boundaries

Tests cover same-size corruption, missing/wrong-sized files, symlinks, multipart
corruption, repair history, file changes after a checkpoint, overwrite/deletion,
bucket reincarnation, part replacement, abort/expiry/completion, cancellation,
hash resume after a real process kill, faults at finding/checkpoint publication,
corrupt durable state, inventory independence, signed private APIs and metrics.

This milestone detects and reports damage. It introduces no automatic repair,
read quarantine, backup/restore, replication, new customer API, database schema
or production enrollment. Verified backup/restore remains the next recovery
milestone before replication and failover.

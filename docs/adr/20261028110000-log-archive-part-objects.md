# ADR: The log archive writes one part object per flush

Date: 2026-10-28
Status: Accepted
Related: issue #562 (log archive), [FaasLogArchiveShipperDegraded](../runbooks/FaasLogArchiveShipperDegraded.md)

## Context

The archive keeps one object per instance and UTC day,
`faas-logs/{instance}/{YYYY}/{MM}/{YYYY-MM-DD}.jsonl.gz`. S3 has no append, so
every five-minute flush re-uploaded the whole day: the bytes already archived
(from the local history file, or from the bucket when that was purged) plus
the new fragment. The 2026-10-25 logs review found three costs:

- Upload volume grows with the square of a day's flushes: a busy instance
  re-sends its whole day 288 times.
- The spool keeps a local copy of every archived day (`.jsonl.gz` history)
  only so the next flush can rebuild the object. That history is outside
  `FAAS_LOG_ARCHIVE_LOCAL_BYTES_MAX` and its alert, and a day whose history
  was purged needs a bucket download before each flush.
- An object rewritten every five minutes restarts its age, so the bucket's
  90-day lifecycle rule measures from the last flush, not from when a line
  was written.

## Decision

1. Each flush uploads only its own lines, as a part object under
   `faas-logs/{instance}/{YYYY}/{MM}/{YYYY-MM-DD}/`, named
   `{seal time in Unix ns, 19 digits}-{first 8 bytes of the SHA-256 of the
   uncompressed lines}.jsonl.gz`. Key order is write order.
2. After the PUT the shipper deletes the sealed upload. No local history is
   kept, so the spool holds only lines that are not in the bucket yet.
3. A sealed upload never changes until it is uploaded: new lines wait in the
   next partial (`Spool.PreparePartUpload`). A crash after the PUT and before
   the delete retries the same bytes under the same key, so a retry never
   duplicates lines.
4. Readers (`S3Client.OpenDay`, used by gatewayd-internal's archive read)
   stream the whole-day object if the day has one, then every part in key
   order. Each object is one gzip member, so the concatenation decodes as one
   JSONL stream. A day with neither answers 404 as before. A part that expires
   between the listing and the read is skipped.
5. Erasure and the lifecycle rule need no change: both work on the
   `faas-logs/{instance}/` prefix, and each part now ages from its own write.

## Rollout

Units release independently (docs/release-compatibility.md) and apid, which
also runs a shipper, deploys before gatewayd-internal. So writers switch on
separately from readers:

- This release: readers read parts; writers keep writing whole days unless
  `FAAS_LOG_ARCHIVE_PART_OBJECTS=1`.
- Once every gatewayd-internal runs this release, set
  `FAAS_LOG_ARCHIVE_PART_OBJECTS=1` for apid and vmmd. A follow-up makes it
  the default and then removes the whole-day writer.

A day that spans the switch reads its whole-day object, then its parts.
Switching writers back reads the same lines, but the whole-day object a
rolled-back writer re-uploads then carries lines newer than the day's parts,
so that day reads out of order; no lines are lost.

## Consequences

- A busy instance's day is up to 288 small objects instead of one large one;
  a read lists the day's prefix once (one page up to 1,000 parts).
- Spool usage equals pending bytes; the `.jsonl.gz` history the whole-day
  writer kept ages out through the existing purge.
- Not in this change: a per-instance share of the spool, so one noisy instance
  cannot fill it for every other instance on the node.

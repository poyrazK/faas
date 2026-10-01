# Remaining S3 implementation work

This tracks the scope requested on 2026-10-02. End to end means customer API,
provider adapter, persisted state/recovery, quota and usage accounting, clients,
and local integration tests. A real provider environment is not required for
this implementation task. Local protocol tests do not establish production
provider qualification.

| Area | Required behavior | Status / acceptance evidence |
| --- | --- | --- |
| Write recovery | Retain per-attempt completion proof through overwrite/delete; settle uncertain attempts without unsafe time-based refunds; cover direct writes, GCS and cross-bucket copies | Partial (ADR-397). Tracked S3 PUT/copy recovery can find exact proof in retained native versions using bounded scans and durable cursors; retained-version observations block unsafe current-object capacity reclamation. Automatic proof retention on unversioned backends, direct writes, GCS and cross-bucket copies remain open. |
| Lifecycle | Bucket rule management, prefix/tag filters, expiration, abandoned multipart cleanup, durable bounded worker, quota reconciliation and restart/race tests | Open. Multipart cleanup already exists; object lifecycle policy does not. |
| Multipart copy | Authenticated same-bucket source/ranges, copied-byte admission, atomic source fence, one dispatch, uncertain response fencing, completion/list/abort compatibility | Implemented for S3 (ADR-396). AWS SDK → gateway → S3 adapter → local HTTP provider tests pass with memory and PostgreSQL stores, including race checks and restart/abort fencing. Other provider adapters return NotImplemented. |
| Copy conditions | Customer ETag and date conditions with S3 precedence and atomic source identity; unsafe/unsupported conditions fail explicitly | ETag predicates implemented for tracked S3 copies and part copies, with signed-header and budget checks. Date predicates remain open: an internal If-Match can override If-Unmodified-Since. |
| Notifications | Create/delete events, filter configuration, durable outbox, function/queue delivery, retries, idempotent delivery identity, recovery and tenant isolation | Open. No object event configuration or delivery yet. |
| Versioning | Configuration, version IDs on I/O, version listing/deletion, delete markers, restore and accounting across all versions | Public feature open. Private historical version listing/HEAD now supports receipt recovery (ADR-397). Bucket activation, customer IDs, all-version accounting/admission, direct URL replay handling, deletion/restore and coordinated account deletion remain required. |
| Large uploads | Production upload profile with bounded resources, configurable part/request sizes, transfer deadlines, reverse proxy settings and local boundary tests | Open. Beta proxy configuration caps requests/parts at 64 MiB. Multipart total size is independently configurable. |
| Customer encryption | Provider capability/placement validation, encryption configuration and KMS identity/permissions, all write/copy/multipart paths, safe response metadata and secret handling | Open. Customer encryption directives currently return NotImplemented. |
| Object Lock | Retention/legal hold management, version ownership, protected deletion/lifecycle/account-deletion behavior, provider capability gating | Open; depends on versioning. |
| Replication | Configured owned destinations, durable copy/delete jobs, version/marker propagation, loop prevention, failures and destination accounting | Open; depends on durable events and versioning. |
| Production accounting/deletion | Authoritative usage adapters and cutoffs, coordinated account deletion across new versions/retention/events/jobs, local E2E acceptance | Existing usage adapters and account deletion need qualification against the expanded features. Live provider tests are excluded by user instruction. |

Implement in reviewable increments, updating the evidence here. An increment
does not mark the entire scope complete. Feature completion requires both the
ordinary path and its failure/restart/tenant-isolation behavior, not just an
endpoint or an adapter interface.

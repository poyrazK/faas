// Package logarchive ships per-Firecracker-instance stdout/stderr to
// an S3-compatible bucket on a 5-minute cadence and purges the local
// spool at the configured retention boundary (issue #562, §16
// follow-up).
//
// The pipeline is four pieces, each small and independently tested:
//
//   - Spool (spool.go) — append-only JSONL file per
//     (instance, day), keyed under /var/log/faas/archive/{instance}/
//     {YYYY}/{MM}/{DD}.jsonl.partial. The file is fed by the
//     OnEvict callback installed on every pkg/fcvm/logbuf.Ring, so
//     a line is persisted to disk BEFORE it's dropped from the
//     ring's byte budget. bufio.Write amortises syscalls across
//     many evictions.
//
//   - Shipper (shipper.go) — the 5-minute ticker that scans the
//     spool dir for .jsonl.partial files, gzips each, and uploads
//     to s3://{bucket}/faas-logs/{instance}/{YYYY}/{MM}/{DD}.jsonl.gz
//     via the S3Client (s3client.go). The active .partial file is
//     rotated to .upload before reading, so new evictions remain
//     writable while the upload runs. On success the .upload file
//     is removed and the .jsonl.gz marker is eligible for the
//     7-day purge; on failure .upload remains for retry.
//     {daemon}_log_archive_failures_total{reason} increments and
//     a slog WARN fires.
//
//   - Purger (shipper.go::PurgeOnce) — the daily ticker that
//     removes any .jsonl.gz older than the configured retention.
//     Independent from the shipper so a bucket outage doesn't
//     prevent local cleanup; the per-tick size is bounded by the
//     instance count on the box.
//
//   - S3Client (s3client.go) — hand-rolled SigV4 signing (no AWS
//     SDK vendoring — keeps the binary small and the dependency
//     surface minimal). PUT and GET shapes match what every
//     S3-compatible vendor (R2, B2, Wasabi, MinIO, AWS S3)
//     accepts. Errors are typed so the shipper can distinguish
//     transient (network, throttle) from terminal (auth, 4xx)
//     without sniffing the wire message.
//
// The package is daemon-agnostic. Each daemon wires the shipper
// into its own lifecycle and supplies a metrics prefix, while the
// credential-management surface remains shared (ADR-020 host.age
// sealed env).
//
// PR-A ships the spool + shipper + s3client + CLI unseal. PR-B
// extends gatewayd-internal's SSE handler to proxy `?after=7d` reads
// through the S3Client. PR-C closes the ansible + runbook gap.
package logarchive

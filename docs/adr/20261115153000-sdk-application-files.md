# ADR-20261115153000: Application file workflows in the SDKs

Status: Accepted

## Context

Typed jobs accept a JobStore, but applications still have to implement that
adapter. Storage already exposes signed requests, durable multipart sessions,
confirmed parts, write receipts and opaque cursors. Browser applications need
a transfer surface they can connect to their own user authorization.

## Decision

Add preview bucket handles at Node `client.storage.files` and Python
`faas_sdk.preview.FaaSClient.files`. Keep all existing generated methods and
public capability surfaces. Export the browser engine only through the preview
browser entry. Server preview admission and bucket grants remain authoritative.

Separate the control-plane broker from direct file HTTP traffic. Direct requests
use returned capability headers, omit ambient credentials and cookies, and
reject redirects, embedded credentials, fragments and non-HTTPS remote URLs.
Loopback HTTP supports local development. Checkpoints and errors exclude signed
URLs and credentials.

Multipart checkpoints bind endpoint, app, bucket, exact key, size, content type,
session, per-part SHA-256 and a digest of concatenated binary part digests.
This source identity is distinct from a complete-file SHA-256 and from provider
checksum attestation. Each uploaded part must match its recorded digest. Resume
rehashes the source and skips only parts whose checkpoint ETags match confirmed
provider parts. Missing evidence causes a sequential replacement upload. The
SDK does not automatically retry failed writes. One writer owns a session and
checkpoint. Completed/completing recovery requires every settled part in the
checkpoint; another logical upload uses a fresh key.

Local cancellation preserves the session. Explicit abort discards it. Total
deadlines include source reads, HTTP bodies, callbacks and polling. Byte, part
and poll limits are bounded. Downloads can stream to a sink or return buffered
bytes. Single PUT supports empty files and saves a receipt before dispatch;
`confirm` recovers the exact attempt without another PUT. A completed receipt
proves that attempt committed, not that the current object is unchanged.

The managed JobStore resolves the app UUID, uses content-addressed create-only
writes and managed `obj://` references, and verifies bytes after writing and on
every read. It accepts references only in its configured app/bucket/prefix.
Workers receive explicitly configured Gregale storage credentials; the adapter
uses no ambient cloud credential chain or provider SDK dependency.

The CSV example issues capabilities for server-generated keys in durable owned
file records. Every operation checks application user ownership before storage
or Jobs access. Routes never proxy arbitrary keys, signing options or paths.
Before submission, the application downloads the file and verifies its initial
fixed-block digest and size, selecting an immutable version when available.
It persists submission intent before calling Jobs, and leaves uncertain
acceptance for explicit investigation. Authenticated routes expose progress and
stored rejection reports. The static user/token map is demo authentication.

## Validation and limits

Shared Node/Python fixtures cover portable checkpoints and failure semantics.
Local integration uses isolated PostgreSQL, actual Node/Python worker processes,
the managed JobStore, restart recovery, cross-language resume and two users.
Headless Chrome covers upload, progress, report download and reload/resume.
The control plane and storage provider in this test are protocol fixtures;
deployed acceptance requires an isolated configured preview account.

Defaults are 100 MiB per operation and 10,000 parts, with at most 64 MiB per
server-selected part. Byte limits can be raised to 1 TiB within the supported
layout. Buffered downloads require memory proportional to the limit. Streaming
sinks can receive partial bytes before a later failure; publish a temporary
destination only after success. ETags remain opaque. Upload and job submission
are separate recoverable operations without an atomicity guarantee.

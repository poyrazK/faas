package api

// Byte caps for operator- and customer-supplied free text that is recorded
// rather than acted on: rollout reasons, failure causes, audit messages.
//
// These are storage bounds, not plan quotas — they do not vary by plan and so
// they live here rather than in the Limits table. They are expressed in bytes
// because the limits they stand in for are byte limits (Postgres column
// widths and the ~8 KB pg_notify payload ceiling).
//
// Always apply them with safetext.Truncate, never with a byte slice: cutting
// a multi-byte rune in half yields invalid UTF-8, which Postgres rejects with
// SQLSTATE 22021 on any UTF8 database.
const (
	// AuditReasonMaxBytes bounds a human-written reason attached to an
	// operator action — `gregale rollout recover --reason`, an abort note, a
	// promotion justification.
	AuditReasonMaxBytes = 1024

	// AuditMessageMaxBytes bounds a recorded failure cause. These are
	// err.Error() strings carrying registry responses, guest output, and
	// upstream API bodies, so they are both longer and far more likely to
	// contain bytes that are not valid UTF-8.
	AuditMessageMaxBytes = 2048

	// CachePurgeGlobMaxBytes bounds the customer-supplied path glob on
	// POST /v1/apps/{slug}/cache/purge. The glob travels in a pg_notify
	// payload, which PostgreSQL caps at 8000 bytes; 1 KiB is generous for a
	// path pattern and leaves the rest of the envelope ample room.
	CachePurgeGlobMaxBytes = 1024
)

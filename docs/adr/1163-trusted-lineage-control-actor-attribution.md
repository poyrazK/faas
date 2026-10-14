# ADR-1163: Trusted lineage control actor attribution

Status: Accepted
Date: 2026-10-13
Related: ADR-1161, ADR-1162

## Decision

Add optional actor_kind, actor_id and credential_type to the existing control
receipt DTO. Persist them in the existing immutable JSON result; no new columns,
indexes, routes or migration are needed. Legacy receipts omit all three fields;
never infer or backfill an actor from present-day job ownership.

HTTP pause/resume/cancel handlers derive identity exclusively from authenticated
middleware context, after decoding the unchanged public request. Session callers
record actor_kind account, the authenticated account UUID, and credential_type
session. Bearer callers record actor_kind credential and the credential UUID,
classified from its trusted projection as api_key, deploy_token or
platform_tenant_token. Do not store token values/hashes, session IDs, emails,
labels, IPs, or user-supplied attribution. Session attribution identifies the
account; it does not distinguish sessions or establish a separate human identity.

Carry identity through a private state context key bound to the authenticated
account. Validate account binding, nonzero canonical UUIDs and allowed kind/type
combinations before creating a new receipt. Internal calls without the HTTP
principal context record internal/store/internal, not an inferred human actor.
Existing authorization and MFA requirements remain authoritative; attribution
never grants access or changes command identity.

Receipt lookup precedes new-actor validation and state transitions. Repeating a
command from another currently authorized credential returns the original actor
and creates no extra receipt, even for legacy unattributed commands. The original
actor is not part of the command conflict key. Revoking a credential does not
remove past attribution; an otherwise authorized caller can still read it while
the job and receipt remain retained. Ownership checks continue before replay.

Expose these optional fields through generated SDKs and CLI control/history
output. Display absent attribution as unknown (legacy receipt), separately from
explicit internal attribution. Retention, receipt limits, sequence ordering,
pruning and metadata-only semantics are unchanged. Legacy controls without command
UUIDs and failed requests remain absent: this is not a complete actor audit.
During a rolling upgrade older writers may still create unattributed receipts;
upgrade all API writers to attribute newly created receipts consistently.

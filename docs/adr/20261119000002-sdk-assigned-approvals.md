# ADR-20261119000002 Assigned pipeline approvals

Approval handoffs reuse the installed clarification grant and pipeline gate
ledger. Each opted-in pipeline tool supplies a typed approval adapter and
reviewer scopes. The authenticated preview and decision tools bind the approval
key to the reviewer, grant, gate, and review digest. PostgreSQL locks the grant
as part of deciding the gate and allows only decisions compatible with its
kind. The existing decision timestamp makes the approval receipt stable on
identical, currently authorized retries.

Approval summaries appear in reviewer inbox pages and watches when the caller
includes approvals. The cursor binds that choice. This keeps existing
clarification consumers and cursors compatible.

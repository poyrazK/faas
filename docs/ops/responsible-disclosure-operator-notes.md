# Responsible disclosure: operator notes

Moved out of the customer page `docs/compliance/responsible-disclosure.md`
(ADR-20261028200304).

## Tracker and audit details

- All reports land in an encrypted internal tracker (operator-side,
  not in this repo) keyed by the `security@gregale.dev` mailbox.
- The tracker row carries: `received_at`, `acknowledged_at`,
  `severity`, `affected_component`, `reporter_handle`,
  `pgp_fingerprint_of_reporter` (if PGP used), `internal_status`,
  `fix_shipped_at`, `public_advisory_url`, `closed_at`.
- The tracker is reviewed at every quarterly `access-review.sql` run
  (PR-9) — any open reports older than 90 days are flagged for
  escalation.
- Inbound reports emit an audit row in the `events` table per
  spec §5.1 + ADR-035. The kind name lives in the ADR-035 family
  (canonical registry of all customer-facing audit kinds); the
  audit row carries no payload beyond `report_id` and a redacted
  `reporter_handle`.

## Control mappings

- `docs/compliance/soc2-control-mapping.md` CC2.3 / CC4.1 / CC7.4 / CC7.5 / CC7.6.
- `docs/compliance/iso27001-statement-of-applicability.md` A.5.24 / A.5.25 / A.5.26 / A.5.28 / A.6.6 / A.6.8.
- `SECURITY.md` — repo-root mirror (GitHub convention).

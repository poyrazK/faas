# ADR-20261114120000 · Key lifetime, rotation, sign-in key caps and money-moving routes

- **Status:** accepted
- **Date:** 2026-11-14
- **Amends:** issue #189 / IAM-5 (API-key lifetime and rotation), ADR-20261111130000 (security notices), ADR-1503 (month-end billing runs)
- **Context:** The 2026-11-10 account-flow audit, passes 9–14, found these key and billing gaps:
  - F9-2: a full-access (admin) key never expired. A key that leaked stayed valid until someone noticed and revoked it.
  - F11-2: every `gregale login` added another 30-day full-access key (`cli-login`). Only the CLI, best-effort, revoked the key it replaced, so logins on many machines piled up live keys. Password sign-in keys were already capped at five.
  - F11-3: rotating a key kept the old one working for the account's grace window (7 days by default), with no way to end it sooner for one rotation. Rotating a leaked key left it usable for a week.
  - F13-1: starting a billing run, setting its schedule, and exporting a statement as an invoice accepted a `deploy:write` key. With a send or charge export these invoice or charge the customer's own end customers through the connected provider, so a CI or agent key could bill people.
  - F14-2: security emails of one kind were spaced at least a minute apart. Minting a second credential within a minute of the first sent no email, so the owner learned of only the first.
- **Decision:**
  1. Every new API key expires `DefaultAPIKeyLifetimeDays` (365) after it is minted, full-access keys included (`api.NewAPIKeyExpiry`). Agent keys keep their shorter lifetime. Keys minted before this change keep the expiry they had.
  2. An account keeps at most `CLILoginKeysMax` (5) live `cli-login` keys. Each CLI login exchange revokes older ones beyond that. MCP sign-in keys are not capped: each belongs to a different agent connector.
  3. `POST /v1/keys/{id}/rotate` takes an optional body `{"grace_window_days": N}` (0–30) that overrides the grace window for that rotation only; 0 revokes the old key at once. The org route `POST /v1/orgs/{slug}/keys/{id}/rotate` now honours the `grace_window_days` its spec already documented. `gregale key rotate` gains `--revoke-old` and `--grace-days N` and prints until when the old key works.
  4. `POST /v1/account/billing-runs`, `PUT /v1/account/billing-run-schedule`, and the two statement invoice exports (`POST .../usage-statements/{statement_id}/invoice`) need a full-access key, a verified email and a step-up within 5 minutes, like a paid-plan change. Reading runs and statements, and deleting the schedule (which only stops charges), are unchanged.
  5. "A new credential was created" emails have their own limit: no minimum gap, at most `CredentialNoticeMailsPerHour` (20) an hour. Other security emails keep one a minute and five an hour.
- **Consequences:**
  - A full-access key now stops working a year after it was made; customers rotate it before then, as with other keys.
  - Automation that started billing runs or invoice exports with a `deploy:write` key needs a full-access key.
  - A customer who logs in on more than five machines is signed out of the oldest CLI.
  - Release skew: no schema change. An older apid ignores `grace_window_days` and uses the account window; an older CLI never sends it.

# Legal documents: operator notes

## DPA (`docs/DPA.md`)

This addendum is part of the Terms of Service: it binds every customer that
accepts them, so it names no customer, date or jurisdiction and has no
placeholders. pkg/legal's TestDPAHasNoPlaceholders fails on any template braces
or bracketed ALL-CAPS text. Ansible ships the file to /etc/faas/dpa.md
(FAAS_DPA_PATH) and the docs site vendors it, so both copies match. Section
sources: usage aggregation follows spec §10; sealed secrets spec §11/G2
(pkg/secretbox); data subject rights spec §11/G6; security measures spec §11.

## Announcing a change (`change_notice`)

Terms section 13 promises account owners an email and a dashboard notice at
least 30 days before a material change takes effect. Set `effective_date` in
`pkg/legal/legal.json` to the new date (at least 30 days after the email) and
`change_notice` to one or two plain sentences: every signed-in dashboard page
shows it until that date. Clear it once the change is in effect. Send the email
yourself. The support-access text (2026-11-21) took effect at once because
Gregale had no customers to notify yet; see
[support-access-legal-text.md](support-access-legal-text.md).

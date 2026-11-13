# Legal documents: operator notes

## DPA (`docs/DPA.md`)

This addendum is part of the Terms of Service: it binds every customer that
accepts them, so it names no customer, date or jurisdiction and has no
placeholders. pkg/legal's TestDPAHasNoPlaceholders fails on any template braces
or bracketed ALL-CAPS text. Ansible ships the file to /etc/faas/dpa.md
(FAAS_DPA_PATH) and the docs site vendors it, so both copies match. Section
sources: usage aggregation follows spec §10; sealed secrets spec §11/G2
(pkg/secretbox); data subject rights spec §11/G6; security measures spec §11.

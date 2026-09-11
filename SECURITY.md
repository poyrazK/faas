# Security policy

> **Single source of truth:** [`docs/compliance/responsible-disclosure.md`](docs/compliance/responsible-disclosure.md).
> This file is a mirror per the GitHub Security tab convention.

## Reporting a vulnerability

Please report security issues via `security@gregale.dev` (PGP-encrypted
reports are welcome — see [the disclosure policy](docs/compliance/responsible-disclosure.md#2-pgp-fingerprint)
for the current fingerprint).

Do not file a public GitHub issue for security vulnerabilities.

## SLAs

- **24 hours** — initial acknowledgement.
- **72 hours** — triage + scope assessment.
- **every 7 days** — status update while the report is open.
- **30 days** — Critical fix timeline.
- **60 days** — High fix timeline.
- **90 days** — Medium / Low fix timeline.

The full policy (scope, safe harbour, what to include in a report,
operator commitments) lives at
[`docs/compliance/responsible-disclosure.md`](docs/compliance/responsible-disclosure.md).
Suspected customer-data breaches follow the operator procedure in
[`docs/runbooks/breach-notification.md`](docs/runbooks/breach-notification.md).

## DPA cross-reference

The 72-hour triage SLA mirrors the 72-hour breach notification SLA
in DPA §10 (GDPR Art. 33). When a vulnerability is exploited against
customer data, the same 72-hour window applies for notifying
Controllers.

## Past advisories

- (none yet — repo is pre-SOC 2 Type 1)

## Contact

- Email: `security@gregale.dev`
- GitHub Security Advisories: <https://github.com/poyrazK/faas/security/advisories/new>

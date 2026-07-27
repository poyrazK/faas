"""tests._constants — shared test fixtures.

`STABLE_IDEMPOTENCY_KEY` is reused across `test_client.py` and
`test_smoke.py` so a tweak lands in one place. The convention is
`<use>-YYYY-MM-DD-<tag>`: callers pin a stable `Idempotency-Key`
across retries, and the test fixture mirrors the CI-deploy pattern
documented in `faas_sdk.idempotency` and the README's idempotency
contract.
"""

from __future__ import annotations

#: Canonical stable key for opt-in idempotency tests. The shape
#: matches what a CI deploy would pin; long enough to be visibly
#: non-random, short enough to fit in a log line.
STABLE_IDEMPOTENCY_KEY = "ci-deploy-2026-07-27-abc"

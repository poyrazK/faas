# ADR-436: Customer-owned route requirements in preview reports

- **Status:** accepted for initial implementation
- **Date:** 2026-10-01
- **Decision:** Add strict, versioned YAML/JSON route requirements to
  `gregale preview report`. Compose existing account-scoped app/rule reads and
  return satisfied, violated, or unknown configuration findings. No new control
  plane writes, storage, notifications, or VM lifecycle operations.
- **Requirements:** Consumer credential configuration, valid matching JWT
  configuration, route throttle keying/rate/missing-key behavior, and explicit
  or bounded configured guest-execution budgets. Application-owned auth remains
  unknown. Current configuration is not historical revision policy evidence.
- **Selection:** Reuse host/method/path matching and budget/throttle resolution
  from edge-rule tracing. Equal-priority and higher-precedence conditional rules
  remain unknown; lower-precedence rules cannot establish coverage. Routing,
  rewrites, and request-header mutation preserve selection uncertainty. Concrete
  paths on the preview platform hostname are the initial scope; no inference
  from a sampled parameter to all values or from shared app to environment scope.
- **Disclosure:** Include expected/actual allowlisted summaries, stable finding
  codes, rule IDs, and a fingerprint of the supplied requirements bytes. Do not
  include raw actions, header selector values, credentials, JWT claims/issuer/
  audience/JWKS values, or customer request/response payloads.
- **Bounds:** Local documents are limited to 1 MiB and 500 routes using constants
  in `pkg/api/limits.go`. These are input/evaluation safety bounds, not plan quotas.
  Unknown fields, duplicate declarations, ambiguous paths, and invalid bounds
  fail before authenticated reads.
- **CI:** `--fail-on-requirements` gates only supplied requirement findings and
  fails on violated or unknown results, independently of optional traffic/tests.
  Print the report before gate failure. Other report gates retain their scope.
- **Acceptance:** Unit tests cover selector precedence, header uncertainty,
  effective-rate floors, budget defaults/clamps, distinct authentication modes,
  strict parsing, and output redaction. CLI HTTP tests prove read-only scoped
  composition, unavailable/cross-app evidence handling, and gate/output behavior.
  No native KVM behavior or application authorization proof is claimed.

See [route requirements](../route-requirements.md) for the customer workflow.

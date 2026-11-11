# Cost-control remediation register

The audit reviewed commit `46aad2a12e4d1be39d0948b6942cb18d8e71d0b2` on
2026-11-11. All twenty original findings are retained below. Implementation and
portable checks do not constitute native release qualification.

| Finding | Priority | Problem | Remediation / remaining work |
| --- | --- | --- | --- |
| CC-001 | P1 | Strict release qualification incomplete | **Open.** Dedicated native two-host/workload/lifecycle/performance and external-provider evidence is still required. Keep promotion blocked. |
| CC-002 | P2 | Spend-cap notice lost after hold write | Atomic durable notice outbox, fenced claims and retries, stable provider message identity. |
| CC-003 | P1 | Organization strict budgets unavailable | **Open.** Implement payer-scoped strict ownership, activation, dispatch and settlement throughout account/app/project/environment books, then qualify native owners. The existing organization restriction remains. |
| CC-004 | P2 | Budgets do not cap the complete invoice | **Open product scope.** Selected supported usage meters exclude subscriptions, add-ons, tax, credits and external/managed services; storage is monitored only. Full-invoice caps require an explicit billing/credit and provider contract, not removal of these disclosures. |
| CC-005 | P2 | Ordinary cap loses known hold on outage | Preserve last-known reached decisions for their UTC month. Cold read failures retain the documented availability tradeoff; ordinary caps remain distinct from strict guarantees. |
| CC-006 | P2 | Strict-scope documentation wrong | Document implemented project/environment gross compute scopes and their exclusions. |
| CC-007 | P1 | Strict compute grants exceed billed allowance | Bound funding by the smallest historical time-weighted hour-segment allowance; preserve paid commitments and pin a conservative month grant. Native qualification still required. |
| CC-008 | P2 | Older admission read erases newer hold | Fence cache publications by read generation, including refresh, eviction and error results. |
| CC-009 | P2 | Deleted-app budget cannot be disabled | Permit disabling an owned unchanged scope; validate changed scopes and all reactivations. API disabling bypasses live activation preflight. |
| CC-010 | P2 | Account preview misses image-run jobs | Use a complete billing inventory, keeping the public jobs index's filter. |
| CC-011 | P1 | Hung egress helper blocks financial expiry | Supervise retirement independently, bound and cancel renewal/helper contexts, join helper owner. **Native teardown/leakcheck pending.** |
| CC-012 | P1 | Legacy raw provider bypasses cap | Apply cap reads/clamping to raw charged quantities, including zero caps and direct-hour delivery. |
| CC-013 | P1 | Historical egress uses today's allowance | Resolved upstream: retain per-hour plan pricing and election coverage shared by replay, live cap observations and cross-meter reservations. |
| CC-014 | P2 | Fork restore bypasses preview stop | Check admission before restore and again before publication; retain existing rollback/retirement. **Native lifecycle qualification pending.** |
| CC-015 | P2 | Retry records a quantity different from provider | Freeze provider event quantity before the first send, retaining it across cap changes and receipt failures. Reconcile already ambiguous pre-migration events separately. |
| CC-016 | P2 | Month rollover loses late threshold alerts | Re-evaluate the closing month during bounded finality; retain its outbox events without historical stops/projections or replacing current runtime health. |
| CC-017 | P2 | Managed PostgreSQL opt-out strands pending step | Resolved upstream: retain pending first charges in subject discovery after opt-out; add a regression witness. |
| CC-018 | P1 | Plan round trip lost while meterd down | Append every plan mutation, invalidate pricing cache by activation head, replay all transitions across restart and UTC months. Pre-migration lost history remains an operational reconciliation task. |
| CC-019 | P2 | Cost route bypasses invoice role restriction | Enrich organization costs with invoices only when the same manage-billing permission as the invoice route allows it. |
| CC-020 | P2 | Month close revives stale storage estimate | Preserve stale and partial coverage until the estimate covers the closed period. |

The changes implement [ADR-20261111140000](adr/20261111140000-cost-control-delivery-safety.md).
The native gates and feature restrictions stay in place until their actual
requirements pass; they are not closed by changing a verifier's verdict.

## Verification

Persistent regression tests live beside the affected Go packages, with shared
memory/PostgreSQL adapter checks under `pkg/state/financialtest`. The original
fifteen audit witnesses remain in the saved scratch register for comparison.
Test outcomes and remaining limitations are recorded below.

Native validation requires the dedicated hosts described in
[the strict compute acceptance runbook](runbooks/financial-strict-compute-acceptance.md)
and [the two-host qualification runbook](runbooks/financial-two-host-qualification.md).
This development environment has PostgreSQL 16 but no `/dev/kvm`.

Validated in this checkout:

- Complete `pkg/meter`, `pkg/billing`, `pkg/state/financialtest`, `pkg/authz`
  and `pkg/apid/orgbilling` suites pass, including migrated PostgreSQL 16.
- Fifteen original defect witnesses have persistent regression counterparts.
  Additional tests cover the API disable path, known-hold outage handling,
  revisions edited after month close, role filtering, twelve shared adapter
  cases, and additive-migration replay without changing durable decisions.
- Focused API, fork, admission-cache, runtime-deadline and managed PostgreSQL
  regression runs pass. Their Go overlay excludes unrelated large state,
  scheduler, runtime and API test fixtures; production source is unchanged by
  the overlay. The API overlay supplies only test exports and exercises the
  real router, middleware and handlers.
- Existing portable financial compute/egress tests pass using the complete
  `pkg/fcvm` test fixture set.
- SQLC regeneration check, expand/contract migration lint, test-store shape
  checks, formatting, diff whitespace and documentation URL checks pass.

The `-race` admission-cache and blocked-helper deadline runs pass, including
the existing cache grace/error cases. PostgreSQL pricing also retains the
recorded timestamp used to order activations in the same minute, matching the
memory adapter and billing replay.
The full repository suite, full repository lint, native metal/leakcheck,
physical cross-host scenarios and external-provider qualification have not
been claimed. No deployment or external provider traffic was performed.

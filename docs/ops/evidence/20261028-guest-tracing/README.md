# Guest tracing metal evidence

The `tracing` capability is `preview`. Its catalogued acceptance test was
`pkg/apid/apidserver/handlers_selected_trace_test.go::TestSelectedAppTraceRoute`,
an apid handler test. This run proves the in-guest path on a native KVM host
(ADR-958):

guest-init env stamping → `127.0.0.1:4318` bridge → vsock 1041 → vmmd broker
(host-owned identity) → apid guest span ingest → accumulator flush →
`request_telemetry.spans_summary` →
`GET /v1/apps/{slug}/debug/requests/{id}/evidence`.

| Test | App |
|---|---|
| `TestGuestTracingMetal` | an app with an OpenTelemetry SDK compiled in and no tracing configuration; its `SELECT orders` span must reach the request evidence |
| `TestGuestTracingGoHelperMetal` | a Go app instrumented only with the SDK's tracing package; its PostgreSQL and HTTP calls must show as `app_dependency` spans |

## Run

| | |
|---|---|
| Host | `gregale-internal-test-1` (nested KVM), under the shared `/var/lock/faas-builder-acceptance.lock` |
| Source | `cf0a7ff91c` (`fix/tracing-catalog`: main `c220613a7b` plus a catalog-only change) |
| Scripts | `e2e-run.sh` builds the daemons, guest-init and fixtures, then calls `e2e-test.sh`, which waits for the lock and runs `e2e-inner.sh` with `E2E_RUN='^(TestGuestTracingMetal\|TestGuestTracingGoHelperMetal)$'` |
| Lock acquired | 2026-10-28 12:46 UTC |

| Check | Result |
|---|---|
| `TestGuestTracingMetal` | PASS (231 s) |
| `TestGuestTracingGoHelperMetal` | PASS (236 s) |
| leakcheck after | clean |

Not run: `TestGuestTracingNodePreloadMetal` and `TestGuestTracingPythonMetal`
skip without prebuilt runner images (`FAAS_E2E_NODE_TRACING_IMAGE_DIR`,
`FAAS_E2E_PYTHON_TRACING_IMAGE_DIR`, built by
`pkg/e2etest/testdata/tracingfixtures/build.py`), and customer OTLP ingest
through `/v1/otel/v1/traces` is not part of these tests. Native CI could not
run this: billing is disabled on the CI project.

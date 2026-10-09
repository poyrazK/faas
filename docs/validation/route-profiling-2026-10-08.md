# Route profiling validation — 2026-10-08

Environment: Go 1.25.13, Pyroscope 1.14.1 with multitenancy enabled,
Chromium, local Docker. All identities were generated for this run.

## Results

- Real Go CPU collector → local HTTP control/ingest bridge → host ingestion
  service → Pyroscope → merged query: passed under the race detector.
  Two captures produced 2.130 CPU seconds and 1,814 labeled request entries.
  The known `cpuHotWork` function was present. Static route filtering, merged
  counter preservation, replay deduplication and tenant isolation passed.
  See `route-profile-live-2026-10-08.json` for the response.
- Existing live backend integration: passed (CPU comparison, worker merging,
  replay deduplication and coverage/failure tenant isolation).
- Profiling and guest collector race suites: passed. Profile protocol has no
  standalone tests; the collector exercises its route report decoder.
- Guest init profiling tests and collector checkpoint acknowledgement: passed.
- Profile investigation, regression and deployment persistence tests: passed.
- Browser drilldown, investigations, regressions and deployment checks suites:
  passed, including CSP, saved evidence and simulated expired-profile views.
- Added labeling tests: incomplete reports and overcounts are unavailable;
  low or substantially changed labeling shares are inconsistent.

## Issues fixed

Live Pyroscope results contained relocated and repeated synthetic route
frames. The decoder now removes every reserved marker, recognizes repeated
matching markers and treats conflicting route markers as unattributed.
Regression tests cover relocated, repeated, conflicting and spoofed markers.

Updated a stale principal equality assertion after route slices were added,
and added missing route fields to the browser rendering fixture.

## Reproduction

Use a private Pyroscope with `-auth.multitenancy-enabled=true`. Its container
network must be in `NO_PROXY` so internal gRPC peers can communicate.

```sh
GREGALE_PROFILE_TEST_BACKEND=http://127.0.0.1:4040 \
  go test -race -v ./pkg/guestprofiling -run TestLiveRouteCollectorPyroscope -count=1
GREGALE_PROFILE_TEST_BACKEND=http://127.0.0.1:4040 \
  go test -v ./pkg/profiling -run TestCPUProfilePyroscopeIntegration -count=1
go test -race ./pkg/profiling ./pkg/profileproto ./pkg/guestprofiling
go test -race ./guest/init -run Profil -count=1
go test ./pkg/state -run 'Profile(Investigation|Regression|Deployment)' -count=1
node tests/profiling/drilldown_browser.cjs
node tests/profiling/investigations_browser.cjs
node tests/profiling/regressions_browser.cjs
node tests/profiling/deployment_checks_browser.cjs
```

## Remaining acceptance checks

This was a real sampler/backend integration, using a local bridge and explicit
host principal. It did not exercise a deployed application, apid telemetry,
vsock authentication or physical snapshot/restore. No KVM or vhost-vsock
device is available in this workspace. A native VM run must still verify
counter boundaries across park/restore and epoch transitions.

The observed request total in the live test is derived from capture reports,
so it verifies transport and merge preservation, not independent reconciliation
against gateway telemetry. Known regressions, sparse traffic and label loss
still need a deployed traffic run. Browser expiry checks use fixtures rather
than waiting for physical backend retention expiry.

The full guest init suite failed two file permission assertions unrelated to
profiling (restored source mode and executable source mode). Focused profiling
tests passed; the full suite is not claimed to pass.

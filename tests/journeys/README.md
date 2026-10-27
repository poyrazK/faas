# Customer journey suite

Black-box end-to-end tests of a **deployed** Gregale. Each journey does what
a customer does — `gregale deploy`, `curl` the app, read logs, set secrets,
park and wake, roll back — and asserts on the CLI's JSON output and the
app's public responses. No Postgres, KVM, daemon build or test-node lock is
involved, so a run needs only Go, Python 3 and network access.

It complements the in-repo gates (`make test`, `make e2e`, `make test-metal`):
those prove a change inside the platform; this proves the deployed product
works for a customer. It cannot see internals (parked RAM, leaked devices,
snapshot sizes) — those stay in the metal tests.

## Run it

```sh
scripts/e2e-journeys.sh                    # smoke tier against https://api.gregale.dev
scripts/e2e-journeys.sh --tier full        # every journey
scripts/e2e-journeys.sh --run 'TestReleaseAndRollback|TestScaleToZero'
scripts/e2e-journeys.sh --list             # journey names
make e2e-journeys TIER=full RUN=TestTemplateDeploys
```

- **CLI**: by default the script downloads the newest `v*` release CLI for
  this OS with `gh` (cached under `~/.cache/gregale-e2e`, checksum-verified).
  Use the release the target runs: `--cli-version v0.1.18-rc.296`, or
  `--cli PATH` for a local build.
- **Account**: by default the CLI's own `gregale login` (kept in the OS
  keychain; the suite never reads it). `GREGALE_E2E_TOKEN` or `--token-file`
  use an API key instead, with the CLI isolated in a private `HOME`.
- **Output**: live PASS/FAIL/SKIP lines, then a summary, every failure with
  its log, and `metric` lines (e.g. cold-wake time). The raw `go test -json`
  stream and `report.md` land in `.e2e-journeys/<timestamp>/` (gitignored).

### Parallelism

Every journey runs in parallel (`go test -parallel 32`); two limits keep
that safe on a shared production fleet:

- **App slots.** Before the first app is created the suite reads the
  account's deployed-app limit and current count, and holds at most that
  many test apps at once (`--max-apps` caps it lower). A journey waits for a
  slot instead of failing on `plan_limit_apps`.
- **Deploy slots.** At most four deploys run at once (`--max-deploys`). The
  probe app's sources are fixed, so after the first run every deploy is a
  build-cache hit that skips the builder VM; a cache-miss build reserves a
  20 GiB builder drive, so lower the limit after changing
  `testdata/probe-node`. A deploy aborted for capacity (RAM admission
  ceiling, builder drive space, `failure_class=infra`) is retried once and
  listed under **warnings** in the report.

Read-only journeys share one probe app (`sharedProbe`); journeys that change
an app (secrets, park, maintenance, releases, delete) deploy their own
(`deployProbe`). Only the safe-rollout journey waits for the default safe
ladder (about 20 minutes on a low-traffic app); every other redeploy uses
`--rollout immediate`.

Even a cache-hit deploy takes about 3 minutes on production (image stage,
security scan, snapshot), so smoke takes about 10 minutes and full about 30,
bound by the safe-rollout journey.

## Journeys

| Test | Tier | Covers (GA inventory rows) |
|---|---|---|
| `TestPublicSurface` | smoke | API health, OpenAPI, status API and page, unknown-host 404, website, `security.txt` and the URLs it names (141, 159, 161) |
| `TestDocsPages` | smoke | every page in `docs/customer-pages.json` answers 200 |
| `TestAccountAndKeys` | smoke | `whoami` and limits, capabilities, usage, invoices; a scoped `apps:read` key reads, cannot mint an admin key, shows in the audit log, and is rejected once revoked (138, 140, 142, 144, 151) |
| `TestTemplateDeploys` | smoke: `hello-node`, `function-node`; full: Python, Go, Node 24, Python 3.13 | template deploy, build, smoke-verified receipt, public URL or `gregale invoke` (2, 3, 9, 41) |
| `TestServeAndObserve` | smoke (shared app) | serve and health, server-sent events, runtime logs, HTTP request logs; full adds metrics (2, 3, 64, 118, 121) |
| `TestReleaseAndRollback` | smoke | redeploy with an immediate rollout, deployment history, rollback to the first revision (1, 24) |
| `TestSecrets` | smoke | sealed secret delivered with a restart (checked by hash), `secrets list` hides the value; full adds `env history` (60, 61) |
| `TestScaleToZero` | smoke | park, `ps` empty, wake on request (cold-wake time), `ps` lists it; full adds `wake --wait`, min instances, fresh restart (38, 42, 43, 46) |
| `TestCronDelivery` | full (shared app) | `crons add` and `crons run` deliver a POST the app counts (92) |
| `TestMaintenanceAndAccess` | full | maintenance 503, `--require-authn` 401, basic auth 401 without and 200 with credentials (77) |
| `TestSafeReleaseZeroDowntime` | full | default safe redeploy while probing every second: no failed request (24, 38) |
| `TestHealthGate` | full | a release whose `/healthz` answers 503 is refused and the previous release keeps serving (47) |
| `TestDeleteAndRestore` | full | delete stops the URL, `apps restore` brings the app back and it serves (1) |
| `TestImageDeploy` | full | digest-pinned public image deploys without a build and serves (4) |
| `TestGuestGuarantees` | smoke (3 wakes) / full (10) | from inside the app: egress refused to metadata, private ranges, the host link and mail ports while public HTTPS works; every wake has fresh RNG output and a stepped clock; a 20-request burst on a parked app is served by one wake; wake p50/p95 and overhead over a warm request (40, §6.2 invariant 5, §11) |

Not covered yet: per-app deploy tokens (API only, no CLI command, needs
step-up auth); and flows that need outside systems: signup and password-reset
emails, custom domains and TLS, GitHub push-to-deploy, billing checkout.

## Known bugs, history and trends

- `known_failures.json` lists steps that fail on a known platform bug. Such a
  step reports **KNOWN** and does not fail the run; when it passes, the report
  says so, so the entry can be removed once the fix is confirmed. Add an entry
  only for a bug that is understood and tracked.
- Every run writes `summary.json` next to its report. The reporter compares
  the run with the newest earlier summary in the same parent directory:
  newly failing and now-passing steps, steps 50% (and 30 s) slower, metric
  changes (wake p50/p95, clock skew, deploy stage times), and steps that both
  passed and failed in the last five runs (flaky).

## Scheduled runs (this Mac, no CI)

```sh
scripts/e2e-journeys-schedule.sh install --hour 3   # LaunchAgent, checks hourly
scripts/e2e-journeys-schedule.sh status
scripts/e2e-journeys-schedule.sh uninstall
```

Each hourly check runs a smoke pass with the matching CLI when a new `v*`
release appears (production deploys every release) and otherwise one full
pass a day after `--hour`. Runs never overlap. Reports go to
`~/.local/state/gregale-e2e/runs/`, and a macOS notification names any run
with a failure not in `known_failures.json`.

## Safety

- Every app is named `e2ej-<run>-<name>`; `<run>` encodes the start minute.
  Each journey deletes its apps when it ends, pass or fail
  (`--keep` keeps a failed journey's apps for debugging).
- Before a run, apps from runs older than `--sweep-hours` (default 3) are
  deleted. Only slugs that parse as `e2ej-<run>-<name>` are touched, so
  hand-made apps on the same account are safe.
- API keys the account journey mints are labelled `e2ej-<run>-key`, scoped
  to `apps:read`, and revoked before the journey ends.
- Key plaintexts and the token are redacted from all test output.

## Writing a journey

A journey is a `Test…` function that starts with
`tg := needs(t, tierSmoke|tierFull, needsAccount)` and `t.Parallel()`.
Ordered steps use a story, so a broken step marks the rest
`blocked: step "x" failed` instead of failing them all:

```go
func TestMyFeature(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	slug, base := tg.deployProbe(t, "myfeature") // own app, v1, deleted at the end
	// or: slug, base := tg.sharedProbe(t)        // read-only use of the shared app
	s := newStory(t)
	s.step("change", func(t *testing.T) {        // later steps depend on it
		tg.ok(t, time.Minute, "app", slug, "--some-flag")   // fails on non-zero exit
	})
	s.check("observe", func(t *testing.T) {      // failure does not block later steps
		doc := object(t, tg.ok(t, time.Minute, "app", slug).stdout) // CLI JSON
		if str(doc, "nested.field") != "want" { t.Fatalf("…: %s", compact(doc)) }
		eventually(t, "app reflects it", 2*time.Minute, 3*time.Second, func() (bool, string) {
			r := get(base + "/")
			return r.status == 200, r.String()
		})
	})
}
```

Add routes to `testdata/probe-node/server.js` when a journey needs the app
to prove something (that changes the source digest, so the next run builds
once). Redeploys outside the safe-release journey should pass
`--rollout immediate`. `go test ./tests/journeys` with no target runs only
the harness's own unit tests; `python3 -I tests/journeys/report_test.py`
tests the reporter.

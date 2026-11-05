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

`COVERAGE.md` maps every GA capability in `docs/ga-capability-inventory.md`
to the journeys and steps that test it, and lists what is not covered.
`scripts/e2e-journeys.sh --list` prints the journeys; `--github-repo`
enables `TestGitHubDeploys` against a fixture repository the account's
GitHub App installation can see (e.g. `poyrazK/gregale-e2e-fixture`).

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

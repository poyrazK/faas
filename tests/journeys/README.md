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
scripts/e2e-journeys.sh --run TestAppLifecycle
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

Smoke takes about 10 minutes and holds 3 apps; full about 20 minutes and 8
apps. Deploys run at most two at a time (`--max-deploys`): each build
reserves a 20 GiB builder drive, so firing every template at once measures
builder capacity rather than the features. A deploy aborted for capacity
(RAM admission ceiling, builder drive space, `failure_class=infra`) is
retried once and listed under **warnings** in the report. The account needs that many free deployed-app slots: the suite checks
before it creates its first app and stops with the numbers if it cannot fit.

## Journeys

| Test | Tier | Covers (GA inventory rows) |
|---|---|---|
| `TestPublicSurface` | smoke | API health, OpenAPI, status API and page, unknown-host 404, website, `security.txt` and the URLs it names (141, 159, 161) |
| `TestDocsPages` | smoke | every page in `docs/customer-pages.json` answers 200 |
| `TestAccountAndKeys` | smoke | `whoami` and limits, capabilities, usage, invoices; a scoped `apps:read` key reads, cannot mint an admin key, shows in the audit log, and is rejected once revoked (138, 140, 142, 144, 151) |
| `TestTemplateDeploys` | smoke: `hello-node`, `function-node`; full: Python, Go, Node 24, Python 3.13 | template deploy, build, smoke-verified receipt, public URL or `gregale invoke` (2, 3, 9, 41) |
| `TestAppLifecycle` | smoke + full steps | source deploy of `testdata/probe-node`, serve, SSE, runtime and HTTP logs, secrets delivery, park and wake on request (cold-wake time), redeploy, deployment history, rollback; full adds a cron fire, maintenance mode, metrics, `wake --wait`, zero-downtime redeploy probe, delete and restore (1, 2, 3, 24, 38, 42, 46, 60, 61, 64, 92, 118, 121) |

Not covered yet (they need outside systems): signup and password-reset
emails, custom domains and TLS, GitHub push-to-deploy, billing checkout.

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
s := newStory(t)
slug := tg.app(t, "myfeature")              // e2ej-<run>-myfeature, deleted at the end
s.step("deploy", func(t *testing.T) {        // later steps depend on it
	tg.deploy(t, slug, "--path", probeSource(t, "v1"))
})
s.check("thing", func(t *testing.T) {        // failure does not block later steps
	res := tg.ok(t, time.Minute, "some", "command", slug)   // fails on non-zero exit
	doc := object(t, res.stdout)                             // CLI JSON
	if str(doc, "nested.field") != "want" { t.Fatalf("…: %s", compact(doc)) }
	eventually(t, "app reflects it", 2*time.Minute, 3*time.Second, func() (bool, string) {
		r := get(tg.appURL(slug) + "/")
		return r.status == 200, r.String()
	})
})
```

Add routes to `testdata/probe-node/server.js` when a journey needs the app to
prove something, and bump `appsNeeded` in `harness_test.go` when a journey
holds another app. `go test ./tests/journeys` with no target runs only the
harness's own unit tests; `python3 -I tests/journeys/report_test.py` tests
the reporter.

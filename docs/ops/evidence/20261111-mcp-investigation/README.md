# Gregale MCP investigation evidence

Retained regression probes for the 21 findings in `docs/ops/mcp-investigation-findings.md`, based on commit `46aad2a12e4d1be39d0948b6942cb18d8e71d0b2`.

Passing remediation evidence and exact validation commands are in [remediation/README.md](remediation/README.md). Historical failure descriptions below refer to the original checkout. Two Node race probes now wait for explicit readiness/claim handoff so their assertions can pass with the corrected asynchronous lifecycle.

Run from the repository root:

```sh
python3 docs/ops/evidence/20261111-mcp-investigation/run_probes.py
```

Use `--go /absolute/path/to/go` to select Go 1.26.10. The runner uses the managed workspace toolchain when present. Python requires the starter's OAuth dependencies; the supplied identity isolates resource authorization and requires no live issuer or credentials.

All probes now pass after remediation. Exit 1 indicates a regression or unmet prerequisite; inspect the assertions to distinguish these cases. New logs default to `remediation/`, preserving the original failure logs in this directory. Use `--output-dir` to select a different destination. Production files are never overlaid or edited: the Go overlay adds probe test files to their original package only for the test invocation. Go probe sources use `.go.txt` filenames so normal repository package discovery ignores them. Stored logs contain synthetic loopback endpoints, fixture IDs and fixed sample data. Trailing whitespace in captured output is normalized for repository checks.

| Probe | Findings |
| --- | --- |
| `management_repro_test.go.txt` | MCP001 through MCP004 |
| `management_round2_repro_test.go.txt` | MCP007 and MCP008 |
| `python_starter_repro_test.py` | MCP006 and MCP009 |
| `hosting_repro_test.go.txt` | MCP010 |
| `python_round3_repro_test.py` | MCP011 |
| `node_round3_repro_test.mjs` | MCP012 and MCP013, including real HTTP catalog discovery |
| `go_starter_round3_repro_test.go.txt` | MCP014, the previously unconfirmed heartbeat lead |
| `hosting_round4_repro_test.go.txt` | MCP015 and MCP016, including official SDK metadata objects |
| `node_round4_repro_test.mjs` | MCP017 and MCP018, including PostgreSQL shutdown/claim handoff |
| `hosting_round5_repro_test.go.txt` | MCP019 and MCP020, with JSON transport and discovery controls |
| `node_round5_repro_test.mjs` | MCP021, with PostgreSQL and successful retained-key recovery |

MCP005 is established by the stock service command, daemon default and connection toolset intersection; it has no runtime probe. MCP002 uses scaled HTTP and handler deadlines to reproduce the timeout mismatch without waiting 75 seconds.

## Original third-pass setup and reproduction

Install Node dependencies into a temporary copy, outside the embedded template tree:

```sh
mcp_probe_dir=$(mktemp -d /tmp/gregale-mcp-probes.XXXXXX)
cp -R cmd/gregale/templates/mcp-node "$mcp_probe_dir/node-starter"
npm ci --prefix "$mcp_probe_dir/node-starter" --ignore-scripts --no-audit --no-fund
```

If the default npm cache is not writable, pass `--cache` with an allowed shared cache directory. This investigation used `/workspace/faas-env/cache/npm`.

Provide a disposable PostgreSQL database whose test role can create and drop schemas, then run:

```sh
MCP_TASKS_TEST_DATABASE_URL='postgresql://testuser@127.0.0.1:5432/mcp_test' \
  python3 docs/ops/evidence/20261111-mcp-investigation/run_probes.py \
  --round3-only --node-starter "$mcp_probe_dir/node-starter"
```

Use the actual local test database URL in place of the example. No database URL is written to the saved result manifest. The heartbeat probe creates and drops an isolated schema, queues 6,000 encrypted tasks and introduces 10 ms of claim latency with a fixture trigger. It runs the original worker loop for 47 seconds, with unchanged ticker and freshness intervals. The latest captured run completed 2,889 executions with 3,111 tasks still queued and a 47.003-second capability heartbeat age, exceeding the 45-second freshness budget.

The runner explicitly reports absent Node/PostgreSQL prerequisites as `not_run`, returns nonzero, and keeps the earlier logs. With third-pass prerequisites supplied, `round3_probe_results.json` records all three probe commands exiting 1. Python reports one regression assertion; Node reports three assertions covering two findings; Go reports the expired heartbeat assertion. These failures reproduce the unresolved defects.

## Original fourth-pass reproduction

Use the same temporary Node starter setup and disposable PostgreSQL prerequisites, then run:

```sh
MCP_TASKS_TEST_DATABASE_URL='postgresql://testuser@127.0.0.1:5432/mcp_test' \
  python3 docs/ops/evidence/20261111-mcp-investigation/run_probes.py \
  --round4-only --node-starter "$mcp_probe_dir/node-starter"
```

`round4_probe_results.json` records both probe commands exiting 1. `hosting_round4_repros.log` contains four literal-data discovery failures for MCP015 and three standard `_meta` failures for MCP016. The `_meta` definitions are marshaled from the pinned official Go SDK's resource/template/prompt types.

`node_round4_repros.log` records two assertions. The startup probe uses a controlled asynchronous store with 10 ms claim latency and fast handlers: successful work continues while initialization remains unresolved during a 200 ms observation window. The shutdown probe uses the actual PostgreSQL store and a private schema, holding delivery of a committed claim until shutdown has completed. It observes `timedOut: false`, a `running` task with one consumed attempt and zero handler executions. It then sets only that fixture's lease to expired and verifies that the ordinary one-attempt claim policy marks the task failed. No production clock or runtime implementation is replaced.

The runner reports missing Node/PostgreSQL prerequisites as not run. The original evidence logs remain separate and unchanged.

## Original fifth-pass reproduction

Use the same temporary Node starter setup and disposable PostgreSQL prerequisites, then run:

```sh
MCP_TASKS_TEST_DATABASE_URL='postgresql://testuser@127.0.0.1:5432/mcp_test' \
  python3 docs/ops/evidence/20261111-mcp-investigation/run_probes.py \
  --round5-only --node-starter "$mcp_probe_dir/node-starter"
```

`round5_probe_results.json` records both probe commands exiting 1. `hosting_round5_repros.log` shows the same tool result succeeding over JSON and failing over SSE with `bufio.Scanner: token too long`. It also records Doctor returning `OK=true` after rejecting its only advertised tool for an invalid header annotation.

`node_round5_repros.log` uses the original PostgreSQL store and a private schema. It retains two v1 payloads with different current/previous reader keys. The store initializes successfully with both keys, but also incorrectly initializes after the previous key is removed. Claiming then fails authentication and leaves both tasks queued without spending attempts. Reinstating the previous key allows the same task to be claimed and completed, proving the retained payload was valid. The task UUIDs determine which row preflight samples; a separate creation-time ordering determines which task claims encounter first. The schema is dropped after the probe.

The runner reports absent prerequisites as not run and preserves the earlier passes' logs.

## Original baselines

The original Node suite ran from the temporary starter with pinned dependencies: 93 passed, 55 skipped, zero failures (`node_round3_baseline.log`). PostgreSQL-dependent Node tests were skipped because no Node test database was configured for that baseline.

The original Go starter package suite ran against the temporary PostgreSQL 17.11 database, without any investigation overlay, and passed in 4.656 seconds (`go_starter_round3_baseline.log`):

```sh
MCP_TASKS_TEST_DATABASE_URL='postgresql://testuser@127.0.0.1:5432/mcp_test' \
  MCP_TASKS_REQUIRE_POSTGRES=1 \
  go test -trimpath -gcflags=all=-dwarf=false -p=1 \
  ./cmd/gregale/templates/mcp-go -count=1 -timeout=120s
```

The fourth-pass baseline ran `go test -trimpath -gcflags=all=-dwarf=false -p=1 ./pkg/mcphosting -count=1 -timeout=90s` without overlays: passed in 3.320 seconds (`hosting_round4_baseline.log`).

The relevant existing Node task tests also passed against disposable PostgreSQL: 24 passed, zero failed or skipped (`node_round4_baseline.log`). They were selected from `test/tasks.test.js` and `test/tasks.postgres.test.js` in the temporary starter with:

```sh
MCP_TASKS_TEST_DATABASE_URL='postgresql://testuser@127.0.0.1:5432/mcp_test' \
  MCP_TASKS_REQUIRE_POSTGRES=1 \
  node --test --test-name-pattern='(lease|drain|shutdown|restart)' \
  test/tasks.test.js test/tasks.postgres.test.js
```

The fifth-pass unmodified Go hosting suite passed in 3.477 seconds (`hosting_round5_baseline.log`). Four relevant existing Node crypto/retained-key tests passed against disposable PostgreSQL, with zero failures or skips (`node_round5_baseline.log`), selected with:

```sh
MCP_TASKS_TEST_DATABASE_URL='postgresql://testuser@127.0.0.1:5432/mcp_test' \
  MCP_TASKS_REQUIRE_POSTGRES=1 \
  node --test \
  --test-name-pattern='(payload rotation|ownership secret|retained native|authenticat|key fingerprint|key removal)' \
  test/tasks.postgres.test.js test/task-crypto.test.js
```

The original logs above predate all source fixes. The `remediation/` directory contains the passing post-fix probes and package/starter checks. No release operations or live provider calls were performed.

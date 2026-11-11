# Current-main integration verification

The branch was integrated with main at
`b376664ec965c8663be7c433b1e10592515bd6dd` before opening PR #7080. These
results verify code head `19bd74d38d1400c6c4bbe22806dbc72783b05a10` and supersede
the earlier local remediation counts where main added more tests.

| Check | Result | Log |
| --- | --- | --- |
| Go regression tests, application resource authorization, cron action history | Passed | `go_regressions.log` |
| Full Node starter suite with PostgreSQL | 236 passed, zero skipped | `node_suite.log` |
| Full Python starter suite with PostgreSQL | 101 passed, zero skipped | `python_suite.log` |

The integration retains main's shared task-name validator, Standard Schema
snapshots, exhaustive retained-payload checker and claim/lease safeguards.
Cron recovery follows main's receipt policy: ambiguous writes require
inspection and cannot be replayed without durable native proof. A matching
configuration alone does not prove that this particular request committed.
The permanent cron test and retained probe now check consistent inspection
guidance, preservation of concurrent changes and absence of repeated PATCHes.

Custom public subscription fixtures explicitly authorize their URIs. Lease
tests wait for the handler to start before advancing their simulated clock.
Neither adjustment weakens ownership, lease or shutdown assertions.

The Go checks used Go 1.26.10 with the shared workspace cache:

```sh
go test -trimpath -gcflags=all=-dwarf=false -p=1 \
  ./pkg/mcpmanagement ./pkg/mcphosting ./cmd/gregale/templates/mcp-go \
  ./cmd/mcp-bridged \
  -run 'MCPRegression|ResourceOwnership|ApplicationResource|CronActionHistory' \
  -count=1 -timeout=180s
```

Starter suites used disposable PostgreSQL and
`MCP_TASKS_REQUIRE_POSTGRES=1`. Node ran `npm test` from the temporary starter
copy. Python ran `python3 -m unittest discover -q` with its pinned
`PyJWT[crypto]==2.15.1` and PostgreSQL dependencies installed outside the
repository. No database URL or credentials are retained in the manifest.

Required GitHub checks and the MCP contract workflow gate the final merge.

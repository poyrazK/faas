# Gregale start acceptance

Run the local gate from the repository root on Linux with the Go version in
`go.mod`, PostgreSQL, `/dev/ptmx`, and CAP_NET_ADMIN in the capability bounding
set (required by schedd). No KVM or platform credentials are needed.

```sh
make e2e-start
```

Set `DATABASE_URL` to a disposable PostgreSQL fixture on which tests can create
isolated schemas/databases. The harness owns migrations and teardown; do not
migrate the shared `public` schema. Unset `FAAS_SKIP_PG_TESTS` and
`FAAS_E2E_BIN_DIR`: the gate refuses skip mode and reused daemon binaries.
The harness builds the current checkout's daemons, and the tests build its CLI.
The command returns nonzero on failure. Use `go test -v -race -count=1
-timeout=12m -run '^TestE2E_Start_' ./cmd/e2e/...` with the same prerequisites for
per-scenario deployment IDs and failure transcripts.

The tests run the CLI as a subprocess with terminal input and isolated config,
cache, and session directories. They drive the actual prompts and assert:

- Public, API-token-protected, and internal apps launch in default and named
  environments.
- The source upload reaches apid, and each successful response belongs to the
  accepted app and deployment after a scheduler cold wake. Public edge health
  answers require selecting an app route before verification can succeed.
- An uncommitted file is reviewed, included in the replacement's archive, and
  followed by a response from that replacement. When public responses omit
  deployment identity, the CLI obtains separately labeled owner-check evidence.
- A missing required key blocks submission; a selected secrets file is sealed
  into the named environment and excluded from the source archive and session
  metadata. The default scope remains empty.
- A failed replacement retains both its accepted ID and the previous verified
  ID; restarting the CLI can review and retry it.
- Ctrl-C returns 130 after acceptance; resuming checks the saved deployment
  without submitting another one.
- A failed GET returns nonzero when finished; retrying it checks the same
  deployment without creating another one.
- Session files use mode `0600` and omit credentials, source contents, and
  response bodies. Account credentials do not reach the guest request.

This gate runs real apid, schedd, both gateways, and PostgreSQL. It simulates
build completion by publishing a signed fixture layer at the state boundary;
the fake VMMD speaks the real streaming protocol and records boot/forward calls.
A disposable trusted TLS adapter forwards preview hosts to the real public edge.
It requires neither external DNS nor changes to the machine's trust store.
The gate does not qualify builder microVMs, guest execution, snapshots, public
DNS/certificates, or native wake latency.

## Nonproduction live gate

Before release, repeat this matrix on an explicitly selected nonproduction
account/project with its normal credentials and dedicated native x86_64 Linux
KVM capacity. Confirm the account, API, project, and environment in the review
screen. Use disposable apps and fixture-only values; do not use customer data.

| Scenario | Required evidence |
| --- | --- |
| Public and private apps, default and named environment | Accepted ID, successful status, correct served deployment; private access remains enforced |
| Edit, review, deploy again | Changed source reaches the guest; new accepted ID matches the served ID |
| Park, then check again | Scheduler/VM evidence identifies the selected deployment and native cold boot or snapshot restore |
| Missing required configuration | No deployment before configuration is resolved; selected scope receives the supplied keys |
| Failed build | Failure is visible; accepted and previously verified IDs remain inspectable; fixed source can be reviewed and deployed |
| Ctrl-C after acceptance, restart, resume | Exit 130, same accepted ID, no duplicate deployment, successful response after recovery |
| Response failure and retry | Non-2xx is not reported as verified; retry checks the same accepted ID |

Save the checkout revision, CLI/server release identities, target IDs, scenario
results, and bounded redacted logs with the release evidence. Keep tokens,
secret-file paths/contents, response bodies containing customer data, and raw
terminal recordings out of published evidence. Remove only the disposable
resources created for this run, using their recorded identities.

## Review and rollout

Review the CLI and response-check contract changes together with
[ADR-1331](../adr/1331-owner-authorized-deployment-response-checks.md). Attach
local gate output and live results to the review; incomplete live evidence stays
explicitly pending. Follow the
[release readiness checklist](release-readiness-checklist.md) for signed bundles
and fleet convergence.

Deploy the gateway response-check capability before enabling its apid route,
then release the CLI. Smoke-check a private named-environment deployment before
expanding the rollout. An older server must return an unsupported check failure;
the CLI must not fall back to an unscoped invocation. If reverting, withdraw the
CLI first, then the apid route, then the gateway capability; retain accepted
deployment records so existing sessions remain inspectable.

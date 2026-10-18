# MCP release qualification

MCP hosting remains preview until the reviewed build has evidence for every
check below. Portable tests qualify protocol handling and PostgreSQL correctness;
they do not qualify Firecracker restore or provider/client login interoperability.
Use a dedicated test account and native x86_64 host with KVM. Keep credentials
in environment bindings and exclude tokens, subjects and customer payloads from
stored evidence.

| Evidence name | Required observation |
| --- | --- |
| `untouched_lockfile_deploy` | Deploy the scaffold with every original npm integrity field; record deployment ID, source digest, lockfile digest and CLI promotion receipt. |
| `native_cold_boot` | Verify a candidate from a cold boot with no reusable snapshot; record its deployment, host/build versions and wake classification. |
| `native_park_restore` | Park, verify discovery and harmless live progress after restore, then park again and record no remaining active instance. |
| `oauth_resource_server` | With a staging issuer, prove a valid token can use one harmless scoped tool, while expired and wrong-audience tokens receive 401 and a valid under-scoped token receives 403. |
| `oauth_provider_lifecycle` | Discover the configured provider, complete authorization code + PKCE, use and refresh a resource token, revoke its refresh token, and optionally qualify signing-key rotation. |
| `oauth_chatgpt_login` | Connect in ChatGPT through the selected real OAuth provider: discovery, PKCE, resource audience, consent, valid request and rejection after expiry. |
| `oauth_codex_login` | Repeat the real provider flow in Codex and record provider/client versions. |
| `official_sdk_interop` | Exercise the Node, Go and Python starters in modern and legacy modes using the pinned official SDK client; check tools, resources, prompts, prompt/resource completions and streamed progress in each runtime. CI also checks Go and Python resource/prompt/completion scope policies, including filtered catalogs, unlisted-entry denial, and insufficient-scope challenges. |
| `tasks_restart_replicas` | Create a task, kill/restart its worker, resume with two replicas, fence the stale lease, and retrieve the result only as its owner. Include final-attempt input resume. |
| `tasks_scale_from_zero` | CI proves the Gregale control-plane path from zero to four MCP workers and back using fresh task-backlog metrics. The staging runner requires a distinct linked observer app with an always-on running replica, a fresh observer heartbeat, a fresh zero metric and zero worker replicas before enqueue, then observes a fresh positive backlog, worker start, task completion, a fresh zero metric and return to zero. |

Reject a candidate with bad protocol behavior or a widened reader catalog and
record the previous serving revision still at 100%. Repeat promotion with a
concurrent serving revision change and record the 409 without a traffic write.
Use explicit harmless probe tools; deployment/catalog gates never execute tools.
Schema migrations, sealed binding changes and gateway policy updates are
separate app-wide operations and need their own reviewed rollout.

Create a private workspace for the reviewed commit with the qualification runner:

```sh
python3 scripts/ops/mcp-qualification.py init --dir <evidence> --commit <reviewed-sha>
```

This writes a manifest with all ten rows pending. Run the operator-assisted
provider lifecycle flow against an isolated staging app and a registered OAuth
test client. The provider must register the loopback redirect URI exactly:

```sh
python3 scripts/ops/mcp-qualification.py oauth-flow --dir <evidence> \
  --app <staging-app> --confirm-staging-app <staging-app> \
  --target '<provider / staging app / test-client version>' \
  --issuer https://identity.example.com \
  --jwks-url https://identity.example.com/.well-known/jwks.json \
  --resource 'https://<staging-app>.gregale.dev/mcp' \
  --client-id '<registered-public-client-id>' \
  --redirect-uri http://127.0.0.1:8765/callback \
  --scope mcp:tools --scope mcp:stream --scope offline_access \
  --stream-tool stream_demo --open-browser
```

The runner checks the staging resource metadata advertises the configured
issuer, plus authorization-server discovery, code response support, S256 PKCE
and the configured JWKS. It binds only to `127.0.0.1`, checks OAuth `state`,
exchanges the code with the in-memory verifier, then verifies the access token
against the staging MCP resource. It refreshes the token, checks a
rotated refresh token cannot be replayed when the provider rotates it, revokes
the active refresh token, and requires a later refresh attempt to return
`invalid_grant`. The provider must issue a JWT access token and a refresh token;
request `offline_access` when required by that provider. Confidential clients
may supply `--client-secret-env` with an environment variable name; the secret
is sent only with HTTP Basic token-endpoint authentication.

The authorization URL requests `prompt=consent`; the test user completes the
provider login and approval in the browser. Providers can suppress repeated
consent screens for an already approved client, so use a fresh test-client grant
when the visible consent screen itself is part of acceptance. The runner never
prints or stores the authorization code, access token, refresh token, subject,
or tool result. It writes only normalized stage results. If the provider
requires parameters such as `audience`, pass them explicitly with
`--authorize-param NAME=VALUE`.
ChatGPT and Codex still need their own live-client login observations below.

To qualify a signing-key rotation, rotate the staging issuer key first and add
`--previous-kid <old-key-id> --key-rotation-timeout 300` to the `oauth-flow`
command. The fresh token must use a different key published in the configured
JWKS, and Gregale must accept it. If the gateway still has its old JWKS cached,
the runner retries read-only discovery for up to the configured interval.

Then run the token rejection checks against the same staging app:

```sh
python3 scripts/ops/mcp-qualification.py oauth-probe --dir <evidence> \
  --app <staging-app> --confirm-staging-app <staging-app> \
  --target '<provider issuer / staging app / MCP client version>' \
  --valid-token-env MCP_VALID_TOKEN \
  --expired-token-env MCP_EXPIRED_TOKEN \
  --wrong-audience-token-env MCP_WRONG_AUDIENCE_TOKEN \
  --insufficient-scope-token-env MCP_LOW_SCOPE_TOKEN \
  --stream-tool stream_demo --arguments-file ./safe-probe-arguments.json
```

The four environment variables must hold distinct tokens: a valid token
authorized for the probe tool, an expired token for this resource, a token with
the wrong audience, and a valid token missing the probe tool's required scope.
The probe calls the harmless tool once with the valid token and once with the
under-scoped token; the latter must receive HTTP 403. Expired and wrong-audience
tokens must receive HTTP 401 during discovery. It stores only normalized check
results and status codes, never token values, command output, or tool results.
Choose a progress-capable tool with no external side effects. The matching
`--confirm-staging-app` value is required before any CLI call.

Qualify the local starter runtimes with the pinned official TypeScript SDK. The
runner makes only loopback requests and uses harmless sample inputs:

```sh
npm ci --prefix scripts/ops/mcp-sdk-client --ignore-scripts
gregale mcp init --runtime node --path /tmp/mcp-node
gregale mcp init --runtime go --path /tmp/mcp-go
gregale mcp init --runtime python --path /tmp/mcp-python
```

Start each generated server in its own terminal on the listed port. First
install the Node starter dependencies:

```sh
cd /tmp/mcp-node && npm ci
```

Then run one server per terminal:

```sh
cd /tmp/mcp-node && PORT=18808 node server.js
```

```sh
cd /tmp/mcp-go && PORT=18809 go run .
```

```sh
cd /tmp/mcp-python && PORT=18810 python3 server.py
```

Run the qualification command after all three `/healthz` endpoints respond:

```sh
python3 scripts/ops/mcp-qualification.py sdk-interop --dir <evidence> \
  --target '<reviewed commit / Node, Go and Python starters / official SDK 2.3.1>' \
  --node-url http://127.0.0.1:18808/mcp \
  --go-url http://127.0.0.1:18809/mcp \
  --python-url http://127.0.0.1:18810/mcp
```

The SDK negotiates the pinned modern protocol and the legacy protocol with all
three servers, lists and calls their harmless tools, and checks that advertised
capabilities match the starter. For Node it also reads the sample resource and
template, renders the sample prompt, and verifies streamed progress. The runner
stores only protocol versions, counts, pass statuses and a redacted failure
stage; URLs, response content and tool results are not written to the receipt.

Qualify task recovery across separate worker processes and two replacement
replicas with a disposable PostgreSQL database. This creates and drops one
randomly named schema; it does not alter the starter's configured database
schema or any app deployment. Materialize the Node starter and install its
dependencies first:

```sh
gregale mcp init --runtime node --path /tmp/mcp-node
cd /tmp/mcp-node && npm ci
```

Then run the task qualification with the disposable database URL in the
environment (the URL is never passed as a command-line argument or stored in
the receipt):

```sh
MCP_TASKS_TEST_DATABASE_URL='<disposable PostgreSQL URL>' \
  python3 scripts/ops/mcp-qualification.py tasks-restart --dir <evidence> \
  --starter-dir /tmp/mcp-node --confirm-disposable-database \
  --target '<reviewed commit / Node starter / disposable PostgreSQL>'
```

The runner kills a worker during a task, starts two replacement processes,
checks that exactly one replica recovers the task, rejects the stale lease,
and verifies that only the task owner can read the result. It also verifies
that input resume on the final attempt does not grant another retry. The
receipt contains only pass states, replica/attempt counts, and a redacted
failure stage. Scale-from-zero still requires an observation on the target
hosting platform.

Qualify real staging scale-from-zero behavior with an MCP tool that returns an
asynchronous task and runs long enough for the observer and scheduler to
publish and act on a positive backlog. Link the worker to its separate,
always-on observer app:

```sh
gregale mcp tasks setup --app <staging-worker-app> --observer-app <staging-observer-app> --min 0 --apply
```

The observer must be an active worker app with a live
deployment, a minimum of one replica and a running instance. Only the observer role publishes
`mcp_tasks_observer_heartbeat`; the runner checks that it is fresh and equal to
1 before enqueue and throughout the scale-up and drain observations. The runner
also checks the linked app remains ready throughout. It requires `min=0`, a
fresh observer heartbeat, fresh zero task metrics including the
no-compatible-worker gauge, and zero running worker replicas before it submits
the task. It then polls task status, aggregate metrics and replica counts. The
runner requires no stranded tasks before enqueue, observes a live compatible
worker while the probe task is outstanding, and confirms no stranded tasks
after drain. If the observer app or heartbeat is unhealthy at
any point, qualification fails closed and requests cancellation of its own task.
Task IDs, task results, tool arguments, tokens and CLI output are never written
to the receipt.

```sh
python3 scripts/ops/mcp-qualification.py tasks-scale-zero --dir <evidence> \
  --app <staging-worker-app> --confirm-staging-app <staging-worker-app> \
  --target '<reviewed commit / staging app / MCP task probe version>' \
  --tool qualification_task --arguments-file ./safe-task-arguments.json \
  --token-env MCP_STAGING_TOKEN --timeout 1200
```

Use an isolated staging worker app and harmless probe arguments. The probe
must take long enough for the runner to observe a fresh positive
`mcp_tasks_outstanding` value and at least one running worker at the same time;
the runner rejects a task that finishes too quickly to prove that transition.
After task completion, it waits for a fresh zero metric and for the worker pool
to return to zero. This staging check complements CI's explicit missing/stale
custom-metric scale-in guard.

Then run the native cold boot and park/restore checks against that staging app:

```sh
python3 scripts/ops/mcp-qualification.py probe --dir <evidence> \
  --name native_cold_boot --app <staging-app> --confirm-staging-app <staging-app> \
  --target '<native host and build / test app>' --stream-tool stream_demo
python3 scripts/ops/mcp-qualification.py probe --dir <evidence> \
  --name native_park_restore --app <staging-app> --confirm-staging-app <staging-app> \
  --target '<native host and build / test app>' --stream-tool stream_demo
```

Each probe parks the app, runs `gregale mcp doctor` with the selected harmless
streaming tool, checks the observed wake tier (`cold` or `restored`), parks the
app again, and verifies no active instances remain. It stores only check
statuses and timing summaries. The app is left parked. The matching
`--confirm-staging-app` value is required because the probe parks the app. For
external OAuth, pass `--token-env` with a short-lived test token; the runner
passes only the environment variable name to Gregale and never stores its value.
Use the restore probe only when the app has a usable snapshot, and the cold boot
probe only when a fresh boot is expected.

For the interactive `oauth_chatgpt_login` and `oauth_codex_login`, save a
redacted receipt after the real observation and record its hash:

This writes a manifest with all eight rows pending. After each real observation,
save a redacted receipt under that directory and record its exact hash:

```sh
python3 scripts/ops/mcp-qualification.py record --dir <evidence> \
  --name untouched_lockfile_deploy --target '<test app and deployment>' \
  --artifact untouched-lockfile-deploy.json --status passed
python3 scripts/ops/mcp-qualification.py status --dir <evidence>
```

Repeat `record` for each manual row. Use `--status failed` to preserve a failed
observation; the gate remains closed. The task-restart runner uses local worker
processes and disposable PostgreSQL; it does not restart deployed workers or
change the target app's replica policy.

Repeat `record` for each required row. Use `--status failed` to preserve a failed
observation; the gate remains closed. When a receipt is a native rollout report,
`record --status passed` verifies that it passed with native observations, matches
the manifest commit, carries matching clean source and binary provenance, and
contains the expected binary and plan digests. Other provider/client receipts
remain operator-supplied and need reviewer assessment. The runner does not deploy
apps or contact OAuth providers, clients or hosts.
Do not commit credentials or unredacted customer data.

The manifest has this shape:

```json
{
  "version": 1,
  "commit": "<full reviewed 40-character commit SHA>",
  "checks": [
    {
      "name": "untouched_lockfile_deploy",
      "status": "passed",
      "target": "<test app, native host and build identifiers>",
      "artifact": "untouched-lockfile-deploy.json",
      "sha256": "<SHA-256 of the exact redacted artifact>"
    }
  ]
}
```

Include all ten rows, using relative artifact paths. Run the local release

Include all eight rows, using relative artifact paths. Run the local release
check:

```sh
python3 scripts/ops/mcp-qualification.py check --dir <evidence> --commit <reviewed-sha>
```

The underlying checker is also callable directly:

```sh
python3 scripts/ci/mcp-qualification-check.py <evidence>/manifest.json --commit <reviewed-sha>
```

The MCP contract workflow also accepts `qualification-manifest` and
`reviewed-commit` dispatch inputs to run this gate against supplied evidence.

The gate fails for missing, skipped or failed checks, mismatched source commits,
missing receipts, path escapes and changed artifact digests. It checks evidence
completeness and integrity; a reviewer must assess whether each recorded
observation proves the corresponding native or provider/client behavior. The
2026-10-01 demo evidence remains historical and does not pass this release gate.

### Disposable native Task rollout harness

`scripts/ops/mcp-hosting-rollout-qualification.py` exercises the native release adapter against a dedicated test account. Local contract tests do **not** constitute live qualification. Its reports cover Task durability and release safety; they do not replace the OAuth, official SDK, cold-boot, or park/restore evidence required above. Tasks are seeded through the database store, rather than an authenticated MCP client.

Build the Gregale binary from the reviewed commit. Prepare disposable fixtures:

```sh
python3 scripts/ops/mcp-hosting-rollout-qualification.py prepare \
  --starter cmd/gregale/templates/mcp-node --dir /tmp/mcp-qualification-fixtures
```

Install each fixture's locked npm dependencies before running. Provision separate `mcp-qual-` web, previous-worker, candidate-worker, and observer apps in a dedicated qualification account. The candidate app must have no instances or deployments; worker and observer scaling must have minimum one. Configure remote `DATABASE_URL` secrets with the runtime account for web/workers and the observer account for the observer. All apps share the disposable namespace and owner key. Follow the migration and role-grant instructions above to establish four distinct database accounts in the same schema; grant them their runtime, observer, operator, and migration profiles. The three service accounts must not have schema CREATE.

Supply the runner's environment bindings: `MCP_QUAL_RUNTIME_DATABASE_URL`, `MCP_QUAL_OBSERVER_DATABASE_URL`, `MCP_QUAL_OPERATOR_DATABASE_URL`, `MCP_TASK_MIGRATION_DATABASE_URL`, `MCP_TASK_OWNER_KEY`, and `MCP_TASK_NAMESPACE` (starting with `mcp-qual-`). Configure CLI authentication separately. Do not put secrets in plans or evidence.

Create a native release plan with paths to the prepared `web` and `worker-candidate` directories, one previous worker app, and the observer plus its metric destination. Use `web-bad` for the unhealthy-candidate scenario. Then run:

```sh
python3 scripts/ops/mcp-hosting-rollout-qualification.py run \
  --plan /tmp/qualification-plan.json --state /tmp/qualification-state.json \
  --fixtures /tmp/mcp-qualification-fixtures --binary /tmp/gregale \
  --account DEDICATED_ACCOUNT_ID --commit FULL_REVIEWED_COMMIT_SHA \
  --scenario rollout --bootstrap --evidence /tmp/qualification-rollout.json
```

Run all four scenarios (`rollout`, `interrupt`, `bad-candidate`, `stale-observer`) with fresh app sets, namespaces, journals, and evidence files. `--bootstrap` deploys the old worker, observer, and initial web fixture; omit it only when those fixtures are already deployed. The interrupt scenario terminates the release after its durable web submission checkpoint, resumes the same journal, and compares deployment IDs. If that window cannot be observed, the run fails. Successful rollouts require all three probe Tasks to complete on the candidate's runtime database account. Failure scenarios require rejection at the readiness gate, unchanged web traffic, and a running previous worker.

Before account access, the runner verifies that the local source checkout is clean at the requested commit and that `go version -m` reports the same revision with `vcs.modified=false` for the supplied binary. It records those provenance fields and the binary and plan digests in each report. Review the report's native observations, database accounts, initial Task states, and completion/preservation evidence. Reports contain fixed probe metadata and IDs, never database URLs or Task payloads. Missing bindings produce a **blocked** report. Go build metadata provides a consistency check, not a signed attestation.

The fixture handlers deliberately keep work running or retrying. The stale-observer scenario parks its observer. Cleanup is manual: retain evidence and journals, then cancel remaining probe Tasks and remove the disposable apps, namespace data, and database accounts. Never deploy these fixtures into customer applications.

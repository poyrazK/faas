# Management MCP native acceptance

This harness exercises customer management tools through Codex's real MCP client,
without model requests. It is separate from hosted MCP app qualification.
Run it on a dedicated **nonproduction native x86_64 Linux KVM host** with the
normal Gregale control plane and native builder microVMs. Having `/dev/kvm`
alone does not establish that a host is native; the operator must verify the host
is bare metal. The runner requires readable/writable KVM and explicit execution
opt-in. It does not create apps, change configuration, probe customer endpoints,
restart daemons, or clean up resources.

Use dedicated preconfigured apps with live predecessors: a request app with a
connected GitHub repository, a service with readiness and drain policy, and a
worker with an isolated test queue and deterministic queue observer. Configure a
customer CLI profile for the nonproduction account. Keep API credentials in the
profile or supported environment variables, never in scenario files.

Copy a template from `scripts/ops/fixtures/management-mcp/`, replace every
placeholder, and review all immutable IDs, source commits, image digests and
expected results before execution. Templates are starting points, not a claim
that an arbitrary application satisfies the qualification criteria.

```sh
python3 scripts/ops/management-mcp-native-acceptance.py scenario.json --validate-only
python3 scripts/ops/management-mcp-native-acceptance.py scenario.json \
  --execute-nonproduction --profile native-acceptance \
  --gregale-binary /absolute/path/gregale --codex-binary /absolute/path/codex
```

The scenario pins account, app and scope; named environments additionally pin
project and environment IDs. Every plan must match these identities before a
write runs. Writes can only reference compatible plans from previous steps.
`{"ref":"step.path.to.field"}` substitutes a previous tool result. `expect`
contains exact field assertions. Read steps may poll for up to 1,800 seconds;
writes cannot poll. `replay` lists fields that must stay identical when the same
arguments are submitted again, such as `deployment.id`. No retries create a new
plan automatically. A failed assertion exits without cleanup: inspect native
history before retrying to avoid starting a second rollout.

The report includes step names, tools and assertion paths, without tool payloads,
credentials, logs or source contents. Save stdout as qualification evidence.
A passing report qualifies only the scenario assertions, not the whole feature.

Use `service-source.json` and `worker-source.json` for commit-pinned native
source activation. Supply an explicit matching `rollout_mode`, and check both
`deployment.id` and `deployment.build_id` on same-plan replay.

Acceptance evidence should include:

- Source: exact reviewed commit built by builderd, live artifact at zero traffic,
  same-plan replay returning the same deployment; separately promote and observe
  passive health before claiming serving readiness.
- Service: live candidate and completed native handoff, then exact predecessor
  drainage and gateway acknowledgements in native rollout evidence. The example's
  live/not-pending assertion alone does not prove those additional observations.
  Test service abort with `gregale_plan_service_abort` and
  `gregale_apply_service_abort` while a deliberately delayed candidate remains
  active; pin both candidate and predecessor IDs.
- Worker: selected generation converged and retired resident generations gone.
  Independently verify queue delivery/acknowledgements; resident convergence does
  not prove queue processing or desired replica counts. To restore a worker,
  plan a workload rollout with `retained_deployment_id` under current settings.
  For a retained GitHub release, use `service-source-rollback.json` or
  `worker-source-rollback.json`; verify the reviewed retained commit and new
  build/deployment IDs. Independently qualify the rebuilt artifact, since mutable
  build dependencies can change the output.
- Request rollback: exact retained deployment, native rollback completion and
  serving health. Adjust assertions for the native response and retained release.

Protected browser approval and encrypted bridge recovery have operator-assisted
remote scenarios: `protected-recovery.json`, `protected-expiry.json`, and
`worker-recovery.json`. Use a dedicated protected **test** environment, not the
production environment. The bridge must enable writes and use its existing
private encrypted state and independent key file. Log Codex into this bridge
before running, with read/write consent for the exact source and target
workloads. Supply that same client profile on reconnect:

```sh
python3 scripts/ops/management-mcp-native-acceptance.py protected-recovery.json \
  --execute-nonproduction --remote-url https://mcp.example/mcp \
  --codex-config-dir /private/acceptance-codex-profile \
  --codex-binary /absolute/path/codex
```

The environment scenario pins account, project, both environment IDs and the
**complete exact** `changes` array returned by a reviewed promotion plan. Copy
all fields, including workload/configuration hashes, from the nonproduction
preview into the scenario. If any workload is added or any identity changes,
the runner stops before requesting approval. Approval tokens cannot be supplied
in scenario arguments.

At `human_review`, the runner displays the public review link only on stderr,
checks its HTTPS origin against `review_origin`, and waits for the human. Open
it in a signed-in browser and approve the exact promotion. Enter `continue` at
the terminal only when ready; the following same-plan apply verifies the actual
approval result. For the expiry scenario, leave the review **unapproved**, wait
until its returned expiry has passed, then continue promptly while the plan is
still valid. It must return `approval_expired` after restart and on replay.
This is an expiry check, not a browser decline-button test.

At `restart_bridge`, the runner closes its Codex connection, asks the operator
to restart the dedicated bridge with the same state/key files, then reconnects
using the same OAuth profile. Scenario results stay in runner memory: later
steps reuse the original plan and compare the original review URL or deployment/
promotion receipt. Operator acknowledgements are reported separately from tool
assertions and do not prove a process restarted; attach bridge service restart
records to the live report. No journal files, approval codes or credentials are
modified by the runner. Keep stderr private because it contains review links.

Local MCP contract tests additionally inject an HTTP rejection, approval expiry,
and a committed write followed by a lost response. They reopen encrypted state
and check that refused/expired approvals never write or renew review, retries
keep the same key/body, and the successful receipt survives another restart.
These controlled faults are not evidence of native host qualification. Use the
remote OAuth client acceptance harness for refresh/revocation checks as well.

Local runner checks:

```sh
python3 scripts/ops/management-mcp-native-acceptance_test.py
```

# Versioned Run tasks and Agent continuation

The [functional KVM report](report.json) passed on the selected
`gregale-internal-test-1` node in `gregale-prod`. It covers the exact source
manifest `ecba7df18ffabcbae8c6675927e7cd82da8e60a8ef94f8004b1311dd249349eb`
(55,457 files), based on `e74c7cfc6e5b9cff5596d7f430a4d5e0c6bbfab1`.

The actual Node 24 task produced one Run and one durable RAP inbox record.
The journey published an immutable task, admitted it while schedd was stopped,
retired its version, disabled task writers and restarted apid. Replaying the
same tool call recovered the original Run. After scheduler restart the Run
returned a valid structured result and a stored artifact. The fresh reader
fetched both through the task-specific routes. Another apid restart and replay
kept one inbox record. The pre-teardown and initial/final physical leakchecks
found no leaked netns, taps, jails, cgroups, processes, bridges, mounts or native
loops. The guest ran as UID 1000 with the cold-boot witness recorded.

This is functional evidence on an operator-selected Google KVM host, with an
explicit test-only stock-runtime scan fixture. It does not qualify a production
runtime release. Customer model invocation and a deployed customer agent are
outside this VM lane; the stateless starter's two-slice, lost-acknowledgement and
redelivery flow is tested separately. App ownership and endpoint delegation are
covered by the API/agent/state contracts.

The [local check record](local-checks.json) covers the Go SDK task retry contract,
9 Node SDK tests, 9 Python SDK tests, 8 stateless starter tests, 18 acceptance
verdict tests and the API, SDK coverage, sealing, migration, environment and
test-policy checks. Legacy admission contracts passed in the local
template-database lane. The node's isolated-schema fixture cannot exercise the
legacy billing triggers that explicitly reference the public schema; its task
contracts use the existing acceptance database without changing database roles.

Selected contract tests passed across six Linux packages in the
[portable validation record](portable-validation.json). The
[Linux lint record](linux-lint.json) reports zero issues across the task domain,
state, API, agent, CLI, admission, SDK coverage and metal acceptance packages.
It retains the first combined lint attempt's package-loading timeout before the
successful serial retry. The [qualification attempts](attempts.json) preserve
the output-budget and API/CLI integration failures that preceded the passing
source snapshot.

Runtime, test and CI files match the tested manifest. Subsequent edits only
record qualification results in the ADR, roadmap and evidence documents.

New writers remain off. Apply the additive task migration, deploy compatible
readers and completion workers, then enable the task gate with the existing
Run API/V2 gates. Pending completions, reads, retirement and replay survive
turning task writers off. No production flags were changed.

Raw logs and generated artifacts remain private on the node under
`/mnt/e2e-scratch/run-lifecycle-tasks-20261115-preview3`. Public evidence removes
machine and boot identifiers and excludes signed callback URLs, credentials,
source payloads and raw daemon logs. The first failed attempt's binaries were
archived with exhaustive byte/mode verification before its generated staging
space was reused; no shared Go cache was cleared.

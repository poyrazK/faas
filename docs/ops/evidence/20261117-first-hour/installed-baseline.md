# First-hour baseline

Scripted CLI outcome: **blocked**.

| Provenance | Value |
|---|---|
| target | https://api.gregale.dev |
| cli_version | gregale v0.1.18-rc.347 |
| release_verify_available | False |
| harness_revision | 676100529dcc13b8d4296504d57cc4aa0faefedb |
| harness_worktree_dirty | True |
| cli_sha256 | f660575b3f233e27eab9579a01df21b7a052f617a9477394d8c4e5cdefd7fb70 |
| plan | scale |
| evidence_kind | deployed |
| started_at | 2026-11-17T07:53:29Z |

## Acceptance coverage

| Lane | Evidence |
|---|---|
| install_login | not_measured |
| interactive_start | not_measured |
| scripted_cli | blocked |
| agent_tasks | not_measured |

## Scripted journey

| Step | Result | Seconds |
|---|---|---|
| first_private_response | not_run | — |
| anonymous_request_denied | not_run | — |
| edit_and_interrupt_wait | not_run | — |
| resume_same_release_and_verify_edit | not_run | — |
| failed_startup_is_diagnosable | not_run | — |
| repair_and_verify | not_run | — |
| cleanup | not_run | — |

## Timings

| Measurement | Value |
|---|---|
| first_hour_first_verified_response_s | not measured |
| first_hour_second_verified_deployment_s | not measured |
| first_hour_resume_to_verified_response_s | not measured |
| first_hour_failure_to_recovery_s | not measured |
| first_hour_complete_s | not measured |
| first_hour_interruptions | not measured |

## Coding agents

| Task | Agent / toolset | Passed / runs | Median seconds | Median tool calls | Follow-ups |
|---|---|---|---|---|---|
| first-hour-private | — / — | 0 / 0 | — | — | — |
| first-hour-edit | — / — | 0 / 0 | — | — | — |
| fix-failed-deploy | — / — | 0 / 0 | — | — | — |

Agent records excluded for missing or mismatched target/CLI checksum: 0.

## Interpretation

- Scripted timings start after authentication and exclude human thinking time.
- No interactive install/login or gregale start acceptance has been recorded.
- One run is a baseline observation, not a percentile or reliability estimate.
- Disposable-app releases use immediate rollouts; default safe-rollout latency is not measured.
- Agent follow-up counts measure harness assistance, not all possible human intervention.
- Account plan is recorded; a Scale run does not qualify Free-plan behavior.

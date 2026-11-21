# First-hour baseline

Scripted CLI outcome: **passed**.

| Provenance | Value |
|---|---|
| target | https://api.gregale.dev |
| cli_version | gregale first-hour-candidate |
| release_verify_available | True |
| harness_revision | 676100529dcc13b8d4296504d57cc4aa0faefedb |
| harness_worktree_dirty | True |
| cli_sha256 | ab9d9ef7ea7f4828841dd45d7f9dcbb2e39c6e9be1a383b82b540fc5b727a8ad |
| plan | scale |
| evidence_kind | deployed |
| started_at | 2026-11-17T07:50:49Z |

## Acceptance coverage

| Lane | Evidence |
|---|---|
| install_login | not_measured |
| interactive_start | not_measured |
| scripted_cli | passed |
| agent_tasks | not_measured |

## Scripted journey

| Step | Result | Seconds |
|---|---|---|
| first_private_response | passed | 201.29 |
| anonymous_request_denied | passed | 0.61 |
| edit_and_interrupt_wait | passed | 4.56 |
| resume_same_release_and_verify_edit | passed | 173.5 |
| failed_startup_is_diagnosable | passed | 194.64 |
| repair_and_verify | passed | 174.25 |
| cleanup | passed | 1 |

## Timings

| Measurement | Value |
|---|---|
| first_hour_first_verified_response_s | 201.287 |
| first_hour_second_verified_deployment_s | 379.953 |
| first_hour_resume_to_verified_response_s | 173.501 |
| first_hour_failure_to_recovery_s | 368.889 |
| first_hour_complete_s | 748.846 |
| first_hour_interruptions | 1.0 |

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

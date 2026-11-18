# First-hour baseline

Scripted CLI outcome: **passed**.

| Provenance | Value |
|---|---|
| target | https://api.gregale.dev |
| cli_version | gregale v0.1.18-rc.349 |
| release_verify_available | True |
| harness_revision | a545bc148803f3f989b00666c86c8a46110dfdfd |
| harness_worktree_dirty | True |
| cli_sha256 | 7a1597ffc8dc91ba76ca4f8478c12cb84fb608da9b513c1881b9c12d7daf12c1 |
| plan | scale |
| evidence_kind | deployed |
| started_at | 2026-11-17T16:55:27Z |

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
| first_private_response | passed | 138.56 |
| anonymous_request_denied | passed | 0.89 |
| edit_and_interrupt_wait | passed | 4.18 |
| resume_same_release_and_verify_edit | passed | 125.49 |
| failed_startup_is_diagnosable | passed | 167 |
| repair_and_verify | passed | 173.9 |
| cleanup | passed | 1.99 |

## Timings

| Measurement | Value |
|---|---|
| first_hour_first_verified_response_s | 138.56 |
| first_hour_second_verified_deployment_s | 269.123 |
| first_hour_resume_to_verified_response_s | 125.488 |
| first_hour_failure_to_recovery_s | 340.899 |
| first_hour_complete_s | 610.025 |
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

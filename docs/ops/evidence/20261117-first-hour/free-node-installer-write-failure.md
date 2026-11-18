# Free Node fresh-onboarding acceptance

Outcome: **failed**.

Reason: `onboarding_step_failed`.

| Phase | Result | Seconds |
|---|---|---|
| isolated_environment | passed | 0.005 |
| published_install | failed | 2.962 |
| fresh_browser_login | not_run | — |
| free_account_preflight | not_run | — |
| start_node | not_run | — |
| edit_same_app | not_run | — |
| cleanup_app | not_run | — |
| logout | not_run | — |

| Provenance | Value |
|---|---|
| evidence_kind | deployed |
| target | https://api.gregale.dev |
| plan | not_measured |
| interaction | scripted_pty_and_browser |
| started_at | 2026-11-17T18:57:39Z |

Terminal input is scripted through a real PTY; browser authorization uses the normal login flow.
The installer uses its default version selection and an isolated destination; the harness adds that bin to its child PATH.
Browser account signup/password entry, shell startup-file integration, other runtimes/platforms, and agent success remain unmeasured.
Customer timing includes install and CLI browser authorization through the verified edit; cleanup is required and timed separately.
One run is an observation, not a latency percentile or reliability estimate.

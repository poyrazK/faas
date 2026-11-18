# Free Node fresh-onboarding acceptance

Outcome: **failed**.

Reason: `onboarding_step_failed`.

| Phase | Result | Seconds |
|---|---|---|
| isolated_environment | passed | 0.001 |
| published_install | passed | 42.869 |
| fresh_browser_login | failed | 305.706 |
| free_account_preflight | not_run | — |
| start_node | not_run | — |
| edit_same_app | not_run | — |
| cleanup_app | not_run | — |
| logout | passed | 1.435 |

| Provenance | Value |
|---|---|
| evidence_kind | deployed |
| target | https://api.gregale.dev |
| plan | not_measured |
| interaction | scripted_pty_and_browser |
| started_at | 2026-11-17T18:46:56Z |
| cli_version | v0.1.18-rc.349 |
| cli_sha256 | 7a1597ffc8dc91ba76ca4f8478c12cb84fb608da9b513c1881b9c12d7daf12c1 |
| harness_revision | d2260cb2c9d49dd2a0a755ff0c64578c1ffcadf6 |
| harness_worktree_dirty | False |
| release_tag | v0.1.18-rc.349 |
| release_url | https://github.com/poyrazK/faas/releases/tag/v0.1.18-rc.349 |
| published_at | 2026-11-17T12:11:51Z |
| platform | darwin/arm64 |
| archive | gregale_0.1.18-rc.349_darwin_arm64.tar.gz |
| archive_url | https://github.com/poyrazK/faas/releases/download/v0.1.18-rc.349/gregale_0.1.18-rc.349_darwin_arm64.tar.gz |
| checksum_manifest_sha256 | 35c0280d016a59d1e305a1af8afeb88cad3200b267b8fef4d92206a3b7c8c006 |
| expected_archive_sha256 | 7b10c2c34b2f93110c8965a252bc95ff8b2030b658e1411299353465f4625c78 |
| archive_sha256 | 7b10c2c34b2f93110c8965a252bc95ff8b2030b658e1411299353465f4625c78 |
| checksum_verified | True |
| cli_source_sha | 37b55bddef0ce36d22b524c9270b290ecd529e81 |
| release_verify_available | True |
| status | passed |
| installer_url | https://get.gregale.dev |
| installer_sha256 | c45e00f965c0cef6ef0b028bdcfa406cce554c4755d36996b63bb95af5d88334 |
| installer_cli_matches_archive | True |
| install_s | 42.799 |

Terminal input is scripted through a real PTY; browser authorization uses the normal login flow.
The installer uses its default version selection and an isolated destination; the harness adds that bin to its child PATH.
Browser account signup/password entry, shell startup-file integration, other runtimes/platforms, and agent success remain unmeasured.
Customer timing includes install and CLI browser authorization through the verified edit; cleanup is required and timed separately.
One run is an observation, not a latency percentile or reliability estimate.

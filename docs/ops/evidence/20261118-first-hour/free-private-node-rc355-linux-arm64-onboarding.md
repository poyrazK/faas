# Free Node fresh-onboarding acceptance

Outcome: **failed**.

Reason: `onboarding_step_failed`.

| Phase | Result | Seconds |
|---|---|---|
| isolated_environment | passed | 0.01 |
| published_install | passed | 21.142 |
| fresh_browser_login | passed | 106.118 |
| free_account_preflight | passed | 1.477 |
| start_node | passed | 67.168 |
| edit_same_app | failed | 0.999 |
| edited_release_rollout | not_run | — |
| cleanup_app | passed | 2.866 |
| logout | passed | 0.519 |

| Provenance | Value |
|---|---|
| evidence_kind | deployed |
| target | https://api.gregale.dev |
| plan | free |
| template | hello-node-private |
| browser_flow | device |
| credential_storage | isolated_file |
| interaction | scripted_pty_and_browser |
| started_at | 2026-11-18T16:41:14Z |
| cli_version | v0.1.18-rc.355 |
| cli_sha256 | c631aebc4f36d90240bf5e828e7263366ab1281d23a9def464626ecee4068d18 |
| harness_revision | 1885028a2a0a1e9b055be02f9fe1cd22f0f12941 |
| harness_worktree_dirty | False |
| release_tag | v0.1.18-rc.355 |
| release_url | https://github.com/poyrazK/faas/releases/tag/v0.1.18-rc.355 |
| platform | linux/arm64 |
| archive | gregale_0.1.18-rc.355_linux_arm64.tar.gz |
| archive_url | https://github.com/poyrazK/faas/releases/download/v0.1.18-rc.355/gregale_0.1.18-rc.355_linux_arm64.tar.gz |
| checksum_manifest_sha256 | e5e7749c5ba6c8da69b0776dc2f224aa3f9ccaef228f352d06d8888bf9714dec |
| expected_archive_sha256 | 0e899e4bc4e2aeae3a3766cd90d48ca21036f89042c0672cf435497f765cb987 |
| archive_sha256 | 0e899e4bc4e2aeae3a3766cd90d48ca21036f89042c0672cf435497f765cb987 |
| checksum_verified | True |
| source_lookup | public_git_tag |
| cli_source_sha | eed7aa6c66d7d7d1b0f1dc710f1b4c0d5f8f4f68 |
| release_verify_available | True |
| status | passed |
| installer_url | https://get.gregale.dev |
| installer_sha256 | c45e00f965c0cef6ef0b028bdcfa406cce554c4755d36996b63bb95af5d88334 |
| installer_cli_matches_archive | True |
| private_starter_available | True |
| install_s | 20.919 |
| browser_login_s | 106.012 |
| first_response_from_start_s | 195.92 |

Terminal input is scripted through a real PTY; browser authorization uses the normal login flow.
The installer uses its default version selection and an isolated destination; the harness adds that bin to its child PATH.
Browser account signup/password entry, shell startup-file integration, other runtimes/platforms, and agent success remain unmeasured.
Customer timing includes install and CLI browser authorization through the verified edit; cleanup is required and timed separately.
One run is an observation, not a latency percentile or reliability estimate.

Private verification checks both exact releases through the owner account and requires anonymous HTTP 404.
The edited release's rollout to 100% traffic is checked and timed after the verified response.

The default public installer delivered the starter in a clean Debian 12
ARM64 VM. The first release returned HTTP 200 through the owner account and
HTTP 404 anonymously. During the edit, Start rejected the checkout link
created by the first project deploy and submitted no second release. Cleanup
verified that the temporary app and project were gone and revoked the
isolated CLI login.

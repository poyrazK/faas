# GA capability coverage

Every GA row of `docs/ga-capability-inventory.md`, mapped to the journeys
that test it end to end against a deployed Gregale, and to what is not
covered. Update this table when a journey or a GA row changes.

| # | GA capability | Journeys and steps | Not covered |
|---|---|---|---|
| 1 | Apps: create, update, rename, delete, restore | `TestAppSettingsAndRename` (update, rename), `TestDeleteAndRestore`, every `deployProbe` | — |
| 2 | Source deploys: local tree, tarball, Dockerfile, resumable uploads | `deployProbe` (local tree), `TestDockerfileTarballAndSecretReload` (tarball + Dockerfile) | resumable upload resume after an interrupted upload |
| 3 | Builds in builder microVMs, build logs, cache | every source deploy (cache hits reported), `TestBuildsAndProvenance` (list, status, provenance, SBOM, build log) | — |
| 4 | Container image deploys, image preflight | `TestImageRegistryAndDeployTokens/image_deploy`, `TestImagePreflight` | image-published deployments (`registry published`) need a project workload (preview) |
| 5 | GitHub: install, push-to-deploy, Checks status | `TestGitHubDeploys` (bind, push_to_deploy, checks_status) | App installation itself (done once for the fixture); push-to-deploy is a known failure (no deployment on push, 2026-10-27) |
| 9 | Starter templates | `TestTemplateDeploys`: the 11 templates that need no backing service | templates needing Postgres, object storage, AI keys, Upstash (cron-worker), event subscriptions (event-worker), queues (queue-worker) or MCP hosting |
| 11 | `gregale.yaml` deploy overrides | `TestDeployOverrides` (port, start, health path) | — |
| 13 | Per-app deploy tokens | `TestImageRegistryAndDeployTokens` (API: list, mint, deploy through the API and the CLI, token reaches nothing else, revoke, revoked token refused) | `gregale app deploy-token` (added in #5794): the suite runs the released CLI, so it mints through the API until a CLI release carries the command; `gregale deploy` with a deploy token is a known failure (insufficient_scope) |
| 15 | Private registry credentials | `TestImageRegistryAndDeployTokens/registry_credentials` | pulling a real private image |
| 16 | Stateless-contract checks and source review | `TestGitHubDeploys/source_review` | stateless advisories |
| 24 | Rollback to a named revision | `TestReleaseAndRollback` (checked rollback) | — |
| 27 | Pull-request previews | `TestGitHubDeploys`: `pull_request_preview` (preview serves the PR, production unchanged), `preview_created_on_pr_open`, `preview_removed_on_pr_close`, `preview_destroy` | automatic creation on PR open and removal on PR close are known failures (GitHub App webhooks look unprocessed, 2026-10-27/28); the lifecycle falls back to `preview create` and `preview destroy` |
| 38 | Scale to zero | `TestScaleToZero`, `TestGuestGuarantees` (burst wake, wake latency) | — |
| 39 | Browser wake page | `TestBrowserWakePage` (shown when a wake exceeds 1.5 s; never for fetch) | forcing a wake slower than 1.5 s |
| 40 | Firecracker isolation | `TestGuestGuarantees` (egress denylist, RNG and clock after restore) | jailer/cgroup internals (metal tests) |
| 41 | Function runtimes | `TestTemplateDeploys` (Node 22/24, Python 3.12/3.13, Go, Rust) | alpine Go variant |
| 42 | Manual park / wake, prewarm | `TestScaleToZero` (park, wake --wait) | prewarm (no CLI command) |
| 43 | Autoscaling and min instances | `TestScaleToZero/min_instances`, `TestResourcesAndAutoscaling` (target RPS/CPU, max instances) | scheduled scaling; scale-out under load |
| 46 | Instances: list, restart | `TestScaleToZero` (ps, fresh restart) | — |
| 47 | Health probes and auto-restart | `TestHealthGate`, `TestDeployOverrides` (health path) | liveness restarts, restart policy, stop signal |
| 49 | Resource sizing | `TestResourcesAndAutoscaling` (RAM, CPU, effective limits) | scratch disk, disk I/O profile, workload classes |
| 60 | Env vars | `TestEnvLifecycle` (push, deliver, pull, diff, history, rollback), `TestSecrets/env_history` | environment clone (preview projects) |
| 61 | Secrets | `TestSecrets`, `TestDockerfileTarballAndSecretReload` (hot reload without restart, ACK, revocation) | secret references (project environments) |
| 63 | Custom domains with managed TLS | `TestCustomDomainBinding` (add, pending, records, remove) | DNS verification, TLS issuance, serving (no test DNS zone). On 2026-10-27 production refused `domains add` ("not available on this Gregale installation"), so the step is a known failure |
| 64 | Streaming, SSE, WebSockets, gRPC | `TestServeAndObserve/server_sent_events`, `TestWebSockets` | gRPC |
| 77 | App access control | `TestMaintenanceAndAccess` (require-authn, basic auth, IP allowlist) | public bearer auth |
| 92 | Crons, fire-now | `TestCronDelivery` | scheduled (non-manual) fires |
| 118 | Logs, tail, archive | `TestServeAndObserve` (runtime and HTTP logs), `TestTailAnalyticsAndMetrics/tail`, `TestLogArchive` | HTTP request logs while the production debugger (ADR-127) is not released for the account: the step skips on the CLI's explanation |
| 121 | Metrics, request analytics | `TestServeAndObserve/metrics`, `TestTailAnalyticsAndMetrics` | service metrics |
| 136 | Wake timeline | `TestWakeTimelineAndCosts/wake_timeline` | — |
| 138 | Audit log | `TestAccountAndKeys/audit_log_records_key` | — |
| 140 | Per-app costs | `TestWakeTimelineAndCosts/app_costs` | cost trend |
| 141 | Public status page and incidents | `TestPublicSurface` (status API, page, incidents API) | — |
| 142 | Signup, login, MFA, reset, verification, CLI login | the CLI login every journey uses | signup, password and magic-link login, MFA, reset, verification (no disposable accounts) |
| 143 | Customer notification emails | — | all (no test inbox) |
| 144 | API keys with scopes | `TestAccountAndKeys` | — |
| 151 | Plans, usage, invoices, overage cap, costs, forecast | `TestBillingAndUsage` (read-only views) | changing plan or overage cap (would bill the account), FOCUS export |
| 153 | GDPR export, account deletion and restore, DPA | `TestAccountExportAndDPA` (export, status, DPA) | deletion and restore (no disposable accounts) |
| 156 | `gregale` CLI | every journey, `TestCLITools` (version, context, doctor, completion, man, help) | interactive `start` and `menu` |
| 157 | Web dashboard | — | all (dashboard journeys not enabled) |
| 159 | Public OpenAPI spec and capabilities endpoint | `TestPublicSurface/openapi_spec`, `TestAccountAndKeys/capabilities` | — |
| 161 | Docs site and trust pages | `TestDocsPages` (every route not marked `unpublished` in `docs/customer-pages.json`), `TestPublicSurface/security_txt` | pages of preview and internal capabilities, which are deliberately unpublished |

40 of the 42 GA rows have at least one journey; 143 and 157 need a test
inbox and a browser. The "Not covered" column lists the remaining gaps.

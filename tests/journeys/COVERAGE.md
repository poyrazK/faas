# GA capability coverage

Every GA row of `docs/ga-capability-inventory.md`, mapped to the journeys
that test it end to end against a deployed Gregale, and to what is not
covered. Update this table when a journey or a GA row changes.

| # | GA capability | Journeys and steps | Not covered |
|---|---|---|---|
| 1 | Apps: create, update, rename, delete, restore | `TestAppSettingsAndRename` (update, rename), `TestDeleteAndRestore`, every `deployProbe` | — |
| 2 | Source deploys: local tree, tarball, Dockerfile, resumable uploads | `deployProbe` (local tree), `TestDockerfileTarballAndSecretReload` (tarball + Dockerfile), `TestPythonSourceDeploys` (Python requirements.txt and `uv run` apps on the shared Python runner base) | resumable upload resume after an interrupted upload |
| 3 | Builds in builder microVMs, build logs, cache | every source deploy (cache hits reported), `TestBuildsAndProvenance` (list, status, provenance, SBOM, build log) | — |
| 4 | Container image deploys, image preflight | `TestImageRegistryAndDeployTokens/image_deploy`, `TestImagePreflight` | image-published deployments (`registry published`) need a project workload (preview) |
| 5 | GitHub: install, push-to-deploy, Checks status | `TestGitHubDeploys` (bind, push_to_deploy, checks_status) | App installation itself (done once for the fixture); push-to-deploy is a known failure (no deployment on push, 2026-10-27) |
| 9 | Starter templates | `TestTemplateDeploys`: the 11 templates that need no backing service | templates needing Postgres, object storage, AI keys, Upstash (cron-worker), event subscriptions (event-worker), queues (queue-worker) or MCP hosting |
| 11 | `gregale.yaml` deploy overrides | `TestDeployOverrides` (port, start, health path) | — |
| 13 | Per-app deploy tokens | `TestImageRegistryAndDeployTokens` (API: list, mint, deploy through the API and the CLI, token reaches nothing else, revoke, revoked token refused) | `gregale app deploy-token` (added in #5794): the suite runs the released CLI, so it mints through the API until a CLI release carries the command; `gregale deploy` with a deploy token is a known failure (insufficient_scope) |
| 15 | Private registry credentials | `TestImageRegistryAndDeployTokens/registry_credentials` | pulling a real private image |
| 16 | Stateless-contract checks and source review | `TestGitHubDeploys/source_review` | stateless advisories |
| 19 | Projects: multi-app apply, submissions, project previews | `TestMultiServiceProject` (`deploy_project`: a Compose repository as one project; `project_lists_services`) | submissions, project previews, `--only`/`--exclude`, renames |
| 24 | Rollback to a named revision | `TestReleaseAndRollback` (checked rollback) | — |
| 27 | Pull-request previews | `TestGitHubDeploys`: `pull_request_preview` (preview serves the PR, production unchanged), `preview_created_on_pr_open`, `preview_removed_on_pr_close` (the close is recorded with its 24h grace), `preview_destroy` | automatic creation on PR open and the recorded close are known failures until #5862 is released (the App webhook URL and secret were wrong until 2026-10-29, and githubd rejected real pull_request payloads); the lifecycle falls back to `preview create` and `preview destroy` |
| 38 | Scale to zero | `TestScaleToZero`, `TestGuestGuarantees` (burst wake, wake latency) | — |
| 39 | Browser wake page | `TestBrowserWakePage` (shown when a wake exceeds 1.5 s; never for fetch) | forcing a wake slower than 1.5 s |
| 40 | Firecracker isolation | `TestGuestGuarantees` (egress denylist, RNG and clock after restore) | jailer/cgroup internals (metal tests) |
| 41 | Function runtimes | `TestTemplateDeploys` (Node 22/24, Python 3.12/3.13, Go, Rust) | alpine Go variant |
| 42 | Manual park / wake, prewarm | `TestScaleToZero` (park, wake --wait) | prewarm (no CLI command) |
| 43 | Autoscaling and min instances | `TestScaleToZero/min_instances`, `TestResourcesAndAutoscaling` (target RPS/CPU, max instances) | scheduled scaling; scale-out under load |
| 44 | Service wake-ahead | `TestMultiServiceProject/wake_ahead_on_by_default` | that a caller's cold wake restores its dependencies (not observable from outside) |
| 46 | Instances: list, restart | `TestScaleToZero` (ps, fresh restart) | — |
| 47 | Health probes and auto-restart | `TestHealthGate`, `TestDeployOverrides` (health path) | liveness restarts, restart policy, stop signal |
| 49 | Resource sizing | `TestResourcesAndAutoscaling` (RAM, CPU, effective limits) | scratch disk, disk I/O profile, workload classes |
| 60 | Env vars | `TestEnvLifecycle` (push, deliver, pull, diff, history, rollback), `TestSecrets/env_history` | environment clone (preview projects) |
| 61 | Secrets | `TestSecrets`, `TestDockerfileTarballAndSecretReload` (hot reload without restart, ACK, revocation) | secret references (project environments) |
| 63 | Custom domains with managed TLS | `TestCustomDomainBinding` (add, pending, records, remove) | DNS verification, TLS issuance, serving (no test DNS zone). On 2026-10-27 production refused `domains add` ("not available on this Gregale installation"), so the step is a known failure |
| 64 | Streaming, SSE, WebSockets, gRPC | `TestServeAndObserve/server_sent_events`, `TestWebSockets` | gRPC |
| 65 | Internal services (call apps by name) | `TestMultiServiceProject`: `api_has_no_public_url`, `call_by_internal_name` (`<service>.internal`), `call_over_private_https` (`https://<service>.internal`), `parked_service_wakes_on_call` | raw TCP services; the HTTPS step skips where the installation has not enabled private service HTTPS, as on production on 2026-10-29 |
| 66 | Service bindings, verification and probes | `TestMultiServiceProject`: `call_by_injected_url` (`GREGALE_SERVICE_<NAME>_URL` from `depends_on`), `explain_authorization` (`gregale app binding service explain`), `declared_policy_allows_the_declared_call`, `declared_policy_refuses_an_undeclared_call`, `allowlist_refuses_other_callers` | call scopes, `gregale bindings verify`, project previews (need a multi-service GitHub fixture) |
| 77 | App access control | `TestMaintenanceAndAccess` (require-authn, basic auth, IP allowlist) | public bearer auth |
| 92 | Crons, fire-now | `TestCronDelivery`: `manual_run` (followed to `succeeded`), `scheduled_fire`, `trigger_inventory` (refuses `--kind cron`) | command crons (preview) |
| 109 | Managed Postgres: create, bind, point-in-time restore, delete, recover, purge | `TestManagedPostgres`: `postgres_available` (provisioning open, region, database limit), `create_database`, `deploy_app` (`testdata/postgres-probe`, a dependency-free PostgreSQL client), `attach_database` (binding ready, fresh restart), `binding_verify`, `write_and_read` (rows through the delivered URL), `restore_point_in_time` (a point between two writes, read through a second binding; skipped when the plan allows one database), `purge_restored`, `scheduled_delete` (undo window about 7 days, name stays taken), `recover` (rows back), `purge` | resize and compute policy (beta), organization databases (beta), credential rotation, `gregale.yaml` databases and `add postgres`, the pooler's transaction-mode limits, compute and storage caps (would need a month's usage), read-only and migration access |
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

45 of the 47 GA rows have at least one journey; 143 and 157 need a test
inbox and a browser. The "Not covered" column lists the remaining gaps.

## GA candidates

Rows not yet GA whose journeys exist ahead of a promotion, so the
evidence is in place before the tier changes. A step whose capability is
hidden for the account (`gregale capabilities`) skips, so the evidence
needs the capability released to the test account first; on 2026-10-29
custom metric history, SLOs and synthetic checks were hidden for it.

| # | Capability (tier today) | Journeys and steps | Not covered |
|---|---|---|---|
| 25 | Progressive rollouts: canary ladder, traffic split, automatic rollback on 5xx (beta) | `TestTrafficSplitAndRollback`: `stage_v2_without_traffic` (`deploy --no-traffic` on a parked app), `production_wakes_v1_while_staged`, `staged_preview_serves_v2`, `split_50_50` (`release traffic set`, both weights in `release traffic status`), `split_serves_both`, `version_key_keeps_one_revision` (`Gregale-Version-Key`), `promote_refuses_changed_serving` (`--if-serving` while split: `traffic_serving_changed`), `promote_v2`, `rollback_on_5xx` (`--rollback-on-5xx` under the `immediate` release policy: a v3 answering 5xx rolls back to v2; skips below Pro); the default safe ladder in `TestSafeReleaseZeroDowntime` | `fast` and custom ladders, a gate holding or aborting a canary, `canary advance`, scheduled promotion (`--at`), version-affinity cookies |
| 74 | Edge rules, rule lists (beta) | `TestEdgeRules` (redirect, response header and its update, IP refusal covering HEAD, a list-keyed rule and the list's edit, history and rollback, delete; each change checked on 12 requests across the edge gateways as soon as the CLI returns, #2683); `TestEdgeTrafficPolicies`: `route` (a path of one app served by a second app; a known failure until #6148), `throttle` (429 `rate_limited` with `X-RouteRateLimit-Limit`, at most 3 of 10 admitted at burst 2), `budget` (503 `request_budget_exceeded` at `budget_ms` 1000), `total_deadline` (the same at `total_deadline_ms` 1500 under a 10 s execution budget) | the other kinds, throttle `key_by` and `count_statuses`, a client budget override header, route rules on custom domains, `gregale.yaml` routing and trace (covered by `TestE2E_EdgeRules_*` in `cmd/e2e`), WAF and challenge |
| 108 | Object storage: buckets, signed URLs, S3 gateway, multipart uploads (preview) | `TestObjectStorage`: `object_storage_enabled`, `create_bucket` (`add bucket` on an app made with `deploy --create-only`), `list_bucket`, `retried_create_keeps_one_bucket` (CLI and API), `signed_url_unicode_key`, `signed_url_empty_object`, AWS SDK for Go v2 on `s3.gregale.dev` with an API-minted credential (`s3_put_get`, `s3_multipart_upload` (5.5 MiB in two parts), `s3_copy_object`, `s3_list_objects` (paged), `s3_delete_object`), `other_tenant_credential_denied`, `forged_signature_denied`, `unsigned_request_denied` (list, read, write), `delete_non_empty_bucket_refused` (binding kept), `empty_bucket`, `revoked_credential_denied`, `delete_bucket` | versioning, Object Lock, retention and legal holds, lifecycle rules, change notifications, CORS rules, encryption defaults and keys: all still preview. Also public buckets, upload routes, API-key bucket grants, an app reading its injected `GREGALE_S3_*` settings, `gregale bucket upload/download`, cross-bucket copy, and flexible checksums (the SDK sends them only where required). The suite runs as one account, so a second app's same-named bucket stands in for another account's credential. Unlike the other journeys here, it does not skip while the capability is unavailable: `object_storage_enabled` is a known failure (`known_failures.json`) and the other steps report blocked |
| 112 | Realtime channels: resume, tickets, access, occupancy (preview) | `TestRealtime`, the app as its own OIDC issuer (ES256 tokens the journey signs; the probe serves the JWKS and answers callbacks with the callback token): `realtime_enabled`, `deploy_issuer_app`, `jwks_served`, `create_endpoint` (`realtime create`, callback token on stdin), `v1_connect`, `unsigned_handshake_refused` (no token, another key's token), `connection_listed` (`realtime connections --principal`), `server_subscribe`, `live_publish_delivered`, `direct_send_delivered`, `close_connection`; on the resumable protocol `resumable_protocol_offered`, `subscribe_with_presence`, `retained_publish_delivered` (API, `delivery=retained`), `retained_replayed_to_late_subscriber` (`after: 0`), `presence_seen_both_ways` (snapshot, joined, updated), `occupancy_counts_both` (API and live counts), `authorize_callback_refusal`, `subscriptions_per_connection_limit` (ninth refused), `presence_left_on_close`, `ticket_read_only_and_revoked`, `usage_reports_connections`, `delete_endpoint` (sockets close, handshake refused) | SSE subscriptions, consumer groups and workers, inbox, push, schedules, snapshots and reducers, principal access controls, realtime function triggers, static bearer rotation, drains, channel rules and `gregale.yaml` endpoints, Gregale Auth sign-in, the account and per-node connection limits (they need hundreds of sockets). Like `TestObjectStorage`, it does not skip while realtime is unavailable: `realtime_enabled` and `resumable_protocol_offered` are known failures (`known_failures.json`) until the e2e account is released, and the steps after them report blocked |
| 122 | Custom metrics and OTLP metric ingest (beta) | `TestCustomMetricsSLOsAndSynthetics`: `custom_metric` (push and read back through the API), `otlp_metrics` (OTLP/HTTP JSON gauge, name normalised) | OTLP protobuf exports; counters; custom metric history (`gregale metrics --custom`, internal); OTLP needs the custom metric history operator switch |
| 125 | Alerts and notification channels (beta) | `TestAlertsAndChannels`: `rule_fires` (request_count rule), `webhook_verified` (the probe's `/hook` answers 200 only for a valid signature), `channel_notified` (the fire is recorded on a Slack channel), `channel_test_send` | real Slack, PagerDuty and email deliveries (no test workspace or inbox: the channel targets a Slack webhook Slack refuses); alert presets; rollback actions; incidents |
| 128 | SLOs and error budgets (preview) | `TestCustomMetricsSLOsAndSynthetics/slo` (create, status) | attainment, budget and burn numbers (hourly rollups; a fresh app has none); SLO alerts |
| 129 | Synthetic checks (preview) | `TestCustomMetricsSLOsAndSynthetics/synthetic_check` (create, first run passes, delete) | 24 h and 7 d uptime; failing runs; synthetic check alerts |
| 168 | On-demand profile captures (preview) | `TestProfileCaptures`: `capture` (CPU and heap of the running probe while requests keep it busy; ready, both views, CPU not empty), `capture_listed`, `pprof_download` (merged gzip pprof per kind), `capture_diff` (a second heap capture against the first), `delete` (the capture is gone), `slow_request_captured` (the probe's `/slow` held past 10 s yields an automatic `slow_request` capture). It skips while `profile-captures` is hidden for the account or the installation has on-demand profiling off | automatic memory-pressure and CPU-saturation captures and OOM linking (`cmd/e2e` metal tests cover the first and the last), Go block and mutex profiles and `pprof_port` (the probe is Node), Node stuck-thread stacks, opting out with `profiling.automatic_captures`, the dashboard capture page, `profile.capture_ready` webhooks |

## Regression journeys

`TestBazaarRegressions` replays the bugs that three rounds of deploying a
multi-service project (bazaar) by hand found, one subtest per area, each
check naming the change that fixed it (#5934, #6001, #6060): an
unpublished Compose service created internal with its `gregale.yaml`
workflows (rows 19, 65); a burst over a throttle rule answered 429, never
503, and a cache hit sending each `X-RateLimit-*` header once (row 74); an
alias whose revision a rollback superseded answering 409 (row 24); a job
that exits at once keeping its output, its command given after `--`; the
standalone default RAM; and `metrics --services` explaining a hidden
service map. A check fails on a release cut before its fix.

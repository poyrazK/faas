#!/usr/bin/env python3
"""Check that every customer entry point maps to a row of docs/ga-capability-inventory.md.

Entry points are enumerated from the code, not from the catalog:
routes, CLI commands, gregale.yaml keys, plan limits, dashboard pages, docs,
OpenAPI tags, daemons, platform flags, feature env flags, catalog ids, Go SDK
files, database tables and Go packages.

Each identifier must match a rule. A rule targets either an inventory row
(by a unique substring of its Capability cell) or an explicit non-capability
class (INFRA, TOOLING, TEST). Anything unmatched is printed: it is either a new
capability or a missing rule. The check passes only with zero unmatched, no
inventory row without an entry point, and no empty source (a collector that
finds nothing after a file moves would otherwise pass silently).

ci-light runs it as `make capability-coverage-check` (ADR-20261023115741).

Usage: python3 -I scripts/ga/capability_coverage.py [--show SOURCE]
"""
import glob
import json
import os
import re
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
INVENTORY = os.path.join(ROOT, "docs", "ga-capability-inventory.md")

# Ordered: the first matching rule wins, so specific rules come first.
# Target: inventory row key (unique substring of the Capability cell), or
# "INFRA" / "TOOLING" / "TEST" for identifiers that are not customer capabilities.
RULES = [
    # --- test fixtures and generated noise
    (r"^/(a|b|boom|echo|counter|dial|h1|users|untrusted|not-net-http|etc/faas/uuid\.txt|contract|probe-contract|identity|signal|requests|last-capture|price|seal|token|grpc|api|internal/?|ingest|control|control/ack|push\.v1\.pusherservice/push|openapi\.json|\{\$\}|/?|\+route\+|`\+route\+`)$", "TEST"),
    (r"test|fixture|fake|example|scenario-restart|rehearsal-native|qualif|conformance|smoke|e2e|bench|eval$|dryrun|leakcheck", "TOOLING"),
    # --- attribution fixes: specific names that broader keywords would misfile
    (r"scheduled-release", "Scheduled releases"),
    (r"release-review", "Release review"),
    (r"image-run|interactive-run", "Image runs"),
    (r"wake-page", "Browser wake page"),
    (r"session-affinity", "Session affinity"),
    (r"synthetic", "Synthetic checks"),
    (r"app-cost|cost-trend|costs-overview", "Per-app costs"),
    (r"async-route|execution-mode", "Async routes at the edge"),
    (r"frozen|thaw", "Frozen tier"),
    (r"^/orders|^/users|^/v1\\?api|^/v1/apps\\$", "TEST"),
    (r"^apps/diff", "Affected-workload preview"),
    (r"^apps/env", "Env vars:"),
    (r"^apps/product", "Product control"),
    (r"^apps/security", "Signed deploys"),
    (r"open-api-doc", "OpenAPI:"),
    (r"web-socket", "Streaming, SSE"),
    # log search, custom dashboards and unified incidents (merged from main 2026-10-24)
    (r"log-search|log-events|log-saved-search|log-projection|logsearch", "Logs, tail"),
    (r"^/v1/dashboards|custom-dashboard|^dashboards$", "App, service and account metrics"),
    (r"^/v1/incidents|^incidents?$|^incident-notes$|^incidents-enabled$", "Alerts, notification channels"),
    # traffic enforcement guarantees (ADR-570, merged from main 2026-10-25):
    # request deadlines are kind=budget edge rules, host and target policy
    # keep route rules' internal apps private
    (r"^(managed|response|total)-deadline$|^(edge-target|public-host)-policy$", "Edge rules"),
    # --- deploy env and gateway internals (round 4)
    (r"^dev-patch-delivery$", "Developer environments"),
    (r"^notifications-unsubscribe-url$", "Customer notification emails"),
    (r"^statuspage-path$", "Public status page"),
    (r"^dns-provider$|hetzner-dns|dns01|^dns-provider-", "Custom domains"),
    (r"^allowlist$|hostname-label|surface-parser", "Custom domains"),
    (r"content-observer", "Route advisor"),
    (r"edge-answers|informational-response|response-trailers|forwardproxy|proxy-raw|proxy-stub|portnorm|origdst", "Streaming, SSE"),
    (r"preauth-limit", "Edge protection"),
    (r"authenticated|internal-svc-auth", "App access control"),
    (r"service-discovery|service-proxy|internal-proxy", "Internal services"),
    (r"retained-service-spans|spans-", "Tracing:"),
    (r"response-status-hooks", "Throttle suggestions"),
    (r"^warmup$", "Warm pool"),
    (r"^(apps-root|artifact-sync-.*|compute-mem-mb|control-plane-api-target|cp-state-backup-.*|curl|drill-.*|edge-discovery-url|git-sha|h2-?c-.*|ha-.*|host-(age-pub|age-recipient-path|bridge-cidr|hmac-key-path|key-path|name)|internal-target|json|masquerade-cidr|origin-ip|output|private-ingress-cidrs|public-(control-addr|edge-control-plane-target|edge-mode|health-path|host|iface|listen-addr|private-listen-addr)|quiet-pause|rclone-bin|repo-root|require-shared-artifacts|sample-interval|sign-pub|token|trusted-ingress-cidrs)$", "INFRA"),
    (r"^(control|dns-handoff|edge-health-outcome|handler|inflight|pgbackend|rule-label-set|stale-target|workload-retirement)$", "INFRA"),
    (r"^/v1/apps/\\$|^/v1\?api.key", "TEST"),
    (r"^app-layer-max", "Builds in builder"),
    (r"^rate-limit-(rps|burst)$", "Edge rules (incl"),
    # --- round-4 triage: specific identifiers seen unmatched
    (r"^/(checkout\\?|generate=\d+\\|orders/\{id\}|users/4f8a|\.\.\.|app\.css|compile)$|^/v1\?api-key|^/v1/users|^/v1/apps/\{slug\}\\$|^$", "TEST"),
    (r"^/compile$|livequery", "Live queries"),
    (r"^/connections|data/connections|data-connection", "Data pipelines"),
    (r"/v1/hooks/", "Durable inbound webhooks"),
    (r"/v1/status|status-incident", "Public status page"),
    (r"/v1/synthesize|synth", "INFRA"),
    (r"/v1/internal/(apps|services)/", "INFRA"),
    (r"/v1/apps/\{slug\}/diff|^diff$", "Affected-workload preview"),
    (r"apps/\{slug\}/env/|app-envs?$", "Env vars:"),
    (r"health/history", "App health assessment"),
    (r"policy/status", "Plans, usage, invoices"),
    (r"apps/\{slug\}/product|apps/ /product", "Product control"),
    (r"apps/\{slug\}/security|apps/ /security", "Signed deploys"),
    (r"grype|vuln|secret-scan|show-scan|scanview", "Deploy-time vulnerability scan"),
    (r"focus", "Plans, usage, invoices"),
    (r"release-command|release-config\.|^release$", "Release commands"),
    (r"after-restore|before-checkpoint|lifecycle-config\.(after|before)", "Snapshot lifecycle hooks"),
    (r"scaling-schedule|scaling-target|scaling-config|worker-scale-spec", "Autoscaling and min instances"),
    (r"amqp|rabbitmq|kafka-config|nats-config|redis-streams-config|sqs-compat-config|sasl-config|tls-config\.|filter-criteria|filter-clause|retry-policy-config|function-bucket-event|function-webhook|^trigger\.|event-trigger", "Triggers and event sources"),
    (r"path-route|async-route\.", "Edge rules (incl"),
    (r"dev-watch|dev-config|devpatch|developer-sync|dev-watch-settings", "Developer environments"),
    (r"queue-binding\.|queue-config\.", "Queues:"),
    (r"work-policy\.|workpolicy|event-routing-retry", "Events:"),
    (r"operation-workflow|^operation\.", "Operations:"),
    (r"exclusive-operation-(policy|binding)|exclusive-operations-config", "Managed exclusive operations"),
    (r"companion-spec|extension-dependency", "Application companions"),
    (r"database-dependency", "Managed Postgres"),
    (r"bucket-dependency", "Object storage"),
    (r"function-spec|function-config", "Function runtimes"),
    (r"worker-spec", "Worker pools"),
    (r"lifecycle-config|healthcheckproto", "Health probes"),
    (r"^manifest\.|release-config", "`gregale.yaml` deploy overrides"),
    (r"app-errors?|error-request", "Issues:"),
    (r"^keys-max$", "API keys"),
    (r"max-response-body|request-body-max|response-write-timeout|websocket", "Streaming, SSE"),
    (r"open-apidoc|openapi-doc", "OpenAPI:"),
    (r"^rammb$|^ram-mb$|default-max-retries", "Resource sizing"),
    (r"app-changes", "Change timeline"),
    (r"faas-implementation-spec|faas-ux-spec|ga-capability-inventory|^status$|^capabilities$", "TOOLING"),
    (r"^quickstart$", "Docs site and trust pages"),
    (r"^security$|httpsec|hsts", "Docs site and trust pages"),
    (r"^internal$|^observability$", "INFRA"),
    (r"^runs$", "Disposable isolated runs"),
    (r"^add$", "`gregale` CLI"),
    (r"^connect$", "GitHub:"),
    (r"^run$", "Disposable isolated runs"),
    (r"platform-flag-groups", "Operator admin"),
    (r"atomic-service-additions", "Projects:"),
    # --- merged from main 2026-10-24
    (r"^multi-service-apps$", "Projects:"),
    (r"penalty-box|attack-mode|ai-crawler|crawler-ip-ranges|crawler-ranges", "Edge protection"),
    (r"^quiet-pause-watermarks$", "Instances:"),
    (r"^(honeytokens|bridgeproc)$", "INFRA"),
    (r"auth-rpc|rekey|standby-warmup|service-capacity-policy|service-recovery|worker-recovery|production-lifecycle-review|app-service-address-cursor|warm-hint|capdecl|renderer|role-templating|safetext|cursor|^data$|dispatch|hostsize|authcode|cosign|daemonenv|daemonunit|cpstate|drills|grafana|pki|guestmemproto|resumetimingproto|loglevel|logsanitize|lruutil|jsonschemautil|httpjson|middleware|nodeclaim|nodejoin|nodereport|promql|redact|crashcrypt|crashcapturewire|markers-internal", "INFRA"),
    (r"^declarative-response-caching$", "Declarative response caching"),
    (r"crash-capture", "Crash snapshots"),
    (r"app-config-(guard|revision)", "`gregale.yaml` deploy overrides"),
    (r"actioncompare|behaviorcompare|rehearsal", "Scenario tests"),
    (r"apptask", "App tasks"),
    (r"apphealth", "App health assessment"),
    (r"appstandards", "Org application standards"),
    (r"authsettings", "Application Auth"),
    (r"^authz$", "Orgs:"),
    (r"oauthmetadata|oauthpkce", "Gregale management MCP server"),
    (r"^browser$|whycopy", "`gregale` CLI"),
    (r"edgevalidate", "Edge rules (incl"),
    (r"^focus$", "Plans, usage, invoices"),
    (r"gitfetch|reposcan", "Repo decomposition"),
    (r"hostport", "TCP listeners"),
    (r"^markers$", "Framework profiles"),
    (r"simpleapp", "Apps: create"),
    (r"presetwhy", "Alerts, notification channels"),
    (r"internalsvc", "App access control"),
    (r"serviceproxy", "Service bindings"),
    (r"publicstatus", "Public status page"),
    (r"securitytxt", "Docs site and trust pages"),
    (r"reservedip", "Static egress IP"),
    # --- operator / internal systems
    (r"internal-?ci|ci-runner|ciruntime|^ci-|/ci/|faas-ci-|^ci$|ci-required|ci-general|github-checks", "Internal CI"),
    (r"internal/hosting|hosting-|internal-frontend|^sites$|^hosting|frontend-hosting|site-", "Internal frontend hosting"),
    (r"internal-?mail|maild|mailctl|mail-feedback|mail-metrics|mail-report|productemail|product-email", "Internal mail service"),
    (r"product-control|productcontrol|product_control|/product/|^product$|product-customers", "Product control"),
    (r"agent-run|agent-execution|^agent|agents", "Gregale Agents"),
    (r"oci/registry|^/v2/?$|registry-storage|ociregistry", "Platform OCI registry"),
    (r"^/v1/admin|^admin|gregalectl|compute-nodes|operator|break-glass|abuse|^/admin|deployctl|nodes?-|fleet|cluster-signing", "Operator admin"),
    (r"data-repair|datarepair|repair", "Selective data repair"),
    (r"data-pipeline|datapipeline|^pipeline|pipelines|connectioncapacity", "Data pipelines"),
    (r"commit-relay|commit-source|^commit|gregale-commit|/commit", "Gregale Commit"),
    (r"exclusive-operation|exclusive_operation|managed-exclusive|^exclusive", "Managed exclusive operations"),
    # --- account, billing, identity
    (r"api-monetization|consumer-billing|consumer-plan|consumer-signup|rate-card|billing-run|consumer-overdue|consumer-credit|consumer-meter|consumer-route-usage|api-consumer-(billing|credit|meter|plan|rate|route|signup|overdue)|lagoinvoice|automatic-tax|billing-connection", "API monetization"),
    (r"consumers?|consumer-keys|api-consumer|end-user", "API consumers"),
    (r"platform-tenant", "Platform tenants"),
    (r"tenant-surface|tenant-hostname|wildcard", "Tenant surfaces"),
    (r"financial-budget|budget|financial", "Budgets:"),
    (r"billing|invoice|plan$|^plan|pricing|upgrade|overage|usage|meter|paddle|stripe|polar|credit|price|included-gb|subscription|dunning|payment|cost", "Plans, usage, invoices"),
    (r"gdpr|account/export|account-export|dpa|subprocessor|deletion|account-restore|deleted-account", "GDPR export"),
    (r"application-auth|gotrue|/v1/auth/|^auth$|auth-admin|auth-sdk|^/auth/|application_auth|end-user-auth", "Application Auth"),
    (r"application-standard|productstandards|^standards|org-standard", "Org application standards"),
    (r"orgs?$|^/v1/orgs|org-|organization|members|invitation|seat|transfer-ownership|iam-|roles?$|ownership", "Orgs:"),
    (r"keys?$|api-key|^/v1/keys|scopes|service-caller-key|key-pool", "API keys"),
    (r"^mail$|mail|email|notification-email|magic-link|postmark|resend", "Customer notification emails"),
    (r"signup|login|logout|mfa|password|session|cli-auth|email-verif|verify|whoami|account|oauth-links|link$|unlink", "Signup, login"),
    (r"feature-flag|customer-flags|^flags|flags-|/flags|platformflags|capflags|platform-flags|feature-flags", "Customer feature flags"),
    # --- developer tools
    (r"mcp-management|mcpmanagement|mcp-serve|oauth-authorization|oauth-protected|/oauth/(authorize|consent|register|revoke|token)|well-known/oauth|jwks", "Gregale management MCP server"),
    (r"security\.txt|security/|pgp|docs-links|customer-pages|docs-content|^/docs|subprocessor-md|^docs$|security-advisories", "Docs site and trust pages"),
    (r"openapi\.(json|yaml)|^/v1/openapi|capabilities|productcap|sdk-coverage|^meta$", "Public OpenAPI spec"),
    (r"^sdk|sdk$|webhook-receiver|idempotency|transport|^client|options$|dto$|codes$|errors$|scopes$|^doc$|streaming$|release-context|^context", "SDKs:"),
    (r"dashboard|templates?/|^index$|^app\.(css|js)|\.js$|^stateless$|email-verification-banner|email-verified|set-password|password-reset|^login$|^profile$", "Web dashboard"),
    (r"^gregale$|^cli|cli-|completion|^man$|^menu$|^start$|^open$|^config$|^version$|doctor|^context|profiles?-config|^init$", "`gregale` CLI"),
    # --- observability
    (r"status-page|^/status|public-status|^status$|status/slo", "Public status page"),
    (r"audit", "Audit log"),
    (r"operational-summary", "Operational summary"),
    (r"app-cost|cost-trend|costs-overview", "Per-app costs"),
    (r"wake-timeline|wakes$", "Wake timeline"),
    (r"app-health|/health$|health-reason|^health$", "App health assessment"),
    (r"debugger|debug-telemetry|request-evidence|safe-replay|otel-spans|spans-writer", "Production debugger"),
    (r"debug|regression", "Debug:"),
    (r"change-timeline|/changes|^changes", "Change timeline"),
    (r"service-map|servicemap", "Service map"),
    (r"profiling|profiled|pyroscope|profile-push", "Continuous profiling"),
    (r"profile|attribution|request-mix", "Traffic profiles"),
    (r"synthetic", "Synthetic checks"),
    (r"slo", "SLOs and error budgets"),
    (r"issue|errors?$|/errors|linear|slack|issues-grouping|error-group|issuetracker", "Issues:"),
    (r"anomaly|baseline-alert", "Anomaly"),
    (r"alert|notification-channel|channels$|pagerduty|obsrule", "Alerts, notification channels"),
    (r"trace|tracing|otlp.*trace|otel/v1/traces", "Tracing:"),
    (r"custom-metric-history", "Custom metric history"),
    (r"custom-metric|otlp|metrics-write", "Custom metrics and OTLP"),
    (r"datadog", "Datadog integration"),
    (r"log-drain|drain-analytics|log-analytics", "Log drains"),
    (r"logs?$|^log-|log-archive|tail|logarchive|/logs", "Logs, tail"),
    (r"metrics|analytics|request-telemetry|telemetry", "App, service and account metrics"),
    # --- data
    (r"ai-gateway|ai-access|aigateway|^ai$|llm|moderation|prompt", "AI gateway"),
    (r"outbound|integration", "Outbound integrations"),
    (r"live-?quer|livequery|/live$|\{database\}/live", "Live queries"),
    (r"realtime", "Realtime channels"),
    (r"entit|durable-entity|__gregale/entities", "Durable entities"),
    (r"data-api|dataapi|postgres-pool|pgpool", "Data API"),
    (r"postgres|database|^pg-|managedpostgres|managed-postgres|/databases|pgbouncer", "Managed Postgres"),
    (r"bucket|object|s3|upload-route|storage", "Object storage"),
    # --- async
    (r"inbound-webhook", "Durable inbound webhooks"),
    (r"webhook|account-release-webhook|delivery-format", "Outbound webhooks"),
    (r"automation", "Automations:"),
    (r"operation|business-(invariant|decision|effect|compensation)|customer-operation", "Operations:"),
    (r"inbox|outbox|messaging|application-message|send$|deliver$", "Application inbox and outbox"),
    (r"worker-pool|^workers$|worker-replica", "Worker pools"),
    (r"queue|dlq|dead-letter|lease|sqs", "Queues:"),
    (r"event|work-polic|acknowledg|consumer-processing|fanout|esm|ack$", "Events:"),
    (r"trigger|kafka|nats|redis-stream|broker", "Triggers and event sources"),
    (r"invocation|invoke|async-result|async-invoke", "Async invocations"),
    (r"delayed", "Delayed tasks"),
    (r"workflow|recovery-polic|recovery-review", "Workflows:"),
    (r"cron", "Crons, fire-now"),
    (r"job", "Jobs, including"),
    # --- edge and networking
    (r"route-source-impact|routeimpact|source-impact", "Route source impact"),
    (r"route-lifecycle|route-removal|deprecat|sunset", "Route lifecycle review"),
    (r"route-advisor|routeadvisor|advise", "Route advisor"),
    (r"route-health", "Critical route health"),
    (r"route-monitor|route-budget|routemonitor|production-route", "Production route budgets"),
    (r"route-requirement|route-polic|saved-route|requirements", "Route requirements"),
    (r"openapi|api-contract|contract-diff|apihostingcontract|deploydiff-openapi", "OpenAPI:"),
    (r"route-priorit|throttle", "Throttle suggestions"),
    (r"discovered-route|route|automatic-route-check", "Discovered routes"),
    (r"session-affinity|affinity", "Session affinity"),
    (r"wake-page", "Browser wake page"),
    (r"cache|purge", "Declarative response caching"),
    (r"async-route|execution-mode", "Async routes at the edge"),
    (r"upstream", "Data upstream discovery"),
    (r"cors", "CORS presets"),
    (r"edge-protection|pre-auth|challenge|waf|bot", "Edge protection"),
    (r"edge-rule|edgerule|geo|asn|jwt|rewrite|redirect", "Edge rules (incl"),
    (r"require-authn|public-auth|appaccess|app-access|authn|ip-allowlist", "App access control"),
    (r"retry", "Gateway request retry"),
    (r"circuit", "Circuit breaking"),
    (r"static-egress|reserved-ip|egress-ip", "Static egress IP"),
    (r"egress-allowlist|egress-extra|egress", "Egress allowlist"),
    (r"data-placement|data-upstream", "Data upstream discovery"),
    (r"tcp|host-port", "TCP listeners"),
    (r"udp", "UDP listeners"),
    (r"network|peering|overlay|vxlan", "Private networks"),
    (r"private-app|private-forward", "Private apps"),
    (r"binding", "Service bindings"),
    (r"service-mesh|internal-service|\.svc|caller", "Internal services"),
    (r"stream|grpc|websocket|sse|protocol", "Streaming, SSE"),
    (r"domain|certificate|tls|acme|cert", "Custom domains"),
    # --- runtime
    (r"workload-identity|oidc", "Workload identity"),
    (r"secret|seal", "Secrets:"),
    (r"env-diff|^env|environment-variable|env-var|/env$", "Env vars:"),
    (r"chaos|scenario|rehearse", "Scenario tests"),
    (r"dev-session|developer-environment|developer-app|developer-lease|/dev/sessions|^dev$|gregale-dev", "Developer environments"),
    (r"dev-bridge|bridged|/dev/bridges|devbridge", "Dev Bridge"),
    (r"crash-snapshot|crashsnapshot", "Crash snapshots"),
    (r"fork", "Production forks"),
    (r"mcp", "MCP hosting"),
    (r"execution|disposable|image-run|interactive-run", "Disposable isolated runs"),
    (r"companion|sidecar|extension", "Application companions"),
    (r"reserved-concurrency|eviction-priority", "Reserved concurrency"),
    (r"warm-pool|warm-snapshot|warmpool", "Warm pool"),
    (r"scratch|disk-io|cpu-millicore|workload-class|vcpu|ram-mb|cpu-weight|cpu-quota|cpu-period|replica|node-sizing|resource-shape", "Resource sizing"),
    (r"probe|liveness|readiness|startup|restart-policy|stop-signal|grace|drain", "Health probes"),
    (r"runtime-config|instance|restart|^ps$", "Instances: list"),
    (r"frozen|thaw|tier", "Frozen tier"),
    (r"wake-ahead", "Service wake-ahead"),
    (r"autoscal|scale-up|scaling|min-instance|max-instance|max-concurrency|concurrency", "Autoscaling and min instances"),
    (r"prewarm|park|wake", "Manual park / wake"),
    (r"runner|runtime|function|node22|node24|python3|go124|binary", "Function runtimes"),
    (r"firecracker|jailer|isolation|vmm", "Firecracker isolation"),
    (r"scale-to-zero|idle|cold-wake|cold-boot|reaper", "Scale to zero"),
    # --- releases
    (r"alert-rollback|rollback-on", "Alert-triggered rollbacks"),
    (r"mirror|traffic", "Traffic mirroring"),
    (r"deployment-staging|cron-staging|staging", "Deployment staging"),
    (r"release-review", "Release review"),
    (r"scheduled-release", "Scheduled releases"),
    (r"freeze", "Deploy freezes"),
    (r"approval", "Release approvals"),
    (r"release-gate|gate", "Release gates"),
    (r"preview", "Pull-request previews"),
    (r"alias", "Deployment aliases"),
    (r"canary|rollout|progressive|safe-release|safedeploy|traffic-split|promotion", "Progressive rollouts"),
    (r"rollback", "Rollback to a named revision"),
    # --- deploy and build
    (r"queue-control|reorder|deploy-immediately|queued-deploy|cancel-ops|reorder-ops", "Deploy queue controls"),
    (r"image-run", "Image runs"),
    (r"app-task|^tasks$|/tasks|app-exec", "App tasks"),
    (r"environment|gitops|field-ownership", "Environments and Environment GitOps"),
    (r"project|submission|release-set", "Projects:"),
    (r"affected|deploydiff|^diff$|^scan$", "Affected-workload preview"),
    (r"stateless|source-review|unreviewed", "Stateless-contract checks"),
    (r"registry", "Private registry credentials"),
    (r"trusted-signer|signer|require-signed|signature-enforcement|fleet-signing", "Signed deploys"),
    (r"deploy-token", "Per-app deploy tokens"),
    (r"source-ref", "Source-ref deploys"),
    (r"manifest|gregalemanifest|config$|deploy-override", "`gregale.yaml` deploy overrides"),
    (r"framework|profile-catalog", "Framework profiles"),
    (r"template", "Starter templates"),
    (r"decompos", "Repo decomposition"),
    (r"migration-(verif|cutover)|project-migration|service-migration|preflight|migrate-from", "Service migration"),
    (r"gitlab", "GitLab connect"),
    (r"github|install", "GitHub:"),
    (r"oci|image|container", "Container / OCI image"),
    (r"build|railpack|buildkit", "Builds in builder"),
    (r"upload|tarball|source|deploy", "Source deploys:"),
    (r"^/v1/apps$|^apps?$|rename|restore|deleted-app|apps-list|apps-new|app-detail", "Apps: create"),
]

# Checked before everything else, for every source: names a broad rule or the
# infra list would misfile.
PRIORITY_RULES = [
    (r"^docs-and-trust$", "Docs site and trust pages"),
    (r"^ai-(content-policy|request-replay)$", "AI gateway"),
    (r"^security-headers$", "Edge rules (incl"),
    (r"^signed-urls$", "Object storage"),
    (r"^test$|^scenario-tests$|^scenario-(test|object|postgres)", "Scenario tests"),
    (r"^(project-)?environment-(workload-)?qualification|^project-environment-qualifications$", "Environments and Environment GitOps"),
    (r"automation-test", "Automations:"),
    (r"realtime-customer-examples", "Realtime channels"),
    (r"^durableentity$", "Durable entities"),
    (r"^appmetrics$", "App, service and account metrics"),
    (r"^otlpmetrics$", "Custom metrics and OTLP"),
    (r"^apislogs$", "Logs, tail"),
    (r"^routestatus$", "Discovered routes"),
    (r"^s3gateway$|^s3-gatewayd$", "Object storage"),
    (r"^apihosting(contract|receipt|scorecard)$|^api-hosting-(contract|scorecard)$", "Framework profiles"),
    (r"^mirror-(dispatch|redact)$", "Traffic mirroring"),
    (r"^api-discovery-enabled$", "OpenAPI:"),
    (r"^statefuldenylist$", "Stateless-contract checks"),
    (r"^observability$", "Log drains"),
]

# Platform internals: not customer capabilities. Classified explicitly so nothing is ignored silently.
INFRA_RULES = [
    r"^(state|statetest|wire|db|migrate|migration-new|schema-dump|goose|sqlc|pgx|api|apid|apidserver|httpkit|durable|receivers|object$)",
    r"sched|schedd|vmmd|fcvm|netns|rootfs|imaged|meterd|gateway$|gatewayd|snapshot|base-|layer-|nft|denylist|hostage|jail|cgroup|kernel|rng|reseed|guest-init|init-helper|vsock|^sdunit$",
    r"lock|lease-|fence|leader|ledger|journal|outbox-reconcil|receipt|idempotency-key|retention|gc$|janitor|reconcil|watchdog|spool|backpressure|ratelimit|rate-limit|quota|admission|eviction",
    r"release-artifact|releasebundle|releaseinstall|release-compat|unit-|deploy-order|version-skew|pricing-md|standards-md|capabilities-md|obs-rule|denylist-md|runtime-qualification|api-hosting-(scorecard|contract)",
    r"metric|prometheus|promauto|otel|slog|log$|telemetry-internal|healthz|livez|readyz|^/metrics",
    r"^target-(hydration|placement|publication)$|^public-routing-(burst|capacity)$",
]


def kebab(s):
    s = re.sub(r"([a-z0-9])([A-Z])", r"\1-\2", s)
    s = re.sub(r"([A-Z]+)([A-Z][a-z])", r"\1-\2", s)
    return s.replace("_", "-").lower()


def go_sources():
    """Yield the text of every non-test Go file under cmd/, pkg/ and guest/."""
    for top in ("cmd", "pkg", "guest"):
        for dirpath, dirnames, filenames in os.walk(os.path.join(ROOT, top)):
            dirnames[:] = [d for d in dirnames if d not in ("testdata", "node_modules", "vendor")]
            for name in filenames:
                if name.endswith(".go") and not name.endswith("_test.go"):
                    with open(os.path.join(dirpath, name), errors="ignore") as f:
                        yield f.read()


def collect():
    src = {}
    routes = set()
    envs = set()
    for text in go_sources():
        envs |= set(re.findall(r'"((?:FAAS|GREGALE)_[A-Z0-9_]*(?:ENABLED|_PREVIEW|_DISPATCH|_TIER|_FAST_PATH))"', text))
        for m in re.findall(r'(?:HandleFunc|Handle)\(\s*"([^"]+)"', text):
            routes.add(re.sub(r"^(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS) ", "", m))
        for m in re.findall(r'"(?:GET|POST|PUT|PATCH|DELETE|HEAD) (/[^" ]*)"', text):
            routes.add(m)
    src["routes"] = routes
    cli = open(os.path.join(ROOT, "cmd/gregale/cli_meta.go")).read()
    src["cli"] = set(re.findall(r'\n\t\{\n\t\tName:\s*"([^"]+)"', cli))
    keys = set()
    for path in glob.glob(os.path.join(ROOT, "pkg/gregalemanifest/*.go")):
        if path.endswith("_test.go"):
            continue
        for name, body in re.findall(r"type ([A-Z][A-Za-z0-9]+) struct \{(.*?)\n\}", open(path).read(), re.S):
            for key in re.findall(r'yaml:"([a-z_]+)', body):
                keys.add(f"{name}.{key}")
    src["manifest_keys"] = keys
    lim = open(os.path.join(ROOT, "pkg/api/limits.go")).read()
    body = re.search(r"type Limits struct \{(.*?)\n\}", lim, re.S).group(1)
    src["plan_limits"] = set(re.findall(r"^\t([A-Z][A-Za-z0-9]+)\s", body, re.M))
    src["dashboard"] = {os.path.splitext(os.path.basename(p))[0] for p in glob.glob(os.path.join(ROOT, "pkg/dashboard/templates/*"))}
    src["docs"] = {os.path.relpath(p, os.path.join(ROOT, "docs"))[:-3] for p in glob.glob(os.path.join(ROOT, "docs/*.md")) + glob.glob(os.path.join(ROOT, "docs/*/*.md")) if "/adr/" not in p and "/ops/" not in p and "/runbooks/" not in p and "/postmortems/" not in p and "/drills/" not in p and "/compliance/" not in p and "/implementation/" not in p and "/validation/" not in p and "/financial/" not in p and "/security-advisories/" not in p and "/diagrams/" not in p and "/break-glass/" not in p}
    oa = open(os.path.join(ROOT, "pkg/apid/openapi.yaml")).read()
    tags = set()
    for m in re.findall(r"^\s+tags:\s*\[([^\]]+)\]", oa, re.M):
        tags |= {t.strip().strip("\"'") for t in m.split(",")}
    for m in re.finditer(r"^\s+tags:\s*\n((?:\s+- .+\n)+)", oa, re.M):
        tags |= {t.strip().strip("\"'") for t in re.findall(r"- (.+)", m.group(1))}
    src["openapi_tags"] = {t for t in tags if not t.startswith("name:")}
    src["daemons"] = {d for d in os.listdir(os.path.join(ROOT, "cmd")) if os.path.isdir(os.path.join(ROOT, "cmd", d))}
    pf = open(os.path.join(ROOT, "pkg/platformflags/catalog.go")).read()
    src["platform_flags"] = set(re.findall(r'^\s+[A-Z][A-Za-z]+\s+=\s+"([a-z_.]+)"', pf, re.M))
    src["feature_env_flags"] = {e.replace("FAAS_", "").replace("GREGALE_", "") for e in envs}
    src["catalog"] = {c["id"] for c in json.load(open(os.path.join(ROOT, "pkg/productcap/catalog.json")))["capabilities"]}
    sdk_go = glob.glob(os.path.join(ROOT, "sdk/go/*.go")) + glob.glob(os.path.join(ROOT, "sdk/go/preview/*.go"))
    src["sdk_go"] = {os.path.splitext(os.path.basename(p))[0] for p in sdk_go if not p.endswith("_test.go")}
    schema = open(os.path.join(ROOT, "schema.sql")).read()
    src["tables"] = {t.split(".")[-1] for t in re.findall(r"^CREATE TABLE (?:IF NOT EXISTS )?([a-z_0-9.]+)", schema, re.M)}
    src["packages"] = {d for d in os.listdir(os.path.join(ROOT, "pkg")) if os.path.isdir(os.path.join(ROOT, "pkg", d))}
    deploy_env = set()
    for path in glob.glob(os.path.join(ROOT, "deploy", "**", "*"), recursive=True):
        if os.path.isfile(path) and "/gcp/" not in path:
            deploy_env |= set(re.findall(r"FAAS_([A-Z0-9_]+)", open(path, errors="ignore").read()))
    src["deploy_env"] = deploy_env
    src["gateway_files"] = {os.path.splitext(os.path.basename(p))[0] for p in glob.glob(os.path.join(ROOT, "pkg/gateway/*.go")) if not p.endswith("_test.go")}
    return src


def normalize(source, ident):
    if source == "manifest_keys":
        struct, key = ident.split(".", 1)
        return kebab(struct) + "." + key.replace("_", "-")
    if source == "routes":
        p = re.sub(r"\{[^}]*\}", "", ident)
        p = re.sub(r"/+", "/", p)
        return p.lower()
    return kebab(ident)


def route_key(path):
    """Reduce /v1 routes to their feature segment so rules see the resource, not the scaffolding."""
    segs = [s for s in path.split("/") if s]
    if not segs or segs[0] != "v1":
        return path
    rest = segs[1:]
    if not rest:
        return path
    if rest[0] in ("apps", "orgs", "account") and len(rest) > 1:
        return "/".join(rest[:1] + rest[1:3]) + " " + (rest[1] if len(rest) > 1 else "")
    return "/v1/" + "/".join(rest[:3])


def main():
    rows = {}
    for line in open(INVENTORY):
        m = re.match(r"^\| (\d+) \| (.*?) \|", line)
        if m:
            rows[int(m.group(1))] = m.group(2)
    def resolve(key):
        hits = [n for n, cap in rows.items() if key in cap]
        if len(hits) != 1:
            raise SystemExit(f"rule target {key!r} matches {len(hits)} rows")
        return hits[0]
    compiled = []
    for pattern, target in RULES:
        tgt = target if target in ("INFRA", "TOOLING", "TEST") else resolve(target)
        compiled.append((re.compile(pattern), tgt))
    infra = [re.compile(p) for p in INFRA_RULES]
    priority = [(re.compile(p), resolve(t)) for p, t in PRIORITY_RULES]

    src = collect()
    show = sys.argv[sys.argv.index("--show") + 1] if "--show" in sys.argv else None
    total_unmatched = 0
    hit_rows = set()
    print(f"{'source':20} {'items':>6} {'mapped':>7} {'infra/tooling':>14} {'unmatched':>10}")
    for source, idents in src.items():
        mapped = other = 0
        unmatched = []
        for ident in sorted(idents):
            norm = normalize(source, ident)
            probe = route_key(norm) if source == "routes" else norm
            tgt = next((t for rx, t in priority if rx.search(probe)), None)
            # Packages and tables are mostly plumbing: try the infra list first for them.
            if tgt is None and source in ("packages", "daemons") and any(r.search(norm) for r in infra):
                tgt = "INFRA"
            if tgt is None:
                for rx, t in compiled:
                    if rx.search(probe) or (source == "routes" and rx.search(norm)):
                        tgt = t
                        break
            if tgt is None and any(r.search(norm) for r in infra):
                tgt = "INFRA"
            if tgt is None:
                unmatched.append(ident)
            elif isinstance(tgt, int):
                mapped += 1
                hit_rows.add(tgt)
            else:
                other += 1
            if show == source:
                print(f"  {ident} -> {tgt if not isinstance(tgt, int) else rows[tgt][:60]}")
        total_unmatched += len(unmatched)
        print(f"{source:20} {len(idents):>6} {mapped:>7} {other:>14} {len(unmatched):>10}")
        for u in unmatched:
            print(f"    UNMATCHED {source}: {u}")
    dead = sorted(set(rows) - hit_rows)
    print(f"\nrows with no entry point mapped: {len(dead)}")
    for n in dead:
        print(f"    {n}: {rows[n][:80]}")
    empty = sorted(source for source, idents in src.items() if not idents)
    for source in empty:
        print(f"    EMPTY {source}: the collector found nothing; fix its path or pattern")
    print(f"\nTOTAL UNMATCHED: {total_unmatched}")
    return 1 if total_unmatched or empty or dead else 0


if __name__ == "__main__":
    sys.exit(main())

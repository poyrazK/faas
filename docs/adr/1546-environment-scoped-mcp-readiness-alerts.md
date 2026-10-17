# 1274: Environment-scoped MCP readiness alerts

Status: Accepted

## Context

Production and staging readiness share an app, but need independent notification thresholds, cooldowns, destinations, and incident state. Existing readiness alerts count unhealthy roles across every environment.

## Decision

Add an optional `mcp_readiness_scope` to alert creation, storage, responses, SDKs, CLI, and dashboard. Empty means all environments for backward compatibility; explicit `default` means only default. Named environments match exactly. Only app-scoped, webhook-only readiness rules accept a nonempty selector.

The selector is immutable in the API and database. Changing it requires a new rule, so existing cooldowns, firing state, and deliveries cannot be attributed to a different environment. Ordinary threshold, cooldown, enabled, and destination updates retain the selector.

Evaluation requests the selected environment and defensively filters returned statuses before counting failures and creating bounded webhook summaries. Summaries retain environment, deployment, role, and policy revision, omit observation history, and include the app and selected environment at payload level. Failed reads degrade evaluation; empty results pause it. Existing presets remain app-wide.

## Consequences

Separate rules provide independent routing and incident state without changing existing alert behavior. The dashboard filters selected rules by environment and continues to show all-environment rules. Migration replay preserves later guard definitions, and rollback refuses to discard selected rules. No automatic traffic action is introduced.

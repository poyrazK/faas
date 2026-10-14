# ADR-1245: Independent role views for MCP catalog monitoring

Extends ADR-1244. Catalog visibility depends on the observer’s MCP role. Apid now
stores up to 16 independent monitor views per app, including the existing default
view. Each view owns its baseline, server-issued revision, heartbeat, latest
receipt and bounded transition history. Configurations may store a token
environment variable name; secret values remain in the observer’s environment.
Role names are bounded and validated. Named-role observers reject token reference
overrides that differ from configuration, including overriding an anonymous role.
Existing default-role observers without references retain their run-time token
selection for compatibility.

The existing app JSON row and transaction lock serialize configuration and reports
across roles. Named snapshots live in a bounded roles map; the default snapshot
retains its original fields. The append-only migration constrains the map shape,
role count and each role’s history. Exact reads select one role; a separate summary
response avoids inventing an empty default configuration for named-only apps.
Superseded or cross-role revisions cannot overwrite current observations.

Every enabled role is required for app health. Unavailable evidence takes precedence
in the summary; fresh known drift remains visible and can fire a drift alert while
another role is unavailable. No unavailable role can produce a healthy aggregate
or resolve drift as healthy. Notification payloads include deterministic affected
role lists and role-specific receipt links. Disabling excludes a role from health
aggregation while retaining its receipts. Rules remain app-scoped and notification
only; there is no automatic action or enabled rule.

Run one supervised observer per role. Environment references prevent accidental
run-time credential selection but do not attest token identity or prevent a trusted
metrics publisher from submitting fabricated observations. Neither these receipts
nor an aggregate healthy state prove visibility for unconfigured roles or every
replica. All other ADR-1244 bounds and authorization rules continue to apply.

# 953: Read-only retained recovery history

Recovery operators need to connect review decisions, policy controls, automatic retry children and health notifications without issuing execution commands.

Expose an app-scoped read endpoint and `gregale automations history`. Both storage backends assemble the timeline from existing retained evidence. PostgreSQL uses SQLC and a consistent read transaction. No migration or additional worker activity is required. Entries exclude execution inputs, outputs and prepared request bodies.

Saved review outcomes, operator resolutions, controls, applied retries, timestamped operator suppressions, recorded child results and notification captures retain their actual timestamps. Policy rows, worker evaluations and health incidents retain only their latest observation. They are labeled observations; replacement suppression without a recorded timestamp is omitted. Health receipts do not infer historical policy versions or reasons from a mutable incident.

Pages default to 25 entries, cap at 100, and use descending timestamp and identifier ordering. The default window is seven days, capped at 30 days. Cursors bind the app and original filters and retain the initial time fence. They do not provide snapshot isolation across requests: retention or later updates can remove or move observations. Workflow, run and policy filters exclude entries lacking the association. This is an operational evidence timeline, not a complete immutable audit ledger.

The endpoint shares existing app read authorization, MFA and no-store responses. Reading never refreshes evaluations, applies plans or schedules notifications.

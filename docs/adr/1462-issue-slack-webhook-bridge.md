# ADR 1462: Slack alerts through verified Issues webhook receivers

Status: Accepted

## Decision

Provide a Node SDK adapter and a runnable Slack incoming-webhook bridge using the existing durable Gregale outgoing webhook ledger. Keep provider configuration in the customer-owned bridge: no Slack identity lookup, app permissions, or new control-plane destination model. Bind each receiver to an app UUID/slug and dashboard HTTPS origin, with an optional current-team UUID filter. Default event selection is issue creation, regression, and verified customer-impact threshold crossing; lifecycle and assignment notifications are explicit opt-ins.

Verify the exact request bytes and timestamp before parsing, claiming a receipt, or sending to Slack. Render issue title, lifecycle, environment, and assignment metadata as Slack plain-text blocks with a trusted dashboard link. Escape Slack control markup in fallback text, cap section text below the provider limit, and omit stacks, request context, raw tags, and customer identities. Return no message for authenticated deliveries outside the selected app, team, or event set.

The example uses a customer-owned PostgreSQL receipt table, independent of Gregale's control-plane schema. Bind delivery IDs to SHA-256 of their canonical logical transition. Atomically claim a 30-second lease across receiver processes; return a retryable response for an active lease. Fence completion and lease release by a fresh UUID token. A completed delivery remains acknowledged without sending again, including after receiver restart. A different logical transition under the same ID conflicts. Configure the database and run the explicit schema setup before starting the receiver; retain receipts for the outgoing webhook replay horizon.

Allow only HTTPS `hooks.slack.com/services/...` incoming-webhook destinations. Disable redirects, use a five-second send timeout, and acknowledge only after Slack accepts and the receipt commits. Provider rejection or transport/database failure remains retryable through Gregale's existing webhook retries and manual replay. Never include the provider URL, response body, signing secret, or original payload in error responses. Bundle only an explicit code/module/lockfile allowlist, excluding credentials and environment files.

## Consequences

Customers can connect app or team Issues alerts to Slack today through a deployable receiver, with no additional privileged connector. This is a customer-operated bridge rather than a native organization integration UI. Slack incoming webhooks supply no idempotency key: if Slack accepts a request but its response or the database acknowledgement is lost, a later retry can duplicate the notification. The bridge provides durable deduplication after confirmed success, not exactly-once external effects. Team filtering uses assignment captured in the emitted transition; it does not notify former owners on reassignment or synchronize Gregale members with Slack members. GitHub/Linear issue synchronization and crash-free session instrumentation remain separate increments.

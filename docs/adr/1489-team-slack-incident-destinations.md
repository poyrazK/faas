# ADR-1489: Team Slack destinations for tracker incidents

Date: 2026-10-15

Status: Accepted

## Context

Incident notices already capture team routing, reminders and one-step escalation,
but app webhook subscribers must build their own chat integration. Teams need an
optional native Slack destination with durable incident threads and delivery status.

## Decision

Allow the app owner to configure one Slack bot token and channel ID per issue team
in the existing notifications dashboard. Use the existing named CSRF, MFA, deploy
scope and owner checks. Seal tokens under a dedicated age namespace with the fleet
recipient; unseal with the existing current/previous identity accessor. Never expose
tokens in settings responses, notices, app webhook payloads or logs. Tokens require
`chat:write`; operators invite the bot to their selected channel. This increment has
no OAuth installer, Slack acknowledgment callback or implicit chat destination.

Queue a delivery inside the transaction that creates a new routed notice, regardless
of app webhook subscription. No Slack network request occurs in that transaction.
Only notices captured for the destination's team are queued; account/owner fallback
retains the existing inbox and app webhook paths. Opt-in does not replay retained
notices. A save increments destination revision, cancels its old queued/in-flight
jobs, and starts fresh threads for future notices. Store the captured channel ID on
each delivery so history never presents a replacement channel as an old target.

Thread identity is destination/revision/incident. Serialize claims using the existing
app lock and a destination lease. Use a database sequence for enqueue ordering; a
failed earlier post blocks later notices in the same thread until it succeeds or
becomes terminal, while other incidents may proceed. Persist Slack's returned root
timestamp before allowing replies. Escalation to another team creates that team's
thread. Recovery follows the existing captured latest-notified-recipient route;
there is no broadcast to teams that no longer own notification responsibility.

The bounded apid worker uses the fixed HTTPS Slack `chat.postMessage` endpoint,
refuses redirects, bounds request time and response size, and rechecks current app,
owner entitlement/deploy eligibility, notification opt-in, destination revision,
claim and captured-recipient membership immediately before sending. Lease fencing
rejects stale completions. Cancel newly ineligible work without sending. Settings
or membership can change after the final check; no local control can recall an HTTP
request already in flight.

Reuse `pkg/webhook.ComputeBackoff` and its default jittered six-retry ladder, with
seven automatic attempts per cycle and a 24-hour delivery deadline. Honor bounded
Retry-After, impose a one-second destination cooldown, and reuse the delivery UUID
as Slack's client message ID. Rate limits also postpone other work for that
destination. Classify only fixed safe error codes; never persist raw provider error
bodies. Known missing-thread errors clear the old root before a bounded retry.
Permanent failures become dead. Expired claims consume the same attempt budget.
Owner retries renew the deadline and reset the automatic cycle counter while
preserving total attempts and stable message identity. Canceled or successful
jobs cannot be manually replayed, and old destination revisions cannot be retried.

Messages use plain-text blocks and disabled automatic parsing/unfurls. They contain
app/incident identifiers, the fixed notice kind, a safe problem reason and links
built from the configured HTTPS dashboard origin. They contain no stack frames,
issue titles, provider URLs or interactive callbacks. Gregale still enforces its
normal permissions when a user follows a link.

Show recent delivery state, total attempts, safe errors and owner retry controls in
the notifications dashboard. Members retain their scoped inbox and triage access;
connection settings and delivery controls remain owner-only. Delivery rows cascade
with retained notices, bounding them by the existing per-link retention and team
limits. Periodically remove thread roots with no retained deliveries. Deleting a
team cascades its destination and delivery records; existing incident history
retains its normal routing evidence.

## Consequences

Slack delivery is optional and independent of generic app webhook delivery. Enqueue
and local delivery identity are deduplicated; HTTP delivery remains at least once.
A timeout or process loss after Slack accepts a message can repeat a root or reply,
so threading and remote deduplication cannot be guaranteed across ambiguous network
failures. Status records provider acceptance, not whether a human read a message.
Deployment requires the existing fleet age recipient and identity and the correct
HTTPS public dashboard origin. Existing auxiliary-token operations apply: retain
the sealing identity until destinations are resaved with replacement credentials.

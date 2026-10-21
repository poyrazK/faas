# ADR-20261021183000: Customers manage their own account, and owners hear about it by webhook

- **Status:** accepted
- **Date:** 2026-10-21
- **Decision:** After sign-up (ADR-20261021143000), a customer could read their usage and invoices (ADR-1506) but not change anything. The owner learned about a new customer only from the audit log. Both change.
  - **Self-service with the billing key.** Four routes take the customer's billing-scope key and its limits (ADR-1506):
    - `GET /v1/consumer-billing/{app}/plans` lists the sign-up plans with prices, the current plan, and what the customer may do.
    - `PUT .../plan` switches to another offered plan from the next minute. The existing mid-month handling of plan changes prices the month.
    - `POST .../portal` returns a short-lived Stripe customer portal URL for the customer's Stripe customer (their `external_ref`), where they update their card and see and pay invoices.
    - `POST .../close` revokes the consumer, so its keys stop working. Usage until then is still billed by the month's statement.
  - **Owner control.** Two sign-up settings, both off by default, allow plan changes (`customer_plan_changes`) and closing (`customer_closing`). They live in `api_consumer_signup_settings`; the CLI flags are `--customer-plan-changes` and `--customer-closing`. Customers switch only between the plans sign-up offers. The portal needs no setting, because it changes nothing Gregale bills, but it does need a Stripe customer.
  - **Owner webhooks.** Three new app webhook events, which belong to API monetization:
    - `consumer.signed_up` carries the sign-up, consumer, Stripe customer, name, email, plan, trial end and livemode.
    - `consumer.plan_changed` carries the assignment, the from and to plans and the start.
    - `consumer.closed` carries the consumer and the time it closed.

    Each is enqueued in the transaction that makes the change, keyed by its source (sign-up, assignment or consumer), and relayed after commit. The outbox drain retries a failed relay. Owner-made plan changes and revocations do not emit them: the owner already knows.
  - **Failures the owner should see.** A portal session Stripe refuses shows up in the audit log as `api_consumer.portal_failed`, with Stripe's reason. The usual reason is that the portal has not been saved in the Stripe dashboard. The customer gets 409 `consumer_self_service_unavailable`. So does an action the owner does not allow, or a consumer that is not billed through Stripe.
- **Why:** Without self-service, a customer whose card expired, or who wanted a bigger plan or to stop, had to ask the owner. Gregale created the customer but never told the owner, who needs to provision accounts, send welcome mail or update a CRM.
- **Consequences:**
  - **Plan changes start at the next minute.** A second change in the same minute gets 409.
  - **Closing cannot be undone by the customer.** The owner can create a new consumer; the Stripe customer and card remain.
  - **Stripe portal setup.** Live-mode portal sessions need the owner to save the portal settings in Stripe once.
  - **Hidden with monetization.** The events and their payload schemas are hidden with the rest of API monetization until released (ADR-20261018090210).
  - **Migration.** Two boolean columns on `api_consumer_signup_settings`, added to its not-yet-released migration.
- **Rejected alternatives:**
  - **A hosted page for self-service.** It would need the customer to paste their API key into a web page. Owners build this on their own site with the API instead.
  - **Emailing the owner on each sign-up.** Webhooks reach the owner's systems; the audit log already records each sign-up.
  - **Letting customers pick any plan.** Owners offer plans at sign-up on purpose, so the same list bounds changes.

# ADR-20261021143000: Customers sign up for an API themselves, with Stripe Checkout

- **Status:** accepted
- **Date:** 2026-10-21
- **Decision:** An app owner can let their API's customers sign up without the owner creating each consumer and key. The customer picks a plan and saves a card in the owner's own Stripe account. They then get a consumer key at once.
  - **Owner settings.** `PUT /v1/apps/{app}/consumer-signup` turns sign-up on for an app. It sets:
    - the display name;
    - the offered plans (none offers only the app's default plan);
    - the scopes of the key a customer gets (default `write` and `billing`);
    - optional terms and return URLs;
    - an optional trial in days;
    - a daily sign-up limit (default 100, at most 1,000).

    It needs the account's Stripe billing connection (ADR-1502) and a plan that allows consumer keys. `GET` and `DELETE` read and remove the settings, and `GET .../consumer-signups` lists recent sign-ups. The CLI adds `gregale consumers signup`, `signup-set`, `signup-delete` and `signups`.
  - **Hosted page.** Gregale serves the sign-up page at `/v1/consumer-signup/{app}/page`. It is a static page with a strict CSP, like the Product Control portal (ADR-1701), and it calls the same-origin JSON API below. Owners can call that API from their own site's backend instead.
    - `GET /v1/consumer-signup/{app}` returns the display name, the offered plans and their current prices.
    - `POST /v1/consumer-signup/{app}` takes an email, a name and a plan. It records a pending sign-up and creates a Stripe customer with them. It then creates a Stripe Checkout Session in `setup` mode for cards, which saves a card without charging it. It returns the Checkout URL and a claim token that is shown only there; Gregale keeps only the token's hash.
    - `POST /v1/consumer-signup/{app}/signups/{id}/complete` takes the claim token. It reads the Checkout Session and continues only once Stripe reports it complete with a succeeded SetupIntent. It makes the saved card the customer's default for invoices, so exports collected with `charge` (ADR-1311) charge it. In one transaction it creates the consumer and the chosen plan's assignment, plus the trial if one is set; the consumer's external ref is the Stripe customer ID, which exports already use as the default customer. It then issues a consumer key and returns it.
  - **Retries.** Completion is safe to retry until the sign-up expires (24 hours). The consumer, plan and trial are created once. Each call issues a new key and revokes the one the sign-up issued before, so one lost response does not strand the customer, and only one sign-up key is ever live. The page clears its claim token once it has shown the key.
  - **Abuse limits.**
    - Creating a sign-up is limited per client IP (`ConsumerSignupPerIPPerHour`) and per app per UTC day (the daily limit).
    - Reading the form and completing a sign-up are limited per IP too.
    - The Checkout Session expires after 23 hours. Pending sign-ups expire after 24 hours, and a worker deletes them a week later along with their email and name.
  - **Release.** The routes belong to API monetization. They answer like unregistered ones until the account has it (ADR-20261018090210, ADR-20261020170649).
- **Why:** Selling an API needs customers to be able to start on their own. Before this, an owner created every consumer, plan assignment and key by hand or with their own code. Saving a card at sign-up lets usage invoices charge it later, without Gregale ever holding payment details or money.
- **Consequences:**
  - **Required Stripe permissions.** A restricted Stripe key needs write access to Customers and Checkout Sessions, and read access to SetupIntents.
  - **Abandoned checkouts.** A customer who closes the tab after saving a card leaves a Stripe customer with a card and no consumer. The customer's metadata names the sign-up, and signing up again starts over.
  - **Unverified email.** Gregale does not verify the email; Stripe has it, and a saved card is the proof of intent.
  - **Migration.** New tables `api_consumer_signup_settings` and `api_consumer_signups` (expand).
- **Rejected alternatives:**
  - **Paddle.** Its checkout cannot save a card without charging it, so Paddle sign-up waits for a design built on a first charge.
  - **Lago.** Lago's checkout URL depends on the payment provider behind it, and Gregale cannot confirm a saved card through Lago's API. Lago is deferred too.
  - **Stripe webhooks.** Confirming on the customer's return needs no webhook secret per account. A customer who never returns has no consumer to receive one anyway.
  - **Keys in the return URL.** A key in a URL leaks through history and referrers, so the key is returned only to the completion call.

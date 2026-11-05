# ADR-20261022100735: Product Control self-serve signup and card on file

- **Status:** accepted for internal implementation
- **Date:** 2026-10-22
- **Amends:** ADR-1703 (invited users only), ADR-1707 (onboarding run by the owner), ADR-20261018212918 §6 (no card storage: the provider stores the card, Gregale never sees it)
- **Decision:** An owner can let new customers sign up through the customer portal, choose a plan and keep a card on file in the owner's Stripe, without the owner inviting or assigning them.
  - **Offering signup.**
    - The catalog's `signup` policy names the plans offered at signup and optionally the billing `provider` (`stripe` or `paddle`) whose customer each signup creates.
    - Signup also needs the hot runtime setting `product_control_signup_enabled` (default off), which operators turn off to stop new signups at once without changing catalogs.
  - **Public offer.**
    - `GET /v1/product-control/customer/{slug}/offer` is public behind the IP limiter. It answers 404 unless both the policy and the setting are on.
    - It returns the offered plans' public terms: fee, trial, and each feature's price, included and monthly starts. It also returns the Auth app's sign-up page.
    - It names no account or customer.
  - **Accounts.** Visitors create their user in the owner's Application Auth app, whose own controls govern who may sign up: sign-up on or off, domain rules, invite or waitlist mode, CAPTCHA, rate limits. Gregale never creates Auth users.
  - **Workspace.** A signed-in user creates a workspace with `POST /v1/product-control/customer/{slug}/signup` (`id`, `plan`, `workspace_name`). Gregale:
    1. Creates the Auth organization with the user's own session (a user may create organizations; ADR-1703 kept the admin key out of Gregale). The slug is derived from the app and `id`, so a retry finds the organization the first attempt created instead of making another.
    2. Creates the provider customer when the policy names a provider: a Stripe customer under an idempotency key derived from `id`, or the Paddle customer with the user's email, reused if Paddle already has one.
    3. Runs the guided onboarding preview and confirm (ADR-1707) in one transaction. It creates the platform tenant with the provider customer ID as its `external_ref` (otherwise `signup:<organization>`), links the organization, assigns the plan and starts any trial (ADR-20261021195901). The tenant quota, account checks and onboarding receipt apply unchanged.
    - The response names the new workspace. It is audited as `product.customer.signed_up`.
    - A user may own at most `ProductControlSignupMaxWorkspacesPerUser` (3) workspaces per product.
  - **Card on file (Stripe).**
    - A workspace whose provider customer is a Stripe customer (`cus_…`), for a role with `billing:manage`, can open Stripe Checkout in setup mode. `POST …/payment-method/checkout` requires `consent: true` to future charges by the product team. It returns the Checkout URL on Stripe's own page, which returns to the portal.
    - `POST …/payment-method/complete` with the session ID then reads the session with the owner's key. If it completed for this customer, Gregale makes its card the customer's default for invoices.
    - `GET …/payment-method` shows the default card's brand, last four digits and expiry.
    - With the owner's Stripe connection on `charge` collection (ADR-1311), exported statements and fees are then charged automatically.
    - Gregale stores no card data, only audits: `product.customer.payment_method_consented`, `product.customer.payment_method_saved`.
- **Why:** Every Product Control customer had to be invited, linked and assigned by the owner, and paid by hand each invoice. Plans with fees, trials and overage (ADR-20261021154511, ADR-20261021195901) need customers who can sign up on their own and be charged without chasing invoices.
- **Consequences:**
  - **Paddle and Lago.** Paddle cannot charge a saved card for one-off transactions outside subscriptions, so Paddle signups pay each emailed invoice (collection `send`). Paddle keeps one customer per email, so a signup whose Paddle customer already bills another workspace of the account is refused (409 `product_signup_limit`) rather than taking it over. Lago customers are not created at signup.
  - **Return links.** Sign-up redirects and Checkout return to the portal on the platform's configured public origin, never on the request's `Host`.
  - **Provider keys.** The owner's Stripe key needs write access to customers and Checkout Sessions and read access to SetupIntents and payment methods. The Paddle key needs customer read and write.
  - **Auth configuration.** The owner enables sign-up in the Auth app and allows the portal as a redirect URL. Gregale cannot see these settings and reports a failed Auth call as unavailable.
  - **Abuse.** Signup is bounded by the Auth app's own sign-up controls, the per-user workspace limit, the account's tenant quota and the kill switch.
  - **No card requirement yet.** A plan cannot yet refuse runs until a card is on file. Signups can start runs, including after a trial ends, without one.
- **Not in this decision:** requiring a card before a trial ends, trial reminder email, Paddle or Lago card collection, and a pricing page outside the portal.
- **Rejected alternatives:**
  - **An Auth admin key in Gregale** to create users or organizations would break the boundary ADR-1703 set. The user's own session can create its organization.
  - **Embedded card forms (Stripe Elements)** in the portal would load Stripe.js under the portal's strict CSP and put Gregale in the card-entry page. The provider's hosted page keeps card data entirely with Stripe.
  - **Webhooks from the owner's Stripe** would need a webhook endpoint and signing secret per account. Reading the Checkout Session when the customer returns is enough for one setup.

## Implementation slices

1. Signup policy, kill switch, public offer, workspace creation with the provider customer, portal signup.
2. Stripe card on file: Checkout setup session, completion, default card, portal payment method section.

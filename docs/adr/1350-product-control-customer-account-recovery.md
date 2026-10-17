# ADR-1350 · Product Control customer account recovery

- **Status:** accepted for internal implementation
- **Date:** 2026-10-17
- **Decision:** Add a password-recovery entry point to the customer portal,
  delegating the existing reset flow to the product's live Application Auth app.
  Keep the entire surface behind the default-off Product Control flag.

## Recovery entry point

Customers can choose **Forgot password?** before signing in. The current product
name selects a relative Product Control URL; neither their email nor password is
included. The link opens a new tab with `noopener noreferrer`. The portal clears
the visible password on a normal recovery click and explains that customers
return to the product afterward to sign in. Changing the product field updates
the link; invalid selectors cannot navigate through the control.

`GET /v1/product-control/customer/{slug}/account-recovery` uses the existing
customer realm resolver. Each request observes the latest catalog's `auth_app`,
an active owning account and its owned managed-PostgreSQL Auth binding. The
production resolver supplies only that app's platform HTTPS origin. It does not
use request Host, owner credentials, customer cookies, commercial revisions or
caller-supplied upstream URLs to select the target. The resolved Auth app must
belong to that account and match the selected slug.

The endpoint returns a 303 redirect to the fixed `/ui/forgot-password` page.
Origins with user information, non-HTTPS schemes, paths, query strings or
fragments are rejected. Request query parameters are rejected, including any
`redirect_to`, email or token selectors. An explicit empty Location fragment
prevents browsers from inheriting a caller's fragment. Responses use `no-store`
and `no-referrer`; no cookies are set or modified. The existing pre-authentication
rate limiter wraps the route. GET/HEAD work while enabled; the disabled gate
hides all methods before realm lookup or authentication.

## Existing Auth owns the reset

Product Control neither requests mail on link activation nor proxies reset tokens
or password updates. The hosted Auth UI obtains the email address, explicitly
requests recovery, handles the one-time reset link and delegates password changes
to GoTrue. Its existing redirect allowlist, password policy, mail configuration,
anti-enumeration behavior, rate limits and session rules continue to apply.
No Auth admin key, service role or platform-owner session grants customer access.
Recovery does not create membership, assign a plan or change a tenant mapping.

After resetting, the customer returns to Product Control and signs in normally.
The product still verifies the live session, membership and current mapping.
The Auth application's configured post-reset destination remains its own choice;
Product Control adds no automatic token handoff or product-portal callback.

This increment changes no schema, issued migrations, catalog format, commercial
receipt or Operations lifecycle. The private redirect route stays outside the
public OpenAPI/generated SDK contract. Existing pilot evidence remains frozen at
its recorded source; this runtime receives new implementation verification.

## Verification and release

Local tests cover current-realm changes, invalid or foreign origins, query/Host
injection, unavailable bindings, cookie privacy, browser interaction, routing and
disabled methods. Browser checks use production portal/Auth UI assets with
fictional Auth API responses and do not send external messages. Actual recovery
email delivery and deployed Auth reset/session behavior remain live acceptance
checks when internal credentials become available. Development can continue
without making that pending deployment a prerequisite for implementation.

See [Application Auth](../application-auth.md),
[the login runbook](../ops/product-control.md#customer-account-recovery) and
[ADR-1333](1333-product-control-customer-portal-and-consent.md).

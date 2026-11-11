# ADR 20261106210000 · Customer verification follows enabled factors

- **Status:** accepted
- **Date:** 2026-11-06
- **Related:** ADR-077, ADR-140, migration 479, operator-session policy

## Problem

Customer MFA is opt-in, but the recent-verification middleware required an
authenticator even from customers who never enabled one. Free-to-paid checkout,
cancellation, billing details and credential creation therefore forced enrollment.
Billing suspension blocked enrollment and factor reset, while recovery codes
cleared sign-in without satisfying the verification needed to pay. The web
console omitted enrollment's existing-password proof and asked for a second
code after successful enrollment.

## Decision

Customer step-up applies to the authenticated actor when MFA is enrolled or an
explicit enrollment policy is armed. Ordinary authenticated accounts without
either proceed through the existing scope, email, origin and confirmation checks.
Resource owners do not determine a teammate's verification policy. Strict
provider/operator gates and their refusal of bearer keys remain mandatory.

Enrollment confirmation, TOTP verification and an accepted saved recovery code
each establish five minutes of recent MFA on the current session. The one-time
code consumption and replay protections remain unchanged. Billing-suspended
customers retain MFA enrollment, confirmation, recovery and reset so they can
restore both their credentials and billing.

Resetting an authenticator refreshes the browser session's pending state from
the committed account policy. Explicit policies and the operator allowlist still
require a replacement. The last saved recovery code remains reserved for factor
reset: `mfa_last_recovery_code` directs both dashboards to reset with that same
code, avoiding advice to supply a password or device the customer lacks.

Sensitive browser forms lead to a usable challenge when verification expires.
Unchanged plan submissions and navigation to the checkout preview require no
fresh proof; the actual billing write retains it for enrolled customers.
PostgreSQL overage uses the same policy on both API and dashboard surfaces.

Invitation acceptance requires the invitee's browser session, current email
verification and their enabled MFA policy. The CLI opens the browser invitation
page instead of sending an API key that cannot satisfy interactive acceptance.
Opening that page is not reported as having joined the organization.

Both dashboards expose email-assisted recovery with its existing 24-hour
cooldown. The console collects the existing password when confirming enrollment,
accepts enrollment/recovery without a redundant code, and retries a refused
customer action at most once after verification. MFA code failures do not end
the underlying browser session.

## Compatibility and validation

No database migration is needed. Existing scopes and operator policies are
unchanged. Deploy the API before the console, which now relies on recovery and
enrollment establishing recent proof. The console's vendored confirmation schema
is brought up to the API's existing optional `current_password` field.

Regression coverage exercises optional and mandatory MFA, actor versus owner
policy, billing suspension, reset-session state, recovery freshness, stale browser
challenges, last-code guidance, CLI handoff and the console's one-retry flows.

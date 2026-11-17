# Support access: proposed Terms, Privacy and DPA text (draft for sign-off)

Status: **draft, not published.** Customer-granted support access
(ADR-20261116163508) is dark behind the `support_grants` platform flag. Publish
these changes before the flag is turned on for any customer outside Gregale.
They change the Terms, so section 13 of the Terms applies: email account owners
and show a dashboard notice at least 30 days before they take effect, and bump
the effective date.

Each block below says where it goes. Wording follows the existing documents'
style: short, plain, no defined terms beyond theirs.

## Terms of Service (`pkg/legal/templates/terms.html`, section 4 "Your content")

Add a bullet after "You give us the permission we need to host, copy, build...":

> If an owner or admin of your account or organization grants Gregale support
> access, our support staff may deploy your app's connected repository at its
> production branch, retry, cancel and roll back deployments, and restart your
> apps, for the apps and the time you choose (at most 7 days). Support access
> never includes reading your secrets or configuration, changing members, keys
> or billing, uploading code, or deleting anything. Every action is recorded in
> your activity log and your owners and admins are notified. You can revoke the
> access at any time, and it ends on its own when it expires.

## Privacy Policy (`pkg/legal/templates/privacy.html`, section 7 "Security")

Append to the paragraph:

> Our support staff cannot reach your apps unless an owner or admin grants them
> time-limited support access; that access is limited to deploying,
> rolling back and restarting, and every use of it is logged in your activity
> log.

## DPA (`docs/DPA.md`)

Section 8 "Security measures", add a bullet:

> - Support personnel access: Processor's support staff have no standing
>   access to Controller's applications. Controller's owners or admins may
>   grant time-limited support access (at most 7 days, limited to the
>   applications they name) that permits only deploying the connected
>   repository's production branch, retrying, cancelling and rolling back
>   deployments, and restarting applications. It never permits reading
>   secrets or configuration or uploading code. Each use requires an
>   MFA-authenticated staff session, is recorded in Controller's activity log
>   and audit trail, and is notified to Controller's owners and admins.
>   Controller may revoke it at any time.

Section 5 "Processor obligations" already covers confidentiality of
authorised persons; no change there.

## Checks before publishing

- The DPA has no placeholders (pkg/legal `TestDPAHasNoPlaceholders`); the text
  above has none.
- Ansible ships `docs/DPA.md` to `/etc/faas/dpa.md` and the docs site vendors
  it; publish both in the same release (see
  [legal-documents-operator-notes.md](legal-documents-operator-notes.md)).
- If counsel changes the wording, keep the limits (production branch only,
  7 days, no secrets or uploads) in line with ADR-20261116163508 and
  `pkg/api/limits.go`.

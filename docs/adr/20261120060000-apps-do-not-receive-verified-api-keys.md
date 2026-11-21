# ADR-20261120060000: Apps do not receive a verified Gregale API key

- **Status:** accepted
- **Date:** 2026-11-20
- **Amends:** ADR-20261102100001 (previews withhold platform gate credentials), ADR-079 (per-app public auth)
- **Context:**
  - Protection by default (ADR-20261116120000) gives every new app `require_authn`, so the usual way to call a fresh app is with an account API key: `Authorization: Bearer fp_live…`. After the gateway verified the key it forwarded the header to the app unchanged. The prod hunt of 2026-11-20 (R12-1) saw the owner's key in a production app's request log.
  - The key belongs to the caller's account, not the app. It can carry far more than `apps:read` (deploy, billing, admin), it is often a CI or teammate key, and app code routinely logs headers, ships them to third-party error trackers, or forwards them upstream. The app needs none of it: the gate has already authenticated the caller.
  - ADR-20261102100001 withheld the credential only on previews and rejected doing it for every app as "a separate decision". This is that decision, made now that the API-key gate is the default rather than an opt-in.
- **Decision:**
  1. On a production app, when `require_authn` or public auth `bearer` is on and the request's `Authorization` carries a bearer token in Gregale API-key format, the gateway removes the header after the gate passes, before anything reaches the app. The request stays marked as credentialed, so shared caches still bypass it.
  2. Unchanged on production: a `basic` public-auth password (the app's own, set by its owner), verified consumer keys (keys of the app's own end customers), and any `Authorization` on an app with no gate that reads it (an app's own scheme).
  3. Previews keep ADR-20261102100001: every credential a platform gate reads is withheld.
- **Consequences:**
  - A production app that read the caller's Gregale key to call the Gregale API on the caller's behalf stops receiving it. Such an app should hold its own key as a sealed secret. No platform component reads it.
  - No migration and no flag; gatewayd-internal ships the change with its unit.
- **Rejected alternatives:**
  - **Strip every `Authorization` header on gated apps.** It would also remove a `basic` password the owner chose and expects their app to see, and consumer keys apps use for their own attribution.
  - **An opt-out flag.** No known app needs the key; an opt-out keeps the leak one setting away. Revisit if one appears.

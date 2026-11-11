# Independent sales frontend

- **Status:** accepted
- **Decision:** own and deploy the sales department frontend in the separate `poyrazK/faas-sales` repository at `https://sales.gregale.dev`. The product/customer website remains `faas-web`. Apid owns sales/referral APIs, data, policy and configurable manager/rep lists.
- **Boundary:** the frontend serves its own staff markup/assets and proxies only sales and required authentication routes to the API. It checks a live session before rendering and takes manager status from the backend. Every API keeps enforcing membership, ownership, verified email and completed MFA. Password sign-in and existing MFA pages are supported; OAuth-only staff can add a password through the product account settings.
- **Session policy:** trust the exact HTTPS `sales.<platform-domain>` origin alongside the apex for ordinary browser session actions. Do not add sales to the narrower provider-administration origin policy. Preserve Origin and Sec-Fetch-Site. No parent-domain cookie or permissive CORS is introduced.
- **Deployment:** deploy backend additive migrations and APIs before the independent frontend and optional customer website integration. Route the sales hostname to the frontend's own service. Keep its label reserved. The old API-host `/dashboard/sales` entry redirects to the frontend. Customer referral status stays in apid.
- **Configuration:** manager/rep email lists and reward/commission terms remain configurable. Payouts stay disabled. This frontend has no database, staff account store or financial settlement.

This supersedes the embedded staff-UI hosting decision in ADR 20261103180000. See [operations](../ops/sales-referrals.md).

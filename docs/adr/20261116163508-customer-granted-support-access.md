# ADR-20261116163508 · Customer-granted support access

- **Status:** proposed
- **Date:** 2026-11-16
- **Amends:** ADR-061 (organization authorization), ADR 20261023214253 (operator reads of customer data), ADR-20261114170352 (organization ownership guards)
- **Context:**
  - A customer who cannot deploy (broken CLI, CI credentials, a deploy they cannot diagnose) often asks support to "just deploy it". Operators cannot. The `/v1/admin/ops` surface only retries or cancels a deployment the customer already submitted, restarts or parks instances, and reads telemetry with `?on_behalf_of=`. The customer routes (`/v1/apps/{slug}/...`) authorize through `appaccess.authorize`, which admits only the app's personal owner or an organization member. Being an operator does not change that.
  - The only workaround today is that the customer invites a support person as an organization member. That gives full developer or admin rights with no end date, reaches every app, and the activity log does not show that the actor was support.
  - Operator-initiated access ("log in as the customer") would make one compromised operator session enough to change every customer's production. It would also undo the operator-session hardening of ADR 20261023214253 and contradict the zero customer-secret exposure posture (secretd, ADR-20261110210000).
- **Decision:**
  1. **Only the customer grants access.** An owner or admin of a shared organization, or the owner of a personal account, creates a *support grant* from a signed-in browser session with a fresh step-up (`requireStepUpStrict`, 5 min). API keys get 403 `browser_session_required`, as with ownership transfer. An operator account can never approve a grant, including for an organization it belongs to.
  2. **Support may ask, the customer decides.** An operator can file a *grant request* for an organization or personal account with a reason (`gregalectl support request --org <slug> --reason ...`, operator session + MFA). The request mails the owners and admins (personal account: the owner) a dashboard link to approve or decline. A request expires after 24 h, sets nothing, and an organization has at most 3 open requests. Approving a request is the same step-up action as creating a grant from scratch.
  3. **Scope: deploy, rollback and restart only.** The grant names the whole organization or a list of its apps. Under a grant the operator may call only this closed route list (`appaccess.supportRoutes`), not everything `org.deploy_app` covers:
     - `POST /v1/apps/{slug}/deployments/source-ref`: builds the customer's connected repository at a ref.
     - `POST /v1/apps/{slug}/deployments`: only with an `image` already built for this app. A digest from another app or an outside registry is refused.
     - `POST /v1/deployments/{id}/retry`, `POST /v1/apps/{slug}/deployments/{id}/cancel`.
     - `POST /v1/apps/{slug}/rollback`, `POST /v1/apps/{slug}/restart`.
     - The reads those steps need: the app, its deployments, deployment summary, build status and build logs.

     Never under a grant: uploaded source (`source-tarball`, `/v1/uploads`, `dev-source`), secret or environment values or bindings, configuration, domains, edge rules, members, API or deploy tokens, billing, deletion, runtime logs, request data, traces, databases, storage and queues. Deploy fields that weaken a customer control are refused: `override_freeze`, `require_signed: false` and release approvals. Uploaded source is excluded because support-supplied code running with the app's secrets is exactly the exfiltration path this feature must not open.
  4. **Expiry:** 24 h by default and 7 days at most, chosen at grant time. An owner or admin can revoke a grant at any moment, from the dashboard or `gregale support revoke`. A grant ends on its own when it expires, when the organization is suspended or deleted, or when the granting owner/admin loses that role. The person who created it may change.
  5. **Support acts as itself, never as the customer.** The operator calls the normal customer routes from its own session (`gregalectl auth login`) and names the grant on every request with `Support-Grant: <id>`. There is no ambient access: without the header an operator is an ordinary account. `appaccess.authorize` checks the support path first, before the personal/shared split, so it works for personal accounts too. It requires all of the following:
     - a session principal, not a key;
     - the operator allowlist and an enrolled MFA (`requireOperatorSession` + `requireEnrolledOperatorSession`);
     - an active, unexpired grant that covers the app;
     - a route in `supportRoutes`.

     The resulting `Grant` has `Actor` = operator, `Owner` = the app's resource account (so billing and quotas are the customer's as usual) and `SupportGrantID` set. The operator's own memberships play no part.
  6. **Revocation stops work in flight.** `OrgAccessFence` gains `SupportGrantID`. `lockOrgAccess` locks the grant row and refuses a write when the grant is revoked or past `expires_at`, so a revoke cancels a deploy admission that has not yet committed, as member removal already does.
  7. **The customer sees everything.** Every request under a grant writes a `support.action` audit row with the operator, grant, organization, app, route, deployment id and reason. These rows appear in the organization's activity log and the personal account's activity, labelled "Gregale support (<operator name>)". Mail goes to owners and admins when a grant is created, requested, revoked or expires, and on every mutation. Mutation mails are coalesced into one per 10 minutes per grant. `gregale support list` and the dashboard show the open grants, their use and their end time.
  8. **Rollout.** Behind the platform flag `support_grants`, off everywhere, with a kill switch that refuses every grant at once. Before it is enabled on prod, the Terms of Service and privacy notice describe customer-granted support access.
- **Data model:** `support_grants(id, org_id, app_ids[] NULL = whole org, created_by, approved_from_request NULL, operator_scope 'any_operator', reason, created_at, expires_at, revoked_at, revoked_by, ended_reason)` and `support_grant_requests(id, org_id, operator_id, reason, created_at, expires_at, decided_at, decided_by, decision)`. A CHECK caps `expires_at - created_at` at 7 days. Personal accounts are addressed through their personal organization row, so one table serves both.
- **Consequences:**
  - A customer who can't deploy gets help within minutes, without handing out a permanent seat. The "invite support as a member" workaround can be retired from the runbooks.
  - A stolen operator session reaches only the customers that granted access right now, and only deploy, rollback and restart. It cannot read secrets or upload code.
  - Support cannot fix a customer whose problem needs a code or configuration change. That stays the customer's work, deliberately.
  - Two more tables and a fence column. Every new customer route stays outside the support path until it is added to `supportRoutes`, and `route_coverage_test.go` gains a check that each entry is a reviewed route.
- **Slices:**
  1. Schema, store and memstore, the flag and kill switch, customer grant create/list/revoke (API, dashboard, `gregale support`), mails.
  2. Support path in `appaccess`, the `supportRoutes` list, the fence column, `support.action` audit, and the deploy-field and image-origin refusals.
  3. Grant requests (`gregalectl support request`, approve/decline), the `gregalectl` client sending `Support-Grant`, and the runbook.
  4. Project routes (`/v1/projects/{slug}/deploy`, release-set rollback), as a separate reviewed list.

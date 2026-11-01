# ADR: A deploy's event subscriptions take effect when its release is live

Date: 2026-11-01
Status: Accepted
Amends: [ADR 20261031130000](20261031130000-event-subscription-reconcile.md)
Related: staged crons (`deployment_cron_activations`, `cronDeploymentDisposition`)

## Context

ADR 20261031130000 made a production deploy's `gregale.yaml` the app's whole
set of event subscriptions. A review (blocker hunt #6, 2026-11-01) found that
the reconcile ran when the deploy was uploaded, not when its release went live:

- **Releases that are not live yet.** A scheduled release (`--release-at`),
  a `--no-traffic` deploy, and a canary still rolling out all disabled the
  dropped triggers at upload, while the old release still served all
  traffic.
- **Releases that never go live.** A build that failed later, a cancelled
  deploy, and a canary rolled back left those subscriptions disabled for
  the release that kept running.
- **Refused deploys.** A deploy refused after the reconcile (by the quota or
  deploy admission) re-enabled the subscriptions on rollback. Events
  published in between were never captured, and in-flight recipients were
  settled as filtered.

Each case lost events silently. Crons already avoid this: a source deploy's
crons are staged and activated only once its deployment serves all production
traffic.

The same review found three more problems with the reconcile:

- The per-plan caps were checked only on the reconcile path. Batch trigger
  creation and stage deploys could grow an app past them.
- A manifest that still named an app by a slug it had been renamed from
  declared nothing for that app, so every one of the app's subscriptions
  was disabled.
- The acknowledgment expiry sorted rows by account but locked by quota
  subject, so it could deadlock.

## Decision

1. **Upload only adds.** A production manifest deploy creates the
   subscriptions it declares and re-enables declared ones that are disabled,
   as before. It disables nothing at upload. It records its declared set
   (subscription row ids) in `deployment_event_subscription_intents`, in the
   same transaction that creates the deployment; the set is carried to
   creation in the request context, like the submission attempt id.
2. **apid applies the set at go-live.** A worker applies a recorded set
   once its deployment serves all production traffic, under exactly the
   rule staged crons use (`cronDeploymentDisposition`):
   - **Activated:** the deployment is the latest non-dark production
     deployment, live, at 100 % traffic, with no competing live deployment
     and any canary complete. The app's subscriptions are then set to the
     declared set: undeclared ones are disabled, declared ones are enabled.
   - **Discarded:** the deployment is dark, at a stage scope, failed,
     cancelled, superseded or aborted, or a newer deployment exists. Its set
     never applies.
3. **Caps hold on every apply.**
   - A production deploy is counted as its declared set will stand once live.
     The app count is the number declared; the account count is the current
     count minus this app's, plus the number declared. So replacing every
     trigger of an app at its cap is allowed.
   - Every other apply (batch create, stage deploys) counts what is enabled
     after its writes and is refused past a cap.
4. **A manifest naming an unknown app only adds.** If any `event_triggers`
   entry names an app that does not exist in the account, the deploy records
   no intent and logs a warning.
5. **Acknowledgment expiry** sorts by the quota subject it locks.

## Consequences

- Removing or changing a trigger takes effect when the release that drops it
  is live. Until then the old subscription keeps delivering, which is right
  while the old release is the one serving.
- A deploy that never goes live changes nothing it would have disabled. It
  can still have added subscriptions, as deploys always could.
- Rolling back to an older deployment does not reapply that deployment's
  set, matching staged crons. The next production deploy reconciles.
- New table `deployment_event_subscription_intents` (expand). An older apid
  writes no intents, and an older apid's worker does not exist, so mixed
  versions only add.

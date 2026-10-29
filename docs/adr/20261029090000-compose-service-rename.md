# ADR-20261029090000: A renamed Compose service keeps its app

- **Status:** accepted
- **Date:** 2026-10-29
- **Owner:** apid and reconcile maintainers
- **Decision:** A Compose service may declare the name it had before:

  ```yaml
  services:
    accounts:
      build: ./accounts
      x-gregale-renamed-from: ledger
  ```

  When the project has an app for the previous name and none for the new name, the plan and the apply treat the service as an update of that app. The app keeps its ID, slug, environment values, secrets, domains and history; only its service name changes.
  - **One rule.** `reconcile.RenamedWorkloads` decides. The reconcile diff, the plan's create/update/remove partition, slug preservation and the slug-ownership check all use it, so the plan shows exactly what the apply does: one update, no remove and no create.
  - **When it applies.** A hint whose previous name has no app does nothing, which covers the first deploy and later deploys after the rename. A hint whose new name already has an app does nothing either: that app is kept and the previous one is removed, as without the hint. The hint can stay in the file.
  - **Validation.** The scan fails with the reason when the hint names the service itself, names a service still in the file, or names a previous name that another service also claims. It needs a deployable service.
- **Why:** Renaming a Compose service used to remove its app and create a new one. The new app started without the old one's environment values, secrets and domains, and its history stayed with the deleted app. Renaming was the only multi-service change that lost configuration without warning beyond the plan's remove line.
- **Consequences:**
  - The service is called by its new name (`accounts.internal`). Services that call it must list the new name in `depends_on` (Compose requires that anyway). Their injected variable becomes `GREGALE_SERVICE_ACCOUNTS_URL`, so code that reads `GREGALE_SERVICE_LEDGER_URL` must change.
  - The app's slug and public URL do not change. A slug derived from the old name (`shop-ledger`) stays.
  - Settings on other services that name the old service — caller allowlists, call scopes, reliability policies — are the customer's to update.
  - A release set or environment published before the rename names the old service until the next publish. Open PR previews follow at their next push.
  - **Known limitation:** the database keeps one project member per name, including soft-deleted ones. Renaming into the name of a service deleted earlier fails the deploy with a conflict. Restore that service and remove it, or choose another name.
- **Rejected alternatives:**
  - **Detect renames automatically** by matching build context, image or ports. Two services can share them, and a wrong guess would hand one service's secrets to another.
  - **A CLI command to rename a member.** The Compose file is the source of truth. A hint in the file is reviewed with the change that renames the service, and every deploy path reads it.

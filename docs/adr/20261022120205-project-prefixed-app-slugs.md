# ADR-20261022120205: Project-prefixed app slugs for service names that cannot be slugs

- **Status:** accepted
- **Date:** 2026-10-22
- **Decision:** When a new project workload's name cannot be its app slug, the platform gives it the project-prefixed slug `<project>-<name>` instead of refusing the plan.
  - **When.** The name is reserved for a Gregale service (`api`, `billing`, `admin`, …), is not a valid slug (fewer than 3 characters, for example), or is held by another app: another account's, another of the account's own apps, or another account's app in its delete grace window.
  - **Not when.** An existing project member keeps its slug. A slug chosen with `app_slugs` is kept, as are migration-mode suggestions, which already prefix every name. A name held by one of the account's own apps in its delete grace window is not stepped around: the plan keeps offering `gregale apps restore`.
  - **The prefixed slug** is the same `SuggestedAppSlug` that migration scans use: `<project>-<name>` when that is a valid, unreserved slug, otherwise a normalized form with a short digest that keeps distinct names distinct. If the prefixed slug is itself taken, admission reports it as before.
  - **Both paths.** The project scan and plan, the plan's apply and GitHub push reconciliation share the allocation. The scan returns the slug in the plan, and the plan token carries it to the apply.
  - **Names inside the project do not change.** Bindings, `depends_on`, `GREGALE_SERVICE_<NAME>_URL` and the service resolver keep the Compose name. A project member already resolves `<name>.svc.gregale` to its own project's workload before any global slug, so `api` still reaches the project's `api` at `shop-api`.
- **Why:** Compose services are named for what they do, and the common names (`api`, `billing`, `admin`, `web`, `worker`) are exactly the ones that are reserved or already held by someone else's app. Today such a project fails its first plan with "reserved for a Gregale service" or "already taken", and the developer has to rename the service or map every name by hand. Docker Compose scopes names to the project; slugs are global only because they name the app's hostname.
- **Consequences:**
  - A first deploy of a project with an `api` service succeeds, and the plan shows the app as `shop-api`. Its public hostname is `shop-api.<domain>`.
  - Which slug a new workload gets can depend on what other accounts hold at plan time. Once allocated, the slug is the member's and never changes on a later scan.
  - Two workloads whose names prefix to the same slug still fail admission as duplicates.
- **Rejected alternatives:**
  - **Prefixing every workload.** It would rename the hostname of every new single-service and conflict-free project, which works today.
  - **Per-account or per-project slugs.** Slugs name public hostnames and every app API route; making them non-global is a much larger change than the collision it fixes.
  - **Stepping around the account's own deleted app.** It would leave the restore offer pointing at an app whose name the project no longer uses.

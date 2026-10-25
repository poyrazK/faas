# ADR-20261025093331: A project file for repositories without Compose, and whole-project previews

- **Status:** accepted
- **Date:** 2026-10-25
- **Amends:** ADR-20261022111119 (project service policy), ADR-20261022131852 (internal Compose services) and the PR-preview dependency closure (`pkg/githubd/preview_dependencies.go`)
- **Decision:**
  1. **`.gregale/project.yaml`.** A repository can declare, at its root, what Compose declares for its own services:

     ```yaml
     service_policy: project        # account | declared | project, for every service
     services:
       billing:
         visibility: internal       # public | internal, when the app is created
         service_policy: declared   # this service only
         depends_on: [ledger]
     ```

     - It applies to every workload the scan finds, whatever detected it: workspace members, `services/*` folders, Compose services.
     - Compose stays the more specific source. A service or top-level `x-gregale-service-policy` wins over the file's policy, and a Compose `depends_on` is merged with the file's, not replaced. `visibility` overrides the Compose default that a service without `ports:` is internal.
     - Visibility applies only when the app is created, as for Compose (ADR-20261022131852). Policy and `depends_on` are source declarations: they update existing apps on the next apply, as Compose keys do. The API keeps refusing these fields on project apps, as "source-managed".
     - The file is strict. An unknown key, an unknown service name, or an invalid policy or visibility fails the scan with the file and the name. `depends_on` targets go through the existing plan gate: unknown services, self-dependencies and cycles are rejected there.
  2. **Whole-project previews.** A PR preview of a service whose effective policy is `project` runs every deployable service in the project at the PR head, not only its `depends_on` closure. Other policies keep the closure.
     - Sibling previews come from the same PR head, and the existing all-or-nothing reservation counts them against the plan's PR-preview allowance. A PR whose project exceeds the allowance fails as the closure does today.
     - Managed datastores are still not previewed.
- **Why:**
  - Repositories without Compose (workspaces, `services/*` folders) had no way to configure their services. Every service was created public. A project created before the `project` default stayed on `declared` with nothing to declare, because the API refuses these fields on project apps. Its services could not call each other and there was no way out.
  - Under the `project` policy, services call siblings without `depends_on`. A preview ran only the changed service, so every sibling call fell back to production and was refused by default (`preview_production_dependency_denied`). The preview could not work.
- **Consequences:**
  - A repository without Compose can keep a service off the internet, switch an existing project to the `project` policy, and declare dependencies. A declared dependency also gives the caller its `GREGALE_SERVICE_<NAME>_URL` and includes the dependency in a `declared`-policy preview.
  - A `project`-policy preview costs one preview app per service. Previews scale to zero like every app, but a large project uses more of the PR-preview allowance.
  - githubd reads the file from the PR head with the rest of the scan, so the preview set follows the branch under review.
  - Rolling upgrades: an older apid or githubd ignores the file, so its scan has today's behaviour. The file adds no wire fields.
- **Rejected alternatives:**
  - **Allow PATCH of service policy on project apps.** The next apply would reconcile it back from source, so the API and the repository would disagree.
  - **Previews follow the learned call graph** (service map or wake-ahead edges). That isn't known for a new service or a first PR, and it would make the preview set depend on production traffic.
  - **Reuse `gregale.yaml`.** It configures one app from its own root; a project-level file at the repository root keeps the two scopes apart.

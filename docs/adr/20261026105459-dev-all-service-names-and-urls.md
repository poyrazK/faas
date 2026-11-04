# ADR-20261026105459: `gregale dev --all` members use their service names and get service URLs

- **Status:** accepted
- **Date:** 2026-10-26
- **Amends:** ADR-20261025121530 (`gregale dev --all` runs its apps in one developer namespace)
- **Decision:** Each `gregale dev --all` member joins its group's namespace under the name the other services call it by in production. Each member also gets the service URL variables for the group services it declares it calls.
  - **Names.** A member registers under its scanner service name, lowercased, when that is a service DNS label (1 to 63 lowercase letters, digits and inner hyphens; the gateway's `<name>.internal` and `<name>.svc.gregale` rule). Otherwise it registers under its developer project name, and the CLI says so. Before this, members registered under the project name, which `gregale dev` pads to 3 characters (`ui` became `app-ui`), so a short service could not be called by name.
  - **Namespace rule.** A plain run-namespace member (no webhook capture, network containment, simulation, cold-start, suspend or managed-work enrollment) may now use any service DNS label. This needed the `scenario_test_members.workload_name` check widened from the app-slug rule (3 to 40 characters), in a `contract` migration: add the new check `NOT VALID`, validate it, drop the old one. Members enrolled in rehearsal plans keep the slug rule, which their action policies and plans are written for.
  - **Service URLs.** The supervisor maps each member's declared dependencies (Compose `depends_on`) to the group's service names. A dependency outside the group, such as a database image, is skipped. Each child sets `GREGALE_SERVICE_<NAME>_URL`, `_HTTPS_URL` and `_HOST` as developer config, with the same values a deployed project service gets for its `depends_on`. They resolve inside the group.
  - **Hand-off.** The supervisor passes each child its service name and dependencies in `GREGALE_DEV_GROUP_SERVICE` and `GREGALE_DEV_GROUP_DEPENDS`, beside the group workspace.
- **Why:** The ADR-20261025121530 group made names resolve inside the group, but code still differed from production in two ways. A short service such as `ui` was reachable only as `app-ui`. Code that reads the injected `GREGALE_SERVICE_<NAME>_URL` found nothing.
- **Consequences:**
  - Calls by production service name work in the group for every service whose name is a DNS label. Names that are not (`Billing_API`) cannot be called in production either; in the group they get the project name.
  - The service URL variables are developer config on the developer environment. They are not manifest bindings, so they do not change its service policy, and a developer can override one with `gregale secrets set`. Setting them needs the same scope as `--env-file`. Without it the CLI warns and the names still work.
  - Rolling upgrades: an API that predates this change refuses a non-slug name with 400. The CLI then registers that member under its project name and warns, which is the previous behaviour. The migration is `contract`, so short names work once a full release applies it.
- **Rejected alternatives:**
  - **Map short names in the gateway** (look up `app-<name>` when `<name>` is missing). That ties the gateway to a CLI naming detail and would reach an unrelated `app-ui` service.
  - **Set manifest service bindings on developer environments.** Their manifests are rewritten by each developer sync, and bindings would change their call policy.

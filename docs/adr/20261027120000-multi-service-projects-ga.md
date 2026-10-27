# ADR-20261027120000: Multi-service projects for general availability

- **Status:** proposed (merges when the gates below are met)
- **Date:** 2026-10-27
- **Owner:** apid (projects, bindings) and gateway (service proxy, wake-ahead) maintainers
- **Amends:** ADR-20261022111119 (project service policy), ADR-20261025105812 (wake-ahead default), ADR-20261026170512 (service DNS names)
- **Decision:** Four capabilities move to `ga` in `pkg/productcap/catalog.json`:
  - `projects` (from `preview`). A Compose file or a repository of several services deploys as one project. Its docs URL moves from `/docs/apps` to `/docs/multi-service`.
  - `internal-services`, `service-bindings` and `service-wake-ahead` (from `beta`).

  **Acceptance evidence.** `cmd/e2e/multi_service_project_e2e_test.go::TestE2E_MultiServiceProject_ComposeServicesCallEachOtherByName` is the acceptance test for `projects`, `service-bindings` and `service-wake-ahead`. `internal-services` keeps the service mesh e2e. The multi-service test runs real apid, schedd, gatewayd-public and gatewayd-internal against PostgreSQL with the fake vmmd. It gates every Go pull request (e2e-general `multi-service` suite) and checks:
  - Compose apply;
  - injected service URLs and imported Compose addresses;
  - the project's call graph;
  - a call that wakes a parked service;
  - the declared-policy refusal;
  - wake-ahead of a declared dependency;
  - a PR preview reaching its own sibling.

  **Ownership stays with preview capabilities.** Before this change, `projects` owned more than projects through prefixes:
  - the org-scoped environment routes (`/v1/orgs/{}/projects/{}/environments/…`);
  - the `gregale environment` commands (formerly `gregale projects environments`);
  - the dashboard GitOps page.

  Promoting it would have released preview environments, promotions, release sets and GitOps to every account. These now belong to `environments`, `environment-promotions`, `environment-release-sets` and `environment-gitops`, the same capabilities as their non-org twins. The public SDK spec gains only the 18 project paths.

  **Rollback.** Each capability keeps its platform flag. `gregalectl flags disable --capability <id> --reason … --yes` hides it again for every account. Narrower rules (`deny --account`, `--org-id`) hide it for one customer. The [multi-service recovery runbook](../ops/multi-service-recovery.md) covers diagnosis.
- **Gates before this merges:**
  1. Native acceptance evidence: the e2e-native lane green on a native x86_64 KVM host, including a guest that resolves `<service>.svc.gregale` and calls through the tenant bridge. The KVM-free test cannot exercise that path.
  2. The plan entitlement for Free (owner decision). Free allows one deployed app, so it cannot deploy a project of two or more services. Either drop `free` from the four capabilities' plans, or change the Free app limit in the financial model and `pkg/api/limits.go`.
  3. Datastores other than Postgres (owner decision). A Compose `redis`, `mysql`, `mongo`, `kafka` or similar image service is listed in the plan with the variable the customer must supply (`REDIS_URL`, `DATABASE_URL`, …) and is not provisioned. Only Compose `postgres` becomes a managed database (ADR-20261026151530). Either GA documents this, or it waits for managed Redis and MySQL.
  4. Rollout of #5651 and #5676 to every node's gatewayd-internal. Until then, a declared-policy caller and a project with a published release set are refused service calls by DNS name.
- **Why:**
  - The multi-service path now has end-to-end evidence on every pull request. Writing that evidence found two production defects (#5651, #5676), and both are fixed.
  - An end-to-end review found no other code-side blocker. It covered name matching, guest DNS, redeploy, previews and project deletion.
  - `projects` at `preview` keeps its platform flag off by default, so `gregale deploy` of a Compose repository is unavailable to accounts the operator has not released.
- **Consequences:**
  - **Flags.** GA and beta capability flags are on for every account (`platformflags.TierDefault`). Promoting `projects` from `preview` releases it to every account that has no stored flag definition. Accounts with an operator-written rule keep it. The three `beta` capabilities were already on, so their flags do not change.
  - **Billing.** Each project service is an app and is billed as one (plan RAM + 8 MB per running second). Projects add no billing line.
  - **Documented behaviours** (`docs/multi-service-apps.md`):
    - renaming a Compose service replaces its app;
    - `gregale project delete` detaches the services instead of deleting them;
    - a PR preview starts without the production environment values (ADR-310).
- **Rejected alternatives:**
  - **Promote only `projects`.** Its value is the calls between services; leaving bindings and wake-ahead in beta would put GA deploys on beta call paths.
  - **Use native evidence only.** The KVM-free test runs on every pull request and found both defects; the native lane cannot run per pull request.

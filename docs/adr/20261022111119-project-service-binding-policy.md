# ADR-20261022111119: Same-project service calls by default

- **Status:** accepted
- **Date:** 2026-10-22
- **Amends:** ADR-266 (new project workloads default to `declared`): new project workloads now default to `project`
- **Decision:** A new caller policy `project` lets a workload call any service in its own project without declaring it. Calls to services outside the project still need a declared binding, as under `declared`.
  - **Who counts as the same project.** The caller and the target have the same non-empty project. A preview counts only where the preview rules already let it reach the target: a same-PR preview, or the production service when the project's preview policy allows production fallback. A scenario-test member never counts; it keeps the declared check against its run's workload names.
  - **What still applies.** Every other check is unchanged and runs in the same order: the account boundary, deleted apps, rehearsal and scenario-test isolation, the project preview policy, the target's `x-gregale-allow-callers` and caller scopes, and service reliability. A target that lists its callers still refuses a sibling that is not on its list.
  - **Default.** A new project workload whose Compose file sets no `x-gregale-service-policy` gets `project`, from the second release below. Compose accepts `x-gregale-service-policy: project`. An existing workload keeps its stored policy until its repository sets one; a top-level `x-gregale-service-policy` sets it for every service of the project at once (slice 3).
  - **Standalone apps** have no project, so the standalone API keeps accepting only `account` and `declared`.
  - **Discovery is unchanged.** `depends_on` still injects `GREGALE_SERVICE_<NAME>_URL` and the binding-scoped `.internal` alias (ADR-271). An undeclared sibling is reached by its `<slug>.svc.gregale` name.
  - **Reviewed GitOps environments** keep their exact reviewed binding graph, which already needs a binding per edge, whatever the caller's policy.
  - **Release order.** The policy lives in the manifest JSON, so no migration is needed. A release from before this change reads `project` as an unknown policy, which `Effective()` treats as `declared`: a gateway enforces it as `declared`, stricter and never looser, but a control plane that reapplies the project would store `declared`, and that narrowing would outlive its rollout or rollback. So the change ships in two releases:
    1. Every unit learns `project`: the gateway enforces it, Compose accepts it as an explicit opt-in, and the CLI, API and SDKs show it. Reconcile also keeps a stored policy it does not know instead of rewriting it, so a later policy survives a rollback to this release. An explicit opt-in on a previous control plane fails the scan loudly instead of narrowing silently.
    2. Once every unit runs the first release, new project workloads default to `project`.
- **Why:** Under `declared`, every call between two services of one project needs a `depends_on` line, even though one team deploys both from one repository and the account boundary already isolates them from everyone else. Docker Compose lets services on a project network reach each other by default, and a project's services are the same kind of trust group. Requiring declarations inside it makes a first multi-service deploy fail with `service_binding_denied` until the developer lists every edge.
- **Consequences:**
  - A new project's services can call each other as soon as they are deployed. A team that wants an edge-by-edge policy sets `x-gregale-service-policy: declared`, or limits the target with `x-gregale-allow-callers`.
  - `gregale bindings service explain` reports a same-project call as allowed by `same_project`, and the plan output names the policy. The binding inventory reports a `project` caller's bindings as `declared`, not `enforced`, because they are not what admits its same-project calls.
  - A workload moved into another project loses its undeclared calls to its old project's services.
- **Rejected alternatives:**
  - **Making `account` the default again.** It would let any service in the account call another project's services, the reach ADR-266 removed.
  - **Synthesizing a binding for every sibling.** It would inject a URL per sibling into every workload's environment, count against the plan's environment variable limits, and make every new service a deploy-wide diff.
  - **Switching existing workloads silently.** It would widen reach that a customer reviewed under `declared`, the breaking change ADR-266 rejected.
  - **Scoping `project` to one environment.** An app carries no environment: a project's environments are deployments of the same app rows, and reviewed GitOps environments already bind edge by edge. The project is the trust boundary that exists today.

## Implementation slices

1. The `project` policy: gateway enforcement, Compose opt-in, reconcile keeping unknown stored policies, CLI explain and plan output, OpenAPI and SDKs, docs.
2. New project workloads default to `project` (after slice 1 is on every unit).
3. A top-level `x-gregale-service-policy` in the Compose file sets the policy of every deployable service that sets none. It is how an existing project moves all its workloads over in one reviewed change, and it ships under slice 2's release order: a previous control plane ignores the top-level key and would reapply a stored `project` as `declared`.

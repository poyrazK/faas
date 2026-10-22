# ADR-20261022160248: `.internal` names every service of the caller's project

- **Status:** accepted
- **Date:** 2026-10-22
- **Amends:** ADR-271 (aliases follow the caller's binding inventory): under the `project` policy they also follow the caller's project
- **Decision:** A caller whose service policy is `project` (ADR-20261022111119) can use `<name>.internal` for every service of its own project, not only those it declared.
  - **Which names.** `<name>` is a service's repository name, or its app slug, as `<name>.svc.gregale` already resolves for a project member. The service must be in the caller's project and account and not deleted.
  - **Who.** Callers under the `project` policy. A PR preview names its project's services; the resolver then routes to its own PR's sibling or to the production fallback the preview policy allows. Scenario-test members, and callers under `declared` or `account`, keep the binding inventory. A qualification graph keeps its scoped aliases.
  - **One check, two paths.** The node-local DNS answer and the proxy's `Host: <name>.internal` check share `NewAliasAllowed`, so a guest that skips DNS gets the same answer. The proxy's authorizer still runs afterwards: account, previews, the target's caller list and scopes.
  - **TLS.** The private HTTPS listener serves one certificate for the `.internal` constraint, so no certificate changes.
  - **Release order.** A gateway from before this change keeps answering only declared names, which leaves an undeclared sibling's `.internal` name unresolved, as today. No stored data changes.
- **Why:** The `project` policy lets a service call its siblings without `depends_on`, but only through the long `<name>.svc.gregale:10081` name. The short `https://billing.internal` name, the one guides recommend and private HTTPS uses, still needed a declaration. That kept the declaration the policy removed.
- **Consequences:**
  - In a project under the `project` policy, `https://<service>.internal` works for every sibling.
  - DNS answers for these names cost one project lookup per alias query, after the existing short-lived caller identity cache.
  - A customer DNS name `<name>.internal` that matches a sibling's name is now answered by Gregale for that caller. Names that match no sibling still go to the upstream resolver, as ADR-271 requires.
- **Rejected alternatives:**
  - **Answering every `.internal` name for project callers.** It would capture the customer's own `.internal` namespace, which ADR-271 rejected.
  - **Injecting a URL variable per sibling.** It would grow every workload's environment with each new service (ADR-20261022111119 rejected this too).

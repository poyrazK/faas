# ADR 1544: Environment-scoped MCP readiness policies

Status: Accepted

Readiness policies and their audit history are keyed by app and deployment scope.
Each scope has its own revision CAS, endpoint, expected catalogs, roles, maximum
age and monitoring settings. Existing policies and history remain under `default`
without revision changes or evidence invalidation. Policies are not inherited.

Traffic increases select the target deployment's policy in memory and in the
PostgreSQL trigger, including direct SQL. In apps with any configured readiness
policy, an unconfigured nondefault scope requires explicit configuration before
new traffic is admitted. Apps with no readiness policy retain opt-in behavior.
An explicitly saved disabled environment policy opts that scope out. Existing
serving traffic and traffic decreases are unaffected.

Probes, evidence, monitor claims/completions and one-use emergency overrides derive
their policy from the owned deployment's scope. Staging edits invalidate only
staging evidence and overrides, preserving production evidence and approvals.
Per-environment catalog expectations and OAuth connection revisions can differ.
Legacy OAuth maps may remain in stored policies; only the policy's own scope is
selected and validated. A missing managed OAuth reference fails closed.

A shared PostgreSQL advisory lock serializes traffic checks and evidence writes
against policy creation/updates. Policy mutations take its exclusive counterpart.
This protects first configuration and missing-policy decisions without adding an
app-row lock to deployment-trigger paths that could invert traffic update locks.

Policy GET accepts `?scope=`; PUT accepts a body scope and optional matching query.
Omission means `default`. Deployment routes infer scope from the deployment ID.
List results pair a single scope's policy with that scope's evidence and overrides.
Monitoring alerts retain their app-wide aggregation, with scope on each status.
Dashboard readiness has an environment selector. CLI policy get/set accepts
`--scope`, and release verification selects the candidate's environment policy.

Migration replay preserves subsequent function definitions. Rollback refuses
retained nondefault policies, history, evidence or overrides, then restores the
predecessor trigger and fingerprint functions. No grants or tokens are copied,
and migration does not move traffic or contact providers.

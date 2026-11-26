# ADR-20261122004000 · Record project deploy membership for rollback

- **Status:** accepted
- **Amends:** [ADR-20261105104500](20261105104500-project-rollback-gate-pins-and-push-addresses.md)

Production project applies and GitHub project pushes record a shared deploy
identity when they create each service revision. GitHub delivery retries reuse
the identity. A service build outside that fan-out has its own independent
history. The rollback planner uses the recorded deploy membership and the
serving predecessor captured by the deployment recovery lineage. If the
current rollback target differs from the recorded predecessor, the CLI stops
and shows the mismatch.

Project rollback still runs callers before dependencies and pins each app
rollback to the exact serving and target revisions. The CLI saves the ordered
plan on the authenticated connection before submitting any rollback. Each step
waits for its committed receipt before the next starts. Resume uses the saved
operation ID, checks the project and serving revisions again, and follows any
accepted service rollback. A completed rollback step is retained in the plan.

Older deployments lack recorded membership. Operators can select exact app
revisions or explicitly plan each service with `--all`. Atomic release sets
use the environment release-set rollback workflow.

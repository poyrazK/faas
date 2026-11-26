# ADR-20261122095000 · Check project rollback blockers before execution

- **Status:** accepted
- **Follows:** [Exact project deploy rollback](20261122004000-exact-project-deploy-rollback.md)

Project rollback checks the exact current and target revisions of each pending
service with the API before it saves or executes a new plan. Dry run and
execution display the same observed blockers. A retry checks remaining
services and follows the receipt for any step already in flight.

The read-only endpoint reuses artifact, approval, migration and API-contract
checks from checked rollback. It also checks serving traffic, active rollback
intent, release-graph ownership, reclaimed layers, and the binding release
policy. Live targets can use current binding observations. The plan reports
when a cold target needs preparation before binding verification. A check
creates no rollback receipt. The existing transaction and routing worker apply
their admission and cutover checks again.

package api

// DeadNodeRecoveryHandoffMaxSeconds bounds how long the dead-node sweep leaves
// a running app row on a recovery-managed node (draining, force_draining,
// unavailable, recovering) to the recovery arbiter. The arbiter's live
// migration needs a reachable source; once the node has not heartbeated for
// this long no handoff can succeed, and the arbiter only retries, so the
// sweep fails the row itself. Before this bound such a ghost stayed RUNNING
// (billable, holding its admission slot) until an operator or a release
// moved it: about 24 h on prod on 2026-11-19 (account usage audit
// 2026-11-21).
const DeadNodeRecoveryHandoffMaxSeconds = 30 * 60

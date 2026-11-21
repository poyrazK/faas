# Post-mortem — 2026-10-22 — fsn3-base-generation-wakes

- **Incident commander:** Gregale maintainer on the rc.290 rollout
- **Severity:** SEV2 (one of two compute nodes refused every new wake for eight minutes; running guests kept serving)
- **Detected:** 2026-10-22T15:32:50Z (rollout attempt 1 of cd-platform run 39790744596 failed)
- **Resolved:** 2026-10-22T15:40:00Z (end of the failure window recorded in ADR-20261024085817; the rollout completed at 16:01:49Z)
- **Status:** draft

## Summary

The rc.290 full release changed the `base-minimal` runtime base generation
(`db529ed…` → `5ca69ea…`). On production-us, fsn-3's rc.290 imaged pre-stage
ran while the node still served on rc.288. It replaced the node's cached base
and marked the cache with the new generation, which the serving rc.288 vmmd
could not boot. The node's drain then timed out, because one busy instance
stayed RUNNING for the whole 10-minute wait: a draining node retried a failing
live migration forever and still thawed, resumed and froze VMs in place. The
rollout's rescue re-activated fsn-3 on rc.288 at 15:32, and from then until
15:40 every wake placed on fsn-3 failed with 503 `scan_critical` ("could not
adopt its configured generation"). Running guests were not affected. A second
rollout attempt, started at 15:36:31, completed the release at 16:01:49. #5278
(ADR-20261024085817) keeps a node off placement when its cache holds a
generation its vmmd cannot boot, and makes graceful drains finish.

## Impact

- **Customers affected:** not measured. Any app whose wake was placed on fsn-3 between 15:32 and 15:40 UTC; running instances on both nodes kept serving.
- **Duration:** 8 minutes, 2026-10-22T15:32:00Z to 15:40:00Z.
- **Requests or workloads affected:** cold boots, snapshot restores and migration adoption on fsn-3 were refused with HTTP 503 `scan_critical`. The count of refused wakes was not recorded; it can be read from the `wake.*` event rows for fsn-3 in that window (action item 4).
- **Data integrity:** no customer data was written or lost. vmmd refused the bring-up before starting the VM; snapshots and app state were untouched. The node-local base cache held the rc.290 generation, which the completed rollout then made correct.

## Timeline (UTC)

| Time | Event | Owner / evidence |
|---|---|---|
| 2026-10-22T13:52:27Z | cd-platform run 39790744596 starts the rc.290 full release, one compute node at a time | GitHub Actions run 39790744596, attempt 1 |
| 2026-10-22T14:35:00Z | fsn-2 rolls to rc.290 (until 14:59); its pre-stage publishes `5ca69ea…` to the shared logical key `base/base-amd64.ext4` | ADR-20261024085817 |
| 2026-10-22T15:14:46Z | fsn-3 runs the candidate rc.290 imaged pre-stage while serving on rc.288 (until 15:19:39); the node's cache marker moves to `5ca69ea…` | ADR-20261024085817 |
| 2026-10-22T15:20:11Z | fsn-3 drain begins; `drain-status` reports one instance still on the node for twelve minutes | ADR-20261024085817 |
| 2026-10-22T15:32:18Z | The rescue re-activates fsn-3 on rc.288 and the run aborts; wakes placed on fsn-3 start failing 503 `scan_critical` | ADR-20261024085817 |
| 2026-10-22T15:32:50Z | Rollout attempt 1 completes as failed | run 39790744596, attempt 1 |
| 2026-10-22T15:36:31Z | Rollout attempt 2 starts | run 39790744596, attempt 2 |
| 2026-10-22T15:40:00Z | Wake failures on fsn-3 end | ADR-20261024085817 incident window |
| 2026-10-22T16:01:49Z | Attempt 2 completes the rc.290 release successfully | run 39790744596, attempt 2 |
| 2026-10-24 | #5278 merged with ADR-20261024085817 | commit 52f38d6b59 |

## 5-Whys

1. **Why did the customer-visible impact occur?** fsn-3 was put back into placement on rc.288 while its node-local base cache held the rc.290 `base-minimal` generation, so its vmmd refused every new bring-up on that base.
2. **Why was that condition possible?** The candidate release's imaged pre-stage ran on a node still serving the old release and replaced the cached bytes and generation marker of the shared logical key. The rescue path re-activated the node without checking whether its cache still held a generation the serving vmmd could boot.
3. **Why did existing detection or controls not prevent it?** The rollout's gates watched the drain and the release install, not the cache generation versus the serving vmmd. Phased mode and no-drain restarts had the same exposure. Whether `FaasBaseScanRefusingBoots` paged is not recorded (action item 3).
4. **Why did the response take as long as it did?** The drain never finished: a draining node retried a failing live migration forever and kept thawing, resuming and freezing VMs in place, so the run waited out its 10-minute bound before the rescue ran. Recovery then needed an operator to start a second attempt.
5. **What systemic change prevents recurrence?** `vmmd base-generation-check` gates every re-activation on the cache holding only generations the serving vmmd can boot; a changed generation pre-stages behind the drain barrier; drains park what they cannot hand off within a bound (#5278). #5340 (ADR-20261024191249) then made the pre-stage publish every configured base and gated activation on adoption, after the rc.293 rollout of 2026-10-24 hit a related gap.

## Action Items

| # | Action | Owner | Due | Issue / status |
|---|---|---|---|---|
| 1 | Re-activation only on a cache generation the serving vmmd can boot; changed generations pre-stage behind the drain barrier; drains finish within a bound | Platform | 2026-10-24 | #5278 merged (52f38d6b59) |
| 2 | Pre-stage publishes every configured base; activation waits for adoption (rc.293 follow-up) | Platform | 2026-10-25 | #5340 merged (cb982792ad) |
| 3 | Confirm `FaasBaseScanRefusingBoots` pages on a node refusing wakes after a rollout rescue, and add a rollout gate that fails the run while any node refuses bring-ups | Platform | 2026-11-01 | open |
| 4 | Count refused wakes and affected apps for 15:32–15:40 from fsn-3's `wake.*` events and record them in this post-mortem | Platform | 2026-10-29 | open |
| 5 | Run the drain and rescue paths of a generation-changing release on the internal test node before the next production base change | Platform | before the next base generation change | open |

## Customer Comms

None recorded. The failures hit new wakes on one of two compute nodes for eight
minutes while running instances kept serving, and no customer report is
recorded. Whether the public status page showed the window is not recorded; if
action item 4 finds affected customers, a short incident note on the status
history should follow.

## Lessons

- A rollout's rescue must re-check the same readiness it checks at
  activation. Re-activating a node "as it was" assumed its local state had not
  changed, and the candidate's pre-stage had changed it.
- Shared logical keys for node caches need a generation check on every node
  that reads them, not only on the node that publishes.
- Drains need a bounded end state (park what cannot be handed off) so a single
  stuck migration cannot hold a rollout until its timeout.
- The same class recurred two days later in the rc.293 rollout (ADR-20261024191249);
  a generation-changing release should be rehearsed off production first.

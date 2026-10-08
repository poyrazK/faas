# ADR-789: Recover startup row charges after an original stop

Date: 2026-10-08
Status: Implemented locally; native lifecycle acceptance pending.

## Context

ADR-788 can bind a live original native owner to a startup row shadow. A durable
first fence deliberately excludes that owner from advisory matching. A scheduler
restart after a lost native reply consequently reconstructs two charges that its
accepted original stop cannot both clear. If a stop was accepted before a failed
scheduler row write, restart reconstructs only the row charge; native dispatch
must not be repeated to remove it.

## Decision

Retirement replay verifies the stored capacity-stop envelope against the
configured cell verification anchors and requires its complete signed claims to
equal the retained stop, including both original fences and the full permit.
Changed or unverifiable proof returns an error without native dispatch, new
signing, replacement fences or local accounting release. Unissued cancellation
retains its existing separate contract.

A successful complete startup traversal is recorded under the ledger lock. This
flag is cleared while seeding and on recovery failure. It grants no admission
authority and does not clear any existing barrier. After an authenticated
accepted original stop has removed its exact physical hold, the scheduler may
retire an unbound row shadow only if this traversal succeeded, the current row
and captured reservation retain the exact lookup spelling, app, deployment,
node and physical run, and no other retained physical attempt shares its
instance UUID. Another permit, host epoch, unknown source or lookup spelling
remains a separate origin and refuses that release. Ordinary reservations,
different bound sources and app-task sources cannot use this path.

The shadow is released before the conditional row write. A write error cannot
restore verified physical residency; its retained accepted receipt can replay
without another native RPC. The row writer still checks the full original source
and state transactionally. Replacement rows remain unchanged and charged. No
source is reconstructed from mutable rows, and no native observation or terminal
state authorizes accounting release.

The cell journal continues retaining its first accepted envelope across signer
rotation. Scheduler replay requires the historical public verification anchor
alongside the current signer anchor while an original retirement may still need
replay. Removing that historical anchor retains the row charge; recovery cannot
replace the original proof or infer physical absence to bypass missing trust.

## Validation

Exercise actual authenticated transport with a new scheduler/ledger after a
first fence, lost native reply and accepted stop followed by a failed row write.
Verify unchanged first host/cell fences, no repeated native dispatch for accepted
stops, unchanged accounting before accepted proof, and complete matching charge
release after it. Test tampered signatures, changed stored claims, valid signatures
for different runs, signer rotation with and without historical trust, replacement
rows, incomplete recovery, competing physical holds and app-task row refusal.
Portable checks are not native process acceptance.

## Remaining work

Complete physical inventory including historical helpers, current task ownership,
safe live-charge deduplication, native first-fence recovery, snapshot capture/pause/
migration and latency qualification, global authorization/billing, independent
regional provisioning/stores/replication, actual cached global ingress serving and
dedicated native Linux x86_64 KVM metal/leakcheck/fleet recovery remain required.
All-selected-regions remains the routing default; regional admission stays frozen.

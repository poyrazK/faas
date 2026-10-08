# ADR-658: Durable global ingress publication

Date: 2026-10-08
Status: Implemented locally; production serving qualification remains pending.

## Context

ADR-656 consumes short-lived signed whole-directory projections for
`all_selected_regions`; ADR-657 verifies exact regional traffic origins. Neither
owns their producer. A private process counter loses its watermark on restart;
a slower process can overwrite a newer file. Accepted quota receipts and quota
delivery consent do not qualify native admission or global billing.

## Decision

APID on the unbound global directory database owns the publisher. It reuses the
immutable directory issuer and configured directory signing key. A bounded
catalog contains every global app, including an empty route for apps with no
candidates. Candidate selection requires a current enabled registry generation,
an explicit apps domain, exact selected binding and original quota snapshot,
the latest desired quota generation, issued unreleased quota, an unretired
allocation, live personal owner entitlement matching the immutable quota policy,
and active unexpired original consent. Shared owners remain unavailable for
positive serving until their execution authorization and billing are implemented.
Capacity holds and original consent/grant records are unchanged.

Publication probes selected candidates off the request path with a dedicated
signed `ingress-status-probe` challenge over the registered HTTPS management
origin. `POST /internal/v1/ingress-status` is a read-only daemon protocol. It
requires pinned directory trust, bounded canonical input, the original grant and
consent, fresh UUID nonce and timestamps, exact cell/region/API origin and app
traffic origin. No customer credential is sent. Redirects are refused. The
regional API checks retained delegated receipt provenance, current head, original
local live consent, current personal app/account, entitlement, public visibility,
and the central regional admission guard. It has no native private key and
never calls VMMD. A signed probe cannot verify as a quota grant or ingress
snapshot and cannot launch, retire, release capacity or bill anything.

The current central admission guard unconditionally freezes every accepted
stream. The production status endpoint therefore returns `admission_frozen`
with `Healthy=false` and `Eligible=false`; an unexpected guard success also
fails closed. There is deliberately no operator readiness switch. Positive
regional observations require subsequent native admission, health and global
billing qualification, including all execution paths. Synthetic ready responses
in protocol tests demonstrate producer/consumer behavior only.

Responses echo the complete challenge and bound observation time and expiry.
Only exact fresh responses can populate eligible targets. Unavailable, redirected,
malformed, mismatched, unsupported or expired observations leave that target
ineligible while other selected eligible regions continue serving. Each resulting
lease is at most 30 seconds and never exceeds an eligible observation or consent
expiry. Existing global UUID app-host addressing and all-selected routing remain
unchanged.

After probing, the store captures the entire catalog again and requires exact
canonical equality. PostgreSQL briefly takes SHARE table locks on the relevant
personal entitlement, directory intent, issuer, quota and consent tables so
insert/delete phantoms and concurrent mutations cannot slip through publication.
No regional I/O occurs while locked. Memory uses its directory mutex and current
account adapter. Any catalog change aborts the candidate publication. Table-wide
locking is a correctness-first bounded producer tradeoff; high-volume directory
work will need a revision journal before relaxing it.

A singleton publication journal persists monotonically increasing generation and
the exact signed envelope in one transaction. Its triggers enforce directory
scope, pinned issuer and generation increment; deletion/truncation are rejected.
Rollback rejects any populated watermark, preventing same-issuer generation
reuse. A second serialized operation rereads the latest committed publication
before exporting it. Signing failure rolls back without export; export failure
retains the committed bytes. Competing publishers cannot export their old private
envelopes. Restarts continue the durable generation. Restoring an older directory
backup still requires an explicit issuer migration; this is not proof of durable
consumer watermarks across gateway restarts.

The publisher writes a complete protected regular file with an exclusive
same-directory temporary, fsync, atomic rename and directory fsync. It requires a
canonical absolute path, an existing publisher-owned directory with no writable
group/other bits or symlink ancestors, and refuses unsafe existing file shapes.
The output is 0640 for a dedicated read-only gateway group. All bounds and worker
intervals live in `pkg/api/limits.go`. APID opts in with
`[global_ingress_publisher].manifest_path`; regional APID cannot publish.

## Validation

Tests cover memory and real PostgreSQL publication contracts, directory mutation
during probing, concurrent producers, restart generation continuity, retained
commit after sink failure, all-selected producer-to-consumer routing, regional
frozen observations, substituted/expired/foreign status, redirect refusal,
owner/plan/consent/quota/registry gates, signature purpose isolation, and protected
atomic file handoff. These tests require no native lifecycle changes and provide
no native KVM acceptance evidence.

## Remaining work

The full multi-region goal remains active. Complete physical inventory and
original source/run/task ownership, first-fence capacity reconciliation, native
snapshot capture/pause/migration and immutable-input latency qualification,
shared authorization and global billing, independent regional provisioning and
stores/replication, public ingress host/session/client identity qualification,
and dedicated native Linux x86_64 KVM metal/leakcheck/fleet recovery acceptance
remain required. No live configuration, infrastructure or deployment changes
are made by this increment.

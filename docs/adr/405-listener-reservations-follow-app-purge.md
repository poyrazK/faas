# ADR-405: Listener reservations follow the app restore and purge lifecycle

## Status

Proposed; public ingress and native lifecycle qualification remain pending.

## Context

Gregale app deletion preserves metadata for the existing seven-day restore
window. PostgreSQL already cascades TCP/UDP listener rows when the app is
permanently purged. MemStore omitted these children, leaving public ports
reserved indefinitely after final purge. TCP binding lookups and the enabled
listener feed also returned listeners belonging to deleted apps.

## Decision

Retain enabled and disabled reservations during the restore window, but exclude
deleted apps and mismatched account ownership from public TCP binding lookups
and the enabled listener feed, matching the UDP contract. Use generated sqlc
queries for these PostgreSQL reads and reject out-of-range ports before int32
conversion. Restoration reuses the original listener identities and ports.

Only a claimed, expired final purge releases reservations. Delete TCP and UDP
children in MemStore under its existing purge mutex, matching PostgreSQL foreign
key cascades. The integration tree also removes associated TCP TLS observations,
matching their existing listener foreign-key cascade. Other apps' listeners are
unaffected. No grace policy, guest storage or plan economics change is made.

## Consequences

Store-level tests cover hidden routes and binding feeds, premature claim/purge
rejection, reservation retention including disabled intent, restoration, final
purge and port reuse for both protocols, and other-app isolation. The integration
fixture also verifies TLS fields survive generated TCP binding reads. These
store contracts do not prove live edge reconciliation, namespace cleanup or VM
leak acceptance; native Linux/amd64 qualification remains required.

# ADR-788: Bind original startup rows for retirement

Date: 2026-10-08
Status: Implemented locally; native lifecycle acceptance pending.

## Context

Startup retains original physical holds and independent live row charges. A row
shadow lacks the original host epoch, so an accepted original stop cannot release
that shadow. Generic row cleanup can also discard its charge before physical
absence is established. ADR-787 now retains the complete original source on the
native owner, enabling an unambiguous retirement relation.

## Decision

After successful full-store hold traversal and row reconstruction, startup reads
fresh authenticated native observations through the existing owner transport.
Only a matching original source, resources, task/cgroup incarnation and native
owner can attach an original source to a row shadow. The current row must retain
the same exact lookup spelling, app, deployment, node and physical run. Another
retained permit for the instance UUID, even under a different spelling or host
epoch, refuses the binding. Unresolved native owners, helper observations and
unclaimed managed tasks refuse the binding as well.

The row remains a shadow and cannot authorize issuance or dispatch. Its own
accounting dimensions remain intact, even when they differ from the original
physical resource dimensions. Both charges and all original serving-seat counts
remain retained. Advisory observations do not merge startup charges, establish
inventory completeness, clear barriers or activate a quota head. Once the existing
authenticated original stop path accepts physical absence, it can release both
the original hold and its equally bound row shadow. A different source or host
epoch cannot release either.

Unavailable observations leave conservative accounting unchanged. Binding occurs
before dispatch and retirement recovery workers start; the matching row and local
reservation are rechecked at the binding boundary. Original app-task holds cannot
borrow instance rows; current task lease adoption remains a separate requirement.
No SQL, customer API, protocol or infrastructure changes are made here.

## Validation

Test original-source binding, unchanged resource/concurrency charges, generic
release and snapshot refusal, exact original stop release, replacement runs,
lookup substitutions, competing host epochs, absent or changed native sources,
unresolved helper/owner observations, unclaimed managed tasks and dispatch refusal.
Existing authenticated first-fence and lost-reply retirement contracts continue
to apply. These are portable ownership and transport checks; native Linux x86_64
KVM metal, leakcheck and fleet recovery acceptance remain required.

## Remaining work

Complete physical inventory, current task ownership, native reconciliation that
can safely merge row charges, first-fence capacity recovery, native snapshot
capture/pause/migration and latency qualification, global authorization/billing,
independent regional provisioning/stores/replication and actual global ingress
serving remain required. The all-selected-regions default is preserved. Regional
admission stays frozen and the end-to-end goal remains active.

# ADR-1283: Retained recovery health overview

Date: 2026-10-13

## Context

The app health command reports automatic queue observations, but omits manual
initial reviews and actual delivery of polling alerts. Captured alerts alone do
not prove delivery and policy intent changes do not prove monitoring recovered.

## Decision

Extend the existing read-only recovery health endpoint with an optional overview.
Use one repeatable-read, read-only PostgreSQL transaction or one MemStore lock
for queue evidence, manual initial review counts and the last retained polling
alert's delivery evidence. Keep the existing queue classifier unchanged so
worker incident transitions retain their semantics.

Count prepared preview reviews only while unexpired and matching the enabled
current policy version. Include unresolved submitted preview reviews across
policy changes. Report exact counts with a bounded issue sample, prioritizing
polling failures and attention. Mark incomplete samples explicitly. This covers
initial manual reviews and the existing automatic queue, not all workflow runs
or manual retry history. No worker runs, pruning or evidence repair occurs.

Inspect the newest retained polling notification in durable app history, including
notifications whose source review was pruned. Delivery counts use only the
receipt's frozen receiver identities and account/app/event fences. Missing rows
with an outbox row mean relay pending; missing rows without it mean unknown.
Failed/dead delivery needs attention. No receivers is explicit, and later
subscribers do not join an older alert. The CLI links existing read-only scoped
inspection endpoints; mutation authorization and version fences remain intact.

## Consequences

Overview status summarizes needs-attention, paused, unknown, waiting, inactive or
healthy observations independently of workflow outcomes. Healthy means no
observed blocker, not scheduler liveness. An enabled preview policy can be healthy
while its automatic queue is inactive. Inactive requires a disabled policy and no
outstanding work. Delivery describes the last retained
polling alert, not every historical alert or health-incident notification.
No schema migration or additional execution authority is introduced.

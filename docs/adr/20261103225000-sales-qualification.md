# ADR: Progressive sales qualification and closing reasons

Status: Accepted 2026-11-04

## Context

The sales workspace has lead stages, conversations and follow-ups. Reps need
structured context for customer needs, expected workload, decision-makers and
buying timelines. Managers need to see missing context and recurring win/loss
reasons without confusing manually closed deals with payment conversions.

## Decision

Apid owns `sales_qualifications` and append-only `sales_outcomes`, both excluded
from application clones. Qualification is a versioned replacement record per
lead: use case, workload, decision-maker, buying timeline and optional decision
date, manual priority/product fit and a main blocker. Four basic details count
as captured when use case, workload and decision-maker are nonempty and the
timeline is not unknown. Capture is progressive and independent of lead stage.
Priority and fit are manual assessments; this release calculates no score.

A buying timeline can be unknown, exploring, within 30/90 days, later or a
specific calendar date. Only the date option has a decision date. Reps edit
their own leads; managers edit the team. Permissions follow current ownership.
Lock the scoped parent lead and compare both parent and qualification versions
before saving, preventing stale edits and reassignment races. Qualification
edits preserve the lead's notes, stage, contact clock and referral credit.

Creating a won/lost lead or changing a lead into either closed stage requires a
stage-appropriate reason. Other requires an explanation. Existing closed leads
can retain their current metadata without inventing a reason; an explicit
reason records or corrects the classification. Save the stage/owner update and
outcome in one transaction. An outcome preserves the actor and owner at that
moment, its parent version, reason, note and recording time. Corrections append
another outcome. Latest outcome ordering uses parent version, avoiding timestamp
ties. Reopening excludes a lead from current closed counts; closing it again
requires a fresh reason. Deleting a lead removes qualification and outcomes.

The manager overview counts basic gaps among currently open leads and groups
current won/lost leads by their latest matching outcome. Each closed lead counts
once under current lead ownership. It also reports historical closed leads
without matching recorded reasons as unclassified. No reasons are backfilled or
inferred. These are current-pipeline summaries, not historical closing cohorts
or revenue forecasts. Existing manager reporting retains its own date semantics.
Lists are bounded, paginated and use actor/role/owner-bound encrypted cursors.
Summary and detail reads share repeatable-read snapshots. Audit events include
IDs and stage, without qualification text or closing notes.

## Consequences

Apply the additive migration before apid. The existing default-off sales flag,
verified email, completed session MFA, sales allowlists and origin guards cover
all routes. The embedded portal ships with the same server release. Older tabs
that attempt a new closure without a reason receive a validation error and
must refresh. Older binaries ignore the new records and cannot enforce the new
closure policy; deploy the new apid and embedded UI together and drain the old
writer before relying on mandatory reasons. Historical outcomes remain visible
as unclassified until explicitly recorded.

The portal keeps drafts after conflicts, displays current saved qualification
for review and makes replacement explicit. No customer-facing website release,
external CRM, outbound message, billing operation or payout integration is
required. Money and immutable acquisition attribution are unaffected.

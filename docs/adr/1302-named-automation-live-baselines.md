# ADR-1302: Named automation live baselines and attributed promotion

Status: Accepted

## Context

The retained-evidence gate requires a baseline UUID for every invocation. Teams
need a stable release-track name and a trace of deliberate changes to the trusted
comparison point. A passing historical result alone cannot authorize promotion.

## Decision

Scope each baseline name to an app and automation. Names such as `production`
or `staging` identify release tracks; they do not select a project environment,
change routing, or bypass the explicit candidate deployment/version tuple.

Expose app-read GETs for the head and history, and a deploy-write PUT for a
caller-selected promotion UUID. Require MFA and verified email on the write as
on other automation mutations. Derive actor account/API-key identifiers from
authentication; reject actor or verdict fields in the request.

Promotion requires an explicit expected baseline revision (0 creates), retained
suite UUID, expected saved/published/deployment tuple and version 2 policy.
Evaluate the suite's current retained results against themselves, including
coverage, compatibility, current reports and original-run evidence age. The CI
gate continues to reject self-comparisons. Failed or inconclusive evidence cannot
change the baseline. No promotion starts, checks or resumes executions.

Append an immutable receipt containing the exact suite target, policy and hash,
actor identifiers, revision, predecessor, promotion time and passing evidence age.
Use the MemStore mutex or PostgreSQL repeatable-read transaction, app admission
lock, target locks and unique revision constraint to serialize updates. Conflicts
require a fresh observed revision. Retrying an identical retained promotion UUID
with the same actor/key and semantic request returns its original receipt without
rechecking evidence or moving a newer head. Reusing its identity for different
intent fails. Receipts are historical facts, not current passing certificates.

Retain at most 20 names per automation and 100 receipts per name. Enforce request,
receipt and name byte limits from the central limits table; mirror them in schema
constraints. Prune older receipts, never modify them. Automation deletion cascades
history. Clearing saved cases removes their evidence but retains promotion audit.
Pin current baseline suite groups within the existing 100-suite retention budget;
linked verifications and reports still follow their own retention rules. Replaced
suite groups become eligible for normal pruning.

Add `gate --baseline-name NAME` as an alternative to an explicit baseline UUID.
Resolve the immutable receipt once, adopt its reviewed policy, then compare its
exact suite UUID with the candidate. Both calls share the existing ten-second
read budget. Record the receipt and resolved UUID in reports. A concurrent later
promotion cannot change that invocation's comparison. A supplied local policy
must match the promoted policy hash or the gate is inconclusive. Missing evidence
never falls back to another suite. Baseline reads never promote or certify runs.

## Consequences

CI no longer needs to copy baseline UUIDs between releases, and every promotion
has attributable, bounded audit history. Named gates fail closed on incompatible
policies, expired evidence, invalid receipts and unusable candidate age. Explicit
UUID gates keep their existing behavior. API clients, schema, SQLC, CLI help and
authoring documentation describe the same contract.

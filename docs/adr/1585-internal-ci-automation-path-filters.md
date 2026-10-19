# ADR-1585: Internal CI automation path filters

- Status: implemented behind existing default-off CI gates; native qualification pending
- Date: 2026-10-17
- Extends: ADR-1572, ADR-1656, ADR-1657, ADR-1581 and ADR-1583

## Decision

Add optional `include_paths` and `exclude_paths` to an internal automation
policy. These filters apply to the complete pipeline on authenticated, newly
captured push/PR events. They do not alter manual submissions or explicit
execution reruns. Empty filters preserve existing behavior without an extra
provider lookup. No production default or compute capability changes.

Each list permits 16 unique, case-sensitive, repository-relative patterns of
at most 256 UTF-8 bytes. `*` and `?` match within one path component; a whole
`**` component matches zero or more components. Character sets, braces,
negations, escapes, absolute paths, empty components, dot traversal and control
characters are rejected. An empty include list includes all paths; exclusions
take precedence for each path. CI runs if any changed path is included and
not excluded. Both names of a rename/copy are considered independently, and
deleted files use their deleted path. A complete empty comparison can skip.
Policy normalization clones and sorts the lists. Equal saves retain their
generation; filter changes revoke pending sources like other policy changes.

## Immutable comparison and conservative fallback

Capture a push webhook's nonzero, canonical `before` SHA as optional source
evidence. It is deliberately excluded from the existing logical event key,
so changed comparison evidence, delivery IDs or admission modes cannot revive
settled sources. New branches, old captures and malformed/missing before SHAs
run CI. PRs retain the normal fresh, same-repository, open, non-draft head
verification and observation fence. That observation supplies the exact base
SHA; compare its merge base with the captured/current head, rather than
querying mutable PR-file pagination or branch refs.

A new local-socket `GetCIChangedFiles` RPC keeps provider credentials and
installation ownership in githubd. The recovery service explicitly forwards
this optional capability. The remote imaged listener cannot call it. Githubd
requests a single exact-SHA compare page with a 100-commit page size, bounded
to 2 MiB and 32 JSON nesting levels. It rejects duplicate/case-alias keys,
missing identity/count/file fields, trailing JSON, pagination, mismatched base
or final head, unknown file statuses, malformed names and incomplete renames.
Only fewer than 100 commits and fewer than 300 files qualify; reaching either
provider boundary is ambiguous. Paths have a 1024-byte limit. Diverged push
comparisons cannot skip because GitHub's three-dot diff excludes changes on
the old branch tip; PRs use the returned pinned merge base. Behind and
unavailable comparisons run CI too.

Missing RPC capability, absent PR base, provider errors/rate limits,
truncation, invalid evidence and evaluation timeout mean `run` with
`changes_unavailable`. They never justify a skip. Existing account/repository
authority failures still refuse admission. Normal account, binding, policy,
profile, image, claim and observation fences are rechecked under the account
lock after provider calls. No SQL transaction spans the changed-file lookup.

## Admission, supersession and receipts

Persist one bounded `path_decision` with the settled source: outcome, stable
reason, base/head/merge-base SHAs and file count. Filenames, patches and provider
errors are not retained or published. A run records `paths_matched` or
`changes_unavailable` while creating the normal execution. A skip settles the
source as `ignored` with `last_error=paths_filtered`, no execution, and no
native intent, command or storage capability. Skips need no compute capacity.
Admission recovery cannot promote a skipped source. Retained source limits and
permanent cross-journal deduplication continue to apply.

A skipped push never supersedes queued/running code work, even with
`latest_only`: an incremental diff cannot prove older changes were tested.
The latest-head observation is still fenced before settling that push. If other
same-generation push sources are pending, processing or exhausted without an
execution, latest-only runs the replacement head with `prior_source_unsettled`
so older unadmitted code changes cannot be stranded by a docs-only push. This
also covers older sources already observed as `head_changed`, until the observed
replacement or a later verified branch head has acquired an execution. A PR
diff is cumulative, so a filtered current PR head uses normal same-generation
supersession, cancellation and native/output/transfer drain before recording
its skip. Prior-generation work still holds its PR slot. Ineligible/stale PRs
and obsolete push heads keep their existing ignore/defer behavior. Manual
work, including reruns, remains independent of automatic slots.

## GitHub feedback and operations

When both existing Check capture gates are enabled, commit an immutable
`ci_automation_path_checks` outbox projection in the same transaction as the
skip. It has the pipeline/binding Check name and distinct external identity
`gregale-ci-source:EVENT_UUID`, with `completed` / `skipped`. Execution and
source IDs are mutually exclusive. No successful native result is invented.
The existing githubd execution Check worker also drains these projections
with installation verification, serialization, bounded leases/backoff/eight
attempts, response-loss discovery and ownership checks. Publication failure
does not run CI or change its skip decision. Delivery exhaustion is visible
in the existing `execution_checks` health queue; explicit execution Check
recovery remains execution-only. ADR-1586 adds separate scoped skipped-source
Check recovery. Do not revive source admission to retry feedback.

Policy lists are configuration clone inventory; decisions and source Check
bookkeeping are operational. Account-scoped foreign keys cascade deletion.
Rollback refuses retained filter configuration, captured filter snapshots,
decisions or Check delivery evidence. Apply the additive migration through the
normal release workflow and deploy all readers/workers before opting in;
keep admission off during rollback. This work does not authorize release or
enable any gate.

The existing policy API accepts the optional lists. The CLI adds
`--include-paths` / `--exclude-paths` on `ci automation put`. Existing event
history/status APIs and CLI expose the stable decision, including when
compute admission is disabled. Portable tests cover policy/API/CLI validation,
provider/RPC identity and limits, defaults, comparisons, deletions/renames,
revocation/fences, supersession, capacity, atomicity, Check response loss,
restart, isolation, migration replay and HTTPS worker wiring. Native and
real-provider qualification remain a separate operator task.

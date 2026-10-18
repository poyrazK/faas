# Explicit operator alert-rule expectation coverage

## Status

Accepted. Backend/API/CLI only; frontend and rollout remain separately owned.

## Context

Loaded-rule health cannot detect an absent alert definition. An empty, quiet or
successfully evaluated subset can hide a critical rule missing from Prometheus.
Repository rules also include optional companion files, so treating every
catalog entry as deployment intent would manufacture a requirement.

## Decision

Add read-only GET `/v1/admin/obs/rule-coverage` and
`gregalectl obs rule-coverage`. Require the existing operator scope, MFA and
allowlist. Select an expectation explicitly per request with `expected_set`
or `--expected-set`. No selection returns unknown, advertises available sets,
and makes no Prometheus request. Unknown, empty, repeated and extra query keys
are rejected. This feature changes no installation configuration or historian.

Compile a generated digest-only catalog from five repository rule files:

- `gregale-core-v1`: alerts in `faas.rules.yml`.
- `gregale-full-v1`: core plus `bridge.rules.yml`, `pg_backup.rules.yml`,
  `data_placement.yaml` and `data_repair.rules.yml` alerts.

Both sets are minimum expectations; additional valid identities are counted
and allowed. A set ID describes its selection policy, while its SHA-256 revision
identifies the exact definitions in this binary. Regenerate with
`make obs-rule-catalog-generate`; an ordinary Go drift test and
`make obs-rule-catalog-check` verify freshness. Source edits update the revision.
Changing selection policy requires a new set version. Canonicalization changes
require reviewing/bumping the fingerprint algorithm and regenerating the catalog.

Identify a rule with SHA-256 over JSON `[file_basename, group_name, alert_name]`.
Directories are ignored so moving files does not create differences. Duplicate
basename/group identities are ambiguous, including an empty duplicate group;
duplicate rule candidates are also ambiguous. Identity is independent of the
historical episode rule key. Do not join them as though they were equivalent.

Fingerprint canonical PromQL syntax, `for`, `keep_firing_for` and sorted rule
labels. Use the pinned official Prometheus parser, sorting selector matchers,
aggregate grouping and vector matching/include label lists. This normalizes
formatting and ordering, not semantic equivalence. Hash each component and
report only closed changed-field names. Reject invalid syntax, nonfinite or
negative durations, oversized expressions/labels and unsupported label names.
Require explicit reported duration, keep duration and labels; absent/null fields
are unknown, never inferred zero/empty. Existing collector acceptance and episode
identity remain unchanged. Group label inheritance is outside v1: generation
rejects it and an observed selected definition with group labels is unknown.

Read one complete strict bounded `/api/v1/rules?type=alert` response. Source
read and comparison share three seconds, with existing eight-MiB and 4096
group/rule/alert bounds. Reject warning/info/absent/trailing/malformed envelopes.
No usable source or no usable catalog yields unknown with null totals and no
items. Discard incomplete comparisons on deadline. A complete empty source has
known missing expected rules. An unattributed rule/group could conceal an absent
identity, so otherwise missing candidates become unknown. Known uniquely
identified changes can still be reported alongside unknown evidence.

Expected categories partition the full expected count: matching, missing,
changed, ambiguous and unknown. Root state is covered when all expectations
match and there are no invalid source identities; degraded for any known
missing/changed/ambiguous definition; otherwise partial for some matching plus
incomplete evidence, or unknown when no definitions can be compared. Additional
valid rules do not degrade a minimum expectation set. Full observed counts
separate additional and invalid identities. Return at most 200 expected items,
ordered ambiguous, changed, missing, unknown, matching and then identity hash.
Expose complete totals, total items, preview limit and truncation.

Only safe catalog names/basenames, digests and closed evidence codes leave the
domain. Exclude raw expressions, rule labels, errors, annotations, source URLs
and absolute paths. The CLI validates metadata, identities, partitions,
fingerprints, ordering, selected profile and closed codes before text or JSON
output. Source error bodies never enter output. Synchronize private-route
exclusions, canonical/embedded OpenAPI and generated Node DTOs.

Coverage does not establish deployment intent, healthy evaluations, metric
input availability, recording-rule dependencies, firing state or notification
delivery. Group intervals/offsets and annotations are not compared. A stale or
failed evaluation can still have matching definitions: consult rule-health and
notification-health separately. There is no coverage history or backfill.

## Validation

Check normalization, component changes, invalid definitions and catalog drift;
generation rejects mixed record/alert rules, duplicate groups and unsupported
group labels. Check explicit zero versus missing/null/reset protocol fields.
Exercise missing critical rules in an otherwise quiet source, complete empty
sources, optional extras, directory relocation, ambiguity, unknown identities,
source errors/cancellation, privacy and full totals before bounded previews.
Verify operator/MFA restrictions, strict selection, no implicit source reads and
unchanged collection/episode history. Exercise CLI text/JSON and rejection of
malformed evidence, then run contract generation, relevant regressions and lint.

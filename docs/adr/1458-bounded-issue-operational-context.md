# ADR 1458: Bounded operational context for Issues

Status: Accepted

## Decision

Store at most 16 explicitly reported breadcrumbs per occurrence. Each entry has a timestamp, a category, a level, and a message sanitized to 256 UTF-8 bytes. Entries older than one hour relative to the occurrence are discarded; malformed or future evidence is rejected. The dropped counter is reporter-provided evidence, not a completeness guarantee.

Allow only these operational tag keys: component, operation, feature_flag, feature_variant, dependency, region, job_type, workflow, state, error_class, release_channel, runtime. Accept at most 16 input tags and sanitize values to 128 UTF-8 bytes. Discard unknown keys with a redaction marker. These values do not supply verified customer identity.

The Node SDK provides an explicitly created per-request or per-job collector. Capture copies an immutable bounded snapshot, so retries cannot incorporate later operations. No ambient collection of bodies, headers, or locals is introduced.

Neither breadcrumbs nor tags participate in fingerprints. Issue listing can filter an exact allowed key=value pair against retained occurrences in a fixed 24-hour window. Canonical assignment membership includes merged groups; pagination binds the tag and the window to the cursor. Cursors are bounded to 2 KiB so a maximum-length escaped tag value remains decodable. Displayed issue totals remain whole-issue totals.

## Consequences

Operators can connect exceptions to recent reported steps and operational dimensions without changing grouping. Retention, missing instrumentation, and reporter omissions limit this evidence. Arbitrary custom tag policy, automatic integrations, and complete session histories remain separate work.

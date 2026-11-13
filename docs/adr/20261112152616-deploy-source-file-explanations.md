# ADR-20261112152616: Explain local deployment payload files

Status: accepted

## Context

Source selection depends on Git provenance, workspace context, ignore rules,
Dockerfile builds, Go vendoring and normalized symlinks. A separate directory
listing can disagree with the actual upload, especially after secret scanning
or generation of a Go function's build-only `go.mod`. The existing deploy
`--dry-run` compares authenticated remote state; it does not explain the archive.

## Decision

Add `gregale deploy --dry-run --explain-files` for local source directories,
with text and versioned JSON output. This combination performs local payload
inspection without login, API requests or deployment mutations. Ordinary
`--dry-run` retains its existing remote comparison behavior. Image, tarball,
repository-fetch and template inputs, and deployment mutation flags, are
refused in this mode. Functions-project fanout requires selecting a member
with `--path`; a project root archive can be selected with `--project`.

Use the existing source-mode/provenance resolution, committed-source
normalization and packers. Record each packer's selection decision, including
the final matching ignore file and line, without printing the rule's text.
Read final archive headers to obtain paths, counts and sizes, and fail if the
selection and archive disagree. Report generated files, secret-scan
replacements and credential-like filenames; never report source contents or
secret values. An explicit `--secrets-file` is excluded using the usual
command-scoped packaging rule without importing or syncing its values as
runtime configuration. The existing local secret scan still applies.

Preserve existing packing policy, including default exclusions, Docker build
outputs, vendored Go dependencies, ignore negation and refusal of invalid
symlinks. Worktree symlinks are identified while walking; HEAD symlinks have
already been normalized. HEAD reports describe Git's normalized archive, so
untracked, uncommitted and export-ignored files are absent rather than listed
as packaging exclusions. A pruned directory represents its whole subtree;
inspection does not traverse it just to enumerate excluded descendants.

Bound explanation metadata by the existing archive-entry limit and apply
the existing maximum source envelope while packing. Account-specific upload
limits are unavailable offline and are explicitly marked unchecked. This
report does not claim runtime readiness or validate remote configuration.

## Consequences

Users can identify large files, unexpected inclusions and the responsible
ignore rule before uploading. JSON consumers get logical and archive paths,
byte sizes and machine-readable reasons. Inspection creates and removes a
temporary archive, so it has packaging's disk and CPU cost. Archive-parity
regressions exercise both ordinary and instrumented packers, and a black-box
CLI journey verifies the offline behavior.

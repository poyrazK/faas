# ADR-942: Commit recovery watch checkpoints

Date: 2026-10-11
Status: Accepted; production qualification pending
Related: ADR-939, ADR-940, ADR-941

## Decision

Add recovery-status --watch --checkpoint PATH. Save the existing resumable
status format after each successful bounded pass, before reporting progress.
Save final partial read/interruption evidence when available. Do not replace
an existing checkpoint with an empty validation-failure result. No new replay
or server mutation is introduced; unknown attempts remain unknown.

Use a private temporary file in the resolved destination directory. Bound
encoded JSON including its newline to the existing 64 MiB status-input limit.
Write and sync the file, close it, then rename it over the destination. Sync
the directory on non-Windows systems. Remove temporary files on ordinary
failure. Abrupt termination can leave a private temporary file; only the named
checkpoint is used for resume. Before rename the prior checkpoint remains
intact; after rename a directory-sync failure means durability is uncertain
and is reported explicitly.

Require an existing directory and a regular destination when present; reject
final-component symlinks and nonregular files. Resolve parent-directory aliases
and reject destination aliases of the plan or apply result, including hard
links. Recheck those protections before replacement. Reject an existing
checkpoint that aliases stdout. Use separate stdout output: shell redirection
can truncate files before the CLI starts. These checks protect ordinary path
mistakes, not hostile concurrent filesystem changes. One writer should own a
checkpoint path; concurrent watches were not coordinated by this decision.
ADR-943 adds exclusive advisory ownership for cooperating Gregale watches.

The checkpoint may intentionally replace the supplied resume/refresh status
file after that file has been loaded and validated. A restart uses the same
source, plan and result with --resume PATH --watch --checkpoint PATH. Existing
selection binding, comparison baseline, bounds and unknown-outcome semantics
remain unchanged. Checkpointing requires watch and never silently creates a
missing directory.

On a save failure, cancel monitoring and return nonzero with an explicit
checkpoint error. Preserve final JSON on stdout and retain the previous
checkpoint when replacement has not occurred. A failed final partial save
also reports a checkpoint error. Validation errors before meaningful
observations do not overwrite an existing checkpoint.

## Consequences

Process restarts can continue bounded observation coverage. This stores local
status evidence, not signed proof or exactly-once replay guarantees. Live
crash/durability qualification is pending. No local tests are added or run;
compliance registries remain unchanged. SDK progress callbacks remain the
existing persistence integration point; this CLI option does not change them.

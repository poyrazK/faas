# Scope uploaded inputs to named Agent pipeline steps

**Status** Accepted for preview

An Agent pipeline can use a private upload on an explicitly named step. The
existing grant adds the step label to its authenticated scope. This separates
pipeline step grants from standalone task grants and prevents moving an upload
to a sibling step, even when both steps reference the same task version.

Submission validates all grants after looking up an exact replay. It commits
the selected file generation and guest path inside the encrypted pipeline plan.
The logical submission digest includes the exact grant intent, while public
receipts carry status and lineage. Before each physical attempt the worker
compares the prepared input with the pinned generation. The file store checks
the same storage key and ready state again in the atomic admission transaction.
An admitted attempt keeps its ordinary Run file lease.

The claim fence prevents an older progress worker from leasing the new sealed
plan format. Existing pipelines keep their plan bytes and replay digests.
Deploy the fence and new readers before enabling this route for pilot traffic.
Exact submission replay preserves the original pipeline. An Agent starting a
new pipeline after failure provides new step grants through its trusted backend.

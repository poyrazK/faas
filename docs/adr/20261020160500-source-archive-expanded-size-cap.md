# Source archives are capped by their expanded size

Date: 2026-10-20
Status: Accepted
Related: spec §9 (source accept rules), spec §11

This record has a date-and-time identifier because date-only identifiers
collide under `make adr-number-uniqueness-check` when two ADRs land on the
same day.

## Context

apid accepts a source tarball up to the plan's compressed cap (100 MB, 250 MB
on Pro and Scale) and checks its shape: at most 10,000 entries, no path or
link escapes. Nothing bounded the expanded size. gzip compresses repeated
bytes about 1000:1, so a legal upload can carry a single entry of tens of
gigabytes. apid inflates the archive on every pass over it: the shape check,
the manifest read, the secret scan, and others. A customer could pin apid
cores for minutes per upload.

## Decision

`tarball.ValidateShape` sums the declared size of every entry and refuses
the archive as soon as the sum, or a single entry, passes
`SourceArchiveMaxExpandedBytes` (2 GiB). The check runs on each header
before its data is read, so an over-large archive is refused without being
decompressed. The deploy path applies the cap; the refusal is the existing
`source_invalid` problem.

## Consequences

- An upload's inflation cost in apid is bounded by 2 GiB per pass.
- A source tree larger than 2 GiB expanded is refused. That is eight times
  the largest compressed upload, and the CLI already excludes dependency and
  build directories from the tarball.
- The cap lives in `pkg/api/limits.go`, and other archive paths keep their
  own expanded caps (environment GitOps 512 MiB, CI job sources 128 MiB).

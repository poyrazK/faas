# ADR-20261107123000: GCS generations are not retained-version evidence

## Status
Accepted. Amends [ADR-628](628-gcs-tracked-writes-and-native-generations.md).

## Context

ADR-628 notes that GCS assigns a generation to every object, versioned bucket
or not, and that treating generations as S3 version history permanently
blocks deletion. Object accounting still inferred retained versions from any
native version identity other than `"null"`, the S3 rule. It applied that rule
in tracked-upload and multipart completion, in version references recorded by
listings, tagging and reads, and in a `object_version_references` CHECK. The
GCS history proof also reported versions for any listed object.

On production GCS the first write to a bucket therefore marked it versioned.
In current-object accounting every later write was refused `409`
(`OperationAborted`, "pending work") and every delete with it, so the bucket
could be neither emptied nor deleted. The customer journey
`TestObjectStorage` found this on its first production run (2026-11-05).

## Decision

- A provider declares `GenerationVersionIDs()` when every object carries a
  version identifier. GCS does.
- A generation identity is recorded with its durable Gregale version ID as
  before, but alone it never sets `versions_observed`,
  `recovery_versions_observed` or `completion_versions_observed`.
  `state.ProviderVersionObserved(id, generation)` is the single rule.
- Retained versions on GCS are shown only by a noncurrent generation (one with
  a `Deleted` time) in a version listing or history proof, by a non-latest item
  of a version listing, or by Gregale's own bucket versioning state, which
  already fences writes during a versioning change.
- Migration `20261107201212427` drops the CHECK that required
  `versions_observed` for every non-null identity (expand: the previous
  release still writes true). The monotonic trigger and one-way
  `all_versions` scope are unchanged.
- Buckets falsely marked before this change are repaired once by an
  operator, in one transaction: clear the three flags for ready buckets on the
  GCS backend still in `current` scope with no bucket versioning history and
  no active inventory, with the reference trigger disabled only inside that
  transaction. A bucket already rebased to `all_versions` stays there.

## Consequences

Ordinary GCS buckets keep current-object accounting, signed-URL PUTs and
deletes after their first write. S3 behaviour is unchanged. A GCS bucket whose
versioning was enabled outside Gregale is detected only when a listing or
history proof sees a noncurrent generation; Gregale's own versioning controls
remain the supported path.

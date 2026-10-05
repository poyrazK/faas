# ADR-549: Tag current and retained object versions

Status: Accepted (2026-10-03)

## Context

Customer version IDs now select retained data for reads, copies and deletion.
Tagging still targets only the current object. Lifecycle tag filters need an
owned version selector, and metadata rewrites would risk losing the private
completion proof used for uncertain write recovery.

## Decision

Add an optional version-aware provider tagging capability. S3 uses native
GetObjectTagging, PutObjectTagging and DeleteObjectTagging with the exact native
VersionId. Resolve public IDs through the durable account/bucket/key-owned
reference before admission or dispatch. Reject missing references and invalid
null/immutable mappings. Successful provider identities must match the selected
target; current observations persist and expose an owned public identity through
the existing reference store and all-version accounting fence. Provider IDs
remain private. Legacy tagging providers support current objects only.

Use one shared service for S3 ingress and authenticated control APIs. Enforce
read/write grants, bounded tag limits and provider-request usage before
dispatch. Tag operations do not reserve data capacity or refund storage. DELETE
tag cleanup remains available with storage ingress disabled, under the existing
customer safety-budget policy. No storage mutation journal or migration is
needed: tagging changes the selected object's tag set in place, without changing
its data version or private completion metadata.

The S3 adapter makes one SDK attempt per customer request. Lost acknowledgment
returns an error without automatically replaying the tag change. Customers can
read the selected version, or explicitly repeat the desired replacement/clear.
Immutable selectors survive reconstructed stores. Current and null selectors
remain mutable, and concurrent tag updates retain provider last-writer behavior.
This does not claim ordered or exactly-once tag updates.

Validate success status and version headers before publishing a result. Reject
duplicate headers, wrong selectors and unexpected marker success responses.
Bound and validate GetObjectTagging XML before the SDK interprets it. The shared
parser rejects ambiguous roots/sets, duplicate keys, missing fields, foreign
namespaces, unknown elements, nested text fields and trailing documents. Empty
values and an empty TagSet are valid. UTF-8 tags excluding ASCII controls retain the portable
128/256 byte key/value limits and ten-tag limit. Body size is 16 KiB and operation
timeout 30 seconds, centralized with tag limits in pkg/api/limits.go.

## Customer surfaces

S3 ?tagging accepts an optional owned versionId and returns public version
headers with standard GET/PUT/DELETE success semantics. MethodNotAllowed for a
delete marker becomes a safe 405 error. Unsupported conditions and tag/copy
directives fail explicitly.

GET, PUT and DELETE objects/tags accept key and optional version_id. PUT replaces
the complete tags map. Every successful control response returns the complete
tags map and optional public version_id; DELETE returns an empty map. The API
requires storage read/write scope plus matching bucket access. Go, Node and
Python typed clients and bucket tags get/set/clear commands expose the contract.

## Operations and rollback

No schema rollout is required. Old workers retain current-only behavior; deploy
updated ingress/control workers before advertising selected tagging. Provider
placements need their native current/version tagging permissions. Disabled or
unsupported capabilities fail without dispatch. Rolling back the code leaves
tag metadata and public references intact but removes selected-tagging APIs.
Local HTTP provider fixtures qualify implementation behavior; real provider
qualification is outside the requested scope.

## Acceptance

Local provider and full S3 gateway race suites passed, including the final
disabled-ingress cleanup change. Focused control API, Go client, CLI and service
race suites passed. Tests cover current/null/immutable selectors, native response
validation, strict input, one-attempt lost acknowledgments, reconstructed
PostgreSQL-backed servers, bucket/key/account ownership, scope/grants and
unchanged data accounting. Invalid escaped selectors cannot become current
requests. Existing GCS current tagging remains covered by the provider suite.

Node production/test builds and six related Node tests passed; nine related
Python tests and scoped Ruff checks passed. Go route/schema parity and SDK route
coverage passed. Node generation is deterministic across its complete generated
tree; seven Python tagging modules match isolated generator output, with scoped
model exports. Existing unrelated Python schema drift is outside this increment.
OpenAPI validation reported zero errors after correcting adjacent duplicated
parameter descriptions. Its existing warnings remain. Changed-code Go lint
reported zero issues. Handler sizes, encoding, shell quoting, ADR uniqueness,
runbook SQL and Git whitespace checks passed. No live provider resources were
created or contacted.

AWS protocol references:
[GetObjectTagging](https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetObjectTagging.html),
[PutObjectTagging](https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutObjectTagging.html),
[DeleteObjectTagging](https://docs.aws.amazon.com/AmazonS3/latest/API/API_DeleteObjectTagging.html).

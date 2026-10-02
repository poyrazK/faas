# ADR-431: Regional source archive references

Date: 2026-10-02

Status: Internal implementation; native fleet qualification pending.

## Context

Regional builder claims under ADR-430 select a worker in the app's immutable
compute region. Source archives were still published and fetched through the
legacy store using a reconstructed build-ID key. Retries did not persist the
identity of the archive they needed, and source retention listed only that store.

## Decision

APID resolves the persisted app before reading or publishing source. New source
objects use `regions/<compute_region>/sources/<build_id>.tar.gz`; legacy apps keep
the unprefixed namespace. Publication succeeds before deployment and queue rows
are created. A regional app requires an explicit storage route even when the
process also has access to its local archive. A missing route or storage outage
does not publish work or redirect it to the legacy store.

Deployments persist an immutable `source_storage_key`. PostgreSQL and the memory
store validate its shape and match its namespace to the app's region. Builderd
fetches that exact reference into its own configured spool root, independently
of the API host's absolute path. It checks routing before accepting a local file
and keeps the existing bounded download, byte count, and SHA-256 checks before
VM dispatch. A route outage requeues the claimed build.

Source retries resolve the original deployment and its committed object key.
For historical rows with an empty reference, they resolve the original build
from state and retain its unprefixed key. Retries republish into the current
app's regional namespace under a fresh build ID before binding the new reference
in the retry transaction. Original immutable inputs, including size and hash,
come from the parent deployment. A storage outage never falls back to the local
archive; a missing object can use a retained regular local archive of the original
size. Removing both makes the existing retry-source-unavailable error authoritative.

Source retention uses the storage router's explicit inventory across all
configured regions and preserves complete keys through deletion. Existing
UUIDv7 age rules still protect fresh uploads and leave unknown or older UUID
formats untouched. Queued/running builds and pending deployment references
protect an archive, including when its original build has already finished.
An incomplete inventory, missing reference capability, or failed state lookup
aborts the pass before any deletion. Run global source retention with all
participating storage routes.

New regional migrations tolerate replay after their schema changes have already
been applied. Region registry backfill preserves an existing admission policy.

## Validation and rollout

Portable tests cover publication failure before durable work, reference binding,
historical and regional retry reads, storage outages, downloads after the API
archive is removed, source corruption before VM dispatch, route removal with a
local archive, and full-namespace source retention. Memory/PostgreSQL tests share
the reference and retry contract; direct SQL tests exercise immutable reference,
namespace and shape constraints. The migration replay gate covers every new
regional migration in this branch.

Configure the routing file for both apid and builderd before starting regional
source builds. Keep compute internal/default-off pending native Linux x86_64/KVM
acceptance, affected `test-metal` coverage and `leakcheck`. ADR-432 supplies the
subsequent remote build-export handoff to imaged; logs, metadata, persistent
services, regional ingress and control-plane independence remain subsequent work.

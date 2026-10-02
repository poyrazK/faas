# Regional runtime artifact routing

Builder claims enforce an app's persisted region regardless of cache affinity
settings (ADR-430). Configure regional builderd and vmmd with the same registered
compute-node identity and drain outstanding builds before upgrading. Cancellation
and timeout VM cleanup stays with the claim owner, including while that node is
draining. Source uploads and retries persist regional object references
(ADR-431). Builderd fetches the committed reference and verifies size and hash
before VM dispatch. Build exports are published before claim-fenced completion
and consumed by an active imaged worker in the same region (ADR-432), even after
the producer's local file is lost. The full source-to-runtime flow still needs
native qualification.

Regional compute is internal and default-off. Configure artifact routes before
enabling regional deployments; an unconfigured region fails publication and reads.

Set `FAAS_STORAGE_REGIONS_FILE=/etc/faas/storage-regions.json` in the storage
environment used by apid, builderd, imaged, schedd, vmmd, and gatewayd-internal. Provision the routing
file on each host that publishes or consumes regional artifacts. Workers may
receive only their own region's route; global artifact lifecycle and drift scans
need all participating routes. Keep the existing `FAAS_STORAGE_BACKEND` settings
for legacy artifacts and platform bases/kernels.

Example:

```json
{
  "regions": [
    {
      "id": "eu-fsn",
      "backend": "oci",
      "registry_url": "https://registry-eu.example.com",
      "repo_prefix": "gregale-runtime",
      "username_env": "GREGALE_EU_ARTIFACT_USER",
      "password_env": "GREGALE_EU_ARTIFACT_PASSWORD"
    },
    {
      "id": "us-east",
      "backend": "gcs",
      "bucket": "gregale-us-east-runtime"
    }
  ]
}
```

Use uppercase environment names for credential references (for example,
`GREGALE_EU_ARTIFACT_USER` and `GREGALE_EU_ARTIFACT_PASSWORD`). Supply their values
through the daemon's secret environment file, never in this JSON. GCS uses the
existing Google application-default credentials. Provision separate read and
lifecycle credentials according to each daemon's existing ownership. OCI routes
use the existing egress policy. Each configured region must use a distinct
bucket or registry/repository namespace, physically located as intended.

New regional sources, build exports, SBOMs, runtime log archives, layers, sidecars, job images, snapshots,
and signatures use regional keys. Existing deployment/image references keep their original store. The local
cache retains one shared budget and includes region in every key. A regional
store failure does not redirect operations to another store. Removing a route
also blocks cached reads; do not remove it while references in that region remain
in use or await cleanup.

The routing file enables source storage for apid and builderd even when their
legacy backend is local. Source uploads complete before a build becomes
claimable. Retried source builds read the original committed key, including an
older unprefixed key, and publish a fresh regional copy. Source cleanup needs
all participating routes and aborts without deleting anything when a region's
inventory or retained-reference lookup is unavailable.

Regional build completion requires a persisted OCI export key, SHA-256 and byte
count. Imaged verifies that descriptor before conversion, serializes consumers
across workers, and polls pending or interrupted materializations. A failed
snapshot-prime notification remains retryable. The producer's absolute path is
diagnostic once its remote reference is committed.

Remote build exports share builderd's existing source sweep cadence and maximum
age (24-hour defaults). Active image work protects them, including security scan
and snapshot preparation; terminal and orphan exports become eligible after
retention. Export cleanup requires all participating routes and aborts before
deletion if inventory or reference lookup is incomplete. Drain regional builds
before installing the export migration and upgrading builderd and imaged
together. A downgrade refuses to discard persisted regional export references.

Build SBOMs use `regions/<region>/sboms/<build-id>.cdx.json` in the same app
artifact store. Install the regional SBOM migration and upgrade imaged and apid
together while regional materialization is drained. Syft must be available on the
materializing peer. API reads validate both build identity and placement, require
an explicit route, and return unavailable for historical global references until
republished. Failed scan/upload retries preserve an existing reference. App
cleanup deletes the full regional key through the configured artifact backend.
A downgrade refuses while regional references remain. See ADR-433.

Runtime log archives share those routes (ADR-434). Gatewayd-internal loads
`storage.env` and then its private cache override in
`/etc/faas/gatewayd-internal-storage.env`. Ansible installs that override with
`FAAS_STORAGE_CACHE_DIR=/var/lib/faas/gatewayd-internal-artifacts`; install it
alongside the unit on manually managed hosts to keep the reader cache writable
and separate from compute caches. Install the archive catalog
migration and upgrade vmmd, gatewayd-internal and apid together while publishers
and regional wakes are drained. Regional-only archives require the routing file;
the global S3 envelope is optional unless legacy apps still use that bucket.
Ownership bindings survive instance and deployment cleanup. Daily references are
reserved before upload, and fragment receipts protect retries across workers.
Daily archive reads/writes bypass immutable artifact cache entries while retaining
the routing guard. History reads verify app/account, placement, compressed size and digest; missing
routes never fall back to global storage. App and account hard deletion collect
pending and published references under the publication lock. Keep every referenced
route available to cleanup workers until its metadata is removed.

Drain old pending spools before upgrading. Unattributed files and historical global
archives of regional apps need a separate verified backfill or purge; filenames and
worker placement do not establish ownership. Configure archive retention on each
regional store's `faas-logs/` prefix (or OCI repository). Catalog-aware remote retention and confirmed local spool collection are described
below; uncataloged objects require verified operator backfill.

Configure all publishers and consumers, restart them, verify storage access with
their respective credentials, then run native regional acceptance and resource
leak checks on the dedicated Linux x86_64/KVM fleet. This file configures runtime
artifact delivery; source builds require native qualification. Runtime log lifecycle qualification, uncataloged archive backfill,
SBOM orphan collection, persistent service recovery auditing, regional ingress and control-plane independence require
further implementation or qualification.

## Runtime archive remote retention

Apid now collects expired cataloged archives, including pending reservations whose
PUT may have committed without publication metadata (ADR-435). The remote window
uses the largest existing plan retention in `pkg/api/limits.go`, currently 90 days;
`FAAS_LOG_ARCHIVE_RETENTION_DAYS` continues to control only the local spool. The
inclusive UTC cutoff matches the gateway. Object-store lifecycle policies must
preserve at least that longest customer window.

Deploy migration `20261002102304135` before updated apid, gatewayd-internal and vmmd.
Apid needs every participating regional archive route and legacy credentials if
legacy archives exist. It visits at most 256 descriptors every five minutes with a
one-minute pass deadline. Pagination advances beyond failed regions and wraps for
retry. An app publication lock orders retirement, DELETE and catalog collection
against late PUTs and hard deletion. Once retired, the durable expiry floor prevents
an old local spool from reserving or publishing that day again.

Storage failures retain the descriptor and receipts. If DELETE succeeded but the
metadata update failed, a later pass confirms the object is missing and completes
metadata cleanup. Expired local pending fragments remain until an exact collection receipt authorizes
the local cleanup described below. They still count against the local byte cap.
Uncataloged objects with no verified owner are not collected automatically. Backfill
verified instance/app/account/region ownership or remove them explicitly after an
operator inventory; a filename or worker region alone is insufficient evidence.

Observe `apid_log_archive_retention_failures_total{operation}` and
`apid_log_archive_retention_last_success_timestamp_seconds`, alongside scanned and
collected counters. A successful page is not proof that the entire expired inventory
has been drained. Size collector throughput against archive volume before expanding
admission. Native KVM and operational qualification remain pending; region admission
stays internal and default-off.


## Runtime archive local collection

Install migration `20261002164048463` before updated apid and vmmd (ADR-436).
Remote collection records an immutable per-day receipt atomically with reservation
removal. A shipper requires that receipt under the app publication lock before
reclaiming expired partial/upload backlog, committed history, or compressed files.
A remote or metadata failure retains local data. A binding's expiry floor alone
does not prove that older never-reserved days were collected.

Each private spool root retains a durable `.jsonl.collected` witness before payload
removal. The writer rejects delayed frames for that day, including after restart;
cleanup retries can use the witness during a database outage. Keep roots private to
one daemon process and preserve witnesses. Cleanup rejects symlinked descendants,
nonregular payloads and damaged witnesses; investigate the per-file failure before
removing or replacing any entry. The configured root itself may be an operator
mount/symlink, but its instance/year/month descendants must be real directories.

Observe `{apid,vmmd}_log_archive_local_collected_files_total` and
`{apid,vmmd}_log_archive_local_collected_pending_bytes_total`, together with local
bytes/capacity and remote retention failures. The pending-byte counter includes
explicitly collected writer buffers; collection never increments upload counters.
Receipts/witnesses from this rollout do not retroactively prove collection of older
uncataloged data. Historical backlog still needs verified operator backfill or
explicit inventory/removal. Account for one small witness per collected day in
spool inode capacity. Native qualification remains pending and admission stays
internal and default-off.

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
environment used by apid, builderd, imaged, schedd, and vmmd. Provision the routing
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

New regional sources, build exports, SBOMs, layers, sidecars, job images, snapshots,
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

Configure all publishers and consumers, restart them, verify storage access with
their respective credentials, then run native regional acceptance and resource
leak checks on the dedicated Linux x86_64/KVM fleet. This file configures runtime
artifact delivery; source builds require native qualification. Log archive routing,
SBOM orphan collection, persistent services, regional ingress and control-plane independence require
further implementation or qualification.

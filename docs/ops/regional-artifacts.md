# Regional runtime artifact routing

Builder claims enforce an app's persisted region regardless of cache affinity
settings (ADR-430). Configure regional builderd and vmmd with the same registered
compute-node identity and drain outstanding builds before upgrading. Cancellation
and timeout VM cleanup stays with the claim owner, including while that node is
draining. Source uploads and build exports still use the legacy handoff and need
the next implementation increment; these placement checks alone do not qualify
source-build residency.

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

New regional layers, sidecars, job images, snapshots, and signatures use regional
keys. Existing deployment/image references keep their original store. The local
cache retains one shared budget and includes region in every key. A regional
store failure does not redirect operations to another store. Removing a route
also blocks cached reads; do not remove it while references in that region remain
in use or await cleanup.

Configure all publishers and consumers, restart them, verify storage access with
their respective credentials, then run native regional acceptance and resource
leak checks on the dedicated Linux x86_64/KVM fleet. This file configures runtime
artifact delivery; source builds, persistent services, regional ingress, and
control-plane independence require further implementation and qualification.

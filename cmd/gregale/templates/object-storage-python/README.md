# Private object storage for Python compute

A FastAPI file service using Gregale's managed `read_write` bucket binding and
`aiobotocore`. PUT/GET require a bearer token. Uploads are staged on private disk;
downloads stream as opaque attachments. The canonical async SDK runtime is bundled
under `runtime`; this project needs no Gregale repository or separate SDK install.

## Create and deploy

```sh
gregale init --template object-storage-python --path files-app
cd files-app
python -m venv .venv
. .venv/bin/activate
pip install -r requirements.txt
python -m unittest discover -s tests -v
gregale deploy --create-only --template object-storage-python --name my-files
gregale add bucket assets --app my-files --permission read_write --prefix GREGALE_S3_ASSETS
```

Create a file **outside this source directory**, chmod it to 0600, and add one
`STORAGE_APP_TOKEN=<random token>` line. Use at least 32 random characters. Do not
commit the file, put credentials in the Dockerfile, or pass them on command lines.
The managed bucket supplies all six `GREGALE_S3_ASSETS_*` settings automatically.

```sh
gregale deploy --name my-files --secrets-file /absolute/private/files.secrets
```

Set APP_URL to your deployment URL and STORAGE_APP_TOKEN in your local shell:

```sh
curl --fail-with-body -X PUT "$APP_URL/objects/file.bin" \
  -H "Authorization: Bearer $STORAGE_APP_TOKEN" --data-binary @file.bin
curl --fail-with-body "$APP_URL/objects/file.bin" \
  -H "Authorization: Bearer $STORAGE_APP_TOKEN" --output downloaded.bin
curl --fail-with-body "$APP_URL/readyz"
```

Keys contain 1–128 letters, digits, dots, underscores or hyphens, starting with a
letter or digit. Nested paths and query strings are rejected. The bucket stays
private; this starter exposes neither listing, deletes nor public signed URLs.
Downloads use `application/octet-stream`, `attachment` and `nosniff` regardless
of uploaded content. PUT overwrites the named object; choose unique keys when
your application needs immutable files. A failed or cancelled PUT can have
committed at the provider. The service never retries transfers or uncertain writes.

## Binding and request lifecycle

Run `python handler.py` on Gregale, on one main-thread event loop and one worker.
The image opts into SIGHUP and required application adoption readiness. The
coordinator reads the modern atomic snapshot/readiness/ACK/generation contract;
there is no startup-environment or ambient AWS credential fallback. Explicit
SigV4 credentials, path-style addressing, certificate verification, required-only
checksums, finite driver waits and one total driver attempt are configured.

Each candidate owns a fresh transport. Before adoption and an applied ACK, it
performs ListObjectsV2(MaxKeys=1), then validates the HTTP application token from
the same snapshot. Missing/revoked settings, an invalid token or failed validation
disable new requests for both consumers. Use a managed `read_write` binding:
the list probe tests read/list access, not write permission or continuous provider
health. Use storage operation canaries for release qualification. Probes consume
ordinary gateway request budgets; no pre-created readiness object is required.

`/healthz` reports process liveness; `/readyz` reports local adoption and the
restore gate. The manifest uses `/readyz`. Neither endpoint certifies continuous
provider availability. SIGHUP rebuilds credentials and authentication together.
Admitted transfers retain the old client through body closure or spool cleanup;
rotation never closes it beneath them. Leases do not extend credential validity
and cannot prevent the provider from rejecting already admitted work.

The sample application bounds uploads to 10 MiB, two simultaneous staged uploads,
16 total transfers, and 30 seconds per transfer. These settings in `storage.py`
are independent of Gregale account/bucket quotas. Temporary directories are 0700,
files are 0600, and cleanup runs after success, failure, timeout or disconnect.
Spooling consumes guest disk and uses bounded synchronous local file writes;
choose smaller limits for your workload. A late driver return remains owned
after the HTTP caller leaves, with its lease/spool retained until actual cleanup.
Driver failures and provider credentials/URLs are excluded from response/log details.

## Gateway TLS trust

HTTPS always verifies the gateway's certificate chain and hostname. By default,
the driver uses its bundled public CA trust. For a gateway signed by your own CA,
set `AWS_CA_BUNDLE` in the application environment to a readable PEM CA-bundle
file inside the application, for example `/app/runtime/company-ca.pem`. The
Dockerfile already copies `runtime/`; add only the public CA certificates there.
You can set `ENV AWS_CA_BUNDLE=/app/runtime/company-ca.pem` in the Dockerfile or
use Gregale's ordinary application environment configuration. A host file outside
the deployed application is not available inside the guest.

This bundle replaces the driver's default trust for S3; include every root your
gateway needs. Empty settings, missing/malformed files, an untrusted issuer,
expired certificates and hostname mismatches fail HTTPS adoption and keep
readiness closed. Certificate failures never produce a successful adoption ACK.
The setting affects S3 trust; guest loopback ACK delivery retains its own contract.

The path is captured at process startup from the same environment supplied to
`Storage`; an explicit `env` mapping does not fall back to the host's environment
or shared AWS profile trust. Changing the path requires a process restart or
redeployment. Each new transport loads the file again, including on SIGHUP and
same-revision restore. A failed restore keeps readiness closed until a successful
restore, even if an ordinary reload subsequently validates the corrected bundle.
No TLS private key belongs in the application or CA bundle.

## Restore and shutdown

The two-second `lifecycle.after_restore` hook calls `/internal/after-restore`.
Only an actual loopback peer with `X-Faas-After-Restore: 1` can call it; forwarded
headers are not trusted. Keep Uvicorn proxy headers disabled.

Recovery immediately closes readiness, cancels captured transfers and fences the
captured transport. Within a maximum 1.5-second application budget it rereads the
snapshot, even at an unchanged revision, validates a fresh transport and drains
captured bodies/spools before reopening readiness. Concurrent hooks coalesce.
A failed, cancelled or timed-out recovery keeps readiness closed until a successful
restore; an ordinary SIGHUP cannot reopen it. The platform's existing cold-boot
fallback applies when the hook fails. A cancelled upload may already have committed.

SIGTERM/SIGINT stop admission and reloads and allow admitted transfers up to five
seconds to drain. On timeout the app cancels remaining transfer work and reports
incomplete cleanup. It does not forcibly retire a leased transport or guarantee
delivery of unfinished transfers. Bound driver callbacks and keep the loop alive
until owned cleanup finishes when embedding this service elsewhere.

## Local acceptance and maintenance

With the OpenSSL command installed for certificate generation,
`python -m unittest discover -s tests -v` runs portable lifecycle tests and real
SigV4 S3 HTTP/HTTPS fixtures, including subprocesses running the actual handler. It covers
exact bytes, bearer authentication, rotation, bounded/private spools, held and
truncated streams, disconnects, late callbacks, same-revision restore, timeout
recovery, actual SIGHUP and graceful process shutdown. HTTPS cases verify explicit
CA trust, rejected certificates/bundles, failed readiness/ACKs, trust-file reload
and sanitized startup failure. OpenSSL is a test prerequisite; the deployed
application uses the pinned Python dependencies. Fixtures use disposable fake
credentials, ephemeral certificates and loopback sockets. They do not qualify a deployed provider
or native microVM snapshot recovery; those remain separate acceptance gates.

For a Gregale developer environment with operator storage configured:

```sh
gregale dev setup --bucket assets --env-file /absolute/private/files.secrets
gregale dev --bucket assets --env-file /absolute/private/files.secrets
gregale dev info
gregale dev --stop
```

The bundled runtime files are generated copies. In the Gregale repository run
`python sdk/python/scripts/sync_binding_starters.py` after SDK changes; CI checks
drift, complete private-module packaging and the exact materialized starter.

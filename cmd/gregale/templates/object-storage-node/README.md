# Managed object storage for Node compute

This private file service uses a Gregale managed `read_write` bucket binding.
It streams binary downloads and stages uploads on private temporary disk before
streaming them to the gateway with an exact ContentLength. Provider credentials
stay behind Gregale; the application uses the six `GREGALE_S3_ASSETS_*` settings.

## Create and deploy

```sh
gregale init --template object-storage-node --path files-app
cd files-app
npm ci --ignore-scripts
npm test
gregale deploy --create-only --template object-storage-node --name my-files
gregale add bucket assets --app my-files --permission read_write --prefix GREGALE_S3_ASSETS
```

Create a file **outside this source directory**, chmod it to 0600, and put one
`STORAGE_APP_TOKEN=<random token>` line in it. Use at least 32 random characters;
do not commit the file or put tokens in the Dockerfile. Then deploy the source:

```sh
gregale deploy --name my-files --secrets-file /absolute/private/files.secrets
```

Use the normal Gregale app URL from the deployment receipt. Every PUT/GET needs
`Authorization: Bearer <STORAGE_APP_TOKEN>`. Set APP_URL and STORAGE_APP_TOKEN in
your local shell without committing them, then:

```sh
curl --fail-with-body -X PUT "$APP_URL/objects/file.bin" \
  -H "Authorization: Bearer $STORAGE_APP_TOKEN" \
  -H 'Content-Type: application/octet-stream' --data-binary @file.bin
curl --fail-with-body "$APP_URL/objects/file.bin" \
  -H "Authorization: Bearer $STORAGE_APP_TOKEN" --output downloaded.bin
curl --fail-with-body "$APP_URL/readyz"
```

Keys are simple file identifiers: 1–128 letters, digits, dots, underscores or
hyphens, starting with a letter or digit. Downloads are opaque attachments with
`nosniff`, regardless of the uploaded Content-Type. The bucket remains private.
The starter does not expose listing, deletes or signed public URLs.

## Compute lifecycle

The Dockerfile opts into SIGHUP and required application readiness. The bundled
Node SDK coordinator reads the guest's atomic snapshot, applies **both** storage
and HTTP authentication consumers, then acknowledges the exact revision and
execution generation. Missing/invalid secrets or failed storage validation block
new requests. It never falls back to ambient AWS credentials or startup env.

Each candidate creates its own S3 transport and performs ListObjectsV2(MaxKeys=1)
through the gateway before adoption. This checks read/list access, not write
permission or continuous provider health. Use the managed read_write binding and
storage operation canaries for release qualification. Probes consume ordinary
gateway request budgets. `/readyz` reports local adoption and the restore gate;
the included `gregale.yaml` selects it as the hosting health check. `/healthz` is
process liveness. Neither endpoint alone certifies provider availability.

Every transfer holds a request lease through upload-file cleanup or the actual
download-body close event. Rotation stops new borrowing of the old client while admitted streams
can finish. Revocation prevents new acquisition after reload; the gateway can
reject a later operation even when a request already holds a lease. In-flight
provider work is not a revocation barrier. A lease never extends credential rights.

SIGTERM/SIGINT stop admission and reloads, invalidate bindings and drain leases
for five seconds. On deadline the app aborts transfers, closes sockets and exits
with a failure code; it cannot promise delivery of those unfinished transfers.
SDK errors, provider URLs and credentials are excluded from response/log details.

Application safeguards in app.mjs bound uploads to 10 MiB, two simultaneous
staged uploads and 30 seconds per transfer. They are sample application settings;
Gregale account/bucket budgets still apply independently. Uploads use private
0600 files in per-request 0700 temporary directories, removed on completion,
failure, timeout or disconnect. A chunked upload exceeding the byte cap may end
with a closed connection because aborting its stream closes the request socket.
Disk staging consumes guest disk and latency; choose smaller limits for your app.

## Snapshot restore recovery

The manifest's two-second `lifecycle.after_restore` hook calls
`POST /internal/after-restore`. Only an actual loopback peer with
`X-Faas-After-Restore: 1` can invoke it; forwarded address headers do not authorize
the hook. It runs before the public bearer-token gate so recovery can repair failed
adoption without an application token.

Recovery immediately closes readiness, cancels captured transfers and fences the
captured client. Within a maximum 1.5-second application budget, it rereads the
latest atomic snapshot, creates and validates a fresh transport even at an
unchanged revision, adopts HTTP authentication, and drains captured body/spool
leases before reopening readiness. Concurrent hooks share one recovery; owner
cancellation aborts that recovery, while a departing follower leaves it running.
The actual body close event and upload-file cleanup precede client retirement.

Failed, timed-out or disconnected recovery keeps readiness closed until a later
successful hook. SIGHUP alone cannot clear this gate. The platform's existing
cold-boot fallback applies when the hook fails. A cancelled queued/active
validation cannot publish a client or initiate an applied ACK later. Adoption
ACKs describe validated resource/auth adoption; they do not certify the subsequent
captured-resource drain. An ACK already sent cannot be retracted.

Cancellation closes captured HTTP connections promptly, including when a driver
ignores its abort signal. Late provider responses remain owned until bodies close
and spools are removed; they cannot send late success headers. Never retry an
uncertain PUT automatically: it may have committed before cancellation. Revocation
and transfer failures do not replay an operation. SIGTERM/SIGINT also cancel an
active recovery and prevent subsequent adoption.

## Local qualification and maintenance

For the remote developer loop, opt into a private workspace bucket:

```sh
gregale dev setup --bucket assets --env-file /absolute/private/files.secrets
gregale dev --bucket assets --env-file /absolute/private/files.secrets
gregale dev info
gregale dev --stop
```

`dev setup` previews the plan. `dev` provisions the bucket and injects the same
managed settings used by this app. This runs the application on Gregale's
developer environment and uses its configured storage provider. Storage requires
the operator's object storage service and accounting configuration to be ready.
Each local source workspace gets separate storage. Reloading source reuses the
bucket; stopping or lease expiry revokes access and deletes workspace buckets.
An omitted `--bucket` preserves existing resources. A revoked automatic binding
requires stopping/restarting the environment; ordinary reloads cannot regrant it.

`npm test` uses owned local HTTP/client fixtures for authentication, exact bytes,
rotation, failed adoption, cancellation, delayed body closure, restore recovery,
cleanup and shutdown. In the Gregale
repository, `bash scripts/test-storage-node-starter.sh` materializes this embedded
template, installs its lockfile and also starts the real application through the
production SigV4 gateway, S3 adapter and memory store using a local provider fixture.
The journey proves a new gateway connection at the same revision, captured-stream
cancellation without replay, sticky failed recovery, bounded validation cancellation,
restore queued behind SIGHUP, and subsequent recovery before graceful shutdown.
This does not qualify GCS/R2, a deployment, native microVM restore or production.

The dependency-free SDK runtime files are generated copies, not independent
implementations. After changing SDK helpers, run `npm run build && npm run
starter:sync` in sdk/node. The SDK unit gate compares these files with the compiled
canonical helpers. Keep the checked-in lockfile with the starter.

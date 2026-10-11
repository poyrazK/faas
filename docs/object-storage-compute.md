# Object storage from compute workloads

Create a managed bucket binding with `gregale add bucket assets --app my-api`.
The runtime receives six settings under `GREGALE_S3_ASSETS`: `ENDPOINT`,
`REGION`, `BUCKET`, `ACCESS_KEY_ID`, `SECRET_ACCESS_KEY`, and `ADDRESSING_STYLE`.
These describe Gregale's gateway and bucket-scoped credentials for either GCS
or an S3-compatible provider. Applications use the gateway rather than upstream
provider credentials.

## Runtime clients and rotation

The Go, Node and Python SDKs offer `ObjectStorageBinding`. Supply a client
factory using your application's S3 library. The helper validates all six
settings, builds the candidate and publishes a client/bucket pair together.
It does not select an SDK version or load ambient AWS credentials. Your factory
must set the supplied endpoint, region, credentials and path-style addressing.
It can also perform an appropriate readiness request before returning.

For example, with [AWS SDK for JavaScript v3](https://docs.aws.amazon.com/AWSJavaScriptSDK/v3/latest/client/s3/):

```js
import { S3Client, GetObjectCommand } from '@aws-sdk/client-s3';
import { ObjectStorageBinding } from '@gregale/sdk-node/object-storage/runtime';

const storage = new ObjectStorageBinding('GREGALE_S3_ASSETS', options => new S3Client(options));
await storage.reload(process.env); // Startup only; environment values do not change on SIGHUP.

async function readAsset(key) {
  const { client, bucket } = storage.current();
  return client.send(new GetObjectCommand({ Bucket: bucket, Key: key }));
}
```

Go factories receive an `ObjectStorageClientConfig`:

```go
storage, err := faas.NewObjectStorageBinding("GREGALE_S3_ASSETS",
    func(ctx context.Context, c faas.ObjectStorageClientConfig) (*s3.Client, error) {
        return s3.New(s3.Options{
            Region: c.Region, BaseEndpoint: aws.String(c.Endpoint), UsePathStyle: c.UsePathStyle,
            Credentials: credentials.NewStaticCredentialsProvider(c.AccessKeyID, c.SecretAccessKey, ""),
            RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
            ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
        }), nil
    })
if err != nil { return err }
if err := storage.Reload(ctx, snapshot.Secrets); err != nil { return err }
client, bucket, err := storage.Current()
```

With Python and [Boto3 configuration](https://docs.aws.amazon.com/boto3/latest/guide/configuration.html):

```python
import boto3
from botocore.config import Config
from faas_sdk.object_storage_runtime import ObjectStorageBinding

storage = ObjectStorageBinding("GREGALE_S3_ASSETS", lambda c: boto3.client(
    "s3", endpoint_url=c.endpoint, region_name=c.region,
    aws_access_key_id=c.access_key_id, aws_secret_access_key=c.secret_access_key,
    config=Config(s3={"addressing_style": c.addressing_style},
                  request_checksum_calculation=c.request_checksum_calculation,
                  response_checksum_validation=c.response_checksum_validation),
))
storage.reload(snapshot["secrets"])
client, bucket = storage.current()
```

The helpers select required-only checksum behavior, following the
[AWS checksum configuration contract](https://docs.aws.amazon.com/sdkref/latest/guide/feature-dataintegrity.html),
so ordinary reads do not implicitly request optional provider checksum APIs.
Go factories should honor `RequiredChecksumsOnly` as illustrated above. Explicit
checksum requests still depend on the selected provider's capabilities.

For in-process rotation, enable Gregale's secret reload protocol and install the
signal handler before publishing reload readiness. Read the single
`FAAS_SECRETS_SNAPSHOT_FILE` `{revision,secrets}` envelope. Pass its `secrets`
map to `reload`/`Reload`, apply the other secret consumers, then ACK that exact
revision using the process generation. The existing
[`secret-reload-node` helper](../cmd/gregale/templates/secret-reload-node/secret-reload.js)
provides snapshot reading, startup waiting, serialization and fenced ACKs:
call `await storage.reload(snapshot.secrets, signal)` from its application
callback. A helper that merely reads startup environment variables cannot
adopt a later credential rotation.

Acquire `current()`/`Current()` for each new request. In-flight requests may
finish using a previously acquired client; the application owns retired-client
lifetime and must not destroy a client still in use. Reloads are serialized.
A failed candidate or missing/revoked settings disables new acquisition and
returns a sanitized error. Do not send an `applied` ACK on failure. Constructors
alone do not verify credentials; a factory's readiness check and the workload
canary provide separate evidence. An ACK remains an application self-attestation.
Never log configuration or explicitly inspect credential fields.

## Workload canaries and promotion

The default canary remains read-only:

```sh
gregale bindings verify my-api --object-storage GREGALE_S3_ASSETS --deployment v12
```

It performs one bounded bucket listing. To explicitly test a writable binding:

```sh
gregale bindings verify my-api --object-storage GREGALE_S3_ASSETS \
  --object-storage-write --deployment v12 --json
gregale bindings check my-api --deployment v12 --require-application-ack
```

The API requires storage management authorization, a current managed binding in
the selected deployment's scope, an exact permission match and a ready bucket
without known Object Lock enabled. It pins the binding revision and mode in
durable task metadata. Update the API/migrations and guest-init before using the
new flag; older guests cannot produce passing write evidence.

| Binding | Default probe | Explicit write probe |
| --- | --- | --- |
| `read` | List at most one object | Rejected before starting a task |
| `write` | Requires explicit opt-in | Conditional tiny PUT, DELETE acknowledgement; no GET or listing |
| `read_write` | List at most one object | Conditional tiny PUT, byte-for-byte GET verification, DELETE, GET confirming absence |

These are ordinary metered operations and can trigger matching object notifications.
Each write probe chooses its own cryptographically random key below
`.gregale/binding-probes/`, uploads fewer than 64 bytes with `If-None-Match: *`,
and deletes the exact returned version when available. It never accepts an
arbitrary customer key. The operation budget is five seconds, with a separate
five-second cleanup budget that survives cancellation. There are no SDK retries
or redirects; replies are bounded. Cleanup runs after a failed download.
Reports contain operation statuses, not object names, endpoint URLs, credentials
or provider diagnostics.

Write-only permission cannot independently verify absence: its cleanup stage
records the DELETE acknowledgement. Lost PUT responses have an unknown version
identity and never produce passing cleanup evidence. Process termination,
provider outages or a concurrent retention-policy change can leave a tiny probe
behind; inspect the reserved prefix after a failed test. The canary never bypasses
retention and does not claim crash-proof cleanup. `--all` does not opt into writes;
write-only bindings appear as partial coverage requiring explicit opt-in.

The admitted mode must match the returned report. Missing download/cleanup
stages, legacy reports for write tasks, nonzero exits and truncated output never
qualify. The existing binding check and promotion gates consume the normalized
operation stages; a failed cleanup makes the latest evidence fail. Existing
read-only evidence retains its original meaning, and promotion does not
automatically require or execute writes. Inspect the recorded stages when a
release requires write-path qualification. Probes run in task guests; they do
not prove resident application adoption, which remains a separate ACK gate.

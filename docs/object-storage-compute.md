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
read-only evidence retains its original meaning. Release policies can require
specific operations per prefix; promotion consumes that evidence and never
executes writes. Probes run in task guests; they do
not prove resident application adoption, which remains a separate ACK gate.

## Required storage operations before traffic

Declare storage requirements per managed environment prefix in one deployment
scope. Setting a policy replaces its complete requirements map; include every
prefix you want to retain. Policy updates use compare-and-set revisions.

```sh
gregale bindings release-policy set my-api --scope production \
  --mode enforce --expected-revision 0 --max-age 10m \
  --object-storage-require GREGALE_S3_ASSETS=read_write \
  --require-application-ack
```

`read` accepts the legacy bucket-listing canary. It does not prove object byte
reads. `write` requires successful conditional upload and cleanup; `read_write`
also requires byte-for-byte download. A complete read/write canary can satisfy
any of these requirements. Write-only cleanup acknowledges deletion; it cannot
independently read back absence. Existing cleanup and Object Lock limits still
apply. Neither setting policy nor increasing traffic schedules a canary.

Collect write evidence explicitly against the exact live candidate, then check
it against the stored scope policy:

```sh
gregale bindings verify my-api --deployment v12 \
  --object-storage GREGALE_S3_ASSETS --object-storage-write
gregale bindings check my-api --deployment v12 --scope production \
  --release-policy --wait --json
```

`--release-policy` resolves and pins the live deployment and its scope, verifies
the app identity, and reads the stored policy. An enforced policy supplies storage
operation requirements and application ACK requirements, disallows unsupported
binding waivers, and limits verification age. Local flags can make these checks
stricter: the shorter verification age applies (the CLI default is 10 minutes),
ACK requirements are combined, and different storage modes for the same prefix
combine into `read_write`. Additional local prefixes remain required. A policy
with mode `off` applies only the local flags and is reported as disabled.

The CLI confirms the same policy after every inventory evaluation, including
blocked iterations with `--wait`. A changed revision or policy content, failed
confirmation, invalid app/scope, or an unavailable policy API blocks the check;
an older server cannot silently fall back to local flags. JSON includes the
`release_policy` snapshot and `release_policy_confirmed`; the normal report fields
show the effective requirements. A blocked check can still have a confirmed
policy. `--timeout` bounds the entire policy-aware check, even without `--wait`.
Waiting observes already scheduled probes and ACKs; it never schedules writes.

Without `--release-policy`, `bindings check` retains its local flag behavior.
Use `bindings release-policy get` to inspect the stored policy separately.

To collect every storage canary required by an enforced policy, preview the plan
and then explicitly consent to its writes:

```sh
gregale bindings verify my-api --deployment v12 --release-policy --dry-run --json
gregale bindings verify my-api --deployment v12 --release-policy \
  --object-storage-write --wait-timeout 5m --json
```

This mode requires a nonempty stored storage-requirements map, a complete
inventory, a valid configuration timestamp and one active, settled binding for
each prefix. It selects and reconfirms the exact deployment and scope even when
`--deployment` is omitted. `--dry-run` performs only metadata reads: `status` is
`planned`, `passed` is false, and `write_consent_required` identifies whether the
whole plan needs opt-in. Preview success does not test connectivity or readiness.

Without consent to every planned write, the entire batch stops before creating
any task. `read` runs a bounded bucket listing. `write` runs upload and cleanup;
if the binding grants `read_write`, the canary also downloads and compares bytes,
and the preview shows that extra operation. Consent does not broaden a binding's
permission. Existing retention/Object Lock admission and cleanup rules still
apply. `verify --all` and single-prefix verification keep their existing behavior.

The command checks the original policy and binding configuration before each
admission and after each successful probe, and stops on the first failure or
observed change. It finishes with a policy-aware preflight against that same
snapshot. JSON contains `plan`, `probes`, `release_policy`, `snapshot_confirmed`
and `preflight`, including remaining application ACK or other binding blockers.
A successful canary batch can therefore still return exit status 1. Canaries
never supply application ACKs or automatically verify unrelated binding families.

`--wait-timeout` bounds the entire workflow; it does not reset for every prefix.
An ordinary timeout keeps the existing task running and reports its ID; inspect
it before retrying. An interrupt requests cancellation and returns exit status
130. Completed probe results remain in the report when a later step fails.

These confirmations observe configuration; they do not lock it. Changes can race
task admission or execution. The existing server-side task revision metadata
prevents stale evidence from qualifying, and promotion still makes its atomic
policy/catalog decision at the traffic write. The command performs no traffic,
policy or binding updates, and does not certify resident application adoption.

Confirmation describes the last read and does not authorize a later traffic
change. Server-side promotion,
traffic redistribution, and existing checked rollout/recovery paths consume the
stored requirements for each deployment gaining traffic. A missing/ambiguous
prefix, insufficient permission, incomplete or failed stages, stale binding or
credential revision, wrong deployment/scope, expired evidence, pending rotation,
or missing required application ACK blocks the release. Waiving unsupported
bindings cannot waive storage operations. Application ACKs remain independent
version-bound self-attestations, not proof of readiness or credential use.

Storage policy updates use a dedicated route that old servers reject. A legacy
policy update cannot silently remove existing requirements. Use the repeated
`--object-storage-require` flags to retain/replace them, or explicitly clear them:

```sh
gregale bindings release-policy set my-api --scope production \
  --mode enforce --expected-revision 1 --clear-object-storage-requirements
```

The locked traffic write compares the evaluated requirements and policy revision
again. Config, probe completion, runtime observations, rotation, or policy changes
invalidate the promotion fence; collect current evidence and retry. Rolling back
the requirements migration is refused while any policy has a nonempty map; the
guard locks policy writes so concurrent updates cannot evade it. Clear requirements
explicitly before a reviewed rollback.

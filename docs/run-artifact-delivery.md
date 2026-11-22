# Durable Run artifact delivery

Promote a declared Run output into an application's existing private object
storage bucket. The delivered object follows that bucket's retention and access
policy, independently of the temporary Run file's 24-hour lifetime and the
original Agent thread callback. Delivery copies bytes; it never executes code.

This is a preview under the existing Run API/v2 and object storage switches.
`gregale sandbox capabilities --json` exposes `artifact_deliveries`. Existing
receipt reads and exact replay remain available when new Run writers stop.

## Configure a destination

Create a private app-owned bucket using [object storage](object-storage.md), then
declare an upload route with the management API:

```http
POST /v1/apps/report-agent/upload-routes
Content-Type: application/json

{
  "name": "reports",
  "bucket_id": "YOUR_BUCKET_UUID",
  "key_prefix": "customer-reports",
  "max_bytes": 8388608,
  "allowed_content_types": ["text/html", "application/pdf", "text/csv"],
  "enabled": true
}
```

The upload route owns the bucket, prefix, allowed content types, byte limit and
encryption selection. A delivery cannot choose an arbitrary object-store URL or
overwrite an application-selected object key. Keys are generated under the
route prefix. HTML and other active document types require an explicit allowlist
entry and cannot be delivered to a bucket served publicly on the app's hostname.

Account API callers need deploy/write authority, current app access, and a bucket
write grant. Download URL issuance needs a bucket read grant. A production app
may instead present its live `gregale:agents` workload token: it reaches its own
app's routes and outputs delegated to its Agent endpoints. Temporary callback
URLs alone authorize no copy or download URL.

## Deliver and inspect

```sh
gregale run deliveries deliver report-agent reports \
  --run RUN_UUID --artifact combined.html --content-type text/html \
  --idempotency-key customer-report-001 --wait --json

gregale run deliveries status report-agent reports DELIVERY_UUID --json
gregale sandbox delivery sign report-agent reports DELIVERY_UUID --json
```

The CLI obtains size and SHA-256 from the source receipt. It refuses unsuccessful
Runs and invalid result contracts. Waiting polls only the storage receipt; timeout
does not cancel a provider write. A failed receipt gives a nonzero exit status.

API clients POST to `/v1/apps/{slug}/run-artifact-deliveries/{route}`:

```json
{
  "idempotency_key": "customer-report-001",
  "execution_id": "RUN_UUID",
  "artifact_name": "combined.html",
  "size_bytes": 1024,
  "sha256": "sha256:EXPECTED_64_HEX_DIGEST",
  "content_type": "text/html"
}
```

Use the real Run receipt's size/digest, not the illustrative values above. The
server checks source authorization, success and contract validity, verifies the
entire artifact before destination dispatch, and returns a storage receipt:

```json
{
  "id": "DELIVERY_UUID",
  "status": "completed",
  "size_bytes": 1024,
  "content_type": "text/html",
  "created_at": "2026-11-17T15:00:00Z",
  "receipt_url": "/v1/apps/report-agent/run-artifact-deliveries/reports/DELIVERY_UUID",
  "object": {"bucket_id": "BUCKET_UUID", "key": "customer-reports/run-artifacts/GENERATED_KEY/combined.html"}
}
```

| Status | Meaning | Action |
| --- | --- | --- |
| `completed` | Destination write has settled successfully. | Persist the receipt/object reference. |
| `pending` | The provider or settlement acknowledgement is uncertain. | Poll the same receipt; keep the same delivery key. |
| `failed` | This storage attempt has a definitive failure. | Inspect `error_code`; deliberately choose a new key to request a new delivery attempt after remediation. |

Pending responses use HTTP 202 and `Retry-After`; settled responses use HTTP 200.
`object` is absent until completion. Changing source or metadata under the same
app/route/key conflicts. A new key means a new object and ordinary storage usage.
Never generate a fresh key merely because an acknowledgement was lost.

The object storage recovery worker probes the exact original provider receipt;
it does not upload again. Replay reads the original delivery before consulting
source bytes, so a committed delivery survives source expiry, callback revocation
and disabled Run writers. Current destination authorization still applies. Deleting
the upload route removes this route's receipt access; retain it while callers
need these receipt locators. The delivered object remains governed by its bucket.

## Customer downloads

Persist `receipt_url` and `object` as metadata. They are authenticated locators,
not public download links. Your application authenticates the customer and checks
that the delivery belongs to them before POSTing to
`{receipt_url}/download-url`. The response is the existing branded object GET
capability with a five-minute expiry. Keep that bearer URL out of Agent history
and logs; issue it when the customer requests the download.

The object reference includes a public `version_id` when available. Without
versioning, app-owned objects retain normal mutation semantics. Bucket deletion,
object mutation, lifecycle and retention continue to apply; delivery promises no
indefinite retention or object-lock policy override.

## SDKs and Agent continuation

The Go client exposes `DeliverRunArtifact`, `GetRunArtifactDelivery` and
`SignRunArtifactDelivery`. The Node preview client exposes `deliverRunArtifact`,
`getRunArtifactDelivery` and `signRunArtifactDelivery`; Python's preview `api.runs`
provides the corresponding sync/async operations. Allow up to the two-minute
transfer budget in your client's request timeout, and preserve the original intent
if the response is uncertain.

The stateless Node platform helper exposes `deliverRunArtifact(app, route, body)`,
`artifactDelivery(app, route, id)` and `signArtifactDelivery(app, route, id)` with
fresh workload identity. The [report Agent example](../examples/gregale-run/README.md)
can use a managed destination without a custom binary report storage adapter:

```js
const processRecord = createReportAgent({
  prefix: 'gr-agent', store, platform,
  destination: { app: 'report-agent', route: 'reports' },
});
```

The completion ID supplies a stable delivery key. A pending or failed copy keeps
the thread open for inspection instead of finalizing a broken report. A successful
delivery reference enters the same transaction as the persisted final Agent turn; callback
revocation follows that commit. On inbox redelivery, the persisted turn finishes
without reopening temporary receipts or copying the report again. Storage and the
Agent transcript remain distinct commits: replay recovers a completed delivery if
the transcript transaction was interrupted.

## Operator behavior

Delivery staging uses a bounded anonymous temporary file, unlinked before any
plaintext is read. At most two transfers per apid process stage at once, each
bounded by the existing 64 MiB hard file limit and two-minute deadline. Busy
staging admits no destination write. Both the temporary Run copy and the durable
customer object count under their respective existing storage budgets while
retained. There is no new guest protocol or VM lifecycle change.

For rollback, disable new Run v2 writers and retain the compatible object storage
receipt readers and recovery workers until pending writes settle. This feature
does not activate a deployment or widen native runtime qualification.

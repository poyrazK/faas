# Gateway safety accounting qualification

This opt-in preview enables provider-neutral admission for new buckets while
customer billing stays off. See [ADR-627](adr/627-prospective-gateway-storage-safety-accounting.md).
It does not supply provider invoice costs, stored byte-hours or qualified
customer read/write billing classes. Egress is a conservative response-length
reservation; interrupted downloads retain the full reservation.

## Local qualification

Build the current CLI, then run the actual API/gateway journeys with a
disposable PostgreSQL database. Do not point `DATABASE_URL` at production.

```sh
go build -o /tmp/gregale-storage-qualification ./cmd/gregale
GREGALE_OBJECT_STORAGE_E2E_CLI=/tmp/gregale-storage-qualification \
  go test ./cmd/apid -run '^TestObjectGatewaySafetyE2E(Mem|PG)$' -count=1 -v
go test ./pkg/state -run '^TestGatewaySafety' -count=1 -race
go test ./pkg/objectstorage ./pkg/s3gateway -run 'TestGateway|TestPublicReadHandlerServes' -count=1
```

With the CLI variable set, the journey provisions an app bucket and binding
through the real CLI, verifies idempotency/discovery, performs signed PUT,
GET, HEAD and range reads through the real gateway and S3 adapter against a
local wire fixture, observes live counters, rejects an oversized replay, then
cleans up with admission closed. It imports no provider usage reports.
Memory/PostgreSQL contracts also test concurrent request/egress reservations,
replacement of the store instance, legacy report gates and incomplete coverage.
Public upload-route tests race concurrent dispatches against the same request
budget and verify that only the remaining admitted request reaches the writer.
The local wire fixture does not qualify native GCS or production routing.

Native GCS qualification is a separate opt-in journey described in
[ADR-628](adr/628-gcs-tracked-writes-and-native-generations.md). The provider
journey covers native receipts, generation-fenced copies and deletions,
version controls and reads, multipart copy, and managed AES256. The local
API/gateway journey uses the built CLI against caller-provisioned disposable
GCS buckets. It covers file upload/download, automatic multipart, receipt and
session inspection, encryption controls, copy grants, deletion, inventories
and safety usage. Neither journey qualifies production routing or every CLI
argument. Object Lock, KMS/DSSE, conditional writes, version-specific tagging
and checksum-mode version reads remain unsupported by the GCS adapter.

## Production preparation

The release must include both gateway safety accounting (ADR-627) and the
native GCS transfer/generation contracts (ADR-628), with their current-head CI
green. Publish and verify a signed release through the normal release pipeline;
use `cd-platform.yml` with the environment belonging to the target fleet. For
the `gregale-prod` GCP fleet that environment is `production-us`. Complete the
fleet release acceptance gates before object-storage activation.

Keep `s3_enabled=false` while rolling out a reviewed release containing the
new metering code to apid, s3-gatewayd and gatewayd-public. All three serving
components must use the same accounting policy. Keep pricing absent and
object-storage billing delivery disabled, including shadow delivery.

Native GCS needs no BigQuery export for this safety mode. Retain the existing
keyless provider impersonation and dedicated storage-project IAM boundary.
The operator explicitly accepts that there is **no monetary provider-cost
ceiling**. Independent finite budgets, short URL lifetimes and provider-side
controls reduce exposure; these meters do not capture every provider-internal
operation or traffic outside Gregale.

After all components are upgraded, record a fixed current UTC coverage start.
Do not backdate it. Restarting components preserves that value. Buckets created
before it cannot qualify; they retain cleanup access and need legacy reports
or a separately qualified migration. An illustrative small qualification policy:

```json
{
  "accounting_mode": "gateway_safety_v1",
  "gateway_metering_since": "<actual UTC coverage start>",
  "max_account_bytes": 16777216,
  "max_bucket_bytes": 8388608,
  "max_account_keys": 100,
  "max_monthly_cost_millicents": 0,
  "max_monthly_requests": 1000,
  "max_monthly_egress_bytes": 67108864,
  "max_monthly_authorizations": 1000,
  "max_report_age_seconds": 900
}
```

Merge that policy into `accounting` in the existing operator storage config;
preserve the GCS placement identity and all unrelated configuration. The
timestamp placeholder is deliberately invalid until replaced. Require
`transfer.profile=proxied`. Restart/reload the serving components and verify
health and billing-off state before enabling the global flag through the
operator API with recent MFA:

```sh
FAAS_APID_URL=https://api.gregale.dev gregalectl auth login --email hpk.poyraz@gmail.com
FAAS_APID_URL=https://api.gregale.dev gregalectl config set \
  --key s3_enabled --value true --reason "GCS gateway safety qualification" --yes
```

Use the current CLI build: older installed releases lack the bucket command
family. Set `FAAS_TOKEN` locally through the existing secure credential workflow;
do not paste it into chat or enable shell tracing. The smoke runner keeps
temporary S3 credentials in a private directory and revokes them during cleanup:

```sh
GREGALE_BIN=/path/to/current/gregale GREGALE_APP_SLUG=e2e-probe \
  deploy/scripts/s3-gateway-smoke.sh
```

The runner waits for a genuine complete inventory, exercises CLI provisioning,
bindings, notification/lifecycle discovery, tracked writes, S3 PUT/GET/HEAD/list
and deletion/revocation, and verifies that gateway usage increased with billing
off. Run broader native-GCS multipart, copy and supported CLI capability probes
separately. Native capabilities such as Object Lock/KMS must report unsupported
when unavailable; do not treat those errors as successful feature qualification.
Managed-job direct object URLs and full-copy environment clones are unavailable
in this preview until their bypass paths have bounded meters.

On a qualification failure, close the global flag through the same operator API
and finish cleanup. Do not replace stale evidence with fabricated reports.
Preserve the upgraded deletion journal: the GCS migration refuses to roll back
while captured native generation fences exist. Disabling admission preserves
metadata and cleanup; it does not immediately revoke previously issued URLs.
Provider-cost reconciliation, exact stored-time billing and a v2 billing cutover
remain separate work.

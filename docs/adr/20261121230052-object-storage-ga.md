# ADR-20261121230052: Object storage for general availability on OVH

- **Status:** accepted
- **Date:** 2026-11-21
- **Owner:** apid and s3-gatewayd (object storage) maintainers
- **Milestone:** object storage GA on OVH
- **Related:** [ADR-1320](1320-object-storage-plan-allowances-ceilings-and-overage-cap.md) (plan allowances and ceilings), [ADR-1430](1430-plan-entitlements-from-october-2026-cogs-report.md) (pricing), [ADR-20261101214152](20261101214152-object-storage-ceiling-follows-spend-cap.md) (paid ceilings), [ADR-20261104145353](20261104145353-mirror-billing-object-requests-signup-cap.md) (monthly request ceilings), [ADR-20261031122757](20261031122757-s3-gateway-on-compute-nodes.md) (monthly egress ceilings), [ADR-20261024132844](20261024132844-gateway-metered-object-storage-billing.md) (gateway-metered billing), [ADR-20261023115741](20261023115741-every-capability-behind-a-platform-flag.md) (capability flags), [ADR-20261121150000](20261121150000-unversioned-s3-backends-read-the-null-version.md) and its two amendments (OVH as an unversioned backend), [ADR-20261031091607](20261031091607-managed-postgres-ga.md) (the GA promotion this one follows)

## Decision

Managed object storage is generally available in the next release. Its
production backend is OVH Object Storage (`ovh-bhs-prod-v1`, region BHS).

In `pkg/productcap/catalog.json`, these move from `preview` to `ga`. Each keeps
the plans it lists today:

| Capability | What it covers | Plans |
|---|---|---|
| `object-storage` | buckets, public buckets, usage, `gregale add bucket`, `gregale.yaml` buckets; operator switch `s3_enabled` | Free, Hobby, Pro, Scale |
| `s3-credentials` | bucket-scoped S3 credentials for `s3.gregale.dev` | Free, Hobby, Pro, Scale |
| `signed-urls` | signed URLs and upload routes | Free, Hobby, Pro, Scale |
| `multipart-uploads` | multipart uploads and their sessions | Free, Hobby, Pro, Scale |
| `object-storage-compute` | bucket bindings to apps, notifications and compute triggers | Hobby, Pro, Scale |
| `object-storage-key-scopes` | bindings limited to one key prefix | Free, Hobby, Pro, Scale |

`object-storage`'s acceptance test becomes the customer journey
`tests/journeys/object_storage_test.go::TestObjectStorage`. The other five keep
their catalogued tests.

These stay `preview`, because the production backend cannot serve them yet:

- **`object-versioning` and `object-protection` (Object Lock).** OVH answers
  every object with a version ID but has no `*Version*` object actions, so
  `ovh-bhs-prod-v1` declares `"versioning": "unsupported"`
  (ADR-20261121150000). Object Lock also needs creation-time Object Lock. The
  OVH runbook names that as ADR-20261119065000, which is not on `main` yet.
- **`bucket-encryption`, `object-lifecycle`, `bucket-cors` and
  `bucket-access-grants`.** They are not qualified on OVH.

`/docs/object-storage` is published, and it says which parts are GA and which
stay preview. `docs-links-check` refuses an unpublished docs page for a GA
capability. The `bucket` CLI command was already in the customer audience. Its
GA verbs now appear in the public CLI reference; the preview verbs stay hidden
from accounts they are not released to.

## What GA promises

**Plans.** These are the `ObjectStoragePlanLimits` values in
`pkg/api/limits.go` (ADR-1320, ADR-20261101214152, ADR-20261104145353,
ADR-20261031122757):

| Plan | Included storage (monthly average) | Storage ceiling | Requests per month | Egress per month |
|---|---|---|---|---|
| Free | 1 GiB | 1 GiB | 50,000 | 10 GiB |
| Hobby | 10 GiB | 20 GiB | 500,000 | 100 GiB |
| Pro | 50 GiB | 250 GiB | 2,500,000 | 500 GiB |
| Scale | 200 GiB | 1,000 GiB | 10,000,000 | 2,000 GiB |

- **Price.** On paid plans, stored bytes above the included amount cost $0.03
  per GiB-month (`ObjectStorageMillicentsPerGiBMonth`, ADR-1430), up to the
  ceiling. Free is never billed for storage and stops at 1 GiB. Requests and
  egress are limits, not meters: they are not billed.
- **Ceilings.** A spend cap lowers a paid ceiling to what the cap pays for
  (`WithSpendCap`). Support can raise a paid account's ceiling.
- **On every plan:**
  - 1,000,000 objects per account, counting retained versions
    (`MaxObjectStorageAccountKeys`);
  - 100 requests per second per account through `s3.gregale.dev`, in bursts
    of 200 (`ObjectStorageAccountRequestsPerSecond`);
  - 10 buckets per app by default, which an operator can raise to at most 100
    (`DefaultObjectBucketsPerApp`, `MaxObjectBucketsPerApp`).
- **Object Lock retention** (`MaxLockRetentionDays`) is not part of this
  promise, because Object Lock stays preview.

**Availability.**
- **One region.** OVH Object Storage region BHS, in its own OVH Public Cloud
  project (`gregale-customer-storage-us-1`). S3 clients reach it as `us-east-1` at
  `https://s3.gregale.dev`.
- **No HA claim.** `s3-gatewayd` runs on the control-plane node
  (`object_storage.gateway: managed`). There is no multi-region or
  high-availability claim.

**Data safety.**
- **Object bytes** rely on OVH Object Storage's own durability. Gregale states
  no durability figure of its own.
- **Gregale's metadata** lives in control-plane PostgreSQL and is covered by its
  existing backups. That metadata is the bucket catalog, bindings, credentials,
  write receipts and usage.
- **No copy outside OVH.** Gregale keeps no copy of object bytes outside the
  provider (open item 2).
- **No undo on OVH.** Versioning is unavailable there, so an overwrite or a
  delete is final. A bucket is deleted only when it is empty, and an app with
  buckets cannot be deleted.
- **Deleted accounts.** Account deletion and non-payment keep the existing
  30-day grace before buckets are emptied and deleted.

## Acceptance evidence

1. **The customer journey.** `tests/journeys/object_storage_test.go::TestObjectStorage`,
   run against production (full tier). It does what a customer does:
   - create a bucket, list it, and retry the create without getting a second
     bucket;
   - mint an S3 credential;
   - round-trip a Unicode key and an empty object through signed URLs;
   - put, get, multipart-upload, copy, list and delete objects with an AWS S3
     SDK on the branded endpoint;
   - check that another tenant's credential, a forged signature, an unsigned
     request and a revoked credential reach nothing;
   - check that a non-empty bucket cannot be deleted;
   - empty the bucket and delete it.

   **The result of its production run is recorded in `docs/STATUS.md` after
   the release that carries this ADR reaches production.**
2. **The live OVH capability probe of 2026-11-21.** These were verified
   directly against OVH:
   - conditional writes (`If-None-Match: *`, `If-Match`);
   - all five checksum algorithms (CRC32, CRC32C, CRC64NVME, SHA1, SHA256);
   - copy, tagging, and the S3 listings Gregale reads.
3. **The fixes for what the smoke and the probe found:**
   - [#7762](https://github.com/poyrazK/faas/pull/7762): OVH's version IDs
     on unversioned buckets are read as the `"null"` version
     (ADR-20261121150000);
   - [#7792](https://github.com/poyrazK/faas/pull/7792): the S3 gateway's
     own dispatches drop those IDs too, and Gregale accepts OVH's final
     `ListParts` page that names its last part (the two amendments).

## Rollout

The six capability flags are currently stored with `default: false` and an
`accounts` rule. That rule releases them to the owner's account and the
release-acceptance account:

- `platform_flag.object_storage`
- `platform_flag.s3_credentials`
- `platform_flag.signed_urls`
- `platform_flag.multipart_uploads`
- `platform_flag.object_storage_compute`
- `platform_flag.object_storage_key_scopes`

A stored definition wins over the tier default, so the release opens object
storage to nobody new. In this order:

1. **Before the release reaches production,** check that each of the six
   flags still has a stored definition
   (`gregalectl flags show --key <key>`). A flag without one would follow the
   new GA default and open at once.
2. **After the release reaches production:**
   - `s3_enabled` is on;
   - the test buckets that the pre-#7792 smoke and probe latched as
     versions-observed are cleaned up by hand (ADR-20261121150000,
     consequences).
3. **Run the journey** with the release-acceptance account:
   `scripts/e2e-journeys.sh --tier full --run TestObjectStorage`.
   - Record the result in `docs/STATUS.md`.
   - Once it passes, remove `TestObjectStorage/object_storage_enabled` from
     `tests/journeys/known_failures.json`.
4. **Only after the journey passes,** the operator makes each flag's stored
   default `true`, after a TOTP step-up. Each write keeps the flag's `accounts`
   rule.

   ```sh
   gregalectl auth step-up
   gregalectl flags default --key s3_credentials --value true --reason "object storage GA (ADR-20261121230052)" --yes
   gregalectl flags default --key signed_urls --value true --reason "object storage GA (ADR-20261121230052)" --yes
   gregalectl flags default --key multipart_uploads --value true --reason "object storage GA (ADR-20261121230052)" --yes
   gregalectl flags default --key object_storage_compute --value true --reason "object storage GA (ADR-20261121230052)" --yes
   gregalectl flags default --key object_storage_key_scopes --value true --reason "object storage GA (ADR-20261121230052)" --yes
   gregalectl flags default --key object_storage --value true --reason "object storage GA (ADR-20261121230052)" --yes
   ```

   - **The parent goes last.** A sub-feature is released only while its
     parent is, so customers get the whole product at once.
   - **Write each `--key` separately.** Do not use
     `--capability object-storage` here. It selects every flag under
     `object-storage`, and that would also open the six preview children.

## Rollback

```sh
gregalectl flags disable --capability object-storage --reason "<why>" --yes
```

This turns off the kill switch of `object_storage` and every sub-feature flag
under it, for every account. apid then answers their routes as if they did not
exist, and the CLI and dashboard hide them.

**What keeps working.** These flags are decided in apid only: their catalog
entries name no other consumer, and s3-gatewayd does not read platform flags.
Existing S3 credentials, app bindings and already-issued signed URLs therefore
keep working against `s3.gregale.dev`.

**To stop the data path as well,** turn off the installation switch:

```sh
gregalectl config set --key s3_enabled --value false --reason "<why>" --yes
```

With it off:
- new buckets, URL issuance, multipart initiation, reads and listings are
  refused;
- deletes and cleanup continue
  ([hot enable / disable](../ops/object-storage-operator-notes.md#hot-enable--disable)).

**Back to the two released accounts only.** To return to the owner and
release-acceptance accounts without hiding anything from them, set each
flag's default back with `gregalectl flags default --key <key> --value false`.

## Open items

Not resolved here.

1. **Versioning and Object Lock on OVH.** OVH has no `*Version*` object
   actions, so `ovh-bhs-prod-v1` is `"versioning": "unsupported"`. Object Lock
   waits for creation-time Object Lock. Until both exist, an overwrite or
   delete cannot be undone.
2. **No copy outside OVH.** A lost OVH project, or a lost OVH account, loses
   its buckets' objects. Only Gregale's metadata is backed up.
3. **Bucket quota of the OVH project.** The registry caps the backend at
   `max_buckets: 95` until OVH raises the project quota to 1,000
   (`docs/runbooks/GcpToOvhMigration.md`). At the cap, new buckets are refused
   for every account.
4. **Egress allowances.** They were sized for GCP egress costs and are to be
   revisited for OVH (runbook, billing review R8-1).
5. **Provider cost.**
   - Production runs `gateway_safety_v1` accounting, which has no monetary
     provider-cost ceiling.
   - Provider-cost reconciliation, exact stored-time billing and a v2 billing
     cutover remain separate work
     (`docs/object-storage-gateway-safety-qualification.md`).
6. **Bypass paths.** The same notes list managed-job direct object URLs and
   full-copy environment clones as unavailable until their bypass paths have
   bounded meters.
7. **Deferred in the operator notes**
   (`docs/ops/object-storage-operator-notes.md`, qualification and launch
   checklist):
   - tenant native storage keys;
   - the S3 compatibility gaps listed there;
   - production edge and service activation;
   - unsupported lifecycle and version behavior;
   - untracked object-capacity rebasing;
   - historical untracked multipart reclamation;
   - automatic migrations.
8. **SDK binding helpers.** They still live in the SDKs' preview namespaces
   (`@gregale/sdk-node/preview/object-storage/runtime`,
   `faas_sdk.preview.object_storage_runtime`, `sdk/go/preview`;
   `docs/object-storage-compute.md`).
9. **What the journey does not run.** It does not exercise:
   - public bucket mounts;
   - upload routes;
   - app bindings, notifications and compute triggers;
   - key-scoped bindings.

   Their acceptance rests on their catalogued tests, not on a production run.

## Why

- **The OVH launch blockers are fixed.** The 2026-11-21 production smoke and
  capability probe found them, and #7762 and #7792 fix them.
- **The GA mechanics are in place:**
  - plan ceilings and allowances;
  - storage billing metered by the gateway;
  - request and egress ceilings;
  - spend-cap handling;
  - the account-deletion purge.

  The operator notes' GA audit (2026-10-26) lists them.
- **The preview parts cannot work, or are unproven, on the one backend
  customers would use.** Releasing versioning or Object Lock on OVH would sell
  features that answer errors. The other four have no OVH evidence.

## Consequences

- **Flags.** The release changes nothing for any account until rollout step 4.
  An operator-written rule for an account keeps deciding for that account.
- **Discovery follows the tier default.** That covers:
  - logged-out CLI help;
  - the public CLI reference (`docs/cli-reference.md`);
  - the GA and beta SDK spec (`sdk/openapi.yaml`) and the public Node and
    Python SDKs.

  All of them now include the GA object-storage commands and paths. The
  preview children stay out.
- **Bucket grants for API keys stay preview** (`bucket-access-grants`). An
  API key without the `admin` scope cannot be granted a bucket until it is
  released. Customers use admin keys, S3 credentials or app bindings.
- **Docs.** `/docs/object-storage` is published. The inventory splits row 108
  into GA (108) and preview (175).

## Rejected alternatives

- **Promote every object-storage sub-capability.** Versioning and Object Lock
  cannot work on OVH. Encryption keys, lifecycle rules, CORS and bucket access
  grants have no OVH evidence.
- **Flip the defaults with `--capability object-storage`.** It would release
  the preview children too.
- **Wait for versioning or an off-provider copy before GA.** Both need provider
  or storage work that does not exist yet. The promise states the gap instead:
  one region, provider durability, no undo.

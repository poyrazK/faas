# Customer Job Operations export

A complete internal preview starter: one tenant-owned batch Job generates a
private CSV, reports progress and prepares a typed result. The browser restores
submissions, reads history, subscribes to progress, requests cancellation and
downloads the confirmed result. Completion delivery is displayed separately.

Production admission remains closed. This starter does not install a cohort or
qualify native execution, public ingress, provider behavior or fleet rollback.
Use it with an operator-qualified Job preview after the required evidence passes.

## Initialize and install

```sh
gregale init --template customer-operation-job-export --path customer-operation-job-export
```

The SDK is internal and unpublished. From an authorized checkout, build and pack
it, then include the archive in this source directory:

```sh
cd <gregale-checkout>/sdk/node
npm ci --ignore-scripts
npm run build
npm pack --ignore-scripts
cd <generated-source>/customer-operation-job-export
mkdir -p packages
cp <gregale-checkout>/sdk/node/gregale-sdk-node-0.1.0.tgz packages/gregale-sdk.tgz
npm install --ignore-scripts
npm test
gregale customer-operations validate --dir . --app <slug> --plan pro
gregale customer-operations types --dir . --app <slug> --plan pro
```

The final command writes `customer-operations.generated.d.ts` from the
validated input and output schemas. The browser starter references those types
in JSDoc, so its `CustomerOperationFeature` session exposes a typed `start()`
input and typed operation result. Regenerate after changing `gregale.yaml` or
either schema; the server remains authoritative at runtime.

Commit the generated declaration and add the read-only freshness check to CI:

```sh
gregale customer-operations types --dir . --app <slug> --plan pro --check
npm run typecheck
```

The freshness check exits nonzero when the file is missing or stale and never
rewrites it. `npm run typecheck` checks the browser app and Operation input/result
types against the generated declarations and packed SDK without emitting files.

Retain `packages/gregale-sdk.tgz` and the generated `package-lock.json` in both
the app source and customer-owned image build context. Neither task nor server
imports from the Gregale checkout. Pro supports the 512 MB example Job.

## Create the Job and prepare the app

Build `Dockerfile.job` in your existing image pipeline for Linux/amd64 and publish
it to your registry. Use an approved, patched Node 22 base and pin its digest in
that pipeline. Register the resulting immutable image digest:

```sh
gregale jobs add customer-export-job --image <registry/image@sha256:digest> \
  --command node,/app/job.mjs --ram 512 --timeout 180 --parallelism 1 \
  --env GREGALE_API_URL=https://<control-plane>
gregale jobs info customer-export-job --json
```

The Job must be active and its image materialization status `ready` before its
Operation definition can be admitted. For a private registry, set the sealed
pull credential using `gregale jobs registry set ... --password-stdin`.
The manifest's `job` names this existing account-owned Job; source deployment
does not create Jobs. Do not call `jobs run`, `retry` or `replay-failed` for this
feature: customer submission and evidenced Operations recovery own its runs.
There is no recurring schedule and no automatic business retry.

Reserve the app with `gregale deploy --create-only --template
customer-operation-job-export --name <slug> --no-require-authn`. Set the public
app selectors through `--secrets-file` or `gregale secrets set`:

| App setting | Value |
|---|---|
| `GREGALE_API_URL` | HTTPS control-plane URL |
| `GREGALE_EXPORT_SCOPE` | Its registered environment, such as `production` |
| `HOST` | `0.0.0.0` for the private guest listener |

Gregale supplies the app ID as `FAAS_APP_ID`; `GREGALE_EXPORT_APP_ID` can specify
it explicitly. The initial deployment may omit `GREGALE_EXPORT_DEFINITION_ID`:
health and static assets are available, while `/config` returns 503 until the
definition selector is present.

Deploy the SDK-equipped app source with your preview environment selector:

```sh
gregale deploy --name <slug> --no-require-authn --secrets-file <app-settings-file>
gregale customer-operations definitions get customer-export --app <slug> --deployment <deployment-uuid> --json
gregale secrets set --app <slug> GREGALE_EXPORT_DEFINITION_ID=<definition-uuid>
gregale customer-operations doctor --app <slug> --deployment <deployment-uuid>
```

The UI needs public app ingress. The browser authenticates directly to the
customer Operations API; the Job uploads directly to its runtime API with its
native task capability. Use the control-plane origin without redirects.
When a deployment creates a new immutable definition, update the browser selector
for new submissions. Accepted work retains its original definition and Job configuration.

`init --deploy` and direct template deployment reject an unprepared source.
These steps register intent and configure an app; they do not open admission.

## Execution, files and credentials

`job.mjs` runs `runJobOperation` from the installed SDK. The scheduler supplies
the customer identity, input, generation and native task capability. Use verified
`scope.operation.platformTenantID` to select real customer data when replacing
the sample rows. The handler reports once, yields/checks control between chunks,
prepares `export.csv` and returns `{rows, artifact_id}`. Files and typed results
stay private until the host confirms successful task exit or an account operator
explicitly confirms success with valid typed output and reconciliation evidence.

The handler calls `scope.uploadArtifact({report_id: 'export-csv', name:
'export.csv', data: csv, maxBytes: 32768})`. Gregale validates the native task
proof, receives bounded bytes, verifies the declared size and SHA-256, and retains
one private file receipt. The Job needs only its public API setting. No private
bucket, app upload endpoint or storage credential is required.

The SDK snapshots bounded text/bytes, checks for an existing durable receipt and
coalesces matching concurrent calls. A lost upload acknowledgement is resolved
through receipt lookup. An interrupted private platform transfer can retry the
same bytes; business code executes once. Reusing a report ID with changed metadata
or content fails. The default SDK memory bound is 8 MiB, independent of the
server's captured plan quota; this example sets 32 KiB. Fresh generations have
fresh receipts. A reference such as `operation://.../artifacts/...` is opaque;
customers download through their scoped Operation API, never from that reference.

No credentials or native proof appear in `/config`, browser assets, result JSON
or customer progress. The browser module graph includes only the SDK's customer
Operations modules. The app exposes no custom upload or status endpoint.

## Customer sessions and recovery

The development sign-in form accepts a tenant-bound customer token, held only
in memory. For an application login, edit `public/customer-auth.mjs` to export
the existing login adapter:

```js
import {appAuth} from './your-auth-module.mjs';

export const customerAuthProvider = {
  getCredential: () => appAuth.getCustomerOperationsToken(),
  onIdentityChange: callback => appAuth.onIdentityChange(callback),
};
```

Replace the import with a browser module available to this app, and add it and
its local browser dependencies to the `files` allowlist in `server.mjs`.
`CustomerOperationFeature` calls the async credential callback on demand and
owns client/session creation, history loading, receipt resumption, and
identity-change teardown. When the identity listener reports signout or a
customer switch, it closes the previous session and clears the displayed
operation details. “Disconnect” leaves the application's login active. Account
keys stay on the backend.

The existing SDK session and browser receipt store coordinate duplicate clicks
and tabs, keep the accepted identity across reload/signout, and restore it through
authenticated lookup/history. Saved metadata contains identity, input digest and
submission receipt; it contains no raw input or credential. Use a secure browser
context with local storage and Web Locks. An unresolved request requires the same
row count and an explicit retry. Expired unconfirmed receipts require inspecting
history before deciding whether to start new work.

Cancellation is cooperative. An interrupted started task can require
reconciliation; cancellation cannot undo a committed upload. Inspect native
attempts and private files with account-authorized `customer-operations inspect`.
Before `safe_to_retry`, reconcile any uncertain business effect. Recovery creates a
new run/generation with the retained image, input and configuration, and fresh
file receipts. The customer UI provides no blind retry/recovery action.

Add an owned `completion_webhook_id` to `gregale.yaml` before registering the
immutable definition to request completion delivery. A failed notification leaves
business success and private downloads available. Retrying notification never
regenerates the export.

## Verification

`npm test` exercises the installed public SDK and local browser server,
private-result visibility, bounded exports, duplicate/uncertain submissions,
session restoration, lost upload/result acknowledgements, interrupted transfers,
cross-customer download denial and task-authority rejection. Platform transport and native
outcomes use explicit portable fixtures. Repository CI also installs the packed
SDK outside the checkout and checks the browser module graph.

Actual native KVM execution, public DNS/TLS ingress and real-provider upload and
failure behavior still require separate qualification before preview activation.
See the repository's `docs/ops/customer-job-operations-native.md`.

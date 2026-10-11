# Customer operation export

An internal HTTP Operations starter with a typed CSV export handler and a browser
feature for history, progress, cancellation requests and downloads. Work status
and completion notification status are displayed separately. The browser uses
the reusable SDK session controller instead of an application status table.

Operations admission remains closed by default. Running this starter does not
enable a preview cohort or qualify native VM lifecycle or fleet rollout.

## Initialize and install the SDK

```sh
gregale init --template customer-operation-export --path customer-operation-export
```

The SDK is currently internal and unpublished. Build and pack it from an
authorized Gregale checkout, then copy the package into the generated source:

```sh
cd <gregale-checkout>/sdk/node
npm ci --ignore-scripts
npm run build
npm pack --ignore-scripts
cd <generated-source>/customer-operation-export
mkdir -p packages
cp <gregale-checkout>/sdk/node/gregale-sdk-node-0.1.0.tgz packages/gregale-sdk.tgz
npm install --ignore-scripts
npm test
```

Keep `packages/gregale-sdk.tgz` and the generated `package-lock.json` with the source
for deployment. The builder installs this local package; it does not require a
public registry release or a repository-relative import. Replace the bundle only
when deliberately upgrading the SDK and rerun the tests.

## Validate and run locally

```sh
gregale customer-operations validate --dir . --app <slug> --plan hobby
gregale customer-operations types --dir . --app <slug> --plan hobby
```

The second command writes `customer-operations.generated.d.ts` from the
validated input and output schemas. The browser starter references those types
in JSDoc, so its `CustomerOperationFeature` session exposes a typed `start()`
input and typed operation result. Regenerate after changing `gregale.yaml` or
either schema; the server remains authoritative at runtime.

Commit the generated declaration and add the read-only freshness check to CI:

```sh
gregale customer-operations types --dir . --app <slug> --plan hobby --check
npm run typecheck
```

The freshness check exits nonzero when the file is missing or stale and never
rewrites it. `npm run typecheck` checks the browser app and Operation input/result
types against the generated declarations and packed SDK without emitting files.

For a deployment whose HTTP Operations preview has already been qualified, use
its public app, environment and immutable definition selectors:

```sh
export GREGALE_API_URL=https://api.example.com
export GREGALE_EXPORT_APP_ID='<app UUID>'
export GREGALE_EXPORT_SCOPE=default
export GREGALE_EXPORT_DEFINITION_ID='<customer-export definition UUID>'
npm start
```

Open `http://127.0.0.1:8080`. The local demo form accepts a short-lived customer
token with `platform_tenant:operations:read` and
`platform_tenant:operations:manage`. For an application login, edit
`public/customer-auth.mjs`:

```js
import {appAuth} from './your-auth-module.mjs';

export const customerAuthProvider = {
  getCredential: () => appAuth.getCustomerOperationsToken(),
  onIdentityChange: callback => appAuth.onIdentityChange(callback),
};
```

Replace the import with a browser module available to this app, and add it and
its local browser dependencies to the `files` allowlist in `server.mjs`.
`CustomerOperationFeature` invokes `getCredential()` on demand and owns the
client/session lifecycle, including history loading and receipt resumption. On
signout or a customer switch it closes the old session, clears visible data, and
requires a new connection. “Disconnect” closes the Gregale session and leaves
the application login active.
Tokens stay in memory. Account API keys cannot authorize these customer routes
and must stay on the application's backend.

The handler reports through workload identity only behind Gregale's trusted
guest listener. Local UI startup leaves the handler unavailable when workload
identity is absent. The sample generates rows; read real customer data under the
verified identity supplied by the guest listener.

## Prepare a preview source deployment

Before deploying, have the operator verify the bounded preview prerequisites,
customer binding, workload trust, storage and rollback evidence. The CLI's
`customer-operations doctor` reports prerequisites; it grants no admission.

For an authorized preview, configure `GREGALE_API_URL`,
`GREGALE_EXPORT_SCOPE` and `HOST=0.0.0.0`, then deploy the initialized,
SDK-equipped source. Gregale supplies the app ID through workload metadata.
Leave `GREGALE_EXPORT_DEFINITION_ID` unset for this first deployment:

```sh
gregale deploy --name <slug> --platform-tenant-required --no-require-authn --secrets-file <app-settings-file>
```

The handler and health endpoint can start before the new immutable definition ID
is known; the browser feature remains unavailable. After deployment, obtain the
definition and configure its ID:

```sh
gregale customer-operations definitions get customer-export --app <slug> --deployment <deployment-uuid> --json
gregale secrets set --app <slug> GREGALE_EXPORT_DEFINITION_ID=<definition-uuid>
```

When deploying a new immutable definition, deliberately update this selector for
new submissions. Previously admitted work retains its original definition.

Use your application's selected environment and existing authenticated ingress
policy. Keep the exported selectors consistent with its registered definition.
`init --deploy` and direct template deployment require initialization first
because the SDK package must be included in the source. This starter makes no
changes to the preview admission policy.

## Customize the feature

- `export.mjs` implements the ordinary HTTP handler. Its small result is typed
  JSON containing CSV; a terminal success comes from output-schema validation.
  One initial progress report avoids a second report within the plan interval.
  Supplying a result store switches the output to a confirmed artifact ID.
- `gregale.yaml` declares schemas, platform-tenant ownership and reconciliation
  on unknown outcomes. Add `completion_webhook_id` before registering the
  immutable definition to request completion delivery.
- `public/app.mjs` uses `GregaleOperationSession` for customer history, selection,
  subscriptions, durable submission receipts and cancellation requests. It restores
  saved acceptance on sign-in without submitting work. Pending metadata survives
  refresh/signout; credentials and raw input stay in memory. Web Locks coordinate
  tabs. Unresolved requests require the same row count and an explicit retry,
  using the saved key/definition. After one day, inspect history before deciding
  how to handle unconfirmed requests. Storage/Web Locks must be available in a
  secure browser context. It renders
  delivery separately and never retries business work because a notification
  failed. Downloads use the exact retained file ID in the typed result.
- `server.mjs` exposes only allowlisted browser modules and public selectors.
  Workload reporting and managed transaction modules are not browser assets.

An uncertain submission retains its key and input while this session is open.
After reload, inspect server history before explicitly starting new work. This
starter stores scoped submission metadata in browser storage, while credentials
and request payloads stay in memory. Recovery requires an operator's confirmed outcome and evidence.

## Private file results

The handler calls `runtime.uploadArtifact` with a stable report ID, filename,
bounded CSV bytes and an application memory bound. The SDK snapshots the bytes
and computes their exact size and SHA-256. Gregale verifies and retains a private
copy using the current invocation's workload identity and dispatch proof. No
bucket writer, storage URI or provider credential is needed.

The typed output contains `artifact_id` and `rows`. The browser checks that ID
against the completed operation's artifacts before requesting its scoped download.
A retained receipt does not complete the invocation. Downloads become available
only after HTTP success or explicit confirmed-success recovery.

Matching concurrent uploads share one receipt. If an upload acknowledgement is
lost, the SDK looks up the receipt before retrying the byte transfer. It makes at
most three transport attempts and never reruns the export handler. Changed bytes
or metadata under the same report ID conflict. Private staging copies left by a
failed transfer use Gregale's existing cleanup policy. Keep uploads within the
original request; a finished or replaced invocation cannot reuse its authority.

## Cooperative cancellation and deadlines

The handler uses `runtime.runCancellableRequest`, yields during CSV generation
and checks current control before uploading a file. The SDK passes the scope
signal to private transfer I/O. The scope stops when cancellation is
requested, its deadline or lease expires, or control becomes unavailable. The
HTTP server sends output only after the final control check.

Stopping remains cooperative. A writer that ignores its signal may keep running,
and an already committed external write cannot be undone by cancellation. The
definition keeps `reconcile_on_unknown`: an interrupted dispatch can require an
operator to confirm its outcome. Neither the starter nor the SDK retries work or
labels an uncertain write safely cancelled. A lost private-upload response is recovered by receipt lookup; it does not
authorize a new business execution.

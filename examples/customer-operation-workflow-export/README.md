# Customer workflow export

An internal workflow Operations starter with collect/transform/finish handlers and a browser
feature for history, progress, cancellation requests and downloads. Work status
and completion notification status are displayed separately. The browser uses
the reusable SDK session controller instead of an application status table.

Operations admission remains closed by default. Running this starter does not
enable a preview cohort or qualify native VM lifecycle or fleet rollout.

## Initialize and install the SDK

```sh
gregale init --template customer-operation-workflow-export --path customer-operation-workflow-export
```

The SDK is currently internal and unpublished. Build and pack it from an
authorized Gregale checkout, then copy the package into the generated source:

```sh
cd <gregale-checkout>/sdk/node
npm ci --ignore-scripts
npm run build
npm pack --ignore-scripts
cd <generated-source>/customer-operation-workflow-export
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
rewrites it. `npm run typecheck` checks the browser app and workflow progress
types against the generated declarations and packed SDK without emitting files.

For a deployment whose Workflow Operations preview has already been qualified, use
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
`public/customer-auth.mjs` and export its adapter:

```js
import {appAuth} from './your-auth-module.mjs';

export const customerAuthProvider = {
  getCredential: () => appAuth.getCustomerOperationsToken(),
  onIdentityChange: callback => appAuth.onIdentityChange(callback),
};
```

Replace the import with a browser module available to this app, and add it and
its local browser dependencies to the `files` allowlist in `server.mjs`.
`getCustomerOperationsToken()` should return a current, short-lived,
customer-bound Operations token. The shared SDK `CustomerOperationFeature`
calls it on demand, loads history and resumes a retained submission receipt.
The identity subscription must call its listener on signout or customer/account
switch and return an unsubscribe function. On that event the controller closes
the old session and the UI clears its visible history before requiring a new
connection. “Disconnect” closes only the Gregale session; the application's
login remains active. Tokens stay in memory. Account API keys cannot authorize
these customer routes and must stay on the application's backend.

The three workflow actions use workload identity behind Gregale's trusted guest
listener. Local UI startup leaves them unavailable when workload identity is
absent. `/exports` is the operation submission route; this backend serves only
its dispatched `/collect`, `/transform` and `/finish` actions. Sample rows are
pure generated data. Replace them with customer data read under verified native
identity, and check cancellation between chunks of longer work.

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

## Follow an export

Sign in, choose 1–100 rows and generate an export. The customer session handles
scoped idempotency receipts and shows collect, transform and finish progress.
Reopen work from history or reload and sign in again: resume looks up the saved
submission without issuing another POST. Credentials and raw input stay in
memory; browser storage contains only scoped receipt metadata and an input hash.
Web Locks coordinate tabs. If acceptance remains unresolved, explicitly retry the
same row count with the retained key; do not interpret a lost response as rejection.
Storage and Web Locks must be available in a secure browser context.

Cancellation uses the observed operation generation. An interrupted final action
can need reconciliation; the UI shows its retained prefix and asks for operator
review. It never automatically retries uncertain business work. Business outcome
and completion delivery are separate: a failed notification does not disable a
confirmed export's download or start another workflow.

The typed result contains `rows` and `artifact_id`. The browser verifies that exact
ID against the confirmed operation's artifact list before its customer-scoped
download. Another customer's credentials cannot access this operation or file.

## Customize and recover

- `server.mjs` runs bounded pure prefix computations inside cancellable workflow
  scopes, serves an allowlist of browser SDK modules and projects only public
  selectors through `/config`.
- `artifact.mjs` uses `runtime.uploadArtifact` in the trusted final action with
  stable report ID `export-csv`, filename and bytes. Gregale retains exact-size,
  SHA-256-verified private bytes with no application bucket writer or provider
  credential. Lost acknowledgements resolve through lookup before transfer retry.
  A retained file does not confirm step success or publish a result.
- `gregale.yaml` declares the linear workflow, typed schemas, platform-tenant
  ownership, native step progress and `reconcile_on_unknown`. Add an owned
  `completion_webhook_id` before registering an immutable definition to request
  completion delivery.
- `public/app.mjs` uses the shared feature controller for session lifecycle and
  keeps rendering, subscriptions, cancellation and private downloads in the UI. Workload/runtime
  modules are not browser assets. `public/customer-auth.mjs` is the integration
  point for the application's async credential callback and identity-change
  subscription. The paste-in token form remains for local development.
- `RECOVERY.md` gives the operator's inspection, read-only preview and receipt-backed
  recovery commands. Approved resume retains confirmed prefix outputs, original
  code/input and a verified final file. Its fresh attempt reuses that file without
  another byte transfer and must confirm success before customer publication.

These upload receipts do not deduplicate arbitrary external effects. Any added
provider write needs its own evidence before an uncertain effect is repeated.
Cooperative cancellation aborts I/O through the scope signal; it cannot undo an
already committed write or stop code that ignores its signal. Keep file work inside
its original trusted action and preserve report metadata across approved resumes.

`npm test` exercises the packed SDK's lost acknowledgements, retained-file resume,
cancellation, input bounds, private bootstrap and progress behavior. Gregale's
repository checks CLI generation, source packing and example parity, and runs the
shipped UI in Chromium for login, reload, progress, cancellation and downloads.
These are portable checks. Native KVM execution, public routing/provider behavior
and fleet activation/rollback remain required before opening workflow admission.

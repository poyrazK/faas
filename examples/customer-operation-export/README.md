# Customer operation export

An internal example of a complete HTTP Operations feature: submit a typed export,
show progress, discover previous work after signing back in, cancel accepted work,
and download the retained result. The browser stores no operation IDs, tokens or
event cursors in local storage. Gregale owns operation history and result state.

The demo exports generated rows as CSV in its typed JSON result (at most 1,000
rows / 32 KiB). Its download control also supports the retained artifact API when
a handler attaches a file. Real customer data must be read under the verified
identity supplied by the trusted guest listener.

## Run in this checkout

Build the local SDK from the repository root first:

```sh
cd sdk/node
npm ci --ignore-scripts
npm run build
cd ../../examples/customer-operation-export
```

Supply public selectors for an operator-qualified local/preview deployment:

```sh
export GREGALE_API_URL=http://127.0.0.1:8081
export GREGALE_EXPORT_APP_ID='<owning app UUID>'
export GREGALE_EXPORT_SCOPE=default
export GREGALE_EXPORT_DEFINITION_ID='<immutable customer-export definition UUID>'
npm start
```

Open `http://127.0.0.1:8080`. The local sign-in form accepts a short-lived
platform-tenant access token with `platform_tenant:operations:read` and
`platform_tenant:operations:manage`. It keeps the token only in the current
session. Account API keys belong on the operator's backend and cannot authorize
these tenant routes.

In an application with login, replace the demo form with the existing
authenticated session and pass a credential callback to `GregaleOperationClient`.
Return a current token for the same customer on every request and reconnect.
Close the old `ExportSession` before switching customers. The application's
authenticated backend supplies login and token issuance.

The server uses the built SDK in this checkout. For a standalone application,
package that SDK with the source and change the source-relative imports and asset
paths to the installed package. The internal SDK is not publicly published.

## Execution and delivery

`gregale.yaml` declares the ordinary `POST /exports` handler, schemas and progress
stages. A trusted guest request supplies execution context;
`FAAS_WORKLOAD_IDENTITY_ENDPOINT` enables workload-authenticated reporting.
The server defaults to loopback. A qualified guest deployment selects
`HOST=0.0.0.0` on its private guest listener. Runtime context is valid only behind
that listener.

To receive completion notifications, set `completion_webhook_id` on the source
declaration before registering its immutable definition. Work and delivery status
are shown separately. A notification failure leaves a completed export
downloadable. Refresh reads current delivery state. Notification errors never
trigger another export. Reconciliation directs the customer to an operator;
the customer view provides no blind recovery action.

A failed/lost submission response keeps the same key and input for an in-session
retry. Closing the page discards that transient key; sign in and inspect history
before explicitly starting another export. History excludes expired settled
operations and is a live view. Opening or downloading can return 410 if retention
expires between requests.

## Verify

```sh
npm test
```

Session tests cover fresh-session discovery, independent delivery failure,
download, duplicate clicks, uncertain submission retry, and signout fencing.
`TestOperationsHTTPPostgresSDKAcceptance` imports this session controller against
actual PostgreSQL and the Node SDK, rediscovers an export after admission rollback,
and checks that notification retry never regenerates it. That fixture uses a
local HTTP bridge; native KVM and fleet qualification still gate rollout.

Production admission remains disabled by default. This example does not enable a
cohort or deploy an app.

# Customer order fulfillment

This internal example connects a customer Operation to an application-owned
PostgreSQL order. The handler checks the verified customer's access, commits
fulfillment and its JSON result together, and reports the committed progress
stage. An authorized recovery returns the saved result without running the
business callback again. Fulfillment here is a database transition; it does not
send goods, charge a card, or call another service.

## Prepare and run

From the repository root, build the internal SDK and install this example:

```sh
npm ci --ignore-scripts --prefix sdk/node
npm run build --prefix sdk/node
npm ci --ignore-scripts --prefix examples/customer-operation-orders
cd examples/customer-operation-orders
```

Use a database owned by this application. Supply its connection string through
your environment or secret configuration, then explicitly install both tables:

```sh
# APPLICATION_DATABASE_URL must point to the application's PostgreSQL database.
npm run setup
```

Create an order using your application's existing authenticated business flow.
For a fixture, insert a UUID and the customer's verified platform-tenant UUID:

```sql
INSERT INTO public.example_orders(id, platform_tenant_id)
VALUES ('11111111-1111-4111-8111-111111111111', '<customer tenant UUID>');
```

The server needs `GREGALE_API_URL` and Gregale's loopback
`FAAS_WORKLOAD_IDENTITY_ENDPOINT`. `npm start` defaults to `127.0.0.1:8080`.
A guest deployment supplies `HOST=0.0.0.0` through its environment and receives
traffic through Gregale's trusted guest listener. Reserved request headers
are execution metadata, not authentication for a directly exposed server.

## Source deployment and inspection

`gregale.yaml` opts into `http_transaction_version: 1`, bundles the two JSON
schemas, and keeps the default `reconcile_on_unknown` recovery policy. Validate
it locally for the selected app and plan:

```sh
gregale customer-operations validate --app orders --plan pro --dir .
npm run bindings:check
```

`workflow-bindings.mjs` is generated from that validated manifest. The handler
uses its transition helper with the state read under the order-row lock; the
helper queues the required `order-fulfilled` payload and the declared edge.
After changing contracts or schemas, run `npm run bindings:generate` and review
the generated diff. `bindings:check` compares the module with the current
contracts without writing, so it can run in CI before packaging.

The internal SDK is not published. Package this example with the built SDK in
a fresh temporary source directory; the bundle contains no database credentials
or installed dependencies:

```sh
bundle=$(npm run --silent bundle)
npm ci --ignore-scripts --prefix "$bundle"
gregale customer-operations validate --app orders --plan pro --dir "$bundle"
```

Use the resulting directory as source for an operator-qualified preview app,
after setting customer authentication on the app:

```sh
gregale app orders --consumer-auth-mode required --platform-tenant-required
gregale deploy --path "$bundle" --source worktree --name orders --platform-tenant-required
```

Configure the
application database and API URL through the app's secret/environment settings.
The app must be in an explicitly qualified preview cohort; packaging does not
enable admission. Ordinary source deployment pins transaction support into the
immutable definition, and build retry retains it. Inspect that pin with:

```sh
gregale customer-operations definitions list --app orders --deployment '<deployment UUID>'
gregale customer-operations definitions get fulfill-order --app orders --deployment '<deployment UUID>'
```

Submit `POST /orders/fulfill` with `{"order_id":"<owned order UUID>","workflow_run_id":"<workflow run UUID>"}`, an app
consumer credential linked to that customer, and a stable `Idempotency-Key`.
Ingress returns the logical Operation ID. Use that customer's operation token
to inspect its result, progress, and history through the tenant API or SDK.
Account credentials authorize recovery and execution inspection.

## Lost responses and recovery

A lost HTTP response after commit leaves the logical Operation requiring
reconciliation. The account operator verifies the application's order and
`public.gregale_customer_operation_inbox` receipt before authorizing
`safe_to_retry` recovery with a stable recovery ID, the expected generation,
and evidence. Recovery retains the Operation UUID and input, creates another
execution, and returns the receipt's exact JSON bytes. The handler checks order
access again before receipt replay. Another customer's order returns 404.

The order row is locked and ownership rechecked inside the business transaction.
A fulfilled order stays fulfilled even under a new Operation ID; its constraint
allows one business transition. Receipt retention is explicit: keep receipts
while the original Operation can replay. Avoid external side effects and
transaction-control statements in the callback.

## Verify

With a disposable PostgreSQL fixture and the built SDK/dependencies, run from
the repository root:

```sh
GREGALE_OPERATIONS_ACCEPTANCE=1 go test ./cmd/apid \
  -run '^TestOperationsOrderHTTPPostgresRecoveryAcceptance$' -count=1 -v
```

The test bundles this manifest, registers its immutable definition, submits
through ingress, and executes this server through the real scheduler. It kills
the Node process after commit but before the HTTP reply, authorizes recovery,
and checks one logical Operation, two execution records, one receipt, one
business change, current-attempt progress, retained customer results, and
customer isolation, including lookup by the recovered order ID. Only the VM HTTP bridge is replaced by local transport.
Production admission and native fleet qualification remain gated.

## Reopen work by order

The manifest declares `subject: {type: order, id_from: /order_id}`. Gregale
captures the submitted order ID once and preserves it through lost-response
recovery. Inspect related work without retaining the Operation UUID:

```sh
gregale customer-operations list --app orders --scope default \
  --subject-type order --subject-id '<order UUID>'
```

Customer applications use the tenant-bound list API with `app_id`, `scope`,
`subject_type=order`, and `subject_id=<order UUID>`, or the browser client's
`list({appID, scope, subjectType: 'order', subjectID: orderID})`. The dashboard
links the same reference to related work. A reference grants no access to the
order; business authorization in this example still checks customer ownership.


The example declares workflow contract version `1`. Its `pending → fulfilled` transition is scoped to `fulfill-order` and requires the `order-fulfilled` milestone in the same committed application transaction. The workflow maps that milestone to the process, with `pending` and `fulfilled` as its states, `fulfilled` as terminal, and `pending` stale after 30 minutes. Supply one stable workflow-run UUID across all Operations participating in the process; separate runs for the same order use different UUIDs. The callback locks the order row, checks that it is pending, changes it to fulfilled, and records both the transition and milestone with the business write and result receipt. Gregale checks the transition contract and verifies the evidence references before the application transaction commits. State and milestone publication then use the durable outbox, and authorized recovery publishes the saved reports without repeating fulfillment. Inspect the order timeline with `gregale customer-operations milestones --app YOUR_APP --scope default --subject-type order --subject-id ORDER_UUID`; add `--workflow order-fulfillment --workflow-instance-id RUN_UUID` to read one run's ordered state changes, or `--stale-only` to show only stale current states. The CLI and dashboard label the reported current state as active or terminal, and the dashboard shows state history and retained workflow steps grouped under the run ID.

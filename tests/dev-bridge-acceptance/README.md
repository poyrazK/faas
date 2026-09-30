# Dev Bridge native acceptance

This fixture checks the deployed public TLS edge, laptop attachment, actual
microVM callers and service discovery. The app graph is frontend → payments →
inventory. Alice and Bob each intercept payments on their laptop; the frontend
and inventory remain deployed. A separate production frontend attempts to use
Alice's context **inside its VM** and must be denied at the service hop.

The fixture-only `X-Gregale-Bridge-Acceptance-Context` header deliberately lets
the frontend inject that probe. Deploy this app only into the isolated
acceptance project/account. It is not a customer application template.

## Prepare the fixture

An operator must first install the feature revision and enable the bridge on
the dedicated development deployment as described in
[the Dev Bridge guide](../../docs/dev-bridge.md). Use a Pro or higher acceptance
account with capacity for three workloads and two concurrent bridge sessions.
Select separate native x86_64 Linux control-plane and compute nodes. Do not
retarget the fleet's destructive metal-test workflow onto serving tenant nodes.

Package the fixture from the repository root. The archive includes only the
three-service fixture and the Node SDK source; Gregale builds its Dockerfile
inside a builder VM:

```sh
scripts/ci/package-dev-bridge-fixture.sh /tmp/gregale-bridge-fixture.tar.gz
gregale deploy --tarball /tmp/gregale-bridge-fixture.tar.gz \
  --project-slug bridge-acceptance --yes
gregale projects environments create bridge-acceptance development --from production
```

Inspect the project and environment releases to obtain the generated app slugs.
Both production and development must report live revisions for all three
workloads; environment creation may need time for its release set to reconcile.
Do not use a real customer production environment for the denial probe.

On the native acceptance runner, an operator creates
`/etc/faas/dev-bridge-acceptance-host` containing the dedicated API and topology:

```json
{
  "api_url": "https://acceptance.example.com",
  "control_plane": "bridge-control-1",
  "compute_nodes": ["bridge-compute-1"]
}
```

The gate requires x86_64 Linux, accessible `/dev/kvm`, a matching API designation,
and distinct control-plane/compute names. These are operator assertions about
the installation; the report additionally requires actual VM instance/wake IDs
for the exact live fixture revisions. A local fixture run is not native evidence.

## Run and retain evidence

Supply the account token through the runner's secure environment. Do not put it
in command arguments or the report. Set these non-secret selectors to the actual
project and app slugs:

```sh
export FAAS_API=https://acceptance.example.com
export FAAS_BRIDGE_PROJECT=bridge-acceptance
export FAAS_BRIDGE_ENVIRONMENT=development
export FAAS_BRIDGE_FRONTEND=frontend
export FAAS_BRIDGE_PAYMENTS=payments
export FAAS_BRIDGE_INVENTORY=inventory
export FAAS_BRIDGE_IDLE_FOR=2m
export FAAS_BRIDGE_EVIDENCE=/tmp/dev-bridge-native-evidence.json
make native-dev-bridge-acceptance
```

The manual **Dev Bridge native acceptance** workflow runs the same target. It
requires a dedicated self-hosted runner with labels `linux`, `x64`,
`gregale-bridge-acceptance` and the `dev-bridge-acceptance` GitHub environment.
Configure `GREGALE_ACCEPTANCE_API_URL`, `GREGALE_BRIDGE_PROJECT`,
`GREGALE_BRIDGE_FRONTEND`, `GREGALE_BRIDGE_PAYMENTS`, `GREGALE_BRIDGE_INVENTORY`
as environment variables, and `GREGALE_ACCEPTANCE_TOKEN` as a secret. The workflow
does not deploy the platform, change flags, stop daemons or provision projects.

A passing report contains ordinary remote routing, two-developer isolation,
remote frontend → local payments → remote inventory, production VM caller
denial, idle edge survival for the **recorded interval**, connection replacement,
revocation, unaffected Bob/ordinary requests, and VM wake evidence. Sessions are
revoked on every exit. Cleanup failure fails acceptance and clears `completed_at`;
the report's session IDs let an operator revoke any remaining leases. It contains
no attachment/request tokens, headers or bodies.

The default two-minute idle test proves two minutes only. Increase the interval
for the intended edge configuration, keeping it below 55 minutes to allow lease
cleanup. Attach the JSON report and deployed platform revision to a rollout
record before claiming native acceptance. A compiled/skipped harness is not a
passing native run.

## Local contract check

```sh
npm --prefix sdk/node run build
node --test tests/dev-bridge-acceptance/app/server.test.mjs
go test ./pkg/devbridgeacceptance -run '^TestNativeAcceptance' -count=1
```

These test SDK propagation through fixture HTTP servers and the harness's input
contracts. They do not prove native topology, VM identity or public edge lifetime.

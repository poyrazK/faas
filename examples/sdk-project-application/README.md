# Reviewed project application

An API, a background worker and PostgreSQL, driven by either SDK. The worker
inserts rows and the API reports their count. `compose.yaml` also runs locally
with Docker Compose; its `local-only` password is a disposable local default.
Gregale's preview reports whether this installation can provision the declared
PostgreSQL service and bind its actual credentials to both consumers.

Build the repository's Node SDK with `npm --prefix sdk/node ci` followed by
`npm --prefix sdk/node run build`, or install the Python SDK into your virtual
environment with `python -m pip install -e sdk/python`.

Set `FAAS_API`, `FAAS_TOKEN`, and a unique `GREGALE_PROJECT_SLUG` (for example,
`my-shop`). The account needs room for two apps and a managed PostgreSQL database.
The server must support durable attempt IDs on cached project apply.

```sh
node examples/sdk-project-application/deploy.mjs preview
node examples/sdk-project-application/deploy.mjs apply
# After stopping the process or losing a response:
node examples/sdk-project-application/deploy.mjs resume
```

The equivalent Python commands replace `node .../deploy.mjs` with
`python examples/sdk-project-application/deploy.py`. Each process reads the same
private `.state/plan.json` and `.state/apply.json` files, so either language can
resume the other's attempt. Review the preview before calling `apply`. It shows
the exact app allocations and whether PostgreSQL is provisioned or needs manual
configuration. An expired plan requires another preview and a new application
journal; keep an uncertain old journal for receipt recovery.
The drivers refuse to overwrite a review once its state directory contains an
application journal. Set a new `GREGALE_PROJECT_STATE` directory for a new intent.

`GREGALE_PROJECT_STATE` changes the state directory; `GREGALE_PROJECT_SOURCE`
changes the repository context. `GREGALE_NODE_SDK` selects another built SDK entry.
The state directory stays outside `source/`, and configuration values and tokens
are omitted from console summaries. A saved plan contains an opaque server token;
keep it private. The examples never automatically retry an uncertain apply.

After success, request the API URL reported in the preview, supplying its required
app authentication. Its JSON `processed` count should grow as the worker writes.
Remove the project and its managed database through the CLI when finished; deleting
apps does not purge a managed database. This example creates real resources only
when `apply` is explicitly run.


Verify the exact deployments after apply or recovery:

```sh
export GREGALE_APPLICATION_URL=https://your-api.example
node examples/sdk-project-application/verify.mjs
# The same verification-target.json works in either language:
python examples/sdk-project-application/verify.py
```

The drivers save an identity-only `verification-target.json` beside the private
journal. Verification needs no source files and performs no apply or repair.
The API must be reachable with application authentication handled separately in
`checks.mjs`/`verification_checks.py`; these examples use a publicly accessible API
and never forward `FAAS_TOKEN`. They bound responses and disable redirects.
The API returns its own `FAAS_DEPLOYMENT_ID` and the heartbeat of the exact worker
requested by ID. A fresh heartbeat follows a successful database insert. Old rows
and a worker from another deployment cannot verify the selected release.

To exercise failure, work on a disposable source copy and set
`worker-fault.json` to `{"fail":true,"round":1}`. Choose a new state directory,
preview and apply that source, then verify: the API check passes and the worker
check reports `smoke_failed`. Set `{"fail":false,"round":2}`, use another new
state directory and deploy the correction. Verification then observes rows and a
fresh heartbeat from the corrected deployment. No automatic recovery is performed.
The round field makes the corrected source distinct from the original build.

Local integration testing uses isolated PostgreSQL and real API/worker processes:

```sh
npm --prefix sdk/node run build
GREGALE_LOCAL_PG_BIN=/path/to/postgresql/bin \
GREGALE_LOCAL_PYTHON=/path/to/sdk-venv/bin/python \
node --test examples/sdk-project-application/local-verification.test.mjs
```

This exercises both SDKs through worker failure and correction with fixture
control-plane observations. The deployed public API/CLI journey is
`tests/journeys/TestSDKReviewedProject`; it requires an explicitly configured
isolated target and checks actual release and health evidence as well.

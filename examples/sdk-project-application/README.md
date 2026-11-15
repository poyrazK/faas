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

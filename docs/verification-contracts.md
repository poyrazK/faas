# Verification contracts

A verification contract is a `gregale.verify.yaml` file at the root of your
project. It lists the checks a new deployment must pass before it takes
traffic. Gregale runs them against the new deployment while the previous one
keeps serving. If a required check fails, the new deployment is not promoted
and the deploy fails with `deployment_verification_contract_failed`.

Verification contracts are in preview.

## Example

```yaml
version: 1
timeout_seconds: 30          # whole contract; at most 120
checks:
  - name: products
    http:
      path: /api/products?limit=1
      headers: {Accept: application/json}
      expect:
        status: 200          # one code or a list; default any 2xx
        max_latency_ms: 800
        headers: {content-type: json}
        body_contains: ["items"]
        json:                # RFC 6901 pointer: expected value
          /items/0/id: 1

  - name: create-order       # runs on previews and environment branches only
    advisory: true           # reported, never blocks
    http:
      method: POST
      path: /api/orders
      json: {sku: demo, qty: 1}
      expect: {status: [200, 201]}

  - name: search-tool
    mcp:
      path: /mcp             # default /mcp
      tool: search
      arguments: {query: shoes}
      expected_result: {content: [{type: text, text: "3 results"}]}

  - name: no-orphan-orders
    sql:
      database: main         # a managed Postgres database of your account
      query: SELECT count(*) FROM orders WHERE customer_id IS NULL
      expect: {value: "0"}   # or rows: N, or null: true
```

## Commands

```sh
gregale release check validate            # check the file locally
gregale release check set --app my-api    # store it on the app
gregale release check get --app my-api
gregale release check delete --app my-api
```

`gregale deploy` and GitHub pushes store the file automatically before they
create the deployment. They read it from the app's source root (the
directory the app builds from). An invalid file stops the deploy and, for a
push, fails the app's GitHub check. Deleting the file does not delete the
stored contract; use `gregale release check delete`. A pull request preview
uses the pull request's file; a branch preview uses the app's stored
contract.

## What runs where

- Checks run one at a time, after the deployment's health check passes.
- In production, checks that could change data are skipped and reported as
  skipped: HTTP methods other than GET, HEAD and OPTIONS, and MCP tools that
  do not declare `readOnlyHint: true`. Previews and environment branches run
  them.
- SQL checks run in a read-only transaction as the database's agent query
  login, with a 5-second statement timeout.
- Do not put credentials in the file: it is stored as plain configuration.
  Reference one of the app's secrets in a header instead (below).

## Secrets in headers

An HTTP check's header value can use one of the app's secrets:

```yaml
  - name: me
    http:
      path: /api/me
      headers:
        Authorization: "Bearer ${secret:API_TOKEN}"
```

Gregale reads `API_TOKEN` from the deployment's own environment scope (the
same value the app gets as env) and sends it only to the new deployment.
Reports show the name, never the value. References work only in HTTP header
values, at most 8 distinct secrets per contract. If the scope does not have
the secret, the check fails with `verify_secret_missing`.

## The report

`gregale deployment <id>` prints the report under the deployment's
hosting receipt, and `--json` returns it as `hosting_receipt.smoke.verification_contract`:

```json
{"version": 1, "status": "failed", "environment": "production",
 "contract_sha256": "…", "duration_ms": 412,
 "checks": [
   {"name": "products", "kind": "http", "required": true, "status": "failed",
    "code": "verify_assertion_failed", "http_status": 500,
    "failures": ["status 500 is not one of [200]"]}]}
```

A check is `passed`, `failed`, `skipped` or `error`. `error` means no verdict
was possible, for example the database does not exist. A required check that
is `failed` or `error` blocks promotion.

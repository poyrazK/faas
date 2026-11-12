# Deploying schema changes

Create a coordinated workflow in an existing project:

```sh
gregale data-api sync notes-data --init --role YOUR_BINDING_LOGIN_ROLE
```

This writes a new `data-api.json` and materializes owner tooling in
`data-api-runtime`. Existing files are preserved. It performs no SQL and creates
no hosted resources. Review the generated migration, client and authorization
commands before using them; the migration preset assumes a `migrations` directory
and client scripts named `typecheck` and `test:authorization`.

Follow the printed setup commands to install dependencies and the private
refresh barrier, enable coordination, refresh the deployed runtime and capture
`schema.before.json`. Supply migration credentials, an application JWT and a
private refresh token through your secret environment.

```sh
gregale data-api sync notes-data --config data-api.json --check
gregale data-api sync notes-data --config data-api.json
# Correct a failed check, then reuse the same token and baseline:
gregale data-api sync notes-data --config data-api.json --resume-only
```

The existing coordinator drains transactions, runs migrations, refreshes the
engine, compares contracts, generates types, runs client and authorization
checks, then releases the verified barrier. A failure keeps the API paused.
Resume-only verifies the current state without repeating the migration command.
Database changes are not automatically rolled back.

Prefer additive changes and expand/contract deployments. Intentional breaking
changes require the existing fingerprint-bound approval. Keep SQL, generated
types, authorization scenarios and reviewed approvals together in version control.
See the [complete coordinated workflow](../data-api.md#coordinated-hosted-schema-updates).

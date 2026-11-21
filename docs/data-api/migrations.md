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

## Migration and local-output boundaries

The runner owns the schema/ledger transaction and advisory lock. Migration SQL
must not contain top-level BEGIN, COMMIT, ROLLBACK, savepoint commands or other
transaction control. Preflight checks all files before running migration SQL;
comments, quoted values and function bodies remain valid. A later SQL failure
rolls schema and ledger changes back together. Standard-conforming strings are
restored before each file is parsed.

Migration inputs must be regular files, with no symlinks or named pipes, within
the existing 4,096-file / 16 MiB aggregate limits. Loading observes cancellation,
rejects files changed during reading, and computes checksums from original UTF-8
bytes. Types and behavior output must remain outside the migration tree and
cannot alias a migration through a symlink or hardlink. Add a new migration
instead of replacing applied SQL with generated output.

After changing a hooked table's key names, schema/table identity or excluded
columns, re-enable its hook with the current relation and intended payload and
exclusions. Capture fails with SQLSTATE 55000 until metadata agrees. Apply
`hooks sql --upgrade` to update both enable and capture functions; INCLUDE
columns are never primary-key attributes. Historical queued events retain their
captured schema meaning; review/discard malformed events from an affected old
installation before resuming publication.

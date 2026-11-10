# PR preview databases

A [PR preview](preview-environments.md) of an app that uses managed PostgreSQL
can get its own database: one per pull request, migrated by the PR's release
command and deleted with the preview. Code under review then never writes to
production's data. This is a preview capability
([ADR-20261102123523](adr/20261102123523-pr-preview-databases.md)).

## Turn it on

The setting belongs to the production database, never to the pull request:

```sh
gregale postgres previews set orders empty   # each preview gets an empty database
gregale postgres previews set orders copy    # each preview gets a copy of orders
gregale postgres previews set orders off     # previews get no database (the default)
```

Over the API: `PUT /v1/postgres/databases/{id}/previews` with
`{"previews": "empty"}`.

- **`empty`**: the database starts empty. The PR's release command creates
  its schema, so a preview also checks the PR's migrations.
- **`copy`**: the database is a copy of the production database as of the
  preview's setup. Later pushes keep the copy and what the preview wrote to
  it. It needs the production database's restore window. Anyone who can open
  a pull request runs code that can read the copy, so use `copy` only when
  that is acceptable for the data.
- **`off`**: previews get no database. Preview databases that already exist
  are removed with their pull requests.

## What the preview sees

Each binding the production app has to the database, in the scopes
production serves, is given to the preview with the same environment key and
access, bound to the preview's database instead. An app with `DATABASE_URL`
and a `migration` binding as `MIGRATION_DATABASE_URL` gets both in its
preview, so the release command migrates the preview's database through
`MIGRATION_DATABASE_URL` while the app serves through `DATABASE_URL`. See
[managed PostgreSQL](postgres.md) for release commands and binding access.

A deployment into a preview waits while its database is provisioned, so the
release command finds its URL. On a push to a pull request the build waits,
and if the database cannot be set up, the pull request shows why.
`gregale preview create` waits too, up to its `--timeout`. Over the API, a
source deployment into the preview answers `503 preview_database_provisioning`
with `Retry-After` until the database is ready; send it again after that
delay.

## Lifecycle

- A preview database is created with its preview: on a push to a pull
  request, or by `gregale preview create`.
- It is deleted and purged when the preview is, with no undo window. Deleting
  a preview database yourself purges it at once.
- Deleting a production database purges its copies first. Empty preview
  databases go with their pull requests.
- `gregale postgres list` shows preview databases with a `preview` field: the
  production database they belong to and the pull request number.

## Limits and capacity

- Preview databases do not count toward your plan's database limit. An
  account may hold at most one per open preview environment and production
  database: your plan's open previews times its databases.
- Each is a small development database that scales to zero. An empty one
  stops at 1 GiB of storage (or your plan's storage, if smaller); a copy
  keeps the production database's storage limit.
- Preview databases never take what production needs. They are left out of
  your plan's storage pool, and once your account has used 80% of its
  monthly compute, preview databases are paused and refuse connections
  until the next month, or until overage or an upgrade raises the limit.
  Production keeps the last 20%.
- Databases owned by an organization do not give previews a database yet.

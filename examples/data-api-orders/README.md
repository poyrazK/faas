# Orders Data API development

With Node.js 22+, npm and a local Docker daemon, run from this directory:

```sh
gregale data-api dev --port 0 --once
gregale data-api dev --port 0
```

The config selects migrations, seed data, buyer/other test identities and
required isolation assertions. It generates `database.types.ts` and prints a
private inspector link. No account or hosted database is required.

Add numbered migrations to refresh the local API. Restart dev after changing
configuration or seed data. This is a disposable local example; use Gregale's
migration app and qualified `data_api` binding for hosted deployment. Seeds and
saved request bodies must contain test data only.

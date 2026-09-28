# Domains

Custom domains are attached to an app after it has a live deployment.

```bash
gregale domains add --app my-api --domain api.example.com
gregale domains verify api.example.com
gregale domains list
```

To send a custom hostname to one project environment rather than the app's
application-wide route, pass `--environment`:

```bash
gregale domains add --app my-api --domain staging.example.com --environment staging
gregale projects environments diff shop --from production --to staging
```

Environment-bound domains follow only that environment's active project
release set. They are not used as the app's default domain, are shown in the
environment state and diff, and are not copied when cloning an environment
because their DNS and certificate ownership is external to Gregale.

The verify output tells you the exact DNS record to add. Keep the record in
place while the certificate is issued and renewed. `gregale domains doctor`
checks DNS, certificate state, and the route without changing the domain.

The default `*.gregale.dev` hostname works without a custom domain. Domain
entitlement, certificate limits, and wildcard behavior are listed in
[plans](plans.md).

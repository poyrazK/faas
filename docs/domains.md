# Domains

Custom domains are attached to an app after it has a live deployment.

```bash
gregale domains add --app my-api --domain api.example.com
gregale domains verify api.example.com
gregale domains list
```

The verify output tells you the exact DNS record to add. Keep the record in
place while the certificate is issued and renewed. `gregale domains doctor`
checks DNS, certificate state, and the route without changing the domain.

The default `*.gregale.dev` hostname works without a custom domain. Domain
entitlement, certificate limits, and wildcard behavior are listed in
[plans](plans.md).

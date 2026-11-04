# ADR-20261023085230: Opt-in import of plain Compose environment values

- **Status:** accepted
- **Date:** 2026-10-23
- **Amends:** the Compose scan's "values never leave the source" rule (`docs/container-compatibility.md`), for an opted-in project only. `env_file` values and secret-looking values still never leave the source.
- **Decision:** A Compose project that sets the top-level `x-gregale-env-import: true` gets the plain values of each service's inline `environment:` copied into that service's app, in the deploy's environment, when the project is applied.
  - **Which values.** A `KEY: value` (or `KEY=value`) entry whose value is literal and plain. The scan skips, and lists under **Needs a value** with the reason:
    - a key that names a credential (a `_`-separated part such as `SECRET`, `TOKEN`, `PASSWORD`, `PASS`, `PWD`, `KEY`, `APIKEY`, `PRIVATE`, `CREDENTIAL`, `AUTH`, `DSN`, `SALT`, `CERT`), or a value the secret scanner flags (provider patterns, private keys, high entropy) or a URL carrying a password;
    - a value that points at another Compose service or at the local machine (`db`, `http://api:3000`, `localhost`, `127.0.0.1`, `host.docker.internal`): it is wrong on Gregale, where services are reached as `<name>.internal`;
    - a value using `$` interpolation, an entry with no value (it reads the host's environment), an empty value, an invalid name, or one over the plan's value size.
    Keys Gregale sets itself (`PORT`, `GREGALE_*`, `FAAS_*`) are ignored as before.
  - **Never overwrite.** A key already configured on the app in that environment, as a variable or a secret, or supplied by `--secrets-file` or initial configuration, keeps its value. The repository fills gaps; it never replaces what someone set.
  - **Reviewed first.** The plan lists the imported names per service under **Imported from Compose**. Values never appear in the plan, the receipt, logs or audit events. Apply re-reads the values from the same reviewed source (the plan token binds its hash), so what is written is what was reviewed.
  - **Written like `gregale env set`.** Each app's imports are one configuration change (`env.set` revision, runtime notification, snapshot invalidation) before any build is admitted. The plan's variable quota is checked for every app before anything is written; a project over quota fails with the limit and observed count and writes nothing.
  - **Opt-in.** Gregale applies `compose.override.yaml`, as `docker compose up` does, and by convention that file holds local-development values (`NODE_ENV: development`, `DEBUG: "true"`). Importing them into production by default would be silent and hard to undo. Without the opt-in, the plan names the importable keys and shows the one line that imports them.
- **Why:** Compose apps routinely configure themselves through `environment:`. Dropping every value meant a Compose project deployed without its configuration and failed at runtime. Item 2 (plan **Needs a value**) made the gap visible; this lets a project close it with one reviewed line instead of re-typing each value.
- **Consequences:**
  - An opted-in project's first deploy has its plain configuration. Credentials and service addresses still need `gregale secrets set` / `gregale env set`.
  - A later deploy imports keys newly added to `environment:` that are still unset. Removing a key from Compose does not delete it from the app; deleting it from the app while it stays in Compose and opted in brings it back on the next deploy.
  - Imports happen on reviewed applies: `gregale deploy`, the dashboard and `gregale deploy --repo … --project`. A GitHub push deploy (githubd) is not reviewed and imports nothing; githubd also does not own configuration writes. A key added to `environment:` therefore arrives with the next reviewed deploy.
  - Rolling upgrades: an older apid ignores the extension and imports nothing; the plan field is additive.
- **Rejected alternatives:**
  - **Import by default.** Rejected for the override-file reason above; it can be revisited once plan review of imported names has been in use.
  - **Show values in the plan.** A classifier miss would print a credential into terminals and CI logs; names are enough to review, and the values are in the reviewed file.
  - **Import `env_file` values.** `.env` files are the conventional home of credentials and machine-local settings.
  - **Rewrite service addresses** (`http://api:3000` → `http://api.internal:10081`). Ports and schemes differ per service and a wrong rewrite fails quietly; the platform already injects `GREGALE_SERVICE_<NAME>_URL` for `depends_on`.

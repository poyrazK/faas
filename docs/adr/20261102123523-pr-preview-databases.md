# ADR-20261102123523: PR previews get their own managed PostgreSQL database

- **Status:** proposed
- **Date:** 2026-11-02
- **Owner:** apid (managed PostgreSQL and PR previews) maintainers
- **Amends:** ADR-095 (PR preview environments), ADR-20261026090000 (PR preview environments for general availability), ADR-20261028090000 (preview configuration), [ADR-20261025094915](20261025094915-managed-postgres-compute-caps-on-running-databases.md) decision 9 (the storage pool)
- **Context:**
  - A PR preview gets no database. Managed credentials are never copied into a preview (ADR-20261028090000), so a preview of an app that binds managed PostgreSQL starts without `DATABASE_URL`, or with a value the customer typed into the `pr-preview` scope by hand. One database shared by every PR breaks as soon as two PRs migrate it differently.
  - Other platforms give each PR its own database: Neon and PlanetScale branch one per PR, and Supabase creates an empty one per PR and runs its migrations. Ours is the one place a whole PR environment, apps and database together, comes up from one push.
  - The pieces exist:
    - A developer session already gets an empty development database bound as `DATABASE_URL` and removed with the session (`ensureDevPostgres`).
    - An isolated environment clone already restores a database copy and binds it in the target scope (`ensureProjectEnvironmentDatabaseClone`).
    - A `migration` binding hands a release command its own credential, so a PR's release step can migrate the PR's own database.
  - Two rules shape the design:
    - **Code under review must not reach production data unless someone decided it may.** A PR can come from any collaborator, and ADR-310 already keeps production values out of previews for that reason.
    - **Previews must never cost production anything.** The storage pool blocks writes on every database of the account once the pool is full (ADR-20261025094915 decision 9), and the compute ceiling suspends them all. A preview database counted like any other could stop production.
- **Decision:**
  1. **Opt-in on the production database.** A managed database gains a `previews` setting:
     - **Values:**
       - `off` is the default and today's behaviour.
       - `empty` gives each PR environment a new empty database.
       - `copy` gives each PR environment a copy of this database as of when the preview was set up.
     - **Where it is set:**
       - `PUT /v1/postgres/databases/{id}/previews`, or `gregale postgres previews NAME empty|copy|off`. It needs the same scopes, MFA and verified email as the database's other changes.
       - The database view shows it (`previews`, omitted when `off`).
     - **Never read from the repository or the PR head.** Code under review cannot opt itself into a copy of production data. Someone who may already restore the database decides.
     - **When it applies:**
       - A change applies to previews set up afterwards.
       - Turning it `off` does not remove existing preview databases; they go with their PR.
       - A preview database cannot have previews of its own.
  2. **One preview database per PR environment and source database.**
     - **The environment** is the open previews of one project and pull request (ADR-20261026090000). A legacy preview without a project is its own environment.
     - **When apid sets it up:**
       - when it prepares a preview build on the GitHub path (the githubd bridge's `EnqueueBuild`, after the `pr-preview` values);
       - when `POST /v1/apps/{slug}/previews` creates or reopens a preview.
     - **What it sets up:**
       - It looks at every live binding the production app holds in a scope production serves (`default` and `production`, as `ResolveEnvironmentDeployment` reads them) whose database has previews on.
       - For each one, it ensures the environment's preview database for that source, then binds the preview app to it in scope `default`, with the production binding's environment key and access.
       - Members of one environment that bind the same source share one preview database, as their production apps share the source.
       - A `migration` binding becomes a `migration` binding on the preview, so the PR's release command migrates the PR's own database.
       - Production's bindings and credentials are never copied. Each preview binding has its own login.
     - **Ownership:**
       - The preview database row records its source (`preview_source_database_id`), its environment (`preview_environment_id`: the project's ID, or for a legacy preview the preview app's own) and its PR (`preview_pr_number`).
       - A unique index over the three, for undeleted rows, makes setup idempotent across retries and apid replicas.
     - **Name:** `pr-<N>-<source name>-<6 hex characters of the environment ID>`. The source name is shortened to fit 63 characters, and the suffix keeps two projects that preview the same database apart.
     - **`empty`:**
       - A development-class, scale-to-zero, single-zone database with no restore window: the developer session's spec.
       - It has the source's region and major, and a storage limit of 1 GiB or the plan's pool, whichever is smaller.
       - The customer's release command creates its schema; seed data comes from the same command, for example behind a `pr-preview` value.
     - **`copy`:**
       - A point-in-time restore of the source as of a second before setup, as an isolated environment clone does it, on the source's backend: on Neon a branch of the source's project.
       - It needs the source's restore window, which every plan has, and a backend that restores to a point in time. `PUT .../previews` refuses `copy` without them (`400`), and setup refuses it if they went away since.
       - It keeps the source's storage limit and restore window, which a Neon branch shares with its project, and is development class, single-zone and scale-to-zero.
       - Its row records the restore (`restore_source_database_id`, `restore_point_in_time`) beside its preview owner. It does not take the restore slot, and the plan's database limit does not count it.
       - It is taken once, when the environment first needs it. Later pushes keep the copy and whatever was written to it, so the PR's migrations run against it the way they will run against production.
  3. **The build waits for its database; a refusal fails the preview.**
     - The GitHub bridge enqueues a preview build only once every preview binding it needs is ready. Until then it answers `Unavailable`, which githubd retries like any retryable failure.
     - A source deployment into a preview (`POST /v1/apps/{slug}/deployments/source-ref`, what `gregale preview create` sends) waits the same way: until then it answers `503 preview_database_provisioning` with `Retry-After`, apid does not replay that answer under its Idempotency-Key, and the CLI resends it up to its `--timeout`.
     - When setup is refused, the reason is recorded as a setup failure on the PR (ADR-20261026090000 decision 3). The check says the preview's database was refused and why, but names no database, because the PR may be public. Reasons include:
       - the plan does not allow it, or the limit in decision 5 is reached;
       - the source has no restore window for `copy`;
       - the backend refuses.
     - A preview without the database its app expects would only fail later, at its first query.
  4. **Teardown removes the database.**
     - When a preview app is torn down (destroy, the janitor after the close grace or lease end, a repository disconnect), apid deletes its preview bindings in the same teardown that already removes a developer session's database.
     - A preview database with no open member left in its environment is deleted and purged at once, with no 7-day undo. It is disposable, and a copy holds production data that should not outlive the review.
     - A sweep in apid purges preview databases whose environment has no undeleted preview left, for teardowns that stopped midway. It runs every 10 minutes on each replica (a purge is idempotent) and leaves a database set up in the last 10 minutes alone, so a preview the listing does not show yet keeps its database.
     - A customer may delete a preview database like any other; the next preview build sets it up again.
     - A customer's delete of a source database purges its preview copies first: on Neon a copy is a branch of the source's project and would go with it. Empty preview databases do not depend on their source and stay with their PR. An internal delete of a source (plan excess, account erasure) does not purge copies first; the copy's teardown or the sweep removes its row and bindings later.
  5. **Limits.**
     - Preview databases are outside the plan's `DatabasesMax`, like the restore slot (ADR-20261031123259). They are bounded by the preview allowance instead: one per open preview environment and source database. That is at most `PreviewApps × DatabasesMax` per account (Free 1, Hobby 2, Pro 15, Scale 200), counted under the owner's reservation lock.
     - Setup also needs a plan that may create a development database (`DevelopmentAllowed`, and not Free while caps are not enforced, as for developer sessions).
     - No plan number changes, so the financial model is unchanged. Preview databases use the account's pooled compute and are metered like any database.
  6. **Previews never take what production needs.**
     - **Storage:**
       - The storage pool leaves preview databases out of its sum, as it does the restore slot's copy. Each stops at its own spec limit instead: the provider object it gets is created with that limit (a Neon project's logical size quota, a database VM's volume).
       - An empty preview database is its own provider object, Neon project or database VM.
       - A copy on Neon is a branch of its source's project, so it also stops at that project's water level.
     - **Compute:**
       - Once the account's pooled compute reaches 80% of its compute ceiling (the allowance, or twice it with overage on), its preview databases are held.
       - The hold is the cap sweep's compute hold (ADR-20261025094915 amendment 2026-11-05): from 80% the sweep holds each preview database, so its compute stops and its connections are refused, and below 80% it releases it. Past the whole ceiling the sweep holds every database, as before.
       - The quota is the backstop, as for production: a provider object holding only preview databases gets what the account has left of 80% of the ceiling instead of what it has left of the whole ceiling.
       - The hold lifts with the next month's use, or when an upgrade or overage raises the ceiling.
       - It follows the compute cap's mode: shadow logs it, on applies it. A backend without compute quotas (the native backend today) caps nothing, previews included.
       - Production databases keep today's caps. A provider object a preview database shares with production (a `copy` branched in its source's Neon project) keeps production's quota, so only the hold stops a copy.
  7. **Visibility.**
     - Preview databases are listed with the account's databases. The view marks them (`preview`: source database ID, PR number), omitted for every other database.
     - Customers can read, connect to and delete them. Binding them to another app, restoring from them and setting their `previews` are refused with `409 managed_postgres_preview_database`.
  8. **Gate.**
     - A new catalog capability `preview-databases` (maturity `preview`, parent `pr-previews`) puts the setting route behind its platform flag (ADR-20261023115741).
     - Outside the flag, the route is absent and apid sets up no preview database even if a setting is stored.
     - It stays off until the owner has tested it on production.
- **Delivery** (one PR each, all behind the flag):
  1. This ADR.
  2. **Schema and service:**
     - an expand migration adds `previews` (`NOT NULL DEFAULT 'off'`, with a CHECK) and the three nullable preview columns to `managed_postgres_databases`, and builds the partial unique index concurrently;
     - `PostgresStore` and `MemoryStore` with the contract test;
     - `Service.SetPreviews`, `Service.EnsurePreviewDatabase` (empty) and `Service.PurgePreviewDatabase`;
     - the reservation rule in decision 5.
  3. **apid:**
     - the setting route and CLI (`gregale postgres previews set DATABASE empty|off`);
     - setup in the bridge and the preview create path (`pkg/apid/previewdb`). The bridge sets up before it spends the deploy window, so retries while a database provisions cost no deploys. The API create path does not wait for a provisioning database; the source deployment into the preview does (decision 3);
     - teardown in the destroy path and the janitor; a customer's delete of a preview database purges it;
     - setup failures on the PR;
     - only the setup may bind a preview database (`CreateBindingRequest.PreviewSetup`).

     Organization-owned databases are not set up in this step: setup and the setting route use the account's personal ownership, as developer-session databases do.
  3b. **The sweep** (`previewdb.RunSweep`) for preview databases whose environment closed while a teardown stopped midway, or while an older apid tore it down. It pages through the undeleted preview databases across accounts and checks each against its account's undeleted previews (`ListPreviewsForAccount`); an account whose previews cannot be listed keeps its databases. It is not behind the flag: it only removes preview databases.
  4. **Guards:** the storage pool leaves preview databases out of its sum, and the compute cap gives preview-only provider objects 80% of the ceiling (decision 6), both in `CapEnforcer`.
  5. **`copy`:** the setting accepts it; setup restores the source (`Service.EnsurePreviewDatabase`, `ReservePreview` taking a copy's restore); the cap sweep's compute hold takes every preview database from 80% (`capHeldDatabases`); a customer's delete of a source purges its copies (`previewdb.PurgeCopies`); the CLI takes `copy`.
  6. **Launch:** customer docs (`docs/preview-databases.md`, unpublished while the capability is `preview`), the dashboard (faas-web), a `tests/journeys` journey (`TestPreviewDatabases`: a preview with an empty database migrated by its release command, purged with the preview), then `beta` once the owner has tested it on production. At `beta` the page is published, and `docs/preview-environments.md` and `docs/postgres.md` link it; until then they still say previews get no managed database credentials.
- **Release skew:**
  - An older apid ignores the new columns. It sets up no preview database and does not remove one. The newer apid's reconciler sweep removes preview databases whose environment closed while an older replica tore it down.
  - githubd is unchanged: apid does the setup inside `EnqueueBuild`, and githubd already retries `Unavailable`.
- **Why:** A preview that cannot reach a database cannot preview most backends. Today customers either share one database across every PR or give up on previews for apps with data. A per-PR database, migrated by the PR's own release command and removed with the PR, closes that gap with parts that already exist, and keeps production data and production capacity out of reach of code under review.
- **Consequences:**
  - An account can hold more databases than `DatabasesMax` while previews are open. Each one is a scale-to-zero development database inside the account's pooled compute, and it is gone when its PR is.
  - On Neon, an empty preview database is a new project (a few seconds to create), and a copy is a branch (shared pages until written).
  - On the native backend (ADR-1027), both are a database VM. A copy is a full restore with no shared pages, so its storage costs what its source does.
  - A held preview database fails its preview's queries for the rest of the month. That is the point of the hold: production keeps the last fifth of the ceiling.
  - On Neon a copy is a branch of its source's project and would go with it. Deleting the source through Gregale purges the copies first.
- **Rejected alternatives:**
  - **The setting in the repository** (`.gregale/project.yaml` or Compose). A PR could turn on `copy` and read production data.
  - **One database shared by every PR** (a `pr-preview` binding). One PR's migration breaks the others, and data written in one review shows up in another.
  - **Count preview databases in `DatabasesMax`.** Free and Hobby allow one database, so they would never get a preview database, and Pro could preview two PRs at most.
  - **`copy` by default.** It would put production data in front of code under review without anyone deciding to. `empty` is the safe choice, and `copy` is a decision per database.
  - **A new copy on every push.** It loses what reviewers entered and multiplies restores. One copy per environment also matches how the PR's migrations will meet production: incrementally.
  - **Keep the 7-day undo.** A preview database is disposable, and a copy's production data should not outlive the review.
  - **Previews counted in the storage pool and compute ceiling like any database.** A busy preview could then block production writes or suspend production compute.

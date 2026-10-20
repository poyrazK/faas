# ADR-1136: Project environment configuration reaches every service as environment variables

- Status: Accepted
- Date: 2026-10-16
- Amends: ADR-590 (the project config JSON gains runtime behaviour through
  named environment variables), ADR-050 (the deferred project-level env)
- Related: ADR-045 (app environment variables), ADR-098 (scoped values),
  ADR-568 (Git-owned environment intent), ADR-1151 (frozen and paused
  instances)

## Context

A project groups the services of one repository (ADR-050), but configuration
is still per app. `app_envs` is keyed by app and scope, so a four-service
project sets `DATABASE_HOST`, `LOG_LEVEL` or `ORDERS_URL` four times, and again
for every service it adds. Compose import keeps only the variable names from
`environment:` and drops the values, so the first deploy of a multi-service
repository ends in repeated `gregale env push` calls. ADR-050 deferred a
project-level tier until environments existed.

They exist now. Each project environment already holds a versioned, non-secret
configuration document (`gregale projects environments config set`,
`PUT /v1/projects/{slug}/environments/{environment}/config`). Plans, release
sets, qualifications, clones and promotions bind to its hash, but ADR-590
records that it is only a plan identity: it "must be connected to runtime
behavior through typed settings or named environment variables" before a
change to it counts as a runtime change. No guest ever sees it.

## Decision

### 1. Which keys become environment variables

A top-level key of the environment's latest configuration version is exported
to the services of that project environment when:

- the key is a valid environment variable name (`^[A-Z][A-Z0-9_]*$`, the rule
  `gregale env` already enforces), and
- its value is a JSON string, number or boolean. Strings are delivered as-is,
  numbers as their JSON text and booleans as `true` or `false`.

Every other key (lowercase, dotted, hyphenated) and every other value (object,
array, null) stays document-only, as today. The configuration API already
rejects secret-shaped keys (`*_TOKEN`, `*_PASSWORD`, `API_KEY` …), so the
document cannot carry credentials; they stay in sealed secrets. The existing
64 KiB document bound caps the exported set, so no new plan limit is needed.

### 2. Which environment a deployment reads

A deployment reads the configuration of the project environment named by its
scope. The `default` scope reads `production`, the same mapping the runtime
owner already uses for a project's implicit production environment.
Deployments of apps outside a project read nothing.

### 3. Precedence

From lowest to highest: manifest environment, project configuration, the app's
own variable in the deployment's scope, sealed secrets and secret references,
platform identity. An app variable with the same key always wins, so any
service can override a shared value. The guest's existing rule (secrets over
API env over manifest env) is unchanged; the project values are part of the API
env, below the app's own rows.

### 4. Delivery

The values are read in the same statement as the deployment's app variables and
secrets (`RuntimeAppValuesForDeployment`) and carried in the runtime value
snapshot. Every boot path that already uses that snapshot (wake, prime boot,
app tasks and cron runs, qualification) delivers them with no path of its own.
GitOps-managed scheduled Jobs are the exception: they run exactly the reviewed,
frozen variable set of their definition (ADR-568), so they inherit nothing; a
shared value belongs in that definition. vmmd's opt-in live runtime
configuration reads the same values (`RuntimeAppEnvForDeployment`), so a
process that polls it sees what it booted with.

### 5. Changes and freshness

A configuration version whose exported set differs from the previous version's
stamps a runtime-configuration change for every app of the project in the
affected scope (`production` also stamps `default`), and marks those apps'
snapshots in that scope stale, as an app variable write does. A database
trigger on the version insert does this, so every writer is covered: the
config API, promotions and their rollbacks, clones and GitOps apply. A version
that changes only document-only keys invalidates nothing.

Running, paused and frozen instances keep the values they booted with until
they restart, as with app variables. The configuration fence of the runtime
snapshot includes the inherited values, so a paused, frozen or warm instance
whose inherited values changed is not resumed into service; it boots again.
When a deployment inherits nothing the fence is computed exactly as before, so
existing instances keep their fingerprints across the upgrade.

Runtime configuration receipts keep recording only the app's own variables:
`environment_runtime_inputs_fresh` compares them with `app_envs` exactly.
Inherited values are covered by the scope-change stamp instead.

### 6. Rollout beside release N-1

Units release on their own (docs/release-compatibility.md), so each step must
serve the previous release of the others:

- **Data.** The migration adds two functions and an insert trigger, with
  `lock_timeout` set. The trigger only stamps scope changes and marks
  snapshots stale, so N-1 code keeps working; at worst it cold boots an
  instance it could have restored.
- **Control plane and node.** An N-1 schedd delivers no inherited values. The
  configuration fence includes them only when there are any, so an instance
  booted by one version and resumed by the other boots again instead of
  resuming with the other set. An N-1 vmmd's live configuration omits
  inherited keys until vmmd is upgraded; the boot environment is unaffected.
- **Runtime.** Nothing: the values travel in the existing API environment, with
  no new guest key or proto field.

### 7. Not in this decision

- Project-level secrets. Shared credentials stay per app (sealed secrets or
  secret references) until a separate decision covers sealing and rotation at
  project scope.
- Importing compose values. Values found in compose files may be credentials,
  and §11 keeps them out of logs and plans; import stays names-only.
- Showing inherited values in `gregale env pull`, which writes a file that
  `env push` would copy back into the app as overrides.

## Consequences

- One `gregale projects environments config set` call configures every service
  of a project environment, and a service added later inherits it.
- A production configuration change also stamps the `default` scope, which the
  freshness check reads for every scope. In environments that require runtime
  receipts (ADR-568), member snapshots of other environments can cold-boot once
  after such a change. Snapshots are a cache (ADR-005); the cost is one boot.
- The configuration document becomes runtime behaviour, which is what ADR-590
  asked for before a configuration change can count as a tested runtime change.
- Rolling back to a release without this decision leaves the stored versions
  untouched; an older schedd simply stops delivering them.

## Rejected alternatives

- **A new project variables table.** It would duplicate the versioning, hashing,
  promotion and GitOps ownership the configuration document already has.
- **A reserved `env` object inside the document.** Explicit, but it forces a
  second spelling of the same idea; env-shaped top-level keys are already what
  customers write.
- **Patching each writer to invalidate snapshots.** Five code paths write
  versions today; a trigger cannot be forgotten by the sixth.
- **Reading the latest version at boot in schedd only.** The fence comparisons
  in the warm pool, quiet pause and frozen tier would not see a change, and the
  read would not be atomic with the app's own values.

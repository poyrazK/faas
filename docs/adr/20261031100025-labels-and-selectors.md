# ADR-20261031100025: Resources carry labels, and lists and waits take a selector

- **Status:** proposed
- **Date:** 2026-10-31
- **Extends:** ADR-20261030151810 (resource conditions: a wait can cover every
  resource a selector picks) and ADR-20261029093621 (Kubernetes manifest import:
  `metadata.labels` gain a destination).
- **Checked against:** ADR-050 (projects group a repository's workloads; labels
  do not replace them), ADR-20261026145832 (CLI command tree: no new top-level
  verb), ADR-20261023115741 (every capability behind a platform flag),
  `docs/release-compatibility.md` (release N beside N-1; expand/contract
  migrations), the pricing principle (software features are free), and the
  `CLAUDE.md` rules that apid is the only writer of customer intent and that SQL
  goes through sqlc with no string-built queries.

## Context

A team with more than a handful of resources needs to say which ones belong
together across the lines Gregale already draws:

- **Projects** (ADR-050) group the workloads of one repository, and
  **environments** scope a project's deployments. Both are structural: an app
  is in one project, and moving it changes how it deploys.
- Nothing groups resources by owner, cost centre, tier or purpose. An agent
  asked to "check every billing service" lists every app and guesses from
  names. `gregale app list` has no filter beyond the project.
- **Kubernetes users bring labels with them.** Every Deployment, CronJob and
  Service in an imported manifest carries `metadata.labels`; the importer uses
  them only to match Services to workloads and then drops them.
- **Conditions** (ADR-20261030151810) made "wait until it is ready" one loop
  per resource. Waiting for a set of resources still takes one command each.

Kubernetes answers this with labels (free-form `key=value` pairs on every
object) and label selectors (`team=billing,tier in (web,api)`) on every list,
watch and bulk command. Most agents and many developers already know both.

Other things that look similar and are not this:

- Object storage tags (`pkg/api/object_tagging.go`) follow the S3 tagging API
  on buckets and objects. They keep their S3 semantics and limits.
- Issue tags and `pkg/manifest` doctor tags are domain-specific filters.
- Metric labels are Prometheus series dimensions, not customer data.

## Decision

### 1. A label is a Kubernetes label

A resource carries up to `api.LabelsPerResourceMax` (32) labels, a map from key
to value, with Kubernetes syntax so a selector copied from `kubectl` works:

- **Key:** an optional prefix and a name, `[prefix/]name`. The name is 1–63
  characters of `[A-Za-z0-9._-]`, starting and ending alphanumeric. The prefix
  is a DNS subdomain of at most 253 characters.
- **Value:** 0–63 characters of the same alphabet, empty or starting and ending
  alphanumeric.
- **Reserved:** the `gregale.dev/` prefix and its subdomains belong to Gregale.
  Customers cannot set them; Gregale may (for example `gregale.dev/imported-from`).
- **Not secret.** Labels appear in lists, audit events and the dashboard, and
  the API and CLI help say so. The importer copies `metadata.labels` only,
  never Secret data.

The limits live in `pkg/api/limits.go` (`LabelsPerResourceMax`,
`LabelKeyNameMax`, `LabelKeyPrefixMax`, `LabelValueMax`, and
`LabelSelectorMaxBytes` (4096), which bounds a selector) and are the same on
every plan: labels hold no resources, so they cost nothing.

Annotations (free-form metadata that selectors cannot match) are out of scope.

### 2. Which resources carry labels

In order:

1. **Apps** (functions, services and workers alike).
2. **Managed databases**, **crons** and **jobs**.

Domains, deployments and job runs do not carry labels: a selector reaches them
through the app or job they belong to (`gregale domain list -l team=billing`
lists the domains of matching apps). Buckets keep their S3 tags.

### 3. Storage: a `labels` column on each resource's row

Each labelled table gains `labels jsonb NOT NULL DEFAULT '{}'`, with a `CHECK`
that it is a JSON object. A label lives and dies with its row, so a purge
needs no extra step and no label can outlive its resource.

- **Writer.** Only apid writes `labels`, through one sqlc statement per table
  that sets and removes keys in a single `UPDATE` and refuses the write when the
  result would exceed the limit. The one exception is the repository reconcile
  (`pkg/reconcile`), which already creates apps in apid and in githubd: it sets
  the imported labels of an app it has just created, through the same
  statement (§7). No other component reads them yet; schedd, vmmd and the
  gateways never do, and nothing on the request path selects the column.
- **Migration.** Expand only: `ADD COLUMN … DEFAULT '{}'` is a metadata change,
  and the object `CHECK` is added `NOT VALID` and validated in a separate
  statement, each after `SET LOCAL lock_timeout = '5s'`.
- **Not one shared table.** A `resource_labels` table would need either a
  foreign key per kind or a purge hook per kind, and every list would join it.
  The column keeps ownership and lifetime where they already are.

### 4. Selectors: the Kubernetes syntax, matched in Go

A selector is a comma-separated list of requirements that all must hold:

| Requirement | Matches |
|---|---|
| `key=value`, `key==value` | the label is set to the value |
| `key!=value` | the label is absent or set to another value |
| `key` | the label is set |
| `!key` | the label is absent |
| `key in (a,b)` | the label is set to one of the values |
| `key notin (a,b)` | the label is absent or set to none of them |

A pure package (`pkg/labels`) validates labels, parses selectors and matches
them; the API, the CLI, the importer and tests share it. An invalid selector is
a 400 with a stable code that names the bad requirement.

**Matching happens in Go over the list a route already reads.** The app,
database and cron lists are per account and bounded by plan limits (100
deployed apps, 10 databases, 500 crons on Scale), and `GET /v1/apps` already
reads the whole account list. Translating selectors to SQL would need either
string-built queries, which `CLAUDE.md` forbids, or a fixed query per selector
shape. Jobs have no per-account cap and their list is paginated, so step 5
decides between a job count limit and an index before jobs take a selector;
filtering one page at a time would return short pages and is not done.

### 5. API

- Each labelled resource's response gains `labels` (`omitempty`, so N-1
  clients see no change).
- `PATCH /v1/apps/{slug}/labels` (and the same under `/v1/postgres/databases/{id}`,
  `/v1/crons/{id}`, `/v1/jobs/{name}`) takes
  `{"set": {"team": "billing"}, "remove": ["tier"]}` and returns the full map.
  One request sets and removes atomically; it is idempotent, and setting a key
  to its current value changes nothing. A write emits a
  `resource.labels.changed` audit event with the keys set and removed.
- Every labelled list route, and `GET /v1/domains`, takes `?selector=`.

### 6. CLI

No new top-level verb and no new verb in the tree: `label` is a sub-noun with
the standard `list`, `set` and `unset` verbs, and `-l`/`--selector` is a flag.

```
gregale app label set my-api team=billing tier=web
gregale app label unset my-api tier
gregale app label list my-api
gregale app list -l 'team=billing,tier in (web,api)'
gregale app wait -l team=billing --for Ready
```

The same `label` verbs and the selector flag reach `postgres`, `cron` and `job`.
A wait with a selector exits 0 when the condition holds on every resource it
picks, 1 when any is terminal without it, 3 on timeout and 4 when it picks
nothing; `--json` prints one status document per resource. The picked set is
read once at the start, so a resource labelled during the wait is not added.

### 7. Manifests

- **`gregale.yaml` / `gregale.toml`** gain a `labels` map per app, applied on
  deploy. A deploy sets the keys the manifest declares and removes nothing:
  dropping a key from the manifest leaves the label until
  `gregale app label unset` removes it. Tracking which keys a manifest last
  applied, as `kubectl apply` does, waits for a need.
- **The Kubernetes importer** carries each Deployment's and CronJob's
  `metadata.labels` to the app it creates (a CronJob imports as a job-class
  app with its schedules), drops `gregale.dev/` keys and reports invalid ones
  and any past the per-resource limit as scan warnings. Pod template labels
  stay matching-only, as today. The scan reads labels only while the account
  has the capability (`reposcan.ScanOptions.ResourceLabels`, decided the same
  way by apid and githubd so a push deploy matches the reviewed plan), and only
  a newly created app is labelled: an existing app keeps the labels its owner
  set.

### 8. Rollout

- **Capability.** A `resource-labels` preview capability, off by default, on
  every plan, owns the routes, the `label` commands and the selector flag
  (ADR-20261023115741). Customer docs and the dashboard come with beta.
- **Release compatibility.** The column, fields and routes are new. N-1 apid
  ignores the column; a new CLI against an apid without the capability gets a
  404 for `label` and a 400 for `?selector=`, and says so.
- **Steps.**
  1. This ADR.
  2. `pkg/labels` (validation, selector parsing and matching) and the limits.
  3. Apps: migration, sqlc writes, `PATCH /v1/apps/{slug}/labels`, `labels` in
     app responses, `?selector=` on `GET /v1/apps` and `GET /v1/domains`.
  4. CLI: `app label list|set|unset`, `app list -l`, `domain list -l`,
     `app wait -l`.
  5. Databases, crons and jobs.
  6. Manifest `labels` and the Kubernetes importer.
  7. Docs, dashboard, MCP tools and a journey; then beta.

## Consequences

- One vocabulary groups resources across projects and kinds, and an agent can
  act on a group with the selectors it already knows from Kubernetes.
- Waits cover a group: deploy several apps, then one `app wait -l` call.
- Kubernetes manifests import with their labels instead of losing them.
- Labels stay customer metadata: they change no scheduling, routing, billing or
  quota. Grouping usage and alert rules by label are separate decisions.
- Selector matching in Go costs one full per-account list read per filtered
  list; plan limits bound it for apps, databases and crons, and jobs need a
  limit or an index first.

## Alternatives considered

- **Tags as a flat list of strings.** Simpler, but a list cannot answer
  "which team" and cannot express `in`/`notin`; every major platform that
  started with flat tags (AWS, GCP) uses key/value pairs.
- **Reuse projects or environments for grouping.** They decide how a workload
  deploys; overloading them with ownership or cost groupings would make a
  reorganisation a redeploy.
- **A shared `resource_labels` table.** Rejected above (section 3): it needs a
  foreign key or purge hook per kind and a join on every list.
- **Selectors compiled to SQL.** Needs string-built queries or a query per
  selector shape; deferred until list sizes need an index.

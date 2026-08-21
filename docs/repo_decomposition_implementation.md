# Repo decomposition — implementation plan

Companion to [ADR-050](adr/050-repo-decomposition-and-project-object.md).
The ADR records *what we decided and why*; this file is *how it gets built*,
in landable order, with the acceptance gate for each phase.

**Goal:** point the CLI at a repo containing N backend workloads — HTTP APIs,
GraphQL services, cron jobs, queue workers — and have all N exist on the
platform after one confirmation keypress. No per-language parser, no manifest
the customer has to author.

**Non-goal:** storage. Datastores found during the scan are reported as
managed-service requirements, never provisioned (ADR-047, `docs/storage.md`).

---

## 1. Target experience

```
$ faas deploy

  Scanning acme/shop @ main …

  Found 4 workloads  ·  source: docker-compose.yml

    NAME      CLASS    DIR         FROM
    api       http     ./api       compose service "api"
    admin     http     ./admin     compose service "admin"
    worker    worker   ./worker    compose service "worker"
    nightly   job      ./jobs      k8s CronJob (0 3 * * *)

  Managed services needed — set these before first request:
    postgres  →  DATABASE_URL       (compose service "db")
    redis     →  REDIS_URL          (compose service "cache")

  Plan Hobby: 5 apps allowed, 0 used, 4 will be created.

  Deploy all 4?  [Y/n]
```

`Y` creates the project, four apps, one cron row, and enqueues four builds.
That is the whole interaction.

On every subsequent `faas deploy`, the same scan runs and renders a **diff**
instead of a first-run list — the repo is the source of truth, so the deployed
set reconciles to whatever the repo now declares:

```
$ faas deploy

  Scanning acme/shop @ main …

  Changes since last deploy  ·  source: docker-compose.yml

    + payments   http     ./payments   new compose service
    ~ api        http     ./api        start command changed
    - legacy     http     ./legacy     no longer declared — will be REMOVED
      worker     worker   ./worker     unchanged
      nightly    job      ./jobs       unchanged

  Removing "legacy" also deletes its 3 env vars and 1 custom domain.

  Plan Hobby: 5 apps allowed, 4 used → 4 after apply.

  Apply?  [Y/n]
```

A push to the production branch performs **the same reconcile, unattended** —
there is no human at a webhook. The three guards in ADR-050 (never reconcile to
empty, production branch only, scan-source stability) are what make the
unattended destructive path safe.

Supporting verbs:

| Command | Behaviour |
|---|---|
| `faas scan` | Print the table and exit. Provisions nothing. |
| `faas deploy --yes` | Skip the prompt (CI). |
| `faas deploy --json` | Emit the plan as JSON, provision nothing unless `--yes`. |
| `faas deploy --only api,worker` | Provision a named subset. |

---

## 2. Data model

Migration **`00074_projects_and_workloads.sql`**. Main is at 66; draft PR #428
(extend-metering) claims 67, 68, 69, 70 and 73. Reserve 74 at PR-open and
renumber on rebase per ADR-041.

```sql
create table if not exists projects (
    id                uuid primary key default gen_random_uuid(),
    account_id        uuid not null references accounts(id) on delete cascade,
    slug              text not null,
    repo_full_name    text,
    production_branch text,
    install_id        bigint,
    scan_source       text not null default 'unknown',
    created_at        timestamptz not null default now(),
    updated_at        timestamptz not null default now(),
    constraint projects_slug_shape check (slug ~ '^[a-z0-9][a-z0-9-]{0,62}$'),
    constraint projects_scan_source_chk check (scan_source in
        ('compose','procfile','k8s','render','fly','serverless',
         'workspace','convention','single','unknown')),
    constraint projects_account_slug_uniq unique (account_id, slug)
);

create unique index if not exists projects_install_repo_uniq
  on projects (install_id, repo_full_name)
  where install_id is not null and repo_full_name is not null;

alter table apps
  add column if not exists project_id     uuid references projects(id) on delete set null,
  add column if not exists root_dir       text not null default '',
  add column if not exists workload_name  text not null default '',
  add column if not exists workload_class text not null default 'http',
  add column if not exists start_command  text;

-- every state column carries a CHECK (CLAUDE.md conventions)
alter table apps drop constraint if exists apps_workload_class_chk;
alter table apps add constraint apps_workload_class_chk
  check (workload_class in ('http','graphql','grpc','job','worker'));

-- one workload NAME per project. Deliberately not (project_id, root_dir):
-- a root Procfile with web: and worker: is one dir, two workloads.
create unique index if not exists apps_project_workload_uniq
  on apps (project_id, workload_name)
  where project_id is not null;

-- superseded by projects_install_repo_uniq
drop index if exists apps_github_install_repo_uniq;
```

**Backfill** (idempotent; safe because the dropped index guarantees ≤1 app per
`(install, repo)` today, so no project-slug collision is reachable):

```sql
insert into projects (account_id, slug, repo_full_name, production_branch, install_id, scan_source)
select distinct on (a.github_install_id, a.github_repo_full_name)
       a.account_id, a.slug, a.github_repo_full_name,
       a.github_production_branch, a.github_install_id, 'single'
from apps a
where a.github_repo_full_name is not null
  and a.github_install_id is not null
on conflict do nothing;

update apps a
   set project_id = p.id,
       workload_name = case when a.workload_name = '' then a.slug else a.workload_name end
  from projects p
 where p.install_id = a.github_install_id
   and p.repo_full_name = a.github_repo_full_name
   and a.project_id is null;
```

Replay-safety is a hard gate (`migrations/replay_safety_test.go`): every
statement above is re-runnable. Pin the shape in
`00074_projects_and_workloads_test.go` with a second `MigrateUp` assertion, per
the 00053 precedent.

Standalone apps are unaffected: `project_id` nullable, `root_dir` defaults to
`''`.

---

## 3. `pkg/reposcan`

Pure package. No network, no exec, no Postgres. Tested with `fstest.MapFS`.

```go
func Scan(fsys fs.FS) (Result, error)

type Result struct {
    Workloads []Workload   // sorted by Name — deterministic table + golden tests
    Managed   []Managed    // datastores: reported, never provisioned
    Tier      Tier         // highest tier that produced a workload
    Warnings  []string
}

type Workload struct {
    Name       string   // compose service / Procfile type / directory name
    RootDir    string   // build context, relative to repo root ("" = root)
    Dockerfile string   // explicit path, if declared
    Command    []string // start-command override
    Class      Class    // http|graphql|grpc|job|worker|unknown — a HINT pre-probe
    Schedule   string   // cron expression, when declared
    Ports      []int
    EnvKeys    []string // KEYS ONLY — never values (spec §11: never log secrets)
    Source     string   // "docker-compose.yml: api" — shown in the table
    Tier       Tier
}

type Managed struct {
    Name    string // "db"
    Kind    string // postgres|redis|mysql|mongo|…
    EnvHint string // DATABASE_URL
    Source  string
}
```

### Tiers

**Tier 1 — the repo already declared its workloads.** Highest confidence,
language-independent, often carries class *and* schedule.

| Source | Extracts |
|---|---|
| `docker-compose.yml` / `compose.yaml` | `services{}`: `build.context`→RootDir, `build.dockerfile`, `command`, `ports`, `environment` keys, `depends_on` |
| `Procfile` | `web:`→http · `worker:`/`consumer:`→worker · `cron:`/`clock:`/`scheduler:`→job · `release:`→skipped (build hook) |
| k8s manifests (`k8s/`, `deploy/`, `manifests/`) | `Deployment`→server · `CronJob`→job + `.spec.schedule` · `StatefulSet`→**refused** (stateful) |
| `render.yaml` | `services[]` (`type: web/worker`) + `cronJobs[].schedule` |
| `fly.toml` | one app + `[processes]` |
| `serverless.yml` | `functions{}` + `events[].schedule` |
| `app.yaml` | services / workers / jobs |

**Tier 2 — workspace manifests.** Enumerates members; says nothing about class.
`package.json` `workspaces`, `pnpm-workspace.yaml`, `turbo.json`, `nx.json`,
`go.work` `use()`, `Cargo.toml` `[workspace] members`. A member becomes a
candidate only if it also carries a Dockerfile or a language marker.

**Tier 3 — directory convention.** `services/*/`, `apps/*/`, `packages/*/`,
`cmd/*/` where each contains a Dockerfile or language marker. This is guessing —
it is exactly why the confirmation table exists.

**Tier 4 — single unit at repo root.** Today's behaviour; the floor.

### Merge rule

Conflicts resolve by `(RootDir, Name)`. Highest tier wins; lower tiers may fill
**empty fields only** (compose supplies ports, a Procfile supplies class).
Output sorted by `Name`.

### Datastore denylist

`postgres`, `postgis`, `mysql`, `mariadb`, `redis`, `valkey`, `mongo`,
`cassandra`, `clickhouse`, `elasticsearch`, `opensearch`, `rabbitmq`, `kafka`,
`nats`, `minio`, `memcached`, `etcd` → `Managed`, with an env hint. Matched on
the image name, ignoring registry host and tag.

Non-datastore services declaring `image:` with no `build:` are **skipped with a
warning** — arbitrary prebuilt images trip the two-drive `FROM`-base constraint
(`pkg/oci/image.go`), which needs its own ADR.

---

## 4. Phases

Each phase is independently landable and independently valuable. Ship in order.

### Phase 1 — schema + project object

Migration 00074, `pkg/state` types + `PgStore`/`MemStore` methods
(`CreateProject`, `ProjectByRepo`, `AppsForProject`), admin-free CRUD wiring.
No scanner, no CLI change.

**Gate:** migration replays clean; backfill converts every existing bound app
into a one-member project; standalone apps keep deploying unchanged;
`make test` green.

### Phase 2 — `pkg/reposcan`, all four tiers

Pure package + exhaustive `fstest.MapFS` table tests, including a golden
fixture per Tier-1 source and a real-world monorepo fixture.

**Gate:** a fixture repo with compose (4 services, 2 of them datastores) +
a k8s CronJob yields exactly 3 workloads, 2 managed entries, correct schedule,
deterministic order. Scanner never reads a file outside the tarball root.

### Phase 3 — CLI plan + one-key provision

`faas scan`, the confirm table, `--yes` / `--json` / `--only`. Server side:
one transactional endpoint that creates project + apps + crons, quota-checked
**before** any write, RFC 7807 limit error carrying limit + observed + docs URL.

**Gate:** `faas deploy` on the fixture repo creates 3 apps + 1 cron on one
keypress; over-quota creates **nothing** and returns the limit problem;
`--json` output is stable enough to assert in CI.

**Status (2026-07-31):** shipped on PR #454 (draft). `cmd/gregale scan`,
`POST /v1/projects/scan` (dry-run) and `POST /v1/projects` (transactional
apply) all green; the §4 acceptance gate passes end-to-end. Migration 00074
slots reserve per ADR-041. Post-merge review fixes (Free-plan downgrade +
deferred-cron skip) landed on the same PR before merge.

### Phase 4 — characterization boot

Specified in full by
**[ADR-051](adr/051-characterization-boot-workload-classification.md)** (accepted
2026-07-31, was proposed since 2026-07-29). Summary of what lands here:

The first cold boot of a new deployment runs in *characterizing mode* — **no
extra VM**, because a separate probe boot would run the app's startup side
effects (schema migrations, lease claims) a second time. guest-init observes
bound sockets (`/proc/net/tcp{,6}`, filtered to the app's process tree), exit
and exit code, and outbound connections; it probes the open port at L7 **from
inside the guest** (HTTP, GraphQL introspection, `/openapi.json`, gRPC
reflection, health paths) and ships one report over AF_VSOCK **STREAM**
(port 1026, msgtype 3) with an ack — not DGRAM, because unlike ADR-047's
advisory this report gates a deploy.

The host re-derives the authoritative class and holds the instance out of the
gateway target set until it lands. `job` and `worker` never join it at all;
`worker` is exempt from idle reaping.

Port normalization happens **in-guest**, never by making `:8080`
configurable — that would need per-app host routing state and chip at ADR-009's
snapshot reuse. Ladder: inject `PORT=8080` → in-guest DNAT → userspace
forwarder (loopback-only binds, or no netfilter NAT in the guest kernel).

Observed class **overrides** the Phase 2 scan hint; disagreements emit a
warning plus an audit event.

Because it reuses the real first boot in the normal tenant netns, the
characterized workload **inherits §11 egress policy by construction** — no
second policy to keep in sync.

**Gate:** an app binding `127.0.0.1:8000` fails with that exact message instead
of `guest not ready after 30s`; an app binding `:3000` is normalized and serves;
a job that exits 0 without listening classifies as `job`; a non-zero exit fails
the deploy with the last log lines; `make test-metal` + `make leakcheck` green.

### Phase 5 — reconcile + path-filtered push fan-out

Push webhook resolves the project, re-scans the pushed tree, and runs
`Reconcile(project, scanResult)` → `create` / `update` / `remove` / `unchanged`
actions.

Reconcile guards (ADR-050), all three ship in this phase:

1. **Never reconcile to empty** — zero workloads is a failed scan; abort + alert.
2. **Production branch only** — feature-branch pushes never mutate membership.
3. **Scan-source stability** — if the tier that produced the last scan is gone,
   abort + alert rather than re-derive from a weaker signal.

Then fan out the builds: rebuild a member iff a changed path falls under its
`root_dir`; a root-level change outside every member rebuilds all; a truncated
payload (GitHub caps at 20 commits / 3000 files) rebuilds all. Builds enqueue
against the 1-guaranteed + 1-opportunistic builder slots and never outrank
tenant wakes.

> **Post-#432 phase 5 flip:** path-filter is the default posture
> (`githubd_path_filter_total{mode="paths"}` on credentialed boxes). The
> full-fan-out fallback fires only when the compare API itself fails:
> truncation, transport error, empty `before`, or the `NewUnavailableChangedFiles`
> stub wired on credentials-missing boxes — the stub surfaces
> `{mode="error"}` → `{mode="breaker_open"}` instead of `{mode="full_fallback"}`
> so the §12 dashboard distinguishes "no GitHub App credentials" from
> "compare API reachable + filter applied." See
> `pkg/githubd/service.go:299` + `pkg/githubd/changedfiles.go:NewUnavailableChangedFiles`.

Every action emits an audit event (`project.workload.added` / `.removed` /
`.changed`) carrying the triggering commit SHA, surfaced on the dashboard and
over SSE. Removal deletes the app row and with it that app's `app_envs`, custom
domains, and `crons`.

Quota is re-evaluated on every reconcile: an over-quota push applies removals
and updates, skips creates as a set, and reports the limit problem.

**Gate:** adding a compose service and pushing creates exactly one app;
deleting one removes exactly one and emits the audit row; deleting the compose
file entirely removes **nothing** and raises the alert; a feature-branch push
changes no membership; a commit touching only `./worker` rebuilds exactly one
app; a commit touching the root lockfile rebuilds all; a 6-member project does
not starve tenant wakes under `make test-load`.

---

## 5. Known-hard and deliberately deferred

- **`workload_class` for `worker` must exempt the app from idle reaping.**
  `SelectEvictions` / `ReapIdle` grow a class guard — the carve-out
  `docs/scale_out_and_workload_classes.md` D4 predicted.
- **Free plan (`DeployedApps: 1`) cannot hold a multi-service repo.** The table
  says so before the prompt; it is a pricing conversation, not a bug.
- **Project-level env** (shared `DATABASE_URL` across members) is not in this
  plan. `app_envs` is keyed `(app_id, key)`; a project-level tier is a later
  migration and should land with preview-environment scoping, not before.
- **Preview deploys per branch** stay out of scope. They need env scoping first
  and are their own milestone.
- **An override file** (`faas.yaml` naming workloads explicitly) remains open
  as an escape hatch for repos with no declarative source. Not the primary
  path — see ADR-050 rejected alternatives.

## 6. Open questions to settle before Phase 3

1. **Project slug source.** Repo name, or prompt? Repo name collides across two
   accounts' forks of the same upstream — but `unique (account_id, slug)` is
   per-account, so repo name is probably fine.

**Settled** (see ADR-050): later pushes **re-scan and reconcile** — new
workloads are auto-created, removed workloads are auto-removed, changed
workloads are updated. `faas deploy` shows the same reconcile as a confirmable
diff. The safety that would otherwise come from a confirmation queue comes
instead from the three reconcile guards.

## 7. Follow-on: affected-workloads preview (`--exclude`)

**[ADR-124](adr/124-affected-workload-preview-and-exclude.md)** (accepted
2026-08-21) extends the `POST /v1/projects/scan` and `POST /v1/projects`
endpoints above so a single tarball commit's blast radius is visible
*before* apply. The `PlanResponse` now carries a partition:

- `will_deploy[]` — scan workloads that will be created or updated.
- `unaffected[]` — every other app in the account that this commit
  does not touch.
- `skipped[]` — scan workloads dropped by `--exclude`.
- `removed[]` — workloads the apply will soft-delete (apply path only).

CLI surface: `gregale scan --show-affected`, `gregale deploy
--exclude=a,b`. Dashboard surface: `GET /dashboard/projects/{slug}/preview`
(form) + `POST .../preview` (re-render) + `POST .../preview/apply` (commit).
The match key is `(RootDir, Name)` — mirrors `pkg/reposcan.Workload.Key()`
and `pkg/reconcile.diff.workloadDiff` so the wire and the apply engine
agree byte-for-byte. No new migrations; this PR is purely derived state.


# Labels and selectors

Labels are key/value pairs you put on apps, managed PostgreSQL databases,
crons and jobs, so you can say which ones belong together: by team, tier,
cost centre or anything else. A selector picks the resources whose labels
match, on lists and waits. Both use Kubernetes syntax, so a selector copied
from `kubectl` works here.

Labels are a preview, released per account (`resource-labels` in
`gregale capabilities`). They are free on every plan: they hold no resources.

Labels are **not secret**. They appear in lists, the dashboard and audit
events. Never put a credential in a label.

Labels are metadata only. They change no scheduling, routing, billing or quota.
Projects and environments still decide how an app deploys; labels group
resources across them.

## Labels

A resource carries up to 32 labels.

| Part | Rule |
|---|---|
| Key | `[prefix/]name`. The name is 1–63 characters of letters, digits, `.`, `_` and `-`, starting and ending with a letter or digit. The optional prefix is a DNS subdomain of at most 253 characters, such as `example.com/team`. |
| Value | 0–63 characters of the same alphabet; empty, or starting and ending with a letter or digit. |
| Reserved | Keys under `gregale.dev/` (and its subdomains) belong to Gregale. You cannot set them. |

The limits are the same on every plan.

## Set and remove labels

```bash
gregale app label set my-api team=billing tier=web
gregale app label set my-api canary=           # an empty value
gregale app label unset my-api tier
gregale app label list my-api

gregale postgres label set orders team=billing
gregale cron label set <cron-id> owner=finance
gregale job label set nightly-report team=billing
```

`set` adds or changes the keys you name and leaves the others. `unset` removes
the keys you name. Both are idempotent and print the full set of labels
afterwards; `--json` prints `{"labels": {...}}`.

Through the API, each resource has a labels route that sets and removes keys
in one request:

```http
PATCH /v1/apps/my-api/labels
Content-Type: application/json

{"set": {"team": "billing"}, "remove": ["tier"]}
```

The response is the full map, `{"labels": {"team": "billing", ...}}`. The same
body works on `/v1/postgres/databases/{id}/labels`, `/v1/crons/{id}/labels` and
`/v1/jobs/{name}/labels`. App, database, cron and job responses carry a
`labels` field when the resource has labels. Every change is recorded in the
audit log as `resource.labels.changed`, with the keys set and removed.

## Selectors

A selector is a comma-separated list of requirements. A resource matches when
all of them hold.

| Requirement | Matches when |
|---|---|
| `team=billing` or `team==billing` | the label is set to the value |
| `team!=billing` | the label is absent or set to another value |
| `team` | the label is set, to any value |
| `!team` | the label is absent |
| `tier in (web,api)` | the label is set to one of the values |
| `tier notin (web,api)` | the label is absent or set to none of the values |

A selector is at most 4096 bytes. Quote it in the shell, because of the spaces
and parentheses:

```bash
gregale app list -l 'team=billing,tier in (web,api)'
gregale domain list -l team=billing          # the domains of matching apps
gregale postgres list -l team=billing
gregale cron list -l owner=finance           # every app's crons; add --app to narrow
gregale job list -l '!tier' --all
```

`-l` is short for `--selector`. Through the API, the same lists take
`?selector=`: `GET /v1/apps`, `GET /v1/domains`, `GET /v1/postgres/databases`,
`GET /v1/crons` and `GET /v1/jobs`. A paged list (jobs) pages the matches, so
`total` and `next_offset` count only matching jobs.

Domains and deployments have no labels of their own: a selector reaches a
domain through its app.

### Wait for a group of apps

```bash
gregale app wait -l team=billing --for Ready --timeout 5m
```

`app wait -l` reads the matching apps once, when it starts, and waits until the
condition holds on every one of them. An app labelled while the wait runs is
not added.

| Exit code | Meaning |
|---|---|
| 0 | the condition holds on every matching app |
| 1 | an app reached a terminal state without the condition |
| 3 | the timeout passed first |
| 4 | the selector matched no app |

## Labels in `gregale.yaml`

Declare an app's labels in its manifest and each deploy applies them:

```yaml
labels:
  team: billing
  tier: web
```

In `gregale.toml` the same labels are a `[labels]` table.

A deploy, from the CLI or from GitHub, sets the keys the manifest declares and
removes none. Removing a key from the manifest leaves the label on the app until
`gregale app label unset` removes it. If labels are not available to your
account yet, the deploy goes on and says the manifest's labels were not
applied.

## Labels from Kubernetes manifests

When you import Kubernetes manifests ([Kubernetes](kubernetes.md)), each
Deployment's and CronJob's `metadata.labels` become the labels of the app it
creates. Keys under `gregale.dev/` are dropped. An invalid key or value, or one
past the 32-label limit, is reported as a scan warning and left out. Only a
newly created app is labelled: an app that already exists keeps the labels you
set. Pod template labels are used only to match Services to workloads, as
before.

## Dashboard and MCP

The dashboard shows labels without editing them. The apps list has a Labels
column and a selector box, and the app page and the managed PostgreSQL page show
app, cron and database labels.

The management MCP server's `gregale_list_apps` tool takes a `selector`, and its
app views carry `labels` ([MCP](mcp.md)).

## Errors

An invalid label or selector is a `400` naming the key or requirement at fault.

| Code | Cause |
|---|---|
| `invalid_label_key` | the key breaks the rules above |
| `invalid_label_value` | the value breaks the rules above |
| `reserved_label_key` | the key is under `gregale.dev/` |
| `invalid_label_change` | one request both sets and removes the same key |
| `label_limit_exceeded` | the change would leave more than 32 labels; the response carries the limit and the observed count |
| `invalid_label_selector` | the selector does not parse, or is longer than 4096 bytes |

While labels are not available to your account, a list request that carries
`?selector=` is a `403` with code `capability_not_released`, and the label
routes answer `404`. A list without a selector works as before, with no
`labels` field.

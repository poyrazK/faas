# ADR-20261118140000: Agents manage edge rules and previews, read objects and run workflow tests

Date: 2026-11-18
Status: Accepted
Amends: ADR-20261117232109 (extends `data:read` to object contents and
end-user identifiers in edge events)

## Context

After ADR-20261117232109 the next gaps in the management MCP server were the
edge, pull-request previews, object storage and tests. An agent that finds a
scanner hammering `/wp-admin` cannot see or add an edge rule; one reviewing a
pull request cannot list or clean up its preview; one debugging an upload
pipeline sees bucket names but no objects; one changing a saved workflow
cannot run its saved test cases. The APIs exist (`/v1/apps/{slug}/edge-rules`,
attack mode, `/v1/apps/{slug}/previews`, `/v1/preview/{slug}`, bucket object
listing and signed URLs, `/v1/apps/{slug}/workflows/stored/{name}/tests/run`).

The owner chose, on 2026-11-18: edge rules as reads plus plan/apply with
approval; preview reads plus plan/apply create and destroy; object reads only;
test runs and product analytics.

## Decision

### 1. Edge rules

Reads (toolset `diagnose`):
- `gregale_list_edge_rules(app)`: the app's rules, attack mode, and rule-set
  versions (without their rules) to roll back to.
- `gregale_get_edge_activity(app, window, rule_id, outcome, limit, cursor)`:
  hits per rule over 1h, 24h or 7d and recent rule events. Client IPs and
  user agents are end-user data: without `data:read` each is replaced with its
  size, like message bodies.

`gregale_plan_edge_rule_change` (toolset `operate`) reviews `create`, `update`,
`delete`, `rollback` to a version, `attack_mode_on` (bounded duration) or
`attack_mode_off`. The rule body is the native request, decoded strictly so an
unknown field fails at plan time. Rules managed by a switch (attack mode) and
rules of another app or of the account are refused. The plan records the
current rule-set `rules_sha256`; `gregale_apply_operation` refuses with
`plan_stale` if the rules changed since, so an agent never applies a change
reviewed against rules that no longer exist.

Approval is the existing route classification (ADR-091 amendment
2026-11-07): edge writes on a production app ask a person first, and every
`DELETE` asks. Edge changes on preview and development apps do not, as for
people's own CLI agents.

### 2. Pull-request previews

Reads (toolset `release`): `gregale_list_previews(app)` (previews whose
parent is the consented app) and `gregale_get_preview(app, preview)`: the
preview's deployment, its differences from production and its links. A
preview is reachable only through its consented parent; a preview of another
app is refused.

`gregale_plan_preview_change(app, op, pr_number, ttl_hours | preview)`
reviews `create` (create or reopen the pull request's preview, ready for a
deployment) or `destroy`. Destroy is a destructive route, so it asks a person
first.

Deploying into a preview stays with the existing deploy tools; a hosted
connection consented to named apps does not cover the preview's own slug.

### 3. Object reads

`gregale_list_objects(app, bucket, prefix, limit, cursor)` lists keys, sizes
and times in one of the app's buckets (toolset `diagnose`). Keys are metadata,
like bucket names.

`gregale_read_object(app, bucket, key, max_bytes)` needs `data:read`. It asks
apid for a 60-second signed GET, reads at most 256 KiB from the start with a
`Range` request, and returns UTF-8 as text or else base64, with `truncated`.
The signed URL is a bearer link: it never appears in a result or an error.
There are no object writes or deletes.

### 4. Saved workflow tests

`gregale_list_workflow_tests(app)` lists saved workflows with their test case
names and last report; `gregale_run_workflow_tests(app, workflow)` runs the
saved cases against the current draft, pinned to the version read just before
(toolset `release`). The cases are simulations with recorded mocks: they call
nothing outside Gregale, so the run is a direct tool, not a plan.

`gregale test` scenarios run on the developer's machine against a deployed
app and have no server-side runner; a hosted connection cannot run them.

### 5. Product analytics is deferred

Product analytics (ADR-20261117084505) is not on `main`. Its read tools follow
when it lands, under `data:read` for raw events.

## Consequences

- Connections granted `data:read` also see object contents and client IPs and
  user agents in edge events; the connect page says so.
- An agent can block a scanner or roll back a bad rule on a production app
  only after a person approves the native call.
- Destroying a preview from MCP always asks a person.

## Alternatives considered

- **Approval for every edge change, on any app.** The route classifier
  already scopes edge approval to production, as for deploys; a preview's
  rules block no customer.
- **Return the signed object URL.** Simpler, but the agent could pass a
  bearer link anywhere and read beyond the size bound.
- **Run `gregale test` in a task VM.** Possible later through
  `gregale_plan_run_command`; it needs the project source, which a hosted
  connection does not have.

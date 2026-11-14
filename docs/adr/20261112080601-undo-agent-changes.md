# ADR-20261112080601: Undo an agent's changes

Date: 2026-11-12
Status: Accepted
Related: ADR-20261107220000 (agent activity log), ADR-20261104140058 (agent
approvals), ADR-20261111114059 (agent budgets), ADR-022 (app soft delete and
restore), ADR-20261029174950 (managed PostgreSQL delete grace)

## Context

A person can see what an agent changed (the activity log) and make its risky
requests wait for approval, but once a change is made there is no way back
short of finding every resource it touched and redoing each value by hand.
The activity log cannot help: it records method, path and status, never the
value a change replaced. Agents make many small changes quickly, so "put back
what it did this afternoon" is the question people ask after a session that
went wrong.

## Decision

### 1. Undo records, captured when the agent writes

For every write an agent key makes (the requests the activity log records),
apid stores an **undo record** next to the activity row: the request that
reverses the write (the *inverse*) and a *check* that tells later whether
someone changed the resource again since. Capture is per route, in a table
in `pkg/apid/agentundo` keyed by the route pattern, and happens in apid's
agent middleware (`authmw.Middleware.UndoCapture`):

- **before the handler**, the route's capturer reads the resource as it is
  (the *before* state);
- **after a 2xx answer**, it reads the resource as the write left it (the
  *after* state, kept in the check), or the id of what the request created
  from the answer, and stores the record.

Capturers read, and undo later writes, through **apid's own handlers** (a
dispatcher that runs the handler of a route with the caller's request
context), never through the store directly. So a read or an undo sees and
does exactly what an API request would, with the caller's access, and goes
through the same validation, side effects and ownership rules (apid writes
intent tables, schedd still owns instances).

A write whose route has no capturer still gets a record, marked
`not_undoable` with a reason, so an undo plan names everything the agent did,
not only what it can reverse. A request that fails (non-2xx) changes nothing
and gets no record. A capture failure never fails the agent's request: the
record is written `not_undoable` ("could not read what this change
replaced").

No secret is copied: a secret change is not undoable (below), and env vars
are restored by rolling the app's configuration back to an earlier revision
(ADR-1451), which apid already keeps; their values are compared in memory and
never stored in a record. Records live as long as activity rows
(`AgentActivityRetention`, 7 days), which is also the restore window of
deleted apps (ADR-022) and managed PostgreSQL databases (ADR-20261029174950).

### 2. What can be undone

| Kind | Agent change | Undo |
|---|---|---|
| Settings and capacity | `PATCH /v1/apps/{slug}`, scaling, prewarms, env vars, cron changes | Restore the before values; cancel a prewarm |
| Deploys and traffic | new deployments, promote, canary advance, traffic, rollback | Make the deployment that served before the agent's first change serve again, with its traffic split (the existing rollback path) |
| Things it created | apps, buckets, crons, domains, managed PostgreSQL databases | Delete them through their own delete routes, which keep their safety: an app's delete parks it (restorable for 7 days), a bucket that holds objects is refused, a database's delete can be undone for 7 days. One already gone counts as undone. (An earlier draft asked for a `--delete-data` flag; the routes' own safety made it unnecessary.) |
| Things it deleted | apps and PostgreSQL databases (restore routes, within their 7-day windows); crons and custom domains (created again from what they read as before the delete; a domain verifies again) | Restore or re-create. A deleted bucket, and anything past its restore window, is not undoable |

Settings and capacity in detail (slice 1):

- **App settings:** the inverse is a `PATCH` writing back the before values
  of the fields the agent set. Only fields that read back from
  `GET /v1/apps/{slug}` in the form `PATCH` takes are restored
  (`TestAgentUndoAppSettingsRoundTrip` checks each); a change to any other
  field (`public_auth`, which holds sealed credentials; service bindings;
  fields that read back empty when unset, such as `app_protocol`) is not
  undoable and the plan names the fields.
- **Scaling:** the inverse is a reviewed scaling request with the before
  values, expecting the scaling revision found at undo time.
- **Prewarms:** the inverse cancels the prewarm; one already over is done.
- **Env vars:** the inverse is a guarded configuration rollback to the
  revision before the agent's change. The check compares the variables of
  the newest revision with those the agent left. Env vars of a named
  environment are undone the same way (amendment 2026-11-12).
- **Secrets:** not undoable. Gregale keeps no copy of a replaced secret; the
  plan says to set it again.

Deploys and rollbacks in detail (slice 2): the capture records the
deployment that served before the agent's deploy or rollback and the one the
agent's request started (its answer). Undo then looks at the app as it is:

- the agent's new deployment is still being built: cancel it;
- it serves: roll back to the deployment that served before;
- it never served and is over (failed, cancelled): nothing to do;
- another deployment serves now: changed since, left unless forced (forced,
  roll back to the one before the agent);
- the agent's deployment shares traffic in a canary, or the agent's rollback
  is still starting: left, with the reason; undo again later. A rollback
  target is never cancelled.

An app that served nothing before the agent's first deploy has nothing to
return to; that deploy is not undoable (park or delete the app). Promotes
and traffic changes put back the earlier split; canary advances are not
undoable (amendment 2026-11-12).

Undo goes through apid's own handlers, as the person who undoes (§4), so
ownership is unchanged: apid writes intent tables, schedd still owns
instances, and a rollback is a normal rollback.

### 3. One undo: a window, or single entries

- **Window:** `gregale mcp connection undo <id> --since 2h` reverses every
  undoable record of that key in the window (default: the key's records from
  the last 24 hours, never past retention).
- **Single entries:** `--entry <activity-id>` (repeatable) reverses those
  records only.

Undo is **plan, then apply**. The plan lists, newest first, each record with
what undoing it will do, and the ones that cannot be undone and why; apply
takes the plan's digest so it does exactly what the person read. Records are
applied newest first, so an app's settings return to their state before the
agent's first change in the window.

**Conflicts.** Before reversing a record, undo compares the resource's state
with the record's *after* state. If someone (a person, another key) changed
it since, the record is skipped as `changed_since` unless the person passes
`--force`. A record undone once is marked undone and is not applied twice.

### 4. Who can undo

- **People:** the member who connected the agent, or an owner or admin of the
  organization the key acts in (the people who decide its approvals,
  ADR-20261104140058 §5). Applying requires MFA, like other destructive
  account changes.
- **Agents, through approval:** an agent key may plan and request an undo of
  its *own* records. Applying an undo from an agent key always waits for a
  person's approval (category `undo`, not opt-out, like `budget`); the
  approval is bound to the plan digest, so the agent cannot widen it after
  approval. An agent cannot undo another key's records. The gate asks for
  `undo` whatever categories the key or its organization chose
  (`agentapproval.Asks`), and the category is not listed among the ones a
  connection chooses. The approved request applies the undo as the agent:
  its inverses are the reverse of writes it was allowed to make. An agent's
  undo requests themselves get no undo records.

### 5. Interfaces

- `POST /v1/agent-connections/{id}/undo/plan`, `POST /v1/agent-connections/{id}/undo`
  (org variants under `/v1/orgs/{slug}/agent-connections/{id}/…`), and
  `…/self/undo/plan` + `…/self/undo` for the agent's own key.
- `gregale mcp connection undo <id> [--since D | --entry ID…] [--force] [--org SLUG] [--plan] [--yes]`:
  prints the plan; applies after confirmation or with `--yes`.
- MCP tools `gregale_plan_undo` (read-only) and `gregale_apply_undo` (a write
  tool, listed where writes are allowed; it returns the approval to wait on
  with `gregale_wait_for_approval`), in the `operate` toolset.
- The plan lists each change with its activity entry id, so a person can
  undo single entries.

### 6. Delivery

Slices, each dark until its captures are complete for the kind:

1. Records table, capture framework, settings and capacity, plan/apply API
   and CLI for people (window and entries).
2. Deploys and traffic.
3. Things it created.
4. Things it deleted.
5. Agent-requested undo through approval (migration
   `20261112080601752_agent_approval_undo_category`, contract: the category
   CHECK gains `undo`), MCP tools, journey.

### Amendment 2026-11-12: traffic and named environments

- **Promotes and traffic changes** (`PATCH /v1/deployments/{id}/traffic`,
  `POST /v1/deployments/{id}/promote` and its application-ack twin) record
  the live split of the deployment's app before and after the change. Undo
  puts the earlier split back exactly through the traffic route, largest
  share first, and verifies it; with two live deployments one call does it.
  It is changed since when the live split is no longer the one the agent
  left (force puts the earlier split back anyway). Undoing a deploy and a
  traffic change of one app share one resource, so they go newest first.
- **Canary advances** stay not undoable: a canary only steps forward and no
  route steps it back; the plan points at aborting the rollout.
- **Env vars of a named environment** are undone like the app's own: the
  capture reads the environment's id and the guarded config rollback names
  it (`expected_environment_id`).
- What the capture cannot read through the routes undo dispatches to (a
  deployment's app, an environment's id) comes from a small lookup apid
  wires with the same account checks as those routes.
- Deleted buckets remain not undoable (owner's decision 2026-11-12: no
  soft-delete window this round).

## Consequences

- Every agent write does one or two extra reads of the resource it touches,
  and one more row. Agent writes are rate limited per key, so this stays
  small; reads are of rows the handler loads anyway.
- A route added later without a capturer shows up in plans as
  `not_undoable` ("Gregale cannot undo this kind of change yet"), never as
  silently missing. `TestAgentUndoRoutesAreServed` checks every route undo
  captures or runs through is still served, so a rename cannot quietly turn
  captures off.
- The capture runs before and after the agent's write, on the request path:
  a few handler reads per agent write.
- Data an agent destroyed outside a restore window (bucket objects,
  databases past grace) cannot be brought back; undo says so instead of
  pretending. Approvals (ADR-20261104140058) remain the protection for those.
- The migration is expand-only (a new table, and a new approval category
  added with the NOT VALID → VALIDATE pattern); release N-1 apid writes no
  records, so a window spanning the upgrade shows those writes as
  `not_undoable`.

# Environment branches

An environment branch is a short-lived copy of a project environment. Making
one takes a moment: Gregale copies the environment's configuration and
reuses each workload's existing build, so nothing is rebuilt and nothing
starts until a workload is called. Use a branch to try a change, run a
migration or a destructive test, or show a reviewer a working copy, then
delete it or let it expire.

Environment branches are not yet available to every account. While they are
off, `gregale environment branch` is hidden and the API answers
`409 environment_branch_unavailable`.

## Create a branch

```sh
gregale environment branch shop pr-42 --from production --ttl 24h
```

```
pr-42
  branched from: production at 2026-11-12T09:00:00Z
  expires: 2026-11-13T09:00:00Z
  egress: live
  data: ready
  web: deployment 3f0c… from 9a1d… (starts on first request)
    https://…
```

- `--from` names the parent environment (default `production`). A branch of
  a branch is allowed, two levels deep.
- `--ttl` is how long the branch lives: from 1 hour to 30 days, 7 days by
  default. Gregale deletes it at its expiry.
- Each workload's line shows the URL it answers on in the branch. Its first
  request starts it from the parent's build.

List an environment's branches with
`gregale environment branch shop --list --from production`, and delete one
early with `gregale environment delete shop pr-42 --yes`. An environment
that has branches cannot be deleted until they are gone
(`409 environment_has_branches`).

## What a branch copies

| Layer | In the branch |
| --- | --- |
| Configuration | The parent's environment variables, secrets and workload settings at the moment of branching. Later changes to the parent do not reach the branch. |
| Workloads | One deployment per live parent workload, from the parent's build. |
| Managed PostgreSQL | A copy of each database the parent binds, as of the moment of branching, with new credentials. Writes stay in the copy; the parent is never touched. |
| Object storage | An overlay bucket per bucket the parent binds. Reads of objects the branch has not written fall through to the parent; writes and deletes stay in the branch. |
| Private networks | Off by default. `--private-networks join` puts the branch's workloads on the parent's private networks with addresses of their own. |

The parent's database credentials never reach a branch: each copy is bound
with its own. A branch never uses the parent's static egress IP addresses.

### Data first

A branch whose parent binds managed PostgreSQL or object storage shows
`data: pending` until its copies exist, usually within a minute. Its
workloads start serving once they do. If a copy cannot be made, the branch
shows `data: failed` and the reason.

### Object storage

Overlay reads show the parent's current objects, not the objects as they
were when you branched: a parent write made after branching shows through
until the branch writes or deletes that key. Copying an object that exists
only in the parent (server-side copy) returns 404 in the branch. The
object-storage commands list only the objects the branch wrote itself.

## Outbound traffic

`--egress` sets what a branch's workloads may reach:

- `live` (default): the internet, like any environment.
- `contained`: only the branch's own services and its own data.
- `simulated`: contained, and the branch can seed rehearsals.

Only a `live` branch can join private networks.

## Plans and limits

| Plan | Branches at once |
| --- | ---: |
| Free | 1 |
| Hobby | 2 |
| Pro | 5 |
| Scale | 20 |

- A database copy needs point-in-time restore on the parent's database. A
  branch that lives 7 days or less puts its copies in the plan's
  [PostgreSQL branch allowance](postgres.md#branches) (Hobby 2, Pro 5,
  Scale 20 at once), not its database limit. A longer branch's copies count
  toward the database limit. Free includes no managed PostgreSQL, so a Free
  project with a database cannot branch it.
- Workloads bill like any instance while they run. A branch's database
  copies and overlay bytes are metered like your other databases and
  buckets; data the branch only reads from its parent is not billed twice.

## Company projects

Members of a company branch its projects by role:

- Viewers list branches.
- Developers, Admins and Owners create branches and delete them.
- Branching a protected environment other than production also needs an
  Admin or Owner, because the branch receives that environment's secrets.
- Deleting an environment that is not a branch still needs an Admin or
  Owner.

Add `--org <company>` to address a company's project.

## Errors

| Code | Meaning |
| --- | --- |
| `environment_branch_unavailable` | Branches are not on for this platform yet. |
| `plan_limit_apps` | The plan's branch limit is reached; the problem carries the limit and the count. Delete a branch or wait for one to expire. |
| `environment_branch_depth_exceeded` | A branch of a branch may go two levels deep. |
| `environment_branch_contained_objects_unsupported` | A contained branch of an environment with object storage needs the object relay, which is off; create the branch with live egress. |
| `environment_has_branches` | Delete the environment's branches first. |
| `org_role_forbidden` | Your role in the company may not make this branch. |

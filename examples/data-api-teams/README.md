# Team orders dashboard

The canonical starter is embedded in Gregale. With Node.js 22+, npm, Docker and
an approved matching CLI/SDK bundle:

```sh
gregale data-api onboard --template data-api-teams --path team-orders --bundle /path/to/data-api-bundle.json
cd team-orders
gregale data-api dev --port 0 --check
```

In a second terminal inside the new project:

```sh
npm run build:browser --prefix client
npm run serve --prefix client
```

Open `http://127.0.0.1:3030` and follow the scaffolded README. It includes
owner/admin/member access, database-backed membership, filtered UUID pagination,
version-checked idempotent saves and explicit same-user recovery. Owners can
manage registered users, transfer ownership with last-owner protection and
browse audit history. Owners can also share expiring member/admin invitation links;
signed-in recipients join under their verified subject without manual registry
provisioning. Invitation lists, revocation and audit are owner-only. CSV mapping and preview,
duplicate-safe resumable imports, import history and filtered export are included.
Reviewed submit/fulfil actions freeze submitted totals and record versioned history;
ordinary saves only create drafts or edit draft totals. Transitions also enqueue
immutable webhook snapshots; a separate signed worker delivers them with leased
retries, and owners inspect attempts and replay exhausted failures. Tenant
pause/resume, queue health, reviewed batch replay and operation history are
included. Active leases can finish while new claims are paused. The example
collection verifies two-team isolation, invitation replay and escalation denial.

[Starter source](../../cmd/gregale/templates/data-api-teams/README.md) is the
single implementation. Test identities and seed memberships are local fixtures;
bootstrap actual team owners with owner-controlled production migrations and
use verified invitation acceptance for subsequent members.

The embedded starter also includes named webhook subscriptions with event filters,
independent delivery queues and pause controls, pinned configuration versions, and
owner-only metadata management. Destination URLs and signing keys remain in private
worker configuration. See the generated README for worker setup and rotation rules.

Owner-approved signed sample tests and per-delivery attempt diagnostics help verify
private webhook bindings. The dashboard also paginates subscription change history.
See the generated README for test receiver handling and pause/retry semantics.

Personal saved views retain order filters and sorting per user/team. Owners and
admins can preview and atomically fulfil up to 20 reviewed orders, with version
checks, immutable batch history and lost-response recovery. Read-only members can
save their own views and inspect batch history. See the generated README for details.

Shared team views let owners/admins publish reusable workflows. Members can apply
published filters or make independent personal copies. Revision checks, immutable
publication history and exact receipt recovery protect edits and copies; copied
preferences remain private from other team members.

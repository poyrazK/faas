# FaasAppOwnerUnauthorizedWrite

ADR-20261110210000 §4. An app's owner node (`apps.node_id`) decides which
node runs the app, and so, once secrets open only on the control plane,
which node may receive the app's secrets. Only the control plane may choose
it. The `apps` trigger `app_owner_authority_check` checks each owner change
against the login that made it: members of `faas_owner_authority` (the
control plane's `faas_schedd`, and the schema owner `faas`) are authorized,
and every compute login (`faas_compute`, `faas_node_schedd`, ...) is not.

The alert fires when the last 15 minutes hold an unauthorized owner change
recorded in `app_owner_writes` (mode `observe`).

## Rollout: turning owner authority on

Per environment, in order:

1. **Deploy** a release with migration `20261110210850013_app_owner_authority`
   and converge the control plane, so `runtime-roles.sql` creates
   `faas_owner_authority`, grants it to `faas_schedd` and `faas`, and revokes
   compute's writes to `app_owner_authority` and `app_owner_writes`.
   Check: `select pg_has_role('faas_schedd','faas_owner_authority','MEMBER')`
   is true and `pg_has_role('faas_compute','faas_owner_authority','MEMBER')`
   is false. The control plane's schedd must log in as `faas_schedd`
   (runtime roles enabled, ADR-20261023104734); one that still logs in as
   `faas` is also authorized.
2. **Observe.** As the owner on the control plane:
   `update app_owner_authority set mode = 'observe', updated_at = now();`
   This alert now fires for every owner change a node makes. That is
   expected until step 3: node-local schedds still claim and recover.
3. **Move the decisions.** Set the Ansible variable
   `faas_app_owner_authority: control-plane` for both the control plane
   (`CP_ANSIBLE_VARS_B64`) and compute (`COMPUTE_ANSIBLE_VARS_B64`) and roll
   out. Compute schedds stop claiming, recovering and pressure-rebalancing;
   the control plane's schedd claims new apps and recovers orphaned ones
   (`sched: ownership recovery: transferred app` in its journal). Pressure
   rebalancing is paused in this mode.
4. **Watch a week.** `faas_app_owner_unauthorized_writes` stays 0 and the
   alert stays quiet. Any row in `app_owner_writes where not authorized` is
   a path still writing owners from a node: fix it before step 5.
5. **Enforce.** `update app_owner_authority set mode = 'enforce', updated_at = now();`
   Postgres now refuses an unauthorized owner change with SQLSTATE 42501
   and logs `faas_app_owner_refused app=... to=... login=...`. Refused
   changes leave no row (they roll back), so watch the Postgres log.

Back out by setting the mode to `observe` or `any`; the variable can stay.

## When it fires

1. `select written_at, app_id, from_node_id, to_node_id, login_role, mode
     from app_owner_writes where not authorized order by id desc limit 50;`
2. **During rollout (before step 3 is everywhere):** the login is a compute
   role and the change is a claim or recovery. Expected; finish step 3.
3. **After step 3:** no node should write an owner. Treat it as a possible
   compromise of the node behind `to_node_id` or `login_role`:
   - Follow `docs/runbooks/compute-node-compromise.md` for that node.
   - Set the mode to `enforce` if it is not already.
   - Move the affected apps back: clear the owner
     (`update apps set node_id = null where id = ...` as the owner) and the
     control plane's claim re-places it.
   - Rotate the secrets of the affected apps once the node has run them.
4. A login that should be authorized but is not (for example the control
   plane's schedd logging in as a role outside `faas_owner_authority`):
   add it to the membership list in
   `deploy/ansible/roles/postgres/files/runtime-roles.sql`, never a compute
   role.

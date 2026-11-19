# Postgres refused a daemon for missing privileges

`FaasDBPermissionDenied` fires when Postgres refused at least one of a
daemon's statements with SQLSTATE 42501 (insufficient privilege) in the last
10 minutes. The counter behind it, `<daemon>_db_permission_denied_total`, is
kept by the pool tracer in `pkg/pgpoolbounds` for every query, batch
statement, COPY and prepare on the daemon's pool.

Every daemon logs in as its own role (ADR-20261023104734), so the alert names
the role whose grants are short. The statement that failed is failing for
every caller until the grant is fixed: treat it as an outage of that path.

## Why it fires

1. **A grant set was narrowed too far.** Step 3 of the ADR replaces a daemon's
   `faas_runtime` membership with the tables and verbs it was seen using
   (`gregalectl audit db-grants`, plus the tables its code names). A rare path
   the evidence missed fails here.
2. **New code touches a table the role was never granted.** A release added a
   query, or a migration added a table a narrowed role now needs.
3. **A table was revoked on purpose** (for example the append-only
   `audit_log`, or tables reserved for signd, secretd or neond) and code in
   another daemon still writes it. That is a bug in the code, not the grant.

## What to do

1. Find the statement and role. Postgres logs every refusal:

   ```sh
   sudo journalctl -u postgresql --since '15 min ago' | grep -i 'permission denied'
   ```

   The line names the role (`faas_<daemon>`) and the object.
2. If the daemon's grants were narrowed in the last release, roll back first:
   restore the role's `faas_runtime` membership
   (`sudo -u postgres psql -d faas -c 'GRANT faas_runtime TO faas_<daemon>'`),
   then undo the narrowing in Ansible so the next converge keeps it.
   Investigate afterwards.
3. Otherwise grant the missing table and verb to that daemon's role, in the
   role's grant set in Ansible, not by hand, and converge.
4. If the table is deliberately withheld (case 3), fix the code path instead;
   do not widen the grant.

## Confirm recovery

The alert clears once `<daemon>_db_permission_denied_total` stops increasing
for 10 minutes; re-run the refused path to confirm it succeeds.

The counter must stay at zero for a week of normal traffic before a daemon's
grants are narrowed further.

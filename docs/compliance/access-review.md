# Quarterly access review

**Status: procedure draft; first signed review outstanding.** This procedure
covers Gregale customer IAM records stored in the control-plane database and
the operator's privileged access to production infrastructure. It does not
replace incident response, customer-specific access reviews, or vendor-risk
assessments.

## Cadence and ownership

Run the review once per calendar quarter, within the first ten business days
after quarter end. The Security owner prepares the inventory and tracks
findings. A second person should review and sign it where staffing permits;
while Gregale has one operator, record that the review was self-reviewed and
do not claim independent approval.

## Prepare the evidence

1. Use an approved read-only database credential and the production read
   replica where available. Run the repository's current script from the
   repository root:

   ```sh
   psql -X -v ON_ERROR_STOP=1 "$DATABASE_URL" -f docs/compliance/access-review.sql
   ```

   The script uses one repeatable, read-only transaction and UTC timestamps.
   It inventories active organization memberships, active and rotation-grace
   API keys, active dashboard sessions, pending invitations, and access-related
   audit events from the previous completed UTC quarter.

2. Save the command output in Gregale's restricted, encrypted evidence store.
   Record the review period, database snapshot time, repository commit, and
   SHA-256 digest of the saved output in
   [`access-review-record-template.md`](access-review-record-template.md).
   Do not commit production query output to this public repository: it contains
   customer email addresses and access metadata.

3. Supplement the database report with the current operator access inventory:
   production host and sudo accounts, SSH access, overlay-network membership,
   hosting-provider console users, DNS/CDN administration, repository
   organization administrators, production secret and backup access, and
   service credentials. Record the source system and snapshot date for each.
   The SQL report does not cover these external systems.

## Review and remediate

- Confirm every active organization membership has a current business need and
  the least-privilege role. Investigate memberships attached to non-active
  accounts and reconcile role changes, additions, removals, and ownership
  transfers with approved work or requests.
- For each active or grace-period API key, confirm its owner, purpose, scopes,
  rotation lineage, expiry, and recent use. Investigate keys that are over 365
  days old, unused or idle for more than 90 days, past their expiry timestamp,
  or whose creator is no longer an active member of the organization. Never
  request or record plaintext key material.
- Ask account owners to identify unfamiliar active sessions through the
  authenticated session-management surface. Revoke sessions the owner does not
  recognize or no longer needs.
- Confirm each pending invitation is expected and unexpired. Revoke
  unneeded invitations and investigate expired invitations that remain
  outstanding.
- Reconcile access-related audit events for the quarter with the approved
  access changes. Investigate missing, unexplained, or unauthorized changes.
- For operator infrastructure access, confirm each account and credential is
  assigned to a current operator, has the required privilege only, and has a
  documented recovery or revocation path. Remove access that is no longer
  needed.

Record every finding with an owner, due date, and ticket or other tracking
reference. Revoke confirmed unauthorized access promptly. Re-run the relevant
inventory after remediation and attach the result to the restricted evidence
record.

## Complete the review

Complete [`access-review-record-template.md`](access-review-record-template.md)
with the review period, reviewer, data sources, findings, remediation status,
and sign-off. A quarter is complete only when the record is signed and stored
with its query output and supporting system exports in the restricted evidence
store. The template and SQL script alone are not evidence that a review has
occurred.

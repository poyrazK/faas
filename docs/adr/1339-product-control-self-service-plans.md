# ADR-1339 · Product Control self-service plans

- **Status:** accepted for internal implementation
- **Date:** 2026-10-16
- **Decision:** Let verified customer workspace members compare and confirm
  owner-offered plan transitions. Require a separate, explicit `plans:manage`
  permission, retain an immutable receipt, and preserve consumed starts and
  accepted-run prices. Keep the default-off Product Control flag.

## Offers and authority

`self_service.transitions` in the latest immutable catalog maps current plan
names to offered target plan names in that catalog. Omitted or empty policy
offers no changes. Sources may be retired plan names; targets must exist in the
latest catalog. A same-name transition can migrate pinned older terms, but the
exact current plan and revision is never offered as a change. The map allows
at most 32 sources and 32 distinct targets per source. It grants no permission.

This extends ADR-1338 with **Manage plan** (`plans:manage`). The original defaults
remain: owner/admin receive work and billing permissions, member receives View
work. No role receives Manage plan by default. An owner explicitly grants it
in the existing role policy. It implies neither billing nor work access; a plan
manager receives safe commercial comparisons without work, results, bindings,
the full role map, or the full transition map.

The customer scope comes from the live Application Auth session and selected
verified organization. Confirmation rechecks the active owning account, product,
tenant, current organization link, current Auth app slug and UUID, and the
latest role grant inside the assignment transaction. Caller-supplied roles,
tenant IDs and Auth identities cannot select authority. Platform service
credentials have no self-service plan endpoint.

## Review and confirmation

Options compare feature inclusion, integer per-success prices, monthly start
caps, and this month's consumed and remaining starts. A preview is read-only.
Its hash binds the customer assignment, original and proposed commercial terms,
latest catalog head, UTC month, relevant start counts, selected change UUID,
and verified account/product/tenant/realm/organization identities.

Confirmation recomputes that review under the existing account → shared app →
tenant locking order. A different head, assignment, month, relevant start count
or authorization requires a new review. The assignment and its receipt commit
atomically. Owner publications and assignments and new Operation admissions
serialize with that transaction. Accepted work retains its original receipt and
price; a plan change never cancels work or resets the monthly starts across
catalog revisions. A downgrade below consumed starts has zero remaining starts.

`product_control_plan_changes` retains scoped immutable commercial previews,
the confirming role, effective time, and request fingerprint. Exact confirmed
retries recover the receipt before evaluating later offers or assignments and
cannot reapply an old plan. Changed arguments under the same UUID conflict.
Current role/identity authority is still required to recover a receipt. A later
customer suspension permits reading/recovering a recorded change while denying
new changes; tenant/account suspension denies the customer boundary. Receipts
cascade with their owning account, app or tenant. History pages use bounded,
scope-bound time/UUID cursors and contain only self-service changes.

The portal requires a visible review and explicit confirmation. Before posting,
it saves only the request UUID, target/revision and review hash in bounded local
storage under the account/app/tenant key. Web Locks prevent tabs replacing each
other's saved reference. Login, reload and status checks only read a saved
receipt; they do not submit a plan change. If not recorded, the customer reviews
and confirms again with that reference. A prior confirmation can win a race
with a fresh review: receipt lookup reports the actual recorded terms rather
than claiming that a newer selection applied. Logout, workspace changes and
detected permission changes fence pending responses and clear rendered terms.

## Owner configuration and rollout

The owner dashboard offers reviewed directed transitions and a close-all action.
Its forms require exact Origin and signed review/confirmation CSRF tokens. The
private API and advanced `gregale product plan-policy inspect/review/apply`
commands use the existing scoped owner boundary. Confirmation reconstructs the
desired catalog from its immutable reviewed base and performs the existing CAS
publication. An exact lost-response retry succeeds while its resulting head is
current; a retry after another publication cannot restore older choices. The
whole review is checked against that base in an exclusive mode-0600 CLI file.
Policy publication preserves access grants, bindings, prices, realm and customer
assignments. Full owner catalog publication may also set `self_service`.

There is no recurring subscription charge, proration, payment collection or
scheduled transition. Future starts use the selected catalog's per-success
prices immediately. The real qualified-host/Auth/customer pilot remains a
release gate, separate from portable Memory/PostgreSQL, HTTP and browser checks.
All routes, owner pages and portal assets remain internal and default off.

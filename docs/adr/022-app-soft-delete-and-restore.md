# ADR-022 · App soft delete and restore (H2)

- **Status:** accepted
- **Date:** 2026-09-07
- **Decision:** Treat `DELETE /v1/apps/{slug}` as a seven-day restorable
  tombstone. The app row records `deleted_at` and `delete_grace_until`; env,
  secrets, domains, crons, and the last live deployment remain available while
  the app is parked. `POST /v1/apps/{slug}/restore` reactivates the row only
  before the deadline. `pkg/grace` permanently removes expired app tombstones.
- **Why:** Customers need an undo window for destructive app operations while
  routing and resident-instance billing stop immediately through the existing
  `status = 'deleted'` lifecycle gate.

## Decisions

- **D1 grace policy** — seven days, defined by `api.AppDeleteGraceDays` and
  shared by apid, both stores, and the grace sweeper.
- **D2 routing and runtime** — deleted apps stay hidden from normal slug/list
  reads and gateway lookups; the existing `NotifyAppDelete` path evicts live
  instances. Restore emits `app_changed` so caches can reload the active row.
- **D3 hard delete** — the sweeper rechecks the deadline in a transaction,
  removes non-cascading child rows, then deletes the app. Snapshot/rootfs
  cleanup remains owned by the existing imaged GC and storage lifecycle.
- **D4 account deletion interaction** — account hard deletion still owns the
  account-wide FK walk from ADR-021; app purge is narrower and cannot delete an
  app with active object-storage buckets.

## Interfaces

- `DELETE /v1/apps/{slug}` → `204` and parks the app.
- `POST /v1/apps/{slug}/restore` → `200 AppResponse`, or `409
  app_not_restorable` after grace.
- `gregale apps restore <slug>` mirrors the restore endpoint.

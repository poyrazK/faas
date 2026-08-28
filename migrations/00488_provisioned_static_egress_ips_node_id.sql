-- filename: 00488_provisioned_static_egress_ips_node_id.sql
-- +goose Up
-- +goose StatementBegin

-- 00488_provisioned_static_egress_ips_node_id.sql — ADR-119 v2
-- (multi-node static outbound IP).
--
-- PR #997 (ADR-119 redesign) shipped per-app static egress IP,
-- Scale-only, operator-provisioned, single-node v1. The v1
-- gate table (migration 00337) keyed each (account_id,
-- customer_ip) row with no node binding — the implicit
-- assumption was "vmmd on the one host has the IP aliased on
-- br-tenants". Multi-node v2 lifts that assumption: each
-- operator-provisioned IP lives on EXACTLY ONE node, and the
-- schedd hard-pins the app's wake to that node. A customer
-- who pinned IP-A on node-A and woke the app on node-B would
-- see the egress source-spoofed at the switch (the v1 BYOIP
-- impossibility — see PR #997 review BLOCKING issue #3).
--
-- The fix is to add node_id to the gate table. vmmd's bundle
-- loader writes only the IPs owned by its own node; schedd's
-- wake path reads node_id via Store.StaticEgressIPNode to
-- stamp RequiredNodeID on the placement Request; the existing
-- owner-node check at pkg/sched/engine.go:1322-1326 then
-- refuses to admit the app on the wrong schedd.
--
-- Backfill: every existing row gets the synthetic
-- 'default-local' node id (seeded by migration 00024). The
-- single-node v1 deployment had exactly one compute_nodes row;
-- this preserves v1 behaviour for existing pins with no
-- operator re-action required. A pre-v2 multi-host deployment
-- that already split static egress IPs across nodes manually
-- would need an operator-driven backfill (manual UPDATE) to
-- re-assign per-node.
--
-- Schema decisions (per ADR-119 v2 plan):
--
--   * Nullable add, NOT NULL after backfill. Mirrors the
--     instances.node_id ADD-then-backfill-then-ENFORCE pattern
--     from migration 00024:67-115. Postgres ADD NOT NULL on a
--     populated table scans + locks; the UPDATE above has set
--     every row's node_id by the time we get here so the
--     constraint add is metadata-only on a clean re-apply.
--
--   * ON DELETE RESTRICT. Deleting a compute_nodes row that
--     owns a static egress IP would orphan the IP's owner
--     contract; RESTRICT forces the operator to clear every
--     pinned app's static_egress_ip first. (CASCADE would
--     silently revoke customer pins; SET NULL would silently
--     shift the IP to "no owner" — the schedd path would then
--     fall back to the legacy single-box semantics.)
--
--   * (node_id) index. vmmd's bundle loader reconciles its
--     bridge alias-IP set against Postgres on SIGHUP — the
--     per-node reverse lookup "which IPs are provisioned on
--     this node?" is index-covered. The (account_id,
--     customer_ip) PK already covers schedd's wake-path lookup
--     "what node owns this (account, ip)?", so no extra index
--     is required for that direction.

ALTER TABLE provisioned_static_egress_ips
    ADD COLUMN IF NOT EXISTS node_id UUID
        REFERENCES compute_nodes(id) ON DELETE RESTRICT;

-- Backfill every existing row to the synthetic default-local
-- node. Mirrors migration 00024:103-105: subselect-resolve
-- (not a literal) so the migration doesn't pin a UUID value.
-- The WHERE node_id IS NULL filter makes the UPDATE idempotent
-- on a re-apply. New rows after this migration runs always
-- carry node_id (the NOT NULL below + the vmmd bundle loader's
-- ReplaceProvisionedStaticEgressIPs(ctx, accountID, nodeID, ips)
-- signature).
UPDATE provisioned_static_egress_ips
   SET node_id = (SELECT id FROM compute_nodes WHERE name = 'default-local' LIMIT 1)
 WHERE node_id IS NULL;

-- NOW enforce NOT NULL. Same defence-in-depth pattern as the
-- family=4 CHECK on customer_ip (migration 00337:51-52): a v2
-- pin without a node is refused at the DB boundary so a buggy
-- vmmd can't land a no-owner row.
ALTER TABLE provisioned_static_egress_ips
    ALTER COLUMN node_id SET NOT NULL;

-- Per-node reverse-lookup index. The vmmd bundle loader uses
-- this on SIGHUP to reconcile its bridge alias-IP set against
-- the authoritative Postgres state. Without the index, the
-- loader scans the full table — fine at v1 scale (single
-- tenant list), O(rows) at v2 scale (every tenant's pins
-- across N nodes).
CREATE INDEX IF NOT EXISTS provisioned_static_egress_ips_node_id_idx
    ON provisioned_static_egress_ips (node_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Reverse-order teardown: index → NOT NULL relax → column.
-- Index first so a downgrade doesn't scan a now-larger table
-- for the reverse lookup. The column drop cascades via the FK
-- automatically (the FK was ON DELETE RESTRICT, not ON DELETE
-- CASCADE — drop column is its own DDL step).
DROP INDEX IF EXISTS provisioned_static_egress_ips_node_id_idx;
ALTER TABLE provisioned_static_egress_ips ALTER COLUMN node_id DROP NOT NULL;
ALTER TABLE provisioned_static_egress_ips DROP COLUMN IF EXISTS node_id;

-- +goose StatementEnd
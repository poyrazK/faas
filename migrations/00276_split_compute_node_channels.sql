-- filename: 00276_split_compute_node_channels.sql
-- +goose Up
-- +goose StatementBegin
--
-- 00276_split_compute_node_channels.sql — issue #911 follow-up
-- (Tier-2 gap #12, multi-host scale-out hardening).
--
-- Splits the single compute_node_changed pg_notify channel into two
-- per-table channels:
--
--   compute_nodes_changed     — fired by compute_nodes row inserts
--                                and updates. Payload: {node_id, active}.
--   compute_node_keys_changed — fired by compute_node_keys row
--                                inserts, deletes, and updates.
--                                Payload: {key_id, fingerprint} where
--                                fingerprint is sha256-hex of the new
--                                public_key_pem (empty on DELETE —
--                                consumer treats empty as revocation).
--
-- Why split (inventory reference: memory/multi-host-scaleout-gap-2026-08-15
-- Tier-2 gap #12):
--
--   The single compute_node_changed channel carried two
--   semantically distinct event streams:
--
--     1. compute_nodes row writes → JSON {node_id, active} payload
--     2. compute_node_keys row writes → bare TG_TABLE_NAME text
--        payload (per the legacy doc-comments at
--        cmd/schedd/node_keys_loader.go:21-23 and
--        cmd/vmmd/main.go:113-115)
--
--   Three consumers (schedd × 4 sites, vmmd × 1, gatewayd-internal × 1)
--   all re-parsed the payload to route to the right handler. The
--   gatewayd-internal nodecache.go:353 "bad compute_node_changed
--   payload" warn-and-drop log fires on every compute_node_keys
--   event because the JSON unmarshal fails. Same code path runs in
--   schedd's subscribeRebalancer + subscribeLiveMigrator.
--
--   The split lets consumer LISTENs route at the wire level — no JSON
--   parse-and-filter inline. The keys-fingerprint payload replaces
--   the bare TG_TABLE_NAME with an actionable identifier (sha256-hex
--   of the new PEM) that schedd's node-key registry can use to
--   detect rotation events without re-fetching the row.
--
-- Trigger posture change (LOAD-BEARING — read carefully):
--
--   The existing compute_node_keys_changed_trg is FOR EACH STATEMENT
--   (schema.sql:8377). STATEMENT-level triggers have NEW = NULL, so
--   the legacy compute_node_keys_notify() function only emitted
--   TG_TABLE_NAME (no row data). The new function must SHA-256 the
--   new public_key_pem, which requires NEW. We change the trigger
--   to FOR EACH ROW. The cardinality cost is one notify per row
--   write (vs. per statement) — bounded, small (one row per
--   registration or rotation event).
--
--   Pre-existing per-statement behaviour WAS load-bearing for some
--   operators who relied on a single notify per transaction; that
--   contract is lost. Documented here because the consumer side
--   change is the dominant reason this gap closes, but the trigger
--   posture is a quiet semantic shift and an operator upgrading
--   with a custom notify catch (e.g. pg_log) should know.
--
-- Atomic migration (NO deprecation alias on the old channel):
--
--   The OLD compute_node_changed channel is dropped here. Any
--   out-of-tree observer (debug psql LISTEN, raw pg_listening_channels
--   probe, e2e fixture) breaks by design. The pre-push
--   `git grep compute_node_changed` audit gate is the only
--   back-compat courtesy — anything left in-tree after the audit
--   has been migrated in the same commit.
--
-- Replay-safety (per migrations/replay_safety_test.go contract):
--   - DROP TRIGGER / DROP FUNCTION are IF EXISTS.
--   - Function bodies are CREATE OR REPLACE so a re-apply on a
--     drifted box (functions present, triggers missing) succeeds.
--   - Trigger names are reused (compute_nodes_changed_trg +
--     compute_node_keys_changed_trg) — Postgres trigger names are
--     schema-local, dropping + recreating under the same name avoids
--     SQLSTATE 42710.

-- Drop the old triggers first. The DROP IF EXISTS guard makes the
-- migration re-apply safe even on a box where the trigger was
-- manually pruned.
DROP TRIGGER IF EXISTS compute_nodes_changed_trg ON public.compute_nodes;
DROP TRIGGER IF EXISTS compute_node_keys_changed_trg ON public.compute_node_keys;

-- Drop the old functions. CASCADE is not strictly needed (the
-- triggers are gone) but guards against any future ad-hoc binding
-- (e.g. a manual CREATE TRIGGER that didn't get the new name).
-- The IF EXISTS form avoids SQLSTATE 42883 on a drifted box.
DROP FUNCTION IF EXISTS public.compute_node_notify() CASCADE;
DROP FUNCTION IF EXISTS public.compute_node_keys_notify() CASCADE;

-- Recreate compute_node_notify under the new name + new channel.
-- Payload shape unchanged: {node_id, active}. Channel renamed to
-- compute_nodes_changed (plural — distinguishes from the keys
-- channel).
CREATE OR REPLACE FUNCTION public.compute_nodes_notify() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
declare
    payload jsonb;
begin
    payload := jsonb_build_object(
        'node_id', new.id::text,
        'active', new.active
    );
    perform pg_notify('compute_nodes_changed', payload::text);
    return new;
end;
$$;

-- Recreate compute_node_keys_notify with the new typed payload.
-- Fingerprint is sha256-hex of the new public_key_pem (when NEW is
-- present, i.e. INSERT + UPDATE). On DELETE, NEW is NULL — emit
-- empty fingerprint. The consumer (schedd's node-key registry)
-- treats {key_id: '<id>', fingerprint: ''} as a revocation event.
--
-- encode(sha256(bytea), 'hex') returns lower-case hex; the
-- schedd-side comparison uses the same lower-case form.
CREATE OR REPLACE FUNCTION public.compute_node_keys_notify() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
declare
    payload jsonb;
    fp text;
begin
    if tg_op = 'DELETE' then
        fp := '';
    else
        fp := encode(sha256(new.public_key_pem::bytea), 'hex');
    end if;
    payload := jsonb_build_object(
        'key_id', coalesce(new.key_id, old.key_id),
        'fingerprint', fp
    );
    perform pg_notify('compute_node_keys_changed', payload::text);
    return coalesce(new, old);
end;
$$;

-- Recreate the triggers. compute_nodes_changed_trg stays FOR EACH
-- ROW (unchanged). compute_node_keys_changed_trg changes from
-- STATEMENT to ROW because the new function reads NEW.public_key_pem.
-- CREATE OR REPLACE TRIGGER is not a Postgres construct — use
-- DROP + CREATE explicitly (the DROP IF EXISTS above already
-- removed the old one).
CREATE TRIGGER compute_nodes_changed_trg
    AFTER INSERT OR UPDATE ON public.compute_nodes
    FOR EACH ROW EXECUTE FUNCTION public.compute_nodes_notify();

CREATE TRIGGER compute_node_keys_changed_trg
    AFTER INSERT OR DELETE OR UPDATE ON public.compute_node_keys
    FOR EACH ROW EXECUTE FUNCTION public.compute_node_keys_notify();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
--
-- Reverse: drop the new triggers, drop the new functions, recreate
-- the OLD channel + functions + triggers so a downgrade restores
-- the pre-00276 wire shape. The fingerprint change is information-
-- losing (the old function ignored public_key_pem), so the
-- downgrade is the only safe path that preserves the cluster's
-- pre-PR reality.
DROP TRIGGER IF EXISTS compute_nodes_changed_trg ON public.compute_nodes;
DROP TRIGGER IF EXISTS compute_node_keys_changed_trg ON public.compute_node_keys;

DROP FUNCTION IF EXISTS public.compute_nodes_notify() CASCADE;
DROP FUNCTION IF EXISTS public.compute_node_keys_notify() CASCADE;

-- Restore the OLD compute_node_notify (typed {node_id, active}
-- payload, channel compute_node_changed).
CREATE OR REPLACE FUNCTION public.compute_node_notify() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
declare
    payload jsonb;
begin
    payload := jsonb_build_object(
        'node_id', new.id::text,
        'active', new.active
    );
    perform pg_notify('compute_node_changed', payload::text);
    return new;
end;
$$;

-- Restore the OLD compute_node_keys_notify (bare TG_TABLE_NAME).
-- This is the original STATEMENT-level trigger's body: the
-- statement-level trigger fires once per tx, not per row, so the
-- legacy notification is a single TG_TABLE_NAME string per
-- statement.
CREATE OR REPLACE FUNCTION public.compute_node_keys_notify() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
begin
    perform pg_notify('compute_node_changed', TG_TABLE_NAME);
    return null;
end;
$$;

CREATE TRIGGER compute_node_changed_trg
    AFTER INSERT OR UPDATE ON public.compute_nodes
    FOR EACH ROW EXECUTE FUNCTION public.compute_node_notify();

CREATE TRIGGER compute_node_keys_changed_trg
    AFTER INSERT OR DELETE OR UPDATE ON public.compute_node_keys
    FOR EACH STATEMENT EXECUTE FUNCTION public.compute_node_keys_notify();

-- +goose StatementEnd

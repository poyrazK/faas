-- filename: 20261113180000000_invocation_due_notify_transitions.sql
-- migration-compat: expand
-- ADR-20261113180000 (queue review round 5-8, R7-1/R5-10): invocation_due
-- fired on every update that set state='pending', including a pending row
-- re-deferred by a hold every minute and a retry whose back-off made it due
-- later. Each one woke every lease waiter and receive of the app, which then
-- ran a lease for nothing. A row now notifies when it becomes pending and is
-- already due; a row due later is found by the drain's and the trigger
-- loop's one-second tick and by the lease waiters' recheck. The payload also
-- names the queue, so a lease waiter wakes only for its own queue.

-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION invocation_due_notify() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
declare
    payload jsonb;
begin
    if tg_op = 'UPDATE' and old.state = 'pending' then
        return new;
    end if;
    if new.due_at > clock_timestamp() then
        return new;
    end if;
    payload := jsonb_build_object(
        'invocation_id', new.id::text,
        'app_id', new.app_id::text,
        'source', new.source,
        'queue_name', coalesce(new.queue_name, '')
    );
    perform pg_notify('invocation_due', payload::text);
    return new;
end;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION invocation_due_notify() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
declare
    payload jsonb;
begin
    payload := jsonb_build_object(
        'invocation_id', new.id::text,
        'app_id', new.app_id::text,
        'source', new.source
    );
    perform pg_notify('invocation_due', payload::text);
    return new;
end;
$$;
-- +goose StatementEnd

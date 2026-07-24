-- +goose Up
-- +goose StatementBegin

-- Two channels, mirroring the convention established by
-- migrations/00026_compute_node_notify.sql (the codebase splits notify
-- channels per consumer field shape rather than per topic):
--
--   invocation_due {"invocation_id":uuid, "app_id":uuid, "source":"..."}
--                  fired on INSERT and on UPDATE state='pending'. Schedd
--                  drain wakes immediately so an async invoke lands
--                  inside the customer's SLO without waiting on the 1s
--                  safety ticker.
--
--   invocation_done {"invocation_id":uuid, "app_id":uuid,
--                    "source":"...", "state":"completed|failed|cancelled"}
--                  dashboard live-update on terminal transitions.
--                  Currently no listener (the dashboard polls
--                  /v1/invocations); the channel is defined here so the
--                  follow-up SSE push lands in one PR.

create or replace function invocation_due_notify() returns trigger
language plpgsql as $$
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

create or replace function invocation_done_notify() returns trigger
language plpgsql as $$
declare
    payload jsonb;
begin
    if (tg_op = 'UPDATE' and old.state in ('pending','dispatching')
        and new.state in ('completed','failed','cancelled')) then
        payload := jsonb_build_object(
            'invocation_id', new.id::text,
            'app_id', new.app_id::text,
            'source', new.source,
            'state', new.state
        );
        perform pg_notify('invocation_done', payload::text);
    end if;
    return new;
end;
$$;

drop trigger if exists invocation_due_trg on invocations;
create trigger invocation_due_trg
    after insert or update of state on invocations
    for each row
    when (new.state = 'pending')
    execute function invocation_due_notify();

drop trigger if exists invocation_done_trg on invocations;
create trigger invocation_done_trg
    after update on invocations
    for each row execute function invocation_done_notify();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
drop trigger if exists invocation_done_trg on invocations;
drop trigger if exists invocation_due_trg on invocations;
drop function if exists invocation_done_notify();
drop function if exists invocation_due_notify();
-- +goose StatementEnd

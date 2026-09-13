-- +goose Up
-- +goose StatementBegin
-- Issue #2444: subscriptions must not accept events with no production
-- producer. Preserve the delivery ledger vocabulary for historical rows, but
-- narrow persisted subscription filters to the three events currently emitted
-- through pkg/webhook.Emit.
--
-- A filter containing only unsupported events cannot become an empty filter:
-- empty means "all events" and would silently broaden the subscription. Disable
-- that row while clearing its inert filter; the customer can explicitly update
-- and re-enable it after choosing a supported event.

with normalized as (
    select
        id,
        event_filter as old_filter,
        coalesce(array_agg(event order by ordinal)
            filter (where event in ('app.parked', 'app.woken', 'usage_statement.finalized')),
            '{}'::text[]) as supported_filter
    from app_webhooks
    left join lateral unnest(event_filter) with ordinality as selected(event, ordinal) on true
    group by id, event_filter
)
update app_webhooks as webhook
set
    event_filter = normalized.supported_filter,
    enabled = case
        when cardinality(normalized.old_filter) > 0
         and cardinality(normalized.supported_filter) = 0 then false
        else webhook.enabled
    end,
    updated_at = now()
from normalized
where webhook.id = normalized.id
  and webhook.event_filter is distinct from normalized.supported_filter;
-- +goose StatementEnd

-- +goose Down
-- Forward-only subscription repair: removed filter names had no producers,
-- and restoring them would recreate silently inert customer configuration.
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd

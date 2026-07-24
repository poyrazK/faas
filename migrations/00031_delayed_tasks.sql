-- +goose Up
-- +goose StatementBegin

-- Helper view used by the per-plan cap check in schedd's drain +
-- dashboard "next due" tooling. The actual cap is enforced at runtime
-- in pkg/sched/drain.go (drain.dispatchOne calls
-- CountPendingInvocations rather than trusting this view), because the
-- cap is plan-dependent and the platform re-evaluates it on every plan
-- change.
--
-- The view exists ONLY so the platform test surface can assert that
-- the per-(app, source) pending count is index-backed
-- (invocations_app_pending_idx). Not exposed via the customer API.

create or replace view invocations_pending_per_app as
  select app_id, source, count(*) as pending
    from invocations
   where state in ('pending','dispatching')
   group by app_id, source;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
drop view if exists invocations_pending_per_app;
-- +goose StatementEnd

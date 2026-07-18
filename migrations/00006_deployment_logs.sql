-- +goose Up
-- +goose StatementBegin

-- M7.5 slice 5: deployment build logs (spec §14 M7.5, ADR-011).
-- Records every stdout/stderr line emitted by the build container
-- (builderd) for each deployment. The dashboard's /apps/{slug} page
-- tails this table via SSE so customers can watch the build finish.
--
-- Schema notes:
--   * seq is a per-deployment bigserial — the dashboard SSElogs
--     endpoint pages by `(deployment_id, seq < $cursor) LIMIT $n`.
--   * stream is one of {stdout, stderr, system}; the dashboard tags
--     rows in the SSE feed so the customer can colour stderr red.
--   * line is the raw text without trailing newline (stripped at
--     insert by the writer).
--   * written_at is server-side now() — clients render relative
--     timestamps off it.

create table if not exists deployment_logs (
  deployment_id uuid         not null references deployments(id) on delete cascade,
  seq           bigserial,
  stream        text         not null default 'stdout',
  line          text         not null,
  written_at    timestamptz  not null default now(),
  primary key (deployment_id, seq)
);

create index if not exists deployment_logs_seq_idx
  on deployment_logs (deployment_id, seq desc);

-- +goose StatementEnd

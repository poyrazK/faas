-- +goose Up
alter table api_consumers
    add column scenario_creation_sha256 text check (scenario_creation_sha256 ~ '^[0-9a-f]{64}$'),
    add column scenario_creation_expires_at timestamptz,
    add constraint api_consumers_scenario_creation_check check (
        (scenario_creation_sha256 is null and scenario_creation_expires_at is null) or
        (scenario_creation_sha256 is not null and scenario_creation_expires_at is not null and isfinite(scenario_creation_expires_at))
    );

-- +goose Down
alter table api_consumers drop constraint api_consumers_scenario_creation_check,
    drop column scenario_creation_sha256, drop column scenario_creation_expires_at;

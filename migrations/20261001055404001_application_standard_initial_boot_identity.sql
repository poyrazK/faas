-- +goose Up
-- The native initial-boot capability consumes an instance identity once,
-- including failed boots. Retrying storage may recover its exact existing
-- token, but cannot mint another initial grant on that row. Future promotion
-- and migration protocols must introduce distinct attempt purposes/generations.
CREATE UNIQUE INDEX instance_application_standard_initial_boot_identity
 ON instance_application_standard_boots(instance_id);

-- +goose Down
-- Retain single-use authority on an already managed runtime.

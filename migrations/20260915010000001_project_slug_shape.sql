-- +goose Up
-- +goose StatementBegin
ALTER TABLE projects DROP CONSTRAINT IF EXISTS projects_slug_shape;
ALTER TABLE projects ADD CONSTRAINT projects_slug_shape CHECK (
    slug ~ '^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$'
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE projects DROP CONSTRAINT IF EXISTS projects_slug_shape;
ALTER TABLE projects ADD CONSTRAINT projects_slug_shape CHECK (
    slug ~ '^[a-z0-9][a-z0-9-]{0,62}$'
);
-- +goose StatementEnd

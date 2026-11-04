-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
CREATE TABLE sales_message_templates (
 id uuid PRIMARY KEY CHECK (id <> '00000000-0000-0000-0000-000000000000'),
 title text NOT NULL CHECK (octet_length(title) BETWEEN 1 AND 120 AND btrim(title)<>''),
 category text NOT NULL CHECK (category IN ('intro','demo','follow_up','stalled')),
 stage text NOT NULL DEFAULT '' CHECK (stage IN ('','new','contacted','qualified','demo','won','lost')),
 status text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','active','archived')),
 subject text NOT NULL DEFAULT '' CHECK (octet_length(subject)<=200),
 body text NOT NULL CHECK (octet_length(body) BETWEEN 1 AND 4000 AND btrim(body)<>''),
 version bigint NOT NULL DEFAULT 1 CHECK (version>0),
 updated_by uuid REFERENCES accounts(id) ON DELETE SET NULL,
 updated_at timestamptz NOT NULL DEFAULT now() CHECK (isfinite(updated_at))
);
CREATE INDEX sales_message_templates_page ON sales_message_templates(status,id);
-- +goose Down
DROP TABLE sales_message_templates;

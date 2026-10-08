-- Both composite FKs bind attachments to the authenticated owner's subject.
CREATE TABLE api.tags (
  id integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  subject text NOT NULL,
  name text NOT NULL,
  UNIQUE (subject, id)
);
ALTER TABLE api.tags ENABLE ROW LEVEL SECURITY;
CREATE POLICY own_tags ON api.tags
  USING (subject = current_setting('request.jwt.claims', true)::jsonb ->> 'sub')
  WITH CHECK (subject = current_setting('request.jwt.claims', true)::jsonb ->> 'sub');

CREATE TABLE api.note_tags (
  subject text NOT NULL,
  note_id integer NOT NULL,
  tag_id integer NOT NULL,
  PRIMARY KEY (subject, note_id, tag_id),
  CONSTRAINT note_tags_note_fkey FOREIGN KEY (subject, note_id)
    REFERENCES api.notes (subject, id) ON DELETE CASCADE,
  CONSTRAINT note_tags_tag_fkey FOREIGN KEY (subject, tag_id)
    REFERENCES api.tags (subject, id) ON DELETE CASCADE
);
ALTER TABLE api.note_tags ENABLE ROW LEVEL SECURITY;
CREATE POLICY own_note_tags ON api.note_tags
  USING (subject = current_setting('request.jwt.claims', true)::jsonb ->> 'sub')
  WITH CHECK (subject = current_setting('request.jwt.claims', true)::jsonb ->> 'sub');

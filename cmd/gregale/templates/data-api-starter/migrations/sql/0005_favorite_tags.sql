-- A second junction requires an explicit path when embedding notes and tags.
CREATE TABLE api.note_favorite_tags (
  subject text NOT NULL,
  note_id integer NOT NULL,
  tag_id integer NOT NULL,
  PRIMARY KEY (subject, note_id, tag_id),
  CONSTRAINT note_favorite_tags_note_fkey FOREIGN KEY (subject, note_id)
    REFERENCES api.notes (subject, id) ON DELETE CASCADE,
  CONSTRAINT note_favorite_tags_tag_fkey FOREIGN KEY (subject, tag_id)
    REFERENCES api.tags (subject, id) ON DELETE CASCADE
);
ALTER TABLE api.note_favorite_tags ENABLE ROW LEVEL SECURITY;
CREATE POLICY own_note_favorite_tags ON api.note_favorite_tags
  USING (subject = current_setting('request.jwt.claims', true)::jsonb ->> 'sub')
  WITH CHECK (subject = current_setting('request.jwt.claims', true)::jsonb ->> 'sub');

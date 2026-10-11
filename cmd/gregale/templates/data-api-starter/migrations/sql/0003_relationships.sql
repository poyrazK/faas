-- Keep the JWT subject in each FK so a user cannot attach rows to another
-- user's note, even though PostgreSQL FK checks bypass the parent's RLS.
ALTER TABLE api.notes ADD CONSTRAINT notes_subject_id_key UNIQUE (subject, id);

CREATE TABLE api.comments (
  id integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  subject text NOT NULL,
  note_id integer,
  body text NOT NULL,
  CONSTRAINT comments_note_fkey FOREIGN KEY (subject, note_id)
    REFERENCES api.notes (subject, id) ON DELETE CASCADE
);
ALTER TABLE api.comments ENABLE ROW LEVEL SECURITY;
CREATE POLICY own_comments ON api.comments
  USING (subject = current_setting('request.jwt.claims', true)::jsonb ->> 'sub')
  WITH CHECK (subject = current_setting('request.jwt.claims', true)::jsonb ->> 'sub');

CREATE TABLE api.note_details (
  subject text NOT NULL,
  note_id integer NOT NULL,
  summary text NOT NULL,
  PRIMARY KEY (subject, note_id),
  CONSTRAINT note_details_note_fkey FOREIGN KEY (subject, note_id)
    REFERENCES api.notes (subject, id) ON DELETE CASCADE
);
ALTER TABLE api.note_details ENABLE ROW LEVEL SECURITY;
CREATE POLICY own_note_details ON api.note_details
  USING (subject = current_setting('request.jwt.claims', true)::jsonb ->> 'sub')
  WITH CHECK (subject = current_setting('request.jwt.claims', true)::jsonb ->> 'sub');

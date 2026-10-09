-- RLS constrains subject; these prefixes isolate each owner's ordered scan.
CREATE INDEX notes_owner_cursor_idx ON api.notes (subject, created_at DESC, id DESC);
CREATE INDEX notes_owner_priority_cursor_idx ON api.notes (subject, priority, created_at DESC, id DESC);
CREATE INDEX comments_owner_note_idx ON api.comments (subject, note_id);
-- Junction PKs cover note -> tag; reverse traversal needs tag before note.
CREATE INDEX note_tags_owner_tag_idx ON api.note_tags (subject, tag_id, note_id);
CREATE INDEX note_favorite_tags_owner_tag_idx ON api.note_favorite_tags (subject, tag_id, note_id);

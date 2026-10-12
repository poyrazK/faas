ALTER TABLE api.documents ADD CONSTRAINT nonempty_document_body CHECK (length(body)>0);

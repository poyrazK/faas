-- OAuth 2.1 authorization server (ADR-1190). A token issued to a client
-- carries the audience of the resource (RFC 8707) the client was authorized
-- for. GoTrue drops the authorization when it exchanges the code, so the
-- custom access token hook (oauth.mjs) records the resource of each client
-- session then, and refreshed tokens keep it. There is no foreign key to
-- auth.sessions: the hook runs while GoTrue's transaction still holds the
-- new session row. Deleting a session forgets its resource.
CREATE TABLE gregale_auth.oauth_session_resources (
    session_id uuid PRIMARY KEY,
    client_id uuid NOT NULL,
    resource text CHECK (resource IS NULL OR length(resource) BETWEEN 1 AND 2048),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE FUNCTION gregale_auth.forget_oauth_session() RETURNS trigger
LANGUAGE plpgsql SET search_path = '' AS $$
BEGIN
    DELETE FROM gregale_auth.oauth_session_resources WHERE session_id = OLD.id;
    RETURN OLD;
END $$;

CREATE TRIGGER gregale_auth_forget_oauth_session AFTER DELETE ON auth.sessions
    FOR EACH ROW EXECUTE FUNCTION gregale_auth.forget_oauth_session();

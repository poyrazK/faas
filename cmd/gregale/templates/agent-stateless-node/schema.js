export const agentSchema = `
CREATE TABLE IF NOT EXISTS agent_messages (
  thread_id  text        NOT NULL CHECK (length(thread_id) BETWEEN 1 AND 64),
  message_id text        NOT NULL CHECK (length(message_id) BETWEEN 1 AND 160),
  seq        bigserial   NOT NULL,
  role       text        NOT NULL CHECK (role IN ('user', 'assistant', 'tool')),
  content    jsonb       NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (thread_id, message_id)
);
CREATE INDEX IF NOT EXISTS agent_messages_thread_seq ON agent_messages (thread_id, seq);
`;

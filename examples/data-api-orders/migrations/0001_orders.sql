CREATE SCHEMA IF NOT EXISTS api;
CREATE TABLE api.orders (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  owner text NOT NULL,
  total integer NOT NULL
);
ALTER TABLE api.orders ENABLE ROW LEVEL SECURITY;
CREATE POLICY own_orders ON api.orders
  USING (owner = current_setting('request.jwt.claims', true)::jsonb ->> 'sub')
  WITH CHECK (owner = current_setting('request.jwt.claims', true)::jsonb ->> 'sub');

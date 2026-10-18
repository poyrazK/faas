-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE managed_postgres_bindings ADD COLUMN IF NOT EXISTS connection_limit bigint NOT NULL DEFAULT 0 CHECK(connection_limit BETWEEN 0 AND 84);
CREATE TABLE IF NOT EXISTS postgres_capacity_resources (
 resource_key text PRIMARY KEY,
 placement text NOT NULL,
 generation bigint NOT NULL CHECK(generation>0),
 report_sha256 text NOT NULL,
 connections bigint NOT NULL CHECK(connections>=0),
 slots bigint NOT NULL CHECK(slots>=0),
 senders bigint NOT NULL CHECK(senders>=0),
 workers bigint NOT NULL CHECK(workers>=0),
 observed_at timestamptz NOT NULL,
 expires_at timestamptz NOT NULL CHECK(expires_at>observed_at AND expires_at<=observed_at+interval '30 seconds')
);
CREATE TABLE IF NOT EXISTS postgres_capacity_sources (
 connection_id uuid PRIMARY KEY, account_id uuid NOT NULL,
 connection_revision bigint NOT NULL CHECK(connection_revision>0),
 resource_key text NOT NULL REFERENCES postgres_capacity_resources(resource_key),
 placement text NOT NULL, generation bigint NOT NULL CHECK(generation>0)
);
CREATE TABLE IF NOT EXISTS postgres_capacity_targets (
 database_id uuid PRIMARY KEY, account_id uuid NOT NULL,
 database_generation bigint NOT NULL CHECK(database_generation>0),
 backend_fingerprint text NOT NULL, data_resource_id text NOT NULL,
 resource_key text NOT NULL REFERENCES postgres_capacity_resources(resource_key),
 placement text NOT NULL, generation bigint NOT NULL CHECK(generation>0)
);
CREATE TABLE IF NOT EXISTS postgres_capacity_reservations (
 owner_kind text NOT NULL CHECK(owner_kind IN ('pipeline','binding')),
 owner_id uuid NOT NULL,
 endpoint text NOT NULL CHECK(endpoint IN ('source','target')),
 resource_key text NOT NULL REFERENCES postgres_capacity_resources(resource_key),
 placement text NOT NULL, generation bigint NOT NULL CHECK(generation>0),
 connections bigint NOT NULL CHECK(connections>=0),
 slots bigint NOT NULL CHECK(slots>=0),
 senders bigint NOT NULL CHECK(senders>=0),
 workers bigint NOT NULL CHECK(workers>=0),
 PRIMARY KEY(owner_kind,owner_id,endpoint)
);
CREATE INDEX IF NOT EXISTS postgres_capacity_reservations_resource ON postgres_capacity_reservations(resource_key);

-- +goose StatementBegin
-- All allocation mutations share a short transaction lock. Remote SQL never
-- runs under it. This staging ledger serializes cross-account physical scopes.
CREATE OR REPLACE FUNCTION postgres_capacity_reserve(kind text, owner uuid, ep text, rk text, place text, gen bigint, conns bigint, sl bigint, se bigint, wo bigint)
RETURNS void LANGUAGE plpgsql AS $$
DECLARE r postgres_capacity_resources; used record; held postgres_capacity_reservations;
BEGIN
 PERFORM pg_advisory_xact_lock(51733);
 SELECT * INTO held FROM postgres_capacity_reservations WHERE owner_kind=kind AND owner_id=owner AND endpoint=ep;
 IF FOUND THEN
  IF (held.resource_key,held.placement,held.generation,held.connections,held.slots,held.senders,held.workers) IS DISTINCT FROM (rk,place,gen,conns,sl,se,wo) THEN
   RAISE EXCEPTION 'capacity placement changed' USING ERRCODE='PCC02';
  END IF;
  RETURN;
 END IF;
 SELECT * INTO r FROM postgres_capacity_resources WHERE resource_key=rk FOR UPDATE;
 IF NOT FOUND OR r.placement<>place OR r.generation<>gen OR r.observed_at>clock_timestamp() OR r.expires_at<=clock_timestamp() THEN
  RAISE EXCEPTION 'capacity requires qualification' USING ERRCODE='PCC02';
 END IF;
 -- A legacy native generation or unbounded credential must be accounted for
 -- before ANY new consumer can claim this allocation.
 IF EXISTS(SELECT 1 FROM data_pipelines p WHERE p.state<>'deleted' AND
  (NOT EXISTS(SELECT 1 FROM postgres_capacity_reservations h WHERE h.owner_kind='pipeline' AND h.owner_id=p.id AND h.endpoint='source') OR
   NOT EXISTS(SELECT 1 FROM postgres_capacity_reservations h WHERE h.owner_kind='pipeline' AND h.owner_id=p.id AND h.endpoint='target')))
 OR EXISTS(SELECT 1 FROM managed_postgres_cutovers c JOIN postgres_capacity_targets t ON t.database_id IN(c.source_database_id,c.target_database_id) WHERE t.resource_key=rk AND c.state<>'cancelled')
 OR EXISTS(SELECT 1 FROM managed_postgres_bindings b JOIN postgres_capacity_targets t ON t.database_id=b.database_id
  WHERE t.resource_key=rk AND b.state<>'deleted' AND b.connection_limit=0) THEN
  RAISE EXCEPTION 'existing consumers require qualification' USING ERRCODE='PCC02';
 END IF;
 SELECT coalesce(sum(connections),0) AS c,coalesce(sum(slots),0) AS sl,coalesce(sum(senders),0) AS se,coalesce(sum(workers),0) AS wo INTO used
 FROM postgres_capacity_reservations WHERE resource_key=rk;
 IF used.c+conns>r.connections OR used.sl+sl>r.slots OR used.se+se>r.senders OR used.wo+wo>r.workers THEN
  RAISE EXCEPTION 'insufficient database capacity' USING ERRCODE='PCC01';
 END IF;
 INSERT INTO postgres_capacity_reservations(owner_kind,owner_id,endpoint,resource_key,placement,generation,connections,slots,senders,workers)
 VALUES(kind,owner,ep,rk,place,gen,conns,sl,se,wo);
END; $$;

CREATE OR REPLACE FUNCTION postgres_capacity_pipeline_reserve(pid uuid, acct uuid, src uuid, dst uuid)
RETURNS void LANGUAGE plpgsql AS $$
DECLARE s postgres_capacity_sources; t postgres_capacity_targets;
BEGIN
 PERFORM pg_advisory_xact_lock(51733);
 SELECT m.* INTO s FROM postgres_capacity_sources m JOIN data_pipeline_connections c ON c.id=m.connection_id AND c.account_id=m.account_id
 WHERE m.connection_id=src AND m.account_id=acct AND m.connection_revision=c.revision;
 IF NOT FOUND THEN RAISE EXCEPTION 'source capacity requires qualification' USING ERRCODE='PCC02'; END IF;
 SELECT m.* INTO t FROM postgres_capacity_targets m JOIN managed_postgres_databases d ON d.id=m.database_id AND d.account_id=m.account_id
 WHERE m.database_id=dst AND m.account_id=acct AND m.database_generation=d.observed_generation AND d.desired_generation=d.observed_generation
  AND d.state='ready' AND m.backend_fingerprint=d.backend_fingerprint AND m.data_resource_id=d.data_resource_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'mirror capacity requires qualification' USING ERRCODE='PCC02'; END IF;
 PERFORM postgres_capacity_reserve('pipeline',pid,'source',s.resource_key,s.placement,s.generation,5,3,3,0);
 PERFORM postgres_capacity_reserve('pipeline',pid,'target',t.resource_key,t.placement,t.generation,5,0,0,3);
END; $$;

CREATE OR REPLACE FUNCTION postgres_capacity_admit(pid uuid, acct uuid, rev bigint, token text)
RETURNS boolean LANGUAGE plpgsql AS $$
DECLARE p data_pipelines;
BEGIN
 SELECT * INTO p FROM data_pipelines WHERE id=pid AND account_id=acct AND revision=rev AND lease_token=token AND lease_until>clock_timestamp() FOR SHARE;
 IF NOT FOUND THEN RETURN false; END IF;
 PERFORM pg_advisory_xact_lock(51733);
 PERFORM postgres_capacity_pipeline_reserve(p.id,p.account_id,p.source_connection_id,p.database_id);
 IF EXISTS(SELECT 1 FROM postgres_capacity_reservations h JOIN postgres_capacity_resources r USING(resource_key)
  WHERE h.owner_kind='pipeline' AND h.owner_id=p.id AND (r.observed_at>clock_timestamp() OR r.expires_at<=clock_timestamp())) THEN
  RAISE EXCEPTION 'capacity requires qualification' USING ERRCODE='PCC02';
 END IF;
 IF EXISTS(SELECT 1 FROM postgres_capacity_resources r JOIN postgres_capacity_reservations own USING(resource_key)
  CROSS JOIN LATERAL(SELECT coalesce(sum(connections),0) c,coalesce(sum(slots),0) sl,coalesce(sum(senders),0) se,coalesce(sum(workers),0) wo
   FROM postgres_capacity_reservations h WHERE h.resource_key=r.resource_key) demand
  WHERE own.owner_kind='pipeline' AND own.owner_id=p.id AND (demand.c>r.connections OR demand.sl>r.slots OR demand.se>r.senders OR demand.wo>r.workers)) THEN
  RAISE EXCEPTION 'insufficient database capacity' USING ERRCODE='PCC01';
 END IF;
 RETURN true;
END; $$;

CREATE OR REPLACE FUNCTION postgres_capacity_pipeline_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN
  PERFORM postgres_capacity_pipeline_reserve(NEW.id,NEW.account_id,NEW.source_connection_id,NEW.database_id);
 ELSIF NEW.state='deleted' AND OLD.state<>'deleted' THEN
  PERFORM pg_advisory_xact_lock(51733);
  DELETE FROM postgres_capacity_reservations WHERE owner_kind='pipeline' AND owner_id=NEW.id;
 ELSIF (NEW.source_connection_id,NEW.database_id) IS DISTINCT FROM (OLD.source_connection_id,OLD.database_id) THEN
  RAISE EXCEPTION 'reserved pipeline placement is immutable' USING ERRCODE='PCC02';
 END IF;
 RETURN NEW;
END; $$;
DROP TRIGGER IF EXISTS postgres_capacity_pipeline_guard ON data_pipelines;
CREATE TRIGGER postgres_capacity_pipeline_guard BEFORE INSERT OR UPDATE OF state,source_connection_id,database_id ON data_pipelines
 FOR EACH ROW EXECUTE FUNCTION postgres_capacity_pipeline_guard();

CREATE OR REPLACE FUNCTION postgres_capacity_binding_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE t postgres_capacity_targets;
BEGIN
 IF TG_OP='UPDATE' AND OLD.connection_limit>0 AND (NEW.connection_limit,NEW.database_id) IS DISTINCT FROM (OLD.connection_limit,OLD.database_id) THEN
  RAISE EXCEPTION 'reserved credential ceiling is immutable' USING ERRCODE='PCC02';
 END IF;
 IF NEW.state='deleted' THEN
  PERFORM pg_advisory_xact_lock(51733);
  DELETE FROM postgres_capacity_reservations WHERE owner_kind='binding' AND owner_id=NEW.id;
  RETURN NEW;
 END IF;
 IF TG_OP='UPDATE' AND OLD.state<>'deleted' THEN RETURN NEW; END IF;
 PERFORM pg_advisory_xact_lock(51733);
 SELECT m.* INTO t FROM postgres_capacity_targets m JOIN managed_postgres_databases d ON d.id=m.database_id AND d.account_id=m.account_id
 WHERE m.database_id=NEW.database_id AND m.account_id=NEW.account_id AND m.database_generation=d.observed_generation
  AND d.desired_generation=d.observed_generation AND d.state='ready' AND m.backend_fingerprint=d.backend_fingerprint AND m.data_resource_id=d.data_resource_id;
 IF NOT FOUND THEN
  IF EXISTS(SELECT 1 FROM postgres_capacity_targets WHERE database_id=NEW.database_id) OR EXISTS(SELECT 1 FROM data_pipelines p WHERE p.database_id=NEW.database_id AND p.state<>'deleted') OR NEW.connection_limit>0 THEN
   RAISE EXCEPTION 'mirror capacity requires qualification' USING ERRCODE='PCC02';
  END IF;
  RETURN NEW;
 END IF;
 -- Both current and previous credentials fit during rotation, irrespective
 -- of runtime version, replica count, wake bursts or client pool settings.
 NEW.connection_limit:=CASE WHEN NEW.access='auth' THEN 84 ELSE 42 END;
 PERFORM postgres_capacity_reserve('binding',NEW.id,'target',t.resource_key,t.placement,t.generation,NEW.connection_limit*2+2,0,0,0);
 RETURN NEW;
END; $$;
DROP TRIGGER IF EXISTS postgres_capacity_binding_guard ON managed_postgres_bindings;
CREATE TRIGGER postgres_capacity_binding_guard BEFORE INSERT OR UPDATE OF state,connection_limit,database_id ON managed_postgres_bindings
 FOR EACH ROW EXECUTE FUNCTION postgres_capacity_binding_guard();

CREATE OR REPLACE FUNCTION postgres_capacity_mapping_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 PERFORM pg_advisory_xact_lock(51733);
 IF (NEW.resource_key,NEW.placement,NEW.generation) IS DISTINCT FROM (OLD.resource_key,OLD.placement,OLD.generation) THEN
  IF TG_TABLE_NAME='postgres_capacity_sources' THEN
   IF EXISTS(SELECT 1 FROM data_pipelines p WHERE p.source_connection_id=OLD.connection_id AND p.state<>'deleted') THEN
    RAISE EXCEPTION 'active source capacity placement is immutable' USING ERRCODE='PCC02';
   END IF;
  ELSE
   IF EXISTS(SELECT 1 FROM data_pipelines p WHERE p.database_id=OLD.database_id AND p.state<>'deleted') OR EXISTS(SELECT 1 FROM managed_postgres_bindings b WHERE b.database_id=OLD.database_id AND b.state<>'deleted') THEN
    RAISE EXCEPTION 'active mirror capacity placement is immutable' USING ERRCODE='PCC02';
   END IF;
  END IF;
 END IF;
 RETURN NEW;
END; $$;
DROP TRIGGER IF EXISTS postgres_capacity_source_mapping_guard ON postgres_capacity_sources;
CREATE TRIGGER postgres_capacity_source_mapping_guard BEFORE UPDATE ON postgres_capacity_sources FOR EACH ROW EXECUTE FUNCTION postgres_capacity_mapping_guard();
DROP TRIGGER IF EXISTS postgres_capacity_target_mapping_guard ON postgres_capacity_targets;
CREATE TRIGGER postgres_capacity_target_mapping_guard BEFORE UPDATE ON postgres_capacity_targets FOR EACH ROW EXECUTE FUNCTION postgres_capacity_mapping_guard();
CREATE OR REPLACE FUNCTION postgres_capacity_cutover_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 PERFORM pg_advisory_xact_lock(51733);
 IF EXISTS(SELECT 1 FROM postgres_capacity_targets WHERE database_id IN(NEW.source_database_id,NEW.target_database_id)) THEN
  RAISE EXCEPTION 'cutover credentials require a qualified capacity model' USING ERRCODE='PCC02';
 END IF;
 RETURN NEW;
END; $$;
DROP TRIGGER IF EXISTS postgres_capacity_cutover_guard ON managed_postgres_cutovers;
CREATE TRIGGER postgres_capacity_cutover_guard BEFORE INSERT ON managed_postgres_cutovers FOR EACH ROW EXECUTE FUNCTION postgres_capacity_cutover_guard();
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TRIGGER postgres_capacity_cutover_guard ON managed_postgres_cutovers;
DROP FUNCTION postgres_capacity_cutover_guard();
DROP TRIGGER postgres_capacity_target_mapping_guard ON postgres_capacity_targets;
DROP TRIGGER postgres_capacity_source_mapping_guard ON postgres_capacity_sources;
DROP TRIGGER postgres_capacity_binding_guard ON managed_postgres_bindings;
DROP TRIGGER postgres_capacity_pipeline_guard ON data_pipelines;
DROP FUNCTION postgres_capacity_mapping_guard();
DROP FUNCTION postgres_capacity_binding_guard();
DROP FUNCTION postgres_capacity_pipeline_guard();
DROP FUNCTION postgres_capacity_admit(uuid,uuid,bigint,text);
DROP FUNCTION postgres_capacity_pipeline_reserve(uuid,uuid,uuid,uuid);
DROP FUNCTION postgres_capacity_reserve(text,uuid,text,text,text,bigint,bigint,bigint,bigint,bigint);
DROP TABLE postgres_capacity_reservations;
DROP TABLE postgres_capacity_targets;
DROP TABLE postgres_capacity_sources;
DROP TABLE postgres_capacity_resources;
ALTER TABLE managed_postgres_bindings DROP COLUMN connection_limit;

package dbmigration

const sourceRelations = `
WITH relations AS (
 SELECT c.oid, n.nspname, c.relname,
   c.relkind = 'r' AND c.relpersistence = 'p' AND n.nspname = 'public'
   AND c.relname ~ '^[a-z][a-z0-9_]{0,62}$' AND c.relname !~ '^gregale_'
   AND NOT c.relispartition AND NOT c.relrowsecurity AND c.relreplident = 'd'
   AND NOT EXISTS (SELECT FROM pg_catalog.pg_inherits WHERE inhrelid = c.oid OR inhparent = c.oid)
   AND pg_catalog.has_table_privilege(c.oid, 'SELECT')
   AND pg_catalog.pg_has_role(c.relowner, 'USAGE')
   AND EXISTS (SELECT FROM pg_catalog.pg_index i WHERE i.indrelid = c.oid AND i.indisprimary
      AND i.indisvalid AND i.indimmediate)
   AND NOT EXISTS (SELECT FROM pg_catalog.pg_attribute a
      JOIN pg_catalog.pg_type t ON t.oid = a.atttypid
      JOIN pg_catalog.pg_namespace tn ON tn.oid = t.typnamespace
      WHERE a.attrelid = c.oid AND a.attnum > 0 AND NOT a.attisdropped
        AND (a.attgenerated <> '' OR tn.nspname <> 'pg_catalog' OR a.attname !~ '^[a-z_][a-z0-9_]{0,62}$'
          OR t.typname NOT IN ('bool','int2','int4','int8','float4','float8','text','bytea','uuid','json','jsonb',
            'date','inet','cidr','macaddr','macaddr8','interval','varchar','bpchar','timestamp','timestamptz','time','timetz','numeric')
          OR t.typname = 'interval' AND a.atttypmod <> -1
          OR a.attcollation NOT IN (0, 'pg_catalog."default"'::regcollation)))
   AND (SELECT count(*) FROM pg_catalog.pg_attribute a WHERE a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped) <= 128
   AS supported
 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
 WHERE c.relkind IN ('r', 'p', 'f', 'm') AND n.nspname !~ '^pg_' AND n.nspname <> 'information_schema'
)`

// InspectSourceSQL reads source settings and catalogs. The source selects
// reportable names separately from the complete count.
const InspectSourceSQL = sourceRelations + `
SELECT pg_catalog.json_build_object(
 'major', pg_catalog.current_setting('server_version_num')::integer / 10000,
 'writable', NOT pg_catalog.pg_is_in_recovery() AND NOT pg_catalog.current_setting('transaction_read_only')::boolean,
 'logical_wal', pg_catalog.current_setting('wal_level') = 'logical',
 'can_replicate', (SELECT rolreplication OR rolsuper FROM pg_catalog.pg_roles WHERE rolname = current_user),
 'can_publish', pg_catalog.has_database_privilege(pg_catalog.current_database(), 'CREATE'),
 'free_slots', pg_catalog.current_setting('max_replication_slots')::bigint - (SELECT count(*) FROM pg_catalog.pg_replication_slots),
 'free_senders', pg_catalog.current_setting('max_wal_senders')::bigint - (SELECT count(*) FROM pg_catalog.pg_stat_replication),
 'retention_bytes', pg_catalog.pg_size_bytes(pg_catalog.current_setting('max_slot_wal_keep_size')),
 'database_bytes', pg_catalog.pg_database_size(pg_catalog.current_database()),
 'tables', (SELECT count(*) FROM relations),
 'sequences', (SELECT count(*) FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
   WHERE c.relkind = 'S' AND n.nspname !~ '^pg_' AND n.nspname <> 'information_schema'),
 'large_objects', EXISTS (SELECT FROM pg_catalog.pg_largeobject_metadata),
 'unsupported_count', (SELECT count(*) FROM relations WHERE NOT supported),
 'unsupported_tables', (SELECT pg_catalog.json_agg(name ORDER BY name)
   FROM (SELECT nspname || '.' || relname AS name FROM relations WHERE NOT supported
     AND nspname ~ '^[a-z_][a-z0-9_]{0,62}$' AND relname ~ '^[a-z_][a-z0-9_]{0,62}$'
     ORDER BY (nspname || '.' || relname) COLLATE "C" LIMIT 16) blocked)
)`

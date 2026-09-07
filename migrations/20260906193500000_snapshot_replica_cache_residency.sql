-- +goose Up
-- +goose StatementBegin

-- The producer is a cache location too. Recording the origin used to delete
-- its replica row on the assumption that the just-written artifacts would
-- remain on disk forever. A byte-bounded cache can evict them, leaving
-- placement with a stale origin hint and making the next wake download the
-- snapshot synchronously.
CREATE OR REPLACE FUNCTION snapshot_replica_refresh_after_origin()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    DELETE FROM snapshot_replicas r
    USING compute_nodes cn
    WHERE r.snapshot_id = NEW.snapshot_id
      AND r.node_id = cn.id
      AND NEW.region <> ''
      AND coalesce(cn.region, '') <> NEW.region;

    INSERT INTO snapshot_replicas (snapshot_id, node_id, region)
    SELECT NEW.snapshot_id, cn.id, coalesce(cn.region, '')
      FROM compute_nodes cn
      JOIN snapshots sn ON sn.id = NEW.snapshot_id
      JOIN deployments d ON d.id = sn.deployment_id
      JOIN apps a ON a.id = d.app_id
     WHERE cn.active
       AND sn.stale = false
       AND sn.storage_key <> ''
       AND a.status <> 'deleted'
       AND d.status IN ('snapshotting', 'live', 'superseded')
       AND (NEW.region = '' OR coalesce(cn.region, '') = NEW.region)
    ON CONFLICT (snapshot_id, node_id) DO NOTHING;

    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION snapshot_replica_refresh_after_compute_node()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.active THEN
        DELETE FROM snapshot_replicas r
        USING snapshots sn
        LEFT JOIN snapshot_origins so ON so.snapshot_id = sn.id
        WHERE r.snapshot_id = sn.id
          AND r.node_id = NEW.id
          AND so.region <> ''
          AND coalesce(NEW.region, '') <> so.region;

        INSERT INTO snapshot_replicas (snapshot_id, node_id, region)
        SELECT e.snapshot_id, NEW.id, coalesce(NEW.region, '')
          FROM snapshot_fanout_events e
          JOIN snapshots sn ON sn.id = e.snapshot_id
          JOIN deployments d ON d.id = sn.deployment_id
          JOIN apps a ON a.id = d.app_id
          LEFT JOIN snapshot_origins so ON so.snapshot_id = sn.id
         WHERE sn.stale = false
           AND sn.storage_key <> ''
           AND a.status <> 'deleted'
           AND d.status IN ('snapshotting', 'live', 'superseded')
           AND (so.region = '' OR coalesce(NEW.region, '') = so.region)
        ON CONFLICT (snapshot_id, node_id) DO NOTHING;
    END IF;
    RETURN NEW;
END;
$$;

-- Repair origins created under the previous trigger and retry rows stranded
-- by a transient registry or old-reader failure. A genuinely missing object
-- will be classified permanent again on this single repair attempt.
INSERT INTO snapshot_replicas (snapshot_id, node_id, region)
SELECT sn.id, cn.id, coalesce(cn.region, '')
  FROM snapshots sn
  JOIN deployments d ON d.id = sn.deployment_id
  JOIN apps a ON a.id = d.app_id
  JOIN snapshot_origins so ON so.snapshot_id = sn.id
  JOIN compute_nodes cn ON cn.id = so.node_id
 WHERE cn.active
   AND sn.stale = false
   AND sn.storage_key <> ''
   AND a.status <> 'deleted'
   AND d.status IN ('snapshotting', 'live', 'superseded')
ON CONFLICT (snapshot_id, node_id) DO NOTHING;

UPDATE snapshot_replicas r
   SET state = 'pending', attempts = 0, last_error = NULL,
       next_attempt_at = NULL, ready_at = NULL, updated_at = now()
  FROM snapshots sn
  JOIN deployments d ON d.id = sn.deployment_id
  JOIN apps a ON a.id = d.app_id
 WHERE r.snapshot_id = sn.id
   AND r.state = 'failed'
   AND sn.stale = false
   AND a.status <> 'deleted'
   AND d.status IN ('snapshotting', 'live', 'superseded');

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION snapshot_replica_refresh_after_origin()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    DELETE FROM snapshot_replicas r
    USING compute_nodes cn
    WHERE r.snapshot_id = NEW.snapshot_id
      AND r.node_id = cn.id
      AND (
          (NEW.node_id IS NOT NULL AND r.node_id = NEW.node_id)
          OR (NEW.region <> '' AND coalesce(cn.region, '') <> NEW.region)
      );

    INSERT INTO snapshot_replicas (snapshot_id, node_id, region)
    SELECT NEW.snapshot_id, cn.id, coalesce(cn.region, '')
      FROM compute_nodes cn
      JOIN snapshots sn ON sn.id = NEW.snapshot_id
     WHERE cn.active
       AND sn.stale = false
       AND sn.storage_key <> ''
       AND (NEW.node_id IS NULL OR cn.id <> NEW.node_id)
       AND (NEW.region = '' OR coalesce(cn.region, '') = NEW.region)
    ON CONFLICT (snapshot_id, node_id) DO NOTHING;

    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION snapshot_replica_refresh_after_compute_node()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.active THEN
        DELETE FROM snapshot_replicas r
        USING snapshots sn
        LEFT JOIN snapshot_origins so ON so.snapshot_id = sn.id
        WHERE r.snapshot_id = sn.id
          AND r.node_id = NEW.id
          AND (
              (so.node_id IS NOT NULL AND so.node_id = NEW.id)
              OR (so.region <> '' AND coalesce(NEW.region, '') <> so.region)
          );

        INSERT INTO snapshot_replicas (snapshot_id, node_id, region)
        SELECT e.snapshot_id, NEW.id, coalesce(NEW.region, '')
          FROM snapshot_fanout_events e
          JOIN snapshots sn ON sn.id = e.snapshot_id
          LEFT JOIN snapshot_origins so ON so.snapshot_id = sn.id
         WHERE sn.stale = false
           AND sn.storage_key <> ''
           AND (so.node_id IS NULL OR so.node_id <> NEW.id)
           AND (so.region = '' OR coalesce(NEW.region, '') = so.region)
        ON CONFLICT (snapshot_id, node_id) DO NOTHING;
    END IF;
    RETURN NEW;
END;
$$;

DELETE FROM snapshot_replicas r
USING snapshot_origins so
WHERE r.snapshot_id = so.snapshot_id
  AND r.node_id = so.node_id;
-- Failure retries and their attempt counters are operational state and are
-- intentionally not reversed.
-- +goose StatementEnd

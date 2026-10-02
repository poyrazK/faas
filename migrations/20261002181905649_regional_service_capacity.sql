-- filename: 20261002181905649_regional_service_capacity.sql
-- ADR-437: retain selected-region recovery room alongside fixed legacy survivors.
-- +goose Up
-- +goose StatementBegin
DO $$ BEGIN
    PERFORM singleton FROM service_capacity_policy WHERE singleton FOR UPDATE;
END; $$;
CREATE OR REPLACE FUNCTION service_capacity_snapshot_inputs() RETURNS jsonb
LANGUAGE sql VOLATILE AS $$
WITH policy AS (SELECT * FROM service_capacity_policy WHERE singleton),
eligible AS (
    SELECT a.id, a.ram_mb, a.cpu_millicores, a.manifest, a.compute_region,
           coalesce((p.plan_vcpus->>ac.plan)::integer, 4) AS vcpu
      FROM apps a JOIN accounts ac ON ac.id=a.account_id CROSS JOIN policy p
     WHERE a.status IN ('active','evicted_cold') AND a.manifest->>'execution_mode'='service'
), scopes AS (
    SELECT id AS app_id, 'default'::text AS scope FROM eligible
    UNION
    SELECT d.app_id, d.scope FROM deployments d JOIN eligible a ON a.id=d.app_id
     WHERE d.status IN ('pending','building','imaging','snapshotting','live')
), declaration AS (
    SELECT s.app_id::text || ':' || s.scope AS key, a.compute_region AS region,
           greatest(0, coalesce((a.manifest->'service_replicas'->>'desired')::integer,1)) AS desired,
           CASE WHEN EXISTS (SELECT 1 FROM deployments d WHERE d.app_id=a.id AND d.scope=s.scope AND d.status='live')
                  AND (EXISTS (SELECT 1 FROM deployments d WHERE d.app_id=a.id AND d.scope=s.scope AND d.status IN ('pending','building','imaging','snapshotting'))
                    OR (SELECT count(*) FROM deployments d WHERE d.app_id=a.id AND d.scope=s.scope AND d.status='live')>1)
                THEN 1 ELSE 0 END AS surge,
           a.ram_mb+p.overhead_mb+coalesce((
               SELECT max((SELECT coalesce(sum(greatest(0,(sc->>'ram_mb')::integer)),0)
                             FROM jsonb_array_elements(coalesce(d.sidecars,'[]'::jsonb)) sc))
                 FROM deployments d WHERE d.app_id=a.id AND d.scope=s.scope
                   AND d.status IN ('pending','building','imaging','snapshotting','live')),0) AS ram,
           greatest(a.cpu_millicores,p.startup_cpu)+coalesce((
               SELECT max((SELECT coalesce(sum(greatest(0,coalesce(nullif(greatest(0,(sc->>'cpu_millicores')::integer),0),p.startup_cpu))),0)
                             FROM jsonb_array_elements(coalesce(d.sidecars,'[]'::jsonb)) sc))
                 FROM deployments d WHERE d.app_id=a.id AND d.scope=s.scope
                   AND d.status IN ('pending','building','imaging','snapshotting','live')),0) AS cpu,
           a.vcpu
      FROM scopes s JOIN eligible a ON a.id=s.app_id CROSS JOIN policy p
), demand AS (
    SELECT key, region, CASE WHEN desired=0 THEN 0 ELSE desired+surge END AS count, desired, ram, cpu, vcpu
      FROM declaration
), resident AS (
    SELECT i.node_id, a.compute_region AS region, a.id::text || ':' || coalesce(d.scope,'default') AS key,
           greatest(i.capacity_ram_mb,i.ram_mb+p.overhead_mb+coalesce((SELECT sum(greatest(0,(sc->>'ram_mb')::integer))
                           FROM jsonb_array_elements(coalesce(d.sidecars,'[]'::jsonb)) sc),0)) AS ram,
           greatest(i.capacity_cpu_millicores,greatest(coalesce(a.cpu_millicores,p.startup_cpu),p.startup_cpu)+coalesce((
               SELECT sum(greatest(0,coalesce(nullif(greatest(0,(sc->>'cpu_millicores')::integer),0),p.startup_cpu)))
                 FROM jsonb_array_elements(coalesce(d.sidecars,'[]'::jsonb)) sc),0)) AS cpu,
           greatest(i.capacity_vcpu,CASE WHEN i.app_id IS NULL THEN 1 ELSE coalesce((p.plan_vcpus->>ac.plan)::integer,4) END) AS vcpu,
           (i.state<>'warm' AND i.mode IN ('normal','service') AND a.manifest->>'execution_mode'='service'
              AND EXISTS (SELECT 1 FROM demand x WHERE x.key=a.id::text || ':' || coalesce(d.scope,'default') )) AS service
      FROM instances i LEFT JOIN apps a ON a.id=i.app_id LEFT JOIN accounts ac ON ac.id=a.account_id
      LEFT JOIN deployments d ON d.id=i.deployment_id CROSS JOIN policy p
     WHERE i.state IN ('waking','cold_booting','running','draining','warm','snapshotting','migrating')
), shape AS (
    SELECT greatest(coalesce((SELECT max(ram) FROM demand WHERE count>0),0),coalesce((SELECT max(ram) FROM resident WHERE service),0),1) AS ram,
           greatest(coalesce((SELECT max(cpu) FROM demand WHERE count>0),0),coalesce((SELECT max(cpu) FROM resident WHERE service),0),1) AS cpu,
           greatest(coalesce((SELECT max(vcpu) FROM demand WHERE count>0),0),coalesce((SELECT max(vcpu) FROM resident WHERE service),0),1) AS vcpu
), other_usage AS (
    SELECT node_id, sum(ram)::bigint AS ram, sum(cpu)::bigint AS cpu, sum(vcpu)::bigint AS vcpu
      FROM resident WHERE NOT coalesce(service,false) GROUP BY node_id
), service_usage AS (
    SELECT r.node_id, count(*)::bigint AS count, count(*) FILTER (WHERE r.region='')::bigint AS legacy_count,
           bool_and(r.region='' OR coalesce(r.region=n.region,false)) AS region_fit
      FROM resident r JOIN compute_nodes n ON n.id=r.node_id WHERE r.service GROUP BY r.node_id
), nodes AS (
    SELECT n.id, n.name, coalesce(n.region,'') AS region, coalesce(s.legacy_count,0) AS legacy_count, coalesce(s.region_fit,true) AS region_fit, coalesce(u.ram,0) AS other_ram, coalesce(u.cpu,0) AS other_cpu, coalesce(u.vcpu,0) AS other_vcpu,
           coalesce(s.count,0) AS service_count,
           (coalesce(u.ram,0)<=n.admission_ceiling_mb AND coalesce(u.cpu,0)<=n.vpcpus::bigint*1000*p.cpu_overcommit AND coalesce(u.vcpu,0)<=n.vcpu_budget) AS ordinary_fit,
           greatest(0,least((n.admission_ceiling_mb-coalesce(u.ram,0))/sh.ram,
                       (n.vpcpus::bigint*1000*p.cpu_overcommit-coalesce(u.cpu,0))/sh.cpu,
                       (n.vcpu_budget-coalesce(u.vcpu,0))/sh.vcpu))::bigint AS slots
      FROM compute_nodes n CROSS JOIN policy p CROSS JOIN shape sh
      LEFT JOIN other_usage u ON u.node_id=n.id LEFT JOIN service_usage s ON s.node_id=n.id
     WHERE n.lifecycle='active' AND n.admission_ceiling_mb>0 AND n.vpcpus>0 AND n.vcpu_budget>0
       AND n.last_heartbeat_at>=clock_timestamp()-make_interval(secs=>p.heartbeat_seconds)
), totals AS (
    SELECT count(*) AS healthy_nodes, coalesce(sum(slots),0) AS fleet_slots,
           coalesce(sum(slots)-max(slots),0) AS failover_slots,
           coalesce(bool_and(service_count<=slots AND ordinary_fit AND region_fit),true) AS placements_fit FROM nodes
), required AS (SELECT coalesce(sum(count),0) AS count, coalesce(sum(desired),0) AS desired FROM demand),
actual_targets AS (SELECT key,count(*) AS count FROM resident WHERE service GROUP BY key),
projection AS (
    SELECT jsonb_build_object(
        'enabled',p.enabled,
        'state',CASE WHEN NOT p.enabled THEN 'disabled'
                     WHEN t.healthy_nodes>=2 AND t.placements_fit AND r.count<=t.failover_slots
                       AND NOT EXISTS (SELECT 1 FROM actual_targets a JOIN demand d ON d.key=a.key WHERE a.count>d.count) THEN 'protected'
                     WHEN t.placements_fit AND r.count<=t.fleet_slots THEN 'degraded' ELSE 'needs_hardware' END,
        'healthy_nodes',t.healthy_nodes,'desired_replicas',r.desired,'reserved_replicas',r.count,
        'replica_ram_mb',sh.ram,'replica_cpu_millicores',sh.cpu,'replica_vcpu',sh.vcpu,
        'fleet_slots',t.fleet_slots,'failover_slots',t.failover_slots,'placements_fit',t.placements_fit,
        'demands',coalesce((SELECT jsonb_object_agg(key,jsonb_build_object('count',count,'desired',desired,'region',region,'ram',ram,'cpu',cpu,'vcpu',vcpu)) FROM demand),'{}'::jsonb),
        'other',coalesce((SELECT jsonb_object_agg(id::text,jsonb_build_object('ram',other_ram,'cpu',other_cpu,'vcpu',other_vcpu)) FROM nodes),'{}'::jsonb),
        'placement',coalesce((SELECT jsonb_object_agg(id::text,jsonb_build_object('slots',slots,'used',service_count,'region',region,'legacy_used',legacy_count,'legacy_allowed',slots>service_count,'pinned_allowed',slots>service_count,'fits',service_count<=slots AND ordinary_fit AND region_fit)) FROM nodes),'{}'::jsonb),
        'region_ids',coalesce((SELECT jsonb_agg(id ORDER BY id) FROM (SELECT id FROM compute_regions UNION SELECT coalesce(region,'') FROM compute_nodes UNION SELECT region FROM demand WHERE region<>'') ids),'[]'::jsonb),
        'legacy_usage',coalesce((SELECT jsonb_object_agg(node_id::text,count) FROM (SELECT node_id,count(*) AS count FROM resident WHERE service AND region='' GROUP BY node_id) legacy),'{}'::jsonb),
        'service_usage',coalesce((SELECT jsonb_object_agg(node_id::text,count) FROM service_usage),'{}'::jsonb),
        'resident',coalesce((SELECT jsonb_object_agg(node_id::text,cost) FROM (SELECT node_id,jsonb_build_object('ram',sum(ram),'cpu',sum(cpu),'vcpu',sum(vcpu)) AS cost FROM resident GROUP BY node_id) u),'{}'::jsonb),
        'actual',coalesce((SELECT jsonb_object_agg(key,count) FROM actual_targets),'{}'::jsonb)
    ) AS data FROM policy p CROSS JOIN totals t CROSS JOIN required r CROSS JOIN shape sh
) SELECT data FROM projection;
$$;

CREATE OR REPLACE FUNCTION service_capacity_region_legacy_limit(nodes jsonb, pinned bigint) RETURNS bigint LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE fleet bigint:=0; legacy bigint:=0; largest_free bigint:=0; pinned_used bigint:=0;
        node jsonb; lower bigint; upper bigint; middle bigint; needed bigint; minimum bigint; feasible boolean;
BEGIN
    FOR node IN SELECT value FROM jsonb_each(nodes) LOOP
        fleet:=fleet+(node->>'slots')::bigint; legacy:=legacy+(node->>'legacy_used')::bigint;
        pinned_used:=pinned_used+(node->>'used')::bigint-(node->>'legacy_used')::bigint;
        largest_free:=greatest(largest_free,greatest(0,(node->>'slots')::bigint-(node->>'legacy_used')::bigint));
    END LOOP;
    IF pinned=0 THEN RETURN greatest(0,fleet-pinned_used); END IF;
    IF pinned>fleet-legacy-largest_free THEN RETURN 0; END IF;
    upper:=fleet-pinned;
    FOR node IN SELECT value FROM jsonb_each(nodes) LOOP
        upper:=least(upper,fleet-pinned-((node->>'used')::bigint-(node->>'legacy_used')::bigint));
    END LOOP;
    IF upper<legacy THEN RETURN 0; END IF;
    lower:=legacy;
    WHILE lower<upper LOOP
        middle:=lower+(upper-lower+1)/2; needed:=0; feasible:=true;
        FOR node IN SELECT value FROM jsonb_each(nodes) LOOP
            minimum:=greatest((node->>'legacy_used')::bigint,middle-(fleet-(node->>'slots')::bigint-pinned));
            needed:=needed+minimum;
            feasible:=feasible AND minimum<=(node->>'slots')::bigint-((node->>'used')::bigint-(node->>'legacy_used')::bigint);
        END LOOP;
        IF feasible AND needed<=middle THEN lower:=middle; ELSE upper:=middle-1; END IF;
    END LOOP;
    RETURN lower;
END;
$$;

CREATE OR REPLACE FUNCTION service_capacity_snapshot() RETURNS jsonb LANGUAGE plpgsql VOLATILE AS $$
DECLARE data jsonb; region_id text; required bigint; desired bigint; fleet bigint; legacy bigint; legacy_required bigint;
        healthy integer; fit boolean; excess boolean; available bigint; failover bigint; largest_free bigint;
        free bigint; cell_capacity bigint; total_legacy_capacity bigint:=0; pinned_total bigint:=0;
        needed_here bigint; next_population bigint; minimum bigint; needed bigint; physical boolean;
        pinned_allowed boolean; legacy_allowed boolean; peer_key text; peer_value jsonb; lower bigint;
        node_key text; node_value jsonb; row_value jsonb; cell_nodes jsonb; state text;
        regional_protected boolean:=true; regional_fit boolean:=true;
        region_rows jsonb:='[]'::jsonb; region_detail jsonb:='{}'::jsonb;
BEGIN
    data:=service_capacity_snapshot_inputs();
    SELECT coalesce(sum((value->>'count')::bigint),0) INTO legacy_required
      FROM jsonb_each(data->'demands') WHERE value->>'region'='';
    FOR region_id IN SELECT value FROM jsonb_array_elements_text(data->'region_ids') ORDER BY value LOOP
        SELECT coalesce(sum((value->>'count')::bigint),0), coalesce(sum((value->>'desired')::bigint),0),
               coalesce(bool_or(coalesce((data->'actual'->>key)::bigint,0)>(value->>'count')::bigint),false)
          INTO required,desired,excess FROM jsonb_each(data->'demands') WHERE value->>'region'=region_id AND region_id<>'';
        pinned_total:=pinned_total+required;
        fleet:=0; legacy:=0; healthy:=0; fit:=true; largest_free:=0; cell_nodes:='{}'::jsonb;
        FOR node_key,node_value IN SELECT key,value FROM jsonb_each(data->'placement') WHERE value->>'region'=region_id LOOP
            cell_nodes:=jsonb_set(cell_nodes,ARRAY[node_key],node_value);
            healthy:=healthy+1; fleet:=fleet+(node_value->>'slots')::bigint; legacy:=legacy+(node_value->>'legacy_used')::bigint;
            fit:=fit AND (node_value->>'fits')::boolean;
            free:=greatest(0,(node_value->>'slots')::bigint-(node_value->>'legacy_used')::bigint);
            largest_free:=greatest(largest_free,free);
        END LOOP;
        available:=greatest(0,fleet-legacy); failover:=greatest(0,available-largest_free);
        cell_capacity:=service_capacity_region_legacy_limit(cell_nodes,required);
        total_legacy_capacity:=total_legacy_capacity+cell_capacity;
        state:=CASE WHEN NOT (data->>'enabled')::boolean THEN 'disabled'
                    WHEN healthy>=2 AND fit AND NOT excess AND required<=failover THEN 'protected'
                    WHEN fit AND required<=available THEN 'degraded' ELSE 'needs_hardware' END;
        IF required>0 THEN
            regional_protected:=regional_protected AND state='protected';
            regional_fit:=regional_fit AND fit AND required<=available;
        END IF;
        row_value:=jsonb_build_object('compute_region',region_id,'state',state,'healthy_nodes',healthy,
            'desired_replicas',desired,'reserved_replicas',required,'fleet_slots',fleet,'available_slots',available,
            'failover_slots',failover,'legacy_resident_replicas',legacy,'legacy_placement_capacity',cell_capacity,'placements_fit',fit);
        IF region_id<>'' THEN region_rows:=region_rows||jsonb_build_array(row_value); END IF;
        region_detail:=jsonb_set(region_detail,ARRAY[region_id],row_value||jsonb_build_object('nodes',cell_nodes));
    END LOOP;
    data:=data||jsonb_build_object('legacy_reserved_replicas',legacy_required,
        'legacy_placement_capacity',least(total_legacy_capacity,greatest(0,(data->>'failover_slots')::bigint-pinned_total)));
    IF (data->>'enabled')::boolean THEN
        IF NOT regional_fit THEN data:=jsonb_set(data,'{state}','"needs_hardware"'::jsonb);
        ELSIF data->>'state'='protected' AND (NOT regional_protected OR legacy_required>(data->>'legacy_placement_capacity')::bigint) THEN
            data:=jsonb_set(data,'{state}','"degraded"'::jsonb);
        END IF;
    END IF;
    data:=jsonb_set(data,'{regions}',region_rows);
    FOR node_key,node_value IN SELECT * FROM jsonb_each(data->'placement') LOOP
        region_id:=node_value->>'region'; row_value:=region_detail->region_id;
        physical:=(node_value->>'slots')::bigint>(node_value->>'used')::bigint;
        pinned_allowed:=physical;
        legacy_allowed:=physical AND (row_value->>'available_slots')::bigint>(row_value->>'reserved_replicas')::bigint;
        required:=(row_value->>'reserved_replicas')::bigint; fleet:=(row_value->>'fleet_slots')::bigint;
        IF data->>'state'='protected' AND required>0 THEN
            cell_capacity:=(row_value->>'legacy_placement_capacity')::bigint; legacy:=(row_value->>'legacy_resident_replicas')::bigint;
            needed_here:=greatest(legacy,legacy_required-(total_legacy_capacity-cell_capacity));
            minimum:=greatest((node_value->>'legacy_used')::bigint,needed_here-(fleet-(node_value->>'slots')::bigint-required));
            pinned_allowed:=physical AND minimum<(node_value->>'slots')::bigint-((node_value->>'used')::bigint-(node_value->>'legacy_used')::bigint);
            next_population:=greatest(legacy+1,legacy_required-(total_legacy_capacity-cell_capacity)); needed:=0;
            FOR peer_key,peer_value IN SELECT * FROM jsonb_each(row_value->'nodes') LOOP
                lower:=(peer_value->>'legacy_used')::bigint;
                IF peer_key=node_key THEN lower:=lower+1; END IF;
                needed:=needed+greatest(lower,next_population-(fleet-(peer_value->>'slots')::bigint-required));
            END LOOP;
            legacy_allowed:=physical AND next_population<=cell_capacity AND needed<=next_population;
        END IF;
        data:=jsonb_set(data,ARRAY['placement',node_key,'pinned_allowed'],to_jsonb(pinned_allowed));
        data:=jsonb_set(data,ARRAY['placement',node_key,'legacy_allowed'],to_jsonb(legacy_allowed));
    END LOOP;
    RETURN data;
END;
$$;

CREATE OR REPLACE FUNCTION service_capacity_after_write() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE before_text text; prior jsonb; next jsonb; k text; value jsonb; grew boolean := false; excess boolean; physical_growth boolean:=false; service_growth boolean:=false; prior_region jsonb;
BEGIN
    before_text := current_setting('gregale.capacity_' || TG_TABLE_NAME || '_' || pg_trigger_depth()::text,true);
    IF coalesce(before_text,'')='' THEN RETURN NULL; END IF;
    prior:=before_text::jsonb; next:=service_capacity_snapshot();
    FOR k,value IN SELECT * FROM jsonb_each(next->'demands') LOOP
        IF (value->>'count')::bigint>0 AND (
            NOT (prior->'demands' ? k) OR
            (value->>'count')::bigint>coalesce((prior->'demands'->k->>'count')::bigint,0) OR
            (value->>'ram')::bigint>(prior->'demands'->k->>'ram')::bigint OR
            (value->>'cpu')::bigint>(prior->'demands'->k->>'cpu')::bigint OR
            (value->>'vcpu')::bigint>(prior->'demands'->k->>'vcpu')::bigint) THEN grew:=true; END IF;
    END LOOP;
    FOR k,value IN SELECT * FROM jsonb_each(next->'resident') LOOP
        IF (value->>'ram')::bigint>coalesce((prior->'resident'->k->>'ram')::bigint,0) OR
           (value->>'cpu')::bigint>coalesce((prior->'resident'->k->>'cpu')::bigint,0) OR
           (value->>'vcpu')::bigint>coalesce((prior->'resident'->k->>'vcpu')::bigint,0) THEN
            physical_growth:=true;
            IF NOT (next->'other' ? k) THEN
                RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='service_capacity_protection',MESSAGE='node is not ready for protected admission';
            END IF;
            IF (next->'other'->k->>'ram')::bigint>coalesce((prior->'other'->k->>'ram')::bigint,0) OR
               (next->'other'->k->>'cpu')::bigint>coalesce((prior->'other'->k->>'cpu')::bigint,0) OR
               (next->'other'->k->>'vcpu')::bigint>coalesce((prior->'other'->k->>'vcpu')::bigint,0) THEN grew:=true; END IF;
        END IF;
    END LOOP;
    -- Instance role changes cannot remove declarations. Ordinary growth from
    -- these writes must preserve headroom; removing app/deployment intent may
    -- reclassify resident guests without blocking stops.
    IF TG_TABLE_NAME='instances' THEN
        FOR k,value IN SELECT * FROM jsonb_each(next->'other') LOOP
            IF (value->>'ram')::bigint>coalesce((prior->'other'->k->>'ram')::bigint,0) OR
               (value->>'cpu')::bigint>coalesce((prior->'other'->k->>'cpu')::bigint,0) OR
               (value->>'vcpu')::bigint>coalesce((prior->'other'->k->>'vcpu')::bigint,0) THEN grew:=true; END IF;
        END LOOP;
    END IF;
    -- Reclassification can occupy service slots without allocating resources.
    -- Include ineligible hosts here, even though placement excludes them.
    FOR k,value IN SELECT * FROM jsonb_each(next->'service_usage') LOOP
        IF value::bigint>coalesce((prior->'service_usage'->>k)::bigint,0) THEN
            service_growth:=true;
            IF NOT (next->'placement' ? k) THEN
                RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='service_capacity_protection',MESSAGE='node is not ready for protected admission';
            END IF;
        END IF;
    END LOOP;
    IF (next->>'replica_ram_mb')::bigint>(prior->>'replica_ram_mb')::bigint OR
       (next->>'replica_cpu_millicores')::bigint>(prior->>'replica_cpu_millicores')::bigint OR
       (next->>'replica_vcpu')::bigint>(prior->>'replica_vcpu')::bigint THEN grew:=true; END IF;
    SELECT EXISTS (SELECT 1 FROM jsonb_each_text(next->'actual') a
                    WHERE a.value::bigint>coalesce((next->'demands'->a.key->>'count')::bigint,0)
                      AND a.value::bigint>coalesce((prior->'actual'->>a.key)::bigint,0)) INTO excess;
    -- Service recovery spends an existing declaration even after a host has
    -- failed. New intent, bursts, mirrors, jobs and excess service replicas
    -- must leave the fleet protected. Releases and unrelated updates pass.
    IF service_growth AND prior->>'state'='protected' AND next->>'state'<>'protected' THEN
        RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='service_capacity_protection',
            MESSAGE='placement would consume regional recovery capacity';
    END IF;
    IF physical_growth OR service_growth THEN
        FOR value IN SELECT * FROM jsonb_array_elements(next->'regions') LOOP
            SELECT regions.region INTO prior_region FROM jsonb_array_elements(prior->'regions') AS regions(region)
             WHERE regions.region->>'compute_region'=value->>'compute_region';
            IF (value->>'available_slots')::bigint<(value->>'reserved_replicas')::bigint
               AND (value->>'available_slots')::bigint<coalesce((prior_region->>'available_slots')::bigint,0) THEN
                RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='service_capacity_protection',
                    MESSAGE='placement would block declared regional recovery';
            END IF;
        END LOOP;
    END IF;
    IF ((grew OR excess) AND next->>'state'<>'protected') OR
       (NOT (next->>'placements_fit')::boolean AND (physical_growth OR service_growth)) THEN
        RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='service_capacity_protection',
            MESSAGE='service admission would consume bare-metal recovery capacity';
    END IF;
    RETURN NULL;
END;
$$;

DROP TRIGGER IF EXISTS service_capacity_nodes_lock ON compute_nodes;
CREATE TRIGGER service_capacity_nodes_lock BEFORE INSERT OR UPDATE OF region,lifecycle,admission_ceiling_mb,vpcpus,vcpu_budget,last_heartbeat_at OR DELETE ON compute_nodes FOR EACH STATEMENT EXECUTE FUNCTION service_capacity_before_write('lock_only');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    PERFORM singleton FROM service_capacity_policy WHERE singleton FOR UPDATE;
    IF EXISTS(SELECT 1 FROM service_capacity_policy WHERE singleton AND enabled) THEN
        RAISE EXCEPTION 'disable service capacity protection before discarding regional certificates';
    END IF;
END; $$;
CREATE OR REPLACE FUNCTION service_capacity_snapshot() RETURNS jsonb
LANGUAGE sql VOLATILE AS $$
WITH policy AS (SELECT * FROM service_capacity_policy WHERE singleton),
eligible AS (
    SELECT a.id, a.ram_mb, a.cpu_millicores, a.manifest,
           coalesce((p.plan_vcpus->>ac.plan)::integer, 4) AS vcpu
      FROM apps a JOIN accounts ac ON ac.id=a.account_id CROSS JOIN policy p
     WHERE a.status IN ('active','evicted_cold') AND a.manifest->>'execution_mode'='service'
), scopes AS (
    SELECT id AS app_id, 'default'::text AS scope FROM eligible
    UNION
    SELECT d.app_id, d.scope FROM deployments d JOIN eligible a ON a.id=d.app_id
     WHERE d.status IN ('pending','building','imaging','snapshotting','live')
), declaration AS (
    SELECT s.app_id::text || ':' || s.scope AS key,
           greatest(0, coalesce((a.manifest->'service_replicas'->>'desired')::integer,1)) AS desired,
           CASE WHEN EXISTS (SELECT 1 FROM deployments d WHERE d.app_id=a.id AND d.scope=s.scope AND d.status='live')
                  AND (EXISTS (SELECT 1 FROM deployments d WHERE d.app_id=a.id AND d.scope=s.scope AND d.status IN ('pending','building','imaging','snapshotting'))
                    OR (SELECT count(*) FROM deployments d WHERE d.app_id=a.id AND d.scope=s.scope AND d.status='live')>1)
                THEN 1 ELSE 0 END AS surge,
           a.ram_mb+p.overhead_mb+coalesce((
               SELECT max((SELECT coalesce(sum(greatest(0,(sc->>'ram_mb')::integer)),0)
                             FROM jsonb_array_elements(coalesce(d.sidecars,'[]'::jsonb)) sc))
                 FROM deployments d WHERE d.app_id=a.id AND d.scope=s.scope
                   AND d.status IN ('pending','building','imaging','snapshotting','live')),0) AS ram,
           greatest(a.cpu_millicores,p.startup_cpu)+coalesce((
               SELECT max((SELECT coalesce(sum(greatest(0,coalesce(nullif(greatest(0,(sc->>'cpu_millicores')::integer),0),p.startup_cpu))),0)
                             FROM jsonb_array_elements(coalesce(d.sidecars,'[]'::jsonb)) sc))
                 FROM deployments d WHERE d.app_id=a.id AND d.scope=s.scope
                   AND d.status IN ('pending','building','imaging','snapshotting','live')),0) AS cpu,
           a.vcpu
      FROM scopes s JOIN eligible a ON a.id=s.app_id CROSS JOIN policy p
), demand AS (
    SELECT key, CASE WHEN desired=0 THEN 0 ELSE desired+surge END AS count, desired, ram, cpu, vcpu
      FROM declaration
), resident AS (
    SELECT i.node_id, a.id::text || ':' || coalesce(d.scope,'default') AS key,
           greatest(i.capacity_ram_mb,i.ram_mb+p.overhead_mb+coalesce((SELECT sum(greatest(0,(sc->>'ram_mb')::integer))
                           FROM jsonb_array_elements(coalesce(d.sidecars,'[]'::jsonb)) sc),0)) AS ram,
           greatest(i.capacity_cpu_millicores,greatest(coalesce(a.cpu_millicores,p.startup_cpu),p.startup_cpu)+coalesce((
               SELECT sum(greatest(0,coalesce(nullif(greatest(0,(sc->>'cpu_millicores')::integer),0),p.startup_cpu)))
                 FROM jsonb_array_elements(coalesce(d.sidecars,'[]'::jsonb)) sc),0)) AS cpu,
           greatest(i.capacity_vcpu,CASE WHEN i.app_id IS NULL THEN 1 ELSE coalesce((p.plan_vcpus->>ac.plan)::integer,4) END) AS vcpu,
           (i.state<>'warm' AND i.mode IN ('normal','service') AND a.manifest->>'execution_mode'='service'
              AND EXISTS (SELECT 1 FROM demand x WHERE x.key=a.id::text || ':' || coalesce(d.scope,'default') )) AS service
      FROM instances i LEFT JOIN apps a ON a.id=i.app_id LEFT JOIN accounts ac ON ac.id=a.account_id
      LEFT JOIN deployments d ON d.id=i.deployment_id CROSS JOIN policy p
     WHERE i.state IN ('waking','cold_booting','running','draining','warm','snapshotting','migrating')
), shape AS (
    SELECT greatest(coalesce((SELECT max(ram) FROM demand WHERE count>0),0),coalesce((SELECT max(ram) FROM resident WHERE service),0),1) AS ram,
           greatest(coalesce((SELECT max(cpu) FROM demand WHERE count>0),0),coalesce((SELECT max(cpu) FROM resident WHERE service),0),1) AS cpu,
           greatest(coalesce((SELECT max(vcpu) FROM demand WHERE count>0),0),coalesce((SELECT max(vcpu) FROM resident WHERE service),0),1) AS vcpu
), other_usage AS (
    SELECT node_id, sum(ram)::bigint AS ram, sum(cpu)::bigint AS cpu, sum(vcpu)::bigint AS vcpu
      FROM resident WHERE NOT coalesce(service,false) GROUP BY node_id
), service_usage AS (
    SELECT node_id, count(*)::bigint AS count FROM resident WHERE service GROUP BY node_id
), nodes AS (
    SELECT n.id, n.name, coalesce(u.ram,0) AS other_ram, coalesce(u.cpu,0) AS other_cpu, coalesce(u.vcpu,0) AS other_vcpu,
           coalesce(s.count,0) AS service_count,
           (coalesce(u.ram,0)<=n.admission_ceiling_mb AND coalesce(u.cpu,0)<=n.vpcpus::bigint*1000*p.cpu_overcommit AND coalesce(u.vcpu,0)<=n.vcpu_budget) AS ordinary_fit,
           greatest(0,least((n.admission_ceiling_mb-coalesce(u.ram,0))/sh.ram,
                       (n.vpcpus::bigint*1000*p.cpu_overcommit-coalesce(u.cpu,0))/sh.cpu,
                       (n.vcpu_budget-coalesce(u.vcpu,0))/sh.vcpu))::bigint AS slots
      FROM compute_nodes n CROSS JOIN policy p CROSS JOIN shape sh
      LEFT JOIN other_usage u ON u.node_id=n.id LEFT JOIN service_usage s ON s.node_id=n.id
     WHERE n.lifecycle='active' AND n.admission_ceiling_mb>0 AND n.vpcpus>0 AND n.vcpu_budget>0
       AND n.last_heartbeat_at>=clock_timestamp()-make_interval(secs=>p.heartbeat_seconds)
), totals AS (
    SELECT count(*) AS healthy_nodes, coalesce(sum(slots),0) AS fleet_slots,
           coalesce(sum(slots)-max(slots),0) AS failover_slots,
           coalesce(bool_and(service_count<=slots AND ordinary_fit),true) AS placements_fit FROM nodes
), required AS (SELECT coalesce(sum(count),0) AS count, coalesce(sum(desired),0) AS desired FROM demand),
actual_targets AS (SELECT key,count(*) AS count FROM resident WHERE service GROUP BY key),
projection AS (
    SELECT jsonb_build_object(
        'enabled',p.enabled,
        'state',CASE WHEN NOT p.enabled THEN 'disabled'
                     WHEN t.healthy_nodes>=2 AND t.placements_fit AND r.count<=t.failover_slots
                       AND NOT EXISTS (SELECT 1 FROM actual_targets a JOIN demand d ON d.key=a.key WHERE a.count>d.count) THEN 'protected'
                     WHEN t.placements_fit AND r.count<=t.fleet_slots THEN 'degraded' ELSE 'needs_hardware' END,
        'healthy_nodes',t.healthy_nodes,'desired_replicas',r.desired,'reserved_replicas',r.count,
        'replica_ram_mb',sh.ram,'replica_cpu_millicores',sh.cpu,'replica_vcpu',sh.vcpu,
        'fleet_slots',t.fleet_slots,'failover_slots',t.failover_slots,'placements_fit',t.placements_fit,
        'demands',coalesce((SELECT jsonb_object_agg(key,jsonb_build_object('count',count,'ram',ram,'cpu',cpu,'vcpu',vcpu)) FROM demand),'{}'::jsonb),
        'other',coalesce((SELECT jsonb_object_agg(id::text,jsonb_build_object('ram',other_ram,'cpu',other_cpu,'vcpu',other_vcpu)) FROM nodes),'{}'::jsonb),
        'placement',coalesce((SELECT jsonb_object_agg(id::text,jsonb_build_object('slots',slots,'used',service_count)) FROM nodes),'{}'::jsonb),
        'service_usage',coalesce((SELECT jsonb_object_agg(node_id::text,count) FROM service_usage),'{}'::jsonb),
        'resident',coalesce((SELECT jsonb_object_agg(node_id::text,cost) FROM (SELECT node_id,jsonb_build_object('ram',sum(ram),'cpu',sum(cpu),'vcpu',sum(vcpu)) AS cost FROM resident GROUP BY node_id) u),'{}'::jsonb),
        'actual',coalesce((SELECT jsonb_object_agg(key,count) FROM actual_targets),'{}'::jsonb)
    ) AS data FROM policy p CROSS JOIN totals t CROSS JOIN required r CROSS JOIN shape sh
) SELECT data FROM projection;
$$;

CREATE OR REPLACE FUNCTION service_capacity_after_write() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE before_text text; prior jsonb; next jsonb; k text; value jsonb; grew boolean := false; excess boolean; physical_growth boolean:=false; service_growth boolean:=false;
BEGIN
    before_text := current_setting('gregale.capacity_' || TG_TABLE_NAME || '_' || pg_trigger_depth()::text,true);
    IF coalesce(before_text,'')='' THEN RETURN NULL; END IF;
    prior:=before_text::jsonb; next:=service_capacity_snapshot();
    FOR k,value IN SELECT * FROM jsonb_each(next->'demands') LOOP
        IF (value->>'count')::bigint>0 AND (
            NOT (prior->'demands' ? k) OR
            (value->>'count')::bigint>coalesce((prior->'demands'->k->>'count')::bigint,0) OR
            (value->>'ram')::bigint>(prior->'demands'->k->>'ram')::bigint OR
            (value->>'cpu')::bigint>(prior->'demands'->k->>'cpu')::bigint OR
            (value->>'vcpu')::bigint>(prior->'demands'->k->>'vcpu')::bigint) THEN grew:=true; END IF;
    END LOOP;
    FOR k,value IN SELECT * FROM jsonb_each(next->'resident') LOOP
        IF (value->>'ram')::bigint>coalesce((prior->'resident'->k->>'ram')::bigint,0) OR
           (value->>'cpu')::bigint>coalesce((prior->'resident'->k->>'cpu')::bigint,0) OR
           (value->>'vcpu')::bigint>coalesce((prior->'resident'->k->>'vcpu')::bigint,0) THEN
            physical_growth:=true;
            IF NOT (next->'other' ? k) THEN
                RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='service_capacity_protection',MESSAGE='node is not ready for protected admission';
            END IF;
            IF (next->'other'->k->>'ram')::bigint>coalesce((prior->'other'->k->>'ram')::bigint,0) OR
               (next->'other'->k->>'cpu')::bigint>coalesce((prior->'other'->k->>'cpu')::bigint,0) OR
               (next->'other'->k->>'vcpu')::bigint>coalesce((prior->'other'->k->>'vcpu')::bigint,0) THEN grew:=true; END IF;
        END IF;
    END LOOP;
    -- Instance role changes cannot remove declarations. Ordinary growth from
    -- these writes must preserve headroom; removing app/deployment intent may
    -- reclassify resident guests without blocking stops.
    IF TG_TABLE_NAME='instances' THEN
        FOR k,value IN SELECT * FROM jsonb_each(next->'other') LOOP
            IF (value->>'ram')::bigint>coalesce((prior->'other'->k->>'ram')::bigint,0) OR
               (value->>'cpu')::bigint>coalesce((prior->'other'->k->>'cpu')::bigint,0) OR
               (value->>'vcpu')::bigint>coalesce((prior->'other'->k->>'vcpu')::bigint,0) THEN grew:=true; END IF;
        END LOOP;
    END IF;
    -- Reclassification can occupy service slots without allocating resources.
    -- Include ineligible hosts here, even though placement excludes them.
    FOR k,value IN SELECT * FROM jsonb_each(next->'service_usage') LOOP
        IF value::bigint>coalesce((prior->'service_usage'->>k)::bigint,0) THEN
            service_growth:=true;
            IF NOT (next->'placement' ? k) THEN
                RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='service_capacity_protection',MESSAGE='node is not ready for protected admission';
            END IF;
        END IF;
    END LOOP;
    IF (next->>'replica_ram_mb')::bigint>(prior->>'replica_ram_mb')::bigint OR
       (next->>'replica_cpu_millicores')::bigint>(prior->>'replica_cpu_millicores')::bigint OR
       (next->>'replica_vcpu')::bigint>(prior->>'replica_vcpu')::bigint THEN grew:=true; END IF;
    SELECT EXISTS (SELECT 1 FROM jsonb_each_text(next->'actual') a
                    WHERE a.value::bigint>coalesce((next->'demands'->a.key->>'count')::bigint,0)
                      AND a.value::bigint>coalesce((prior->'actual'->>a.key)::bigint,0)) INTO excess;
    -- Service recovery spends an existing declaration even after a host has
    -- failed. New intent, bursts, mirrors, jobs and excess service replicas
    -- must leave the fleet protected. Releases and unrelated updates pass.
    IF ((grew OR excess) AND next->>'state'<>'protected') OR
       (NOT (next->>'placements_fit')::boolean AND (physical_growth OR service_growth)) THEN
        RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='service_capacity_protection',
            MESSAGE='service admission would consume bare-metal recovery capacity';
    END IF;
    RETURN NULL;
END;
$$;

DROP TRIGGER IF EXISTS service_capacity_nodes_lock ON compute_nodes;
CREATE TRIGGER service_capacity_nodes_lock BEFORE INSERT OR UPDATE OF lifecycle,admission_ceiling_mb,vpcpus,vcpu_budget,last_heartbeat_at OR DELETE ON compute_nodes FOR EACH STATEMENT EXECUTE FUNCTION service_capacity_before_write('lock_only');
DROP FUNCTION service_capacity_snapshot_inputs();
DROP FUNCTION service_capacity_region_legacy_limit(jsonb,bigint);
-- +goose StatementEnd

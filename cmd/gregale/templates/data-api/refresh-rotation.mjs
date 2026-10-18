import pg from 'pg'
import { createHash } from 'node:crypto'
import { pathToFileURL } from 'node:url'
import { databaseURL, limits, schemaNames } from './config.mjs'
import { inspectClient, fingerprint } from './types.mjs'
import { checkSQLDeadlineClient } from './sql-deadline.mjs'

const hash = /^[a-f0-9]{64}$/
const digest = value => createHash('sha256').update(value).digest('hex')
const quote = value => '"' + value.replaceAll('"','""') + '"'
const groupKey = row => `gregale-data-api-binding:${row.scope}:${row.binding_id}`

// All four exact bodies are pinned by the provider. Only the first two are
// executable by serving roles; preparation and catalog witnesses are owner-only.
const rotationCheckBody = `
DECLARE binding text := current_setting('role', true); member record; gate record; claims jsonb;
BEGIN
  IF current_setting('transaction_isolation') <> 'read committed' THEN
    RAISE SQLSTATE 'PT503' USING MESSAGE='data_api_refresh_isolation_unsupported';
  END IF;
  IF binding IS NULL OR binding='none' THEN binding:=session_user; END IF;
  SELECT * INTO member FROM gregale_data_api.refresh_rotation_members WHERE role_name=binding AND role_oid=to_regrole(binding)::oid;
  IF FOUND THEN
    PERFORM pg_advisory_xact_lock_shared(hashtextextended('gregale-data-api-binding:'||member.scope||':'||member.binding_id::text,0));
    SELECT b.paused,b.token_hash,m.fingerprint INTO gate FROM gregale_data_api.refresh_rotation_bindings b
      JOIN gregale_data_api.refresh_rotation_members m USING(binding_id,scope)
      WHERE m.role_name=binding AND m.role_oid=to_regrole(binding)::oid;
  ELSE
    PERFORM pg_advisory_xact_lock_shared(hashtextextended('gregale-data-api:'||binding,0));
    SELECT paused,token_hash,fingerprint INTO gate FROM gregale_data_api.refresh_gate WHERE role_name=binding;
  END IF;
  IF NOT FOUND THEN RAISE SQLSTATE 'PT503' USING MESSAGE='data_api_refresh_unavailable'; END IF;
  claims:=coalesce(nullif(current_setting('request.jwt.claims',true),''),'{}')::jsonb;
  IF gate.paused THEN
    IF gate.token_hash IS NULL OR claims->>'gregale_refresh_hash' IS DISTINCT FROM gate.token_hash THEN
      RAISE SQLSTATE 'PT503' USING MESSAGE='data_api_refresh_paused';
    END IF;
  ELSIF gate.fingerprint IS NULL THEN
    RAISE SQLSTATE 'PT503' USING MESSAGE='data_api_refresh_unavailable';
  ELSIF claims->>'gregale_serving_fingerprint' IS DISTINCT FROM gate.fingerprint THEN
    RAISE SQLSTATE 'PT409' USING MESSAGE='schema_contract_mismatch';
  END IF;
END`

const rotationStatusBody = `
  SELECT b.paused,m.fingerprint,b.paused AND b.token_hash=candidate_hash
  FROM gregale_data_api.refresh_rotation_members m JOIN gregale_data_api.refresh_rotation_bindings b USING(binding_id,scope)
  WHERE m.role_name=session_user AND m.role_oid=to_regrole(session_user)::oid
  UNION ALL
  SELECT g.paused,g.fingerprint,g.paused AND g.token_hash=candidate_hash FROM gregale_data_api.refresh_gate g
  WHERE g.role_name=session_user AND NOT EXISTS(SELECT 1 FROM gregale_data_api.refresh_rotation_members m WHERE m.role_name=session_user)`

// Compare structural catalog metadata and effective privileges, never raw ACLs
// or role names. Role creation/grants and DML/statistics cannot change this
// witness. Uncoordinated DDL conservatively requires owner re-verification.
const rotationWitnessBody = `
SELECT encode(sha256(convert_to(jsonb_build_object(
 'schemas',(SELECT jsonb_agg(jsonb_build_array(n.oid,n.nspname,n.nspowner,has_schema_privilege(subject,n.oid,'USAGE'),has_schema_privilege(subject,n.oid,'CREATE')) ORDER BY n.oid) FROM pg_namespace n WHERE n.nspname !~ '^pg_(temp|toast)'),
 'relations',(SELECT jsonb_agg(jsonb_build_array(c.oid,c.relnamespace,c.relname,c.relkind,c.relowner,c.relrowsecurity,c.relforcerowsecurity,
   CASE WHEN c.relkind='S' THEN ARRAY[has_sequence_privilege(subject,c.oid,'SELECT'),has_sequence_privilege(subject,c.oid,'USAGE'),has_sequence_privilege(subject,c.oid,'UPDATE')]
   ELSE ARRAY[has_table_privilege(subject,c.oid,'SELECT'),has_table_privilege(subject,c.oid,'INSERT'),has_table_privilege(subject,c.oid,'UPDATE'),has_table_privilege(subject,c.oid,'DELETE')] END) ORDER BY c.oid)
   FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.relkind IN ('r','p','v','m','f','S') AND n.nspname !~ '^pg_(temp|toast)'),
 'columns',(SELECT jsonb_agg(jsonb_build_array(a.attrelid,a.attnum,a.attname,a.atttypid,a.atttypmod,a.attnotnull,a.attidentity,a.attgenerated,a.attisdropped,col_description(a.attrelid,a.attnum),
   has_column_privilege(subject,a.attrelid,a.attnum,'SELECT'),has_column_privilege(subject,a.attrelid,a.attnum,'INSERT'),has_column_privilege(subject,a.attrelid,a.attnum,'UPDATE')) ORDER BY a.attrelid,a.attnum)
   FROM pg_attribute a JOIN pg_class c ON c.oid=a.attrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.relkind IN ('r','p','v','m','f') AND a.attnum>0 AND NOT a.attisdropped AND n.nspname !~ '^pg_(temp|toast)'),
 'types',(SELECT jsonb_agg(jsonb_build_array(t.oid,t.typnamespace,t.typname,t.typtype,t.typbasetype,t.typelem,t.typrelid,t.typnotnull,has_type_privilege(subject,t.oid,'USAGE')) ORDER BY t.oid) FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace WHERE n.nspname !~ '^pg_(temp|toast)'),
 'functions',(SELECT jsonb_agg(jsonb_build_array(p.oid,p.pronamespace,p.proname,p.proowner,p.prokind,p.prorettype,p.proretset,p.proargtypes::text,p.proallargtypes,p.proargnames,p.proargmodes,p.pronargdefaults,p.provariadic,p.provolatile,p.prosecdef,p.proisstrict,p.proleakproof,p.proparallel,p.prorows,p.proconfig,p.prosrc,p.prosqlbody::text,p.proargdefaults::text,obj_description(p.oid,'pg_proc'),has_function_privilege(subject,p.oid,'EXECUTE')) ORDER BY p.oid) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname !~ '^pg_(temp|toast)'),
 'defaults',(SELECT jsonb_agg(to_jsonb(d) ORDER BY d.oid) FROM pg_attrdef d),
 'constraints',(SELECT jsonb_agg(to_jsonb(c) ORDER BY c.oid) FROM pg_constraint c),
 'enums',(SELECT jsonb_agg(to_jsonb(e) ORDER BY e.oid) FROM pg_enum e),
 'rewrites',(SELECT jsonb_agg(to_jsonb(r) ORDER BY r.oid) FROM pg_rewrite r),
 'triggers',(SELECT jsonb_agg(to_jsonb(t) ORDER BY t.oid) FROM pg_trigger t),
 'policies',(SELECT jsonb_agg(jsonb_build_array(to_jsonb(p),0=ANY(p.polroles) OR (subject::regrole)::oid=ANY(p.polroles)) ORDER BY p.oid) FROM pg_policy p)
)::text,'UTF8')),'hex')`

const rotationPrepareBody = `
DECLARE policy record; source record; existing record; routine oid; login_oid oid;
BEGIN
  PERFORM pg_advisory_xact_lock(hashtextextended('gregale-data-api-binding:'||resource_scope||':'||binding::text,0));
  SELECT * INTO policy FROM gregale_data_api.refresh_rotation_bindings WHERE binding_id=binding AND scope=resource_scope FOR UPDATE;
  IF NOT FOUND OR policy.version<>1 THEN RAISE SQLSTATE '55000' USING MESSAGE='data_api_refresh_preparation_conflict'; END IF;
  SELECT m.* INTO source FROM gregale_data_api.refresh_rotation_members m JOIN pg_roles r ON r.rolname=m.role_name AND r.oid=m.role_oid
    WHERE m.role_name=policy.source_role AND m.binding_id=binding AND m.scope=resource_scope AND r.rolcanlogin;
  IF NOT FOUND OR source.catalog_witness<>gregale_data_api.refresh_catalog_witness(source.role_name::name) THEN
    RAISE SQLSTATE '55000' USING MESSAGE='data_api_refresh_preparation_conflict';
  END IF;
  login_oid:=to_regrole(candidate)::oid;
  IF login_oid IS NULL THEN RAISE SQLSTATE '55000' USING MESSAGE='data_api_refresh_preparation_conflict'; END IF;
  EXECUTE format('GRANT USAGE ON SCHEMA gregale_data_api TO %I',candidate);
  EXECUTE format('GRANT EXECUTE ON FUNCTION gregale_data_api.check_request(),gregale_data_api.refresh_status(text) TO %I',candidate);
  IF policy.sql_deadline THEN
    EXECUTE format('GRANT EXECUTE ON FUNCTION gregale_data_api.enforce_deadline(),gregale_data_api.check_deadline_request() TO %I',candidate);
  END IF;
  FOREACH routine IN ARRAY source.rpcs LOOP
    IF NOT EXISTS(SELECT 1 FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE p.oid=routine AND n.nspname='api' AND p.prokind='f' AND NOT p.prosecdef AND p.proowner=(current_user::regrole)::oid AND obj_description(p.oid,'pg_proc')='@gregale:rpc') THEN
      RAISE SQLSTATE '55000' USING MESSAGE='data_api_refresh_preparation_conflict';
    END IF;
    EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO %I',routine::regprocedure,candidate);
  END LOOP;
  IF gregale_data_api.refresh_catalog_witness(candidate)<>source.catalog_witness THEN
    RAISE SQLSTATE '55000' USING MESSAGE='data_api_refresh_preparation_conflict';
  END IF;
  INSERT INTO gregale_data_api.refresh_rotation_members(role_name,role_oid,binding_id,scope,fingerprint,catalog_witness,rpcs)
    VALUES(candidate,login_oid,binding,resource_scope,source.fingerprint,source.catalog_witness,source.rpcs) ON CONFLICT DO NOTHING;
  SELECT * INTO existing FROM gregale_data_api.refresh_rotation_members WHERE role_name=candidate;
  IF existing.role_oid<>login_oid OR existing.binding_id<>binding OR existing.scope<>resource_scope OR existing.fingerprint<>source.fingerprint OR existing.catalog_witness<>source.catalog_witness OR existing.rpcs<>source.rpcs THEN
    RAISE SQLSTATE '55000' USING MESSAGE='data_api_refresh_preparation_conflict';
  END IF;
  UPDATE gregale_data_api.refresh_rotation_bindings SET source_role=candidate WHERE binding_id=binding AND scope=resource_scope;
END`

const hooks = [
  ['check_request','', 'void','plpgsql',true,rotationCheckBody],
  ['refresh_status','candidate_hash text','TABLE(paused boolean,fingerprint text,owns_pause boolean)','sql',true,rotationStatusBody],
  ['refresh_catalog_witness','subject name','text','sql',false,rotationWitnessBody],
  ['prepare_refresh_member','binding uuid,resource_scope text,candidate name','void','plpgsql',false,rotationPrepareBody],
]

export const rotationTablesDDL = `
CREATE TABLE IF NOT EXISTS gregale_data_api.refresh_rotation_bindings(
 binding_id uuid NOT NULL,scope text NOT NULL,version integer NOT NULL,source_role text NOT NULL,sql_deadline boolean NOT NULL,
 paused boolean NOT NULL,token_hash text,PRIMARY KEY(binding_id,scope),
 CHECK(version=1),CHECK((paused AND token_hash ~ '^[a-f0-9]{64}$' AND token_hash IS NOT NULL) OR (NOT paused AND token_hash IS NULL)));
CREATE TABLE IF NOT EXISTS gregale_data_api.refresh_rotation_members(
 role_name text PRIMARY KEY,role_oid oid NOT NULL,binding_id uuid NOT NULL,scope text NOT NULL,fingerprint text NOT NULL,catalog_witness text NOT NULL,rpcs oid[] NOT NULL,
 FOREIGN KEY(binding_id,scope) REFERENCES gregale_data_api.refresh_rotation_bindings(binding_id,scope),
 CHECK(fingerprint ~ '^[a-f0-9]{64}$'),CHECK(catalog_witness ~ '^[a-f0-9]{64}$'));`

const shapes = {
  refresh_rotation_bindings: ['binding_id:uuid:true','scope:text:true','version:integer:true','source_role:text:true','sql_deadline:boolean:true','paused:boolean:true','token_hash:text:false'],
  refresh_rotation_members: ['role_name:text:true','role_oid:oid:true','binding_id:uuid:true','scope:text:true','fingerprint:text:true','catalog_witness:text:true','rpcs:oid[]:true'],
}

export async function rotationInstalled(client) {
  return (await client.query("SELECT EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='gregale_data_api' AND c.relname IN ('refresh_rotation_bindings','refresh_rotation_members')) AS installed")).rows[0].installed
}

async function verifyTables(client) {
  for (const [name, shape] of Object.entries(shapes)) {
    const result = await client.query(`SELECT c.relkind='r' AND c.relpersistence='p' AND NOT c.relrowsecurity AND NOT c.relforcerowsecurity
      AND c.relowner=n.nspowner AND c.relowner=(current_user::regrole)::oid
      AND NOT EXISTS(SELECT 1 FROM aclexplode(coalesce(c.relacl,acldefault('r',c.relowner))) a WHERE a.grantee<>c.relowner)
      AND NOT EXISTS(SELECT 1 FROM pg_attribute a,LATERAL aclexplode(a.attacl) x WHERE a.attrelid=c.oid AND x.grantee<>c.relowner)
      AND NOT EXISTS(SELECT 1 FROM aclexplode(coalesce(n.nspacl,acldefault('n',n.nspowner))) a WHERE a.grantee=0)
      AND NOT EXISTS(SELECT 1 FROM pg_inherits WHERE inhrelid=c.oid OR inhparent=c.oid)
      AND (SELECT array_agg(a.attname::text||':'||a.atttypid::regtype::text||':'||a.attnotnull::text ORDER BY a.attnum) FROM pg_attribute a WHERE a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped)=$2::text[]
      AND EXISTS(SELECT 1 FROM pg_constraint p WHERE p.conrelid=c.oid AND p.contype='p' AND p.convalidated AND p.conkey=CASE WHEN $1='refresh_rotation_bindings' THEN ARRAY[1,2]::smallint[] ELSE ARRAY[1]::smallint[] END) AS valid
      FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='gregale_data_api' AND c.relname=$1`, [name,shape])
    if (result.rows[0]?.valid !== true) throw new Error('Refresh rotation table ownership, shape or permissions drifted')
  }
}

async function verifyHooks(client) {
  const result = await client.query(`SELECT p.proname,p.prosrc,l.lanname,p.prosecdef,p.prokind='f' AND p.provolatile='v' AND p.proparallel='u'
    AND NOT p.proisstrict AND NOT p.proleakproof AND p.pronargdefaults=0 AND p.provariadic=0 AND p.proretset=(p.proname='refresh_status')
    AND p.prorettype=CASE p.proname WHEN 'refresh_status' THEN 'record'::regtype WHEN 'refresh_catalog_witness' THEN 'text'::regtype ELSE 'void'::regtype END
    AND p.oid IN(to_regprocedure('gregale_data_api.check_request()'),to_regprocedure('gregale_data_api.refresh_status(text)'),to_regprocedure('gregale_data_api.refresh_catalog_witness(name)'),to_regprocedure('gregale_data_api.prepare_refresh_member(uuid,text,name)'))
    AND p.proconfig=ARRAY['search_path=pg_catalog'] AND p.proowner=n.nspowner AND p.proowner=(current_user::regrole)::oid
    AND NOT EXISTS(SELECT 1 FROM aclexplode(coalesce(p.proacl,acldefault('f',p.proowner))) a WHERE a.grantee=0 AND a.privilege_type='EXECUTE')
    AND (p.proname IN ('check_request','refresh_status') OR NOT EXISTS(SELECT 1 FROM aclexplode(coalesce(p.proacl,acldefault('f',p.proowner))) a WHERE a.grantee<>p.proowner AND a.privilege_type='EXECUTE')) AS valid
    FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_language l ON l.oid=p.prolang
    WHERE n.nspname='gregale_data_api' AND p.proname=ANY($1::text[])`, [hooks.map(h=>h[0])])
  if (result.rowCount !== hooks.length || result.rows.some(row => {
    const expected=hooks.find(h=>h[0]===row.proname)
    return !row.valid || row.prosrc!==expected[5] || row.lanname!==expected[3] || row.prosecdef!==expected[4]
  })) throw new Error('Protected refresh rotation hooks do not match')
}

async function memberContext(client, role) {
  if (!await rotationInstalled(client)) return null
  await verifyTables(client)
  await verifyHooks(client)
  const result=await client.query(`SELECT b.*,m.role_oid,m.fingerprint,m.catalog_witness FROM gregale_data_api.refresh_rotation_members m
    JOIN gregale_data_api.refresh_rotation_bindings b USING(binding_id,scope) WHERE m.role_name=$1 AND m.role_oid=to_regrole($1)::oid`,[role])
  return result.rows[0] ?? null
}

async function capture(client, role) {
  const snapshot=await inspectClient(client,['api'],{privilegeRole:role})
  const names=snapshot.functions.map(f=>f.name)
  const rpcs=(await client.query(`SELECT p.oid FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
    WHERE n.nspname='api' AND p.proname=ANY($1::text[]) AND p.proowner=(current_user::regrole)::oid AND NOT p.prosecdef AND obj_description(p.oid,'pg_proc')='@gregale:rpc' ORDER BY p.oid`,[names])).rows.map(r=>r.oid)
  if(rpcs.length!==names.length) throw new Error('Approved refresh RPCs must belong to the managed schema owner')
  const witness=(await client.query('SELECT gregale_data_api.refresh_catalog_witness($1::name) AS witness',[role])).rows[0].witness
  return { fingerprint:fingerprint(snapshot),witness,rpcs }
}

async function servingRole(client, role, scope) {
  const result=await client.query(`SELECT r.oid,r.rolcanlogin AND NOT(r.rolsuper OR r.rolcreatedb OR r.rolcreaterole OR r.rolreplication OR r.rolbypassrls OR r.rolinherit)
    AND NOT EXISTS(SELECT 1 FROM pg_auth_members WHERE member=r.oid)
    AND NOT has_database_privilege(r.oid,current_database(),'CREATE,TEMPORARY')
    AND NOT EXISTS(SELECT 1 FROM pg_namespace n WHERE n.nspname !~ '^pg_' AND has_schema_privilege(r.oid,n.oid,'CREATE'))
    AND shobj_description(r.oid,'pg_authid')=$2 AS valid FROM pg_roles r WHERE r.rolname=$1`,[role,`gregale:credential:v1:${scope}:data_api`])
  if(result.rows[0]?.valid!==true) throw new Error('Refresh rotation requires the restricted managed binding login')
  return result.rows[0].oid
}

async function deadlineMode(client,role,enabled) {
  if(enabled) { await checkSQLDeadlineClient(client,role,true); return }
  const result=await client.query(`SELECT EXISTS(SELECT 1 FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
    WHERE n.nspname='gregale_data_api' AND p.proname IN ('enforce_deadline','check_deadline_request') AND has_function_privilege($1::name,p.oid,'EXECUTE')) AS granted`,[role])
  if(result.rows[0].granted) throw new Error('Existing deadline grants require DATA_API_SQL_DEADLINE=1 for refresh rotation enrollment')
}

// Explicit upgrade accepts only the known legacy hook or this exact protocol.
// A mutated function is never silently adopted during enrollment.
async function installHooks(client) {
  const previous=await client.query(`SELECT p.proname,encode(sha256(convert_to(p.prosrc,'UTF8')),'hex') AS hash,
    p.prosecdef AND p.proconfig=ARRAY['search_path=pg_catalog'] AND p.proowner=n.nspowner AND p.proowner=(current_user::regrole)::oid
    AND NOT EXISTS(SELECT 1 FROM aclexplode(coalesce(p.proacl,acldefault('f',p.proowner))) a WHERE a.grantee=0 AND a.privilege_type='EXECUTE') AS valid
    FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
    WHERE p.oid IN (to_regprocedure('gregale_data_api.check_request()'),to_regprocedure('gregale_data_api.refresh_status(text)'))`)
  const legacy=['037e4e977ceb69b169e6282d40b7de56fdf01b9825061a67446be05ee0240a19','403293bd1e1cf49fe6c65c47d6c6dd78cc6b27c4915bb296e27d83481a1d1e5b']
  if(previous.rowCount!==2 || previous.rows.some(row=>{
    const index=row.proname==='check_request'?0:1
    return !row.valid || (row.hash!==legacy[index] && row.hash!==digest(hooks[index][5]))
  })) throw new Error('Install matching legacy refresh hooks before enrolling rotation; repair drift explicitly')
  for(const [name,args,result,language,definer,body] of hooks) {
    await client.query(`CREATE OR REPLACE FUNCTION gregale_data_api.${name}(${args}) RETURNS ${result}
      LANGUAGE ${language} SECURITY ${definer?'DEFINER':'INVOKER'} VOLATILE SET search_path=pg_catalog AS $rotation$${body}$rotation$;
      REVOKE ALL ON FUNCTION gregale_data_api.${name}(${args}) FROM PUBLIC`)
  }
}

export async function manageRefreshRotation(client, action, role, bindingID, { sqlDeadline=false, schemas=['api'] }={}) {
  if(!['enroll','check-enrollment'].includes(action) || !/^[a-f0-9]{8}(-[a-f0-9]{4}){3}-[a-f0-9]{12}$/.test(bindingID) || bindingID==='00000000-0000-0000-0000-000000000000' || typeof sqlDeadline!=='boolean' || JSON.stringify(schemas)!=='["api"]') throw new Error('Use a binding UUID and the managed api schema for refresh rotation enrollment')
  await client.query(action==='enroll'?'BEGIN':'BEGIN READ ONLY')
  try {
    await client.query("SELECT set_config('statement_timeout',$1,true),set_config('lock_timeout',$2,true)",[String(limits.queryMs),String(limits.lockMs)])
    const authority=(await client.query(`SELECT n.nspowner::regrole::text AS owner,shobj_description(r.oid,'pg_authid') AS marker,
      n.nspowner=(current_user::regrole)::oid AND n.nspowner=(SELECT nspowner FROM pg_namespace WHERE nspname='api')
      AND n.nspowner=(SELECT nspowner FROM pg_namespace WHERE nspname='public')
      AND shobj_description(n.nspowner,'pg_authid')='gregale:schema-owner:v1' AS valid
      FROM pg_namespace n CROSS JOIN pg_roles r WHERE n.nspname='gregale_data_api' AND r.rolname=$1`,[role])).rows[0]
    const scope=/^gregale:credential:v1:([a-f0-9]{40}):data_api$/.exec(authority?.marker??'')?.[1]
    if(!scope || !authority?.valid || !/^gregale_schema_[a-f0-9]{32}$/.test(authority.owner)) throw new Error('Refresh rotation enrollment requires the stable managed schema owner')
    await client.query('SELECT pg_advisory_xact_lock(hashtextextended($1,0))',[authority.owner])
    const roleOID=await servingRole(client,role,scope)
    if(action==='enroll') {
      // Drain legacy admissions before changing a role's lock domain. Queued
      // old hook calls find their legacy row removed and fail closed.
      await client.query("SELECT pg_advisory_xact_lock(hashtextextended('gregale-data-api:'||$1,0))",[role])
      await client.query('SELECT pg_advisory_xact_lock(hashtextextended($1,0))',[groupKey({scope,binding_id:bindingID})])
      if(await rotationInstalled(client)) { await verifyTables(client); await verifyHooks(client) }
      await client.query(rotationTablesDDL)
      await verifyTables(client)
      await deadlineMode(client,role,sqlDeadline)
      const oldPolicy=await client.query("SELECT to_regclass('gregale_data_api.deadline_binding_policies') IS NOT NULL AS present")
      if(oldPolicy.rows[0].present && (await client.query('SELECT 1 FROM gregale_data_api.deadline_binding_policies WHERE binding_id=$1::uuid AND scope=$2',[bindingID,scope])).rowCount) throw new Error('Unenroll the deadline-only policy before enrolling coordinated refresh rotation')
      await installHooks(client)
      await client.query(`GRANT USAGE ON SCHEMA gregale_data_api TO ${quote(role)};
        GRANT EXECUTE ON FUNCTION gregale_data_api.check_request(),gregale_data_api.refresh_status(text) TO ${quote(role)}`)
      const existing=await client.query('SELECT * FROM gregale_data_api.refresh_rotation_bindings WHERE binding_id=$1::uuid AND scope=$2',[bindingID,scope])
      if(existing.rowCount) {
        const member=await memberContext(client,role)
        if(!member || member.binding_id!==bindingID || member.scope!==scope || member.sql_deadline!==sqlDeadline) throw new Error('Refresh rotation enrollment conflicts with the existing binding policy')
      } else {
        const legacy=(await client.query('SELECT paused,token_hash,fingerprint FROM gregale_data_api.refresh_gate WHERE role_name=$1',[role])).rows[0]
        const current=await capture(client,role)
        if(legacy && !legacy.paused && legacy.fingerprint!==current.fingerprint) throw new Error('Legacy refresh fingerprint changed; verify and resume before enrollment')
        await client.query(`INSERT INTO gregale_data_api.refresh_rotation_bindings VALUES($1::uuid,$2,1,$3,$4,$5,$6)`,[bindingID,scope,role,sqlDeadline,legacy?.paused??false,legacy?.token_hash??null])
        await client.query(`INSERT INTO gregale_data_api.refresh_rotation_members VALUES($1,$2::oid,$3::uuid,$4,$5,$6,$7::oid[])`,[role,roleOID,bindingID,scope,legacy?.fingerprint??current.fingerprint,current.witness,current.rpcs])
        await client.query('DELETE FROM gregale_data_api.refresh_gate WHERE role_name=$1',[role])
      }
    }
    const member=await memberContext(client,role)
    if(!member || member.binding_id!==bindingID || member.scope!==scope || member.sql_deadline!==sqlDeadline) throw new Error('Refresh rotation enrollment is absent or incompatible')
    const current=await capture(client,role)
    if(current.witness!==member.catalog_witness || (!member.paused && current.fingerprint!==member.fingerprint)) throw new Error('Refresh rotation contract changed; keep paused and verify through resume')
    await deadlineMode(client,role,sqlDeadline)
    await client.query(action==='enroll'?'COMMIT':'ROLLBACK')
    return {version:1,enrolled:true,paused:member.paused,fingerprint:member.fingerprint,live_fingerprint:current.fingerprint}
  } catch(error) { await client.query('ROLLBACK'); throw error }
}

// Called by existing owner pause/resume/check commands. Absence of the new
// tables or a mapping preserves the original per-login protocol.
export async function changeRotationBarrier(client,action,role,tokenHash,expected,schemas) {
  if(!await rotationInstalled(client)) return false
  await client.query('BEGIN')
  try {
    const member=await memberContext(client,role)
    if(!member) { await client.query('ROLLBACK'); return false }
    if(JSON.stringify(schemas)!=='["api"]') throw new Error('Refresh rotation uses the managed api schema')
    await client.query('SELECT pg_advisory_xact_lock(hashtextextended($1,0))',[groupKey(member)])
    const current=await memberContext(client,role)
    if(!current || current.binding_id!==member.binding_id || current.scope!==member.scope) throw new Error('Refresh membership changed')
    if(action==='resume') {
      if(!current.paused || current.token_hash!==tokenHash) throw new Error('Refresh owner changed')
      const members=(await client.query('SELECT role_name FROM gregale_data_api.refresh_rotation_members WHERE binding_id=$1::uuid AND scope=$2 ORDER BY role_name',[member.binding_id,member.scope])).rows
      for(const entry of members) {
        await servingRole(client,entry.role_name,member.scope)
        await deadlineMode(client,entry.role_name,member.sql_deadline)
        const contract=await capture(client,entry.role_name)
        if(entry.role_name===role && contract.fingerprint!==expected) throw new Error('Schema changed after verification; keep the barrier paused')
        await client.query(`UPDATE gregale_data_api.refresh_rotation_members SET fingerprint=$2,catalog_witness=$3,rpcs=$4::oid[] WHERE role_name=$1`,[entry.role_name,contract.fingerprint,contract.witness,contract.rpcs])
      }
    }
    const updated=action==='pause'
      ? await client.query('UPDATE gregale_data_api.refresh_rotation_bindings SET paused=true,token_hash=$3 WHERE binding_id=$1::uuid AND scope=$2 AND NOT paused',[member.binding_id,member.scope,tokenHash])
      : await client.query('UPDATE gregale_data_api.refresh_rotation_bindings SET paused=false,token_hash=NULL WHERE binding_id=$1::uuid AND scope=$2 AND paused AND token_hash=$3',[member.binding_id,member.scope,tokenHash])
    if(updated.rowCount!==1) throw new Error('Refresh state or owner changed')
    await client.query('COMMIT'); return true
  } catch(error) { await client.query('ROLLBACK'); throw error }
}

export async function checkRotationSetup(client,role,schemas) {
  if(!await rotationInstalled(client)) return null
  await client.query('BEGIN READ ONLY')
  try {
    const member=await memberContext(client,role)
    if(!member) return null
    if(JSON.stringify(schemas)!=='["api"]') throw new Error('Refresh rotation uses the managed api schema')
    await servingRole(client,role,member.scope)
    const current=await capture(client,role)
    if(member.sql_deadline) await checkSQLDeadlineClient(client,role,true)
    const grants=await client.query("SELECT has_schema_privilege($1::name,'gregale_data_api','USAGE') AND has_function_privilege($1::name,'gregale_data_api.check_request()','EXECUTE') AND has_function_privilege($1::name,'gregale_data_api.refresh_status(text)','EXECUTE') AS valid",[role])
    if(!grants.rows[0]?.valid) throw new Error('Refresh serving grants drifted')
    return {version:1,ready:true,paused:member.paused,fingerprint:member.fingerprint,live_fingerprint:current.fingerprint}
  } finally { await client.query('ROLLBACK') }
}

// Owner diagnostics only. Never create a candidate, invoke the preparation
// hook, acquire a coordination lock, or adopt the current catalog as approved.
export async function preflightRefreshRotation(client,role,bindingID,{sqlDeadline=false,schemas=['api']}={}) {
  if(!/^[a-f0-9]{8}(-[a-f0-9]{4}){3}-[a-f0-9]{12}$/.test(bindingID) || /^0{8}-0{4}-0{4}-0{4}-0{12}$/.test(bindingID) || typeof role!=='string' || !role || role.includes('\0') || typeof sqlDeadline!=='boolean') throw new Error('Invalid preflight input')
  const actions={
    owner_authority:'Use the managed migration connection with its stable schema-owner role and the api schema.',
    enrollment_tables:'Enroll coordinated rotation with the matching owner tool; explicitly repair private table ownership, shape or ACL drift.',
    refresh_hooks:'Install the matching rotation tool and explicitly repair protected refresh hook versions, ownership or ACLs.',
    binding_enrollment:'Enroll this binding UUID and current managed login in the same database and resource scope.',
    source_role:'Repair the recorded source login identity, OID, restricted attributes and memberships through the managed owner workflow.',
    serving_grants:'Restore private schema usage and execution of the two fixed serving refresh hooks for the source login.',
    sql_deadline:'Use the enrolled DATA_API_SQL_DEADLINE mode and repair the matching protected deadline hooks and grants.',
    rpc_grants:'Review approved RPC ownership, invoker security, annotations and grants; verify them through coordinated pause and resume.',
    catalog_witness:'Review catalog and privilege drift, then use coordinated pause, verification and resume before retrying rotation.',
    serving_contract:'Verify schema and source-login permissions through coordinated refresh before retrying rotation.',
    rls_compatibility:'Review policies tied to the source SQL login. Use an owner-reviewed claim-based policy applicable to replacement logins, then verify through coordinated refresh.',
    baseline_privileges:'Restore the managed api table, sequence and schema grants; remove unsupported owner or external table privileges before verifying coordinated refresh.',
  }
  const report={version:1,kind:'data_api_rotation_preflight',binding_id:bindingID,ready:false,complete:false,checks:Object.entries(actions).map(([code,next_action])=>({code,status:'unknown',next_action}))}
  async function check(code,run) {
    const item=report.checks.find(c=>c.code===code)
    try { const result=await run();item.status=result===false?'blocked':'passed';return item.status==='passed' }
    catch(error) { item.status=error.code?'unknown':'blocked';return false }
  }
  await client.query('BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY')
  try {
    await client.query("SELECT set_config('statement_timeout',$1,true),set_config('lock_timeout',$2,true)",[String(limits.queryMs),String(limits.lockMs)])
    let scope,member
    if(!await check('owner_authority',async()=>{
      const row=(await client.query(`SELECT n.nspowner::regrole::text AS owner,shobj_description(r.oid,'pg_authid') AS marker,
        n.nspowner=(current_user::regrole)::oid AND n.nspowner=(SELECT nspowner FROM pg_namespace WHERE nspname='api')
        AND n.nspowner=(SELECT nspowner FROM pg_namespace WHERE nspname='public')
        AND shobj_description(n.nspowner,'pg_authid')='gregale:schema-owner:v1' AS valid
        FROM pg_namespace n CROSS JOIN pg_roles r WHERE n.nspname='gregale_data_api' AND r.rolname=$1`,[role])).rows[0]
      scope=/^gregale:credential:v1:([a-f0-9]{40}):data_api$/.exec(row?.marker??'')?.[1]
      return Boolean(scope && row?.valid && /^gregale_schema_[a-f0-9]{32}$/.test(row.owner) && JSON.stringify(schemas)==='["api"]')
    })) return report
    if(!await check('enrollment_tables',async()=>{if(!await rotationInstalled(client))return false;await verifyTables(client)}))return report
    if(!await check('refresh_hooks',()=>verifyHooks(client)))return report
    if(!await check('binding_enrollment',async()=>{
      member=(await client.query(`SELECT b.*,m.role_oid,m.fingerprint,m.catalog_witness,m.rpcs,
        (SELECT count(*)::integer FROM gregale_data_api.refresh_rotation_members x WHERE x.binding_id=b.binding_id AND x.scope=b.scope) AS member_count
        FROM gregale_data_api.refresh_rotation_bindings b JOIN gregale_data_api.refresh_rotation_members m
        ON m.binding_id=b.binding_id AND m.scope=b.scope AND m.role_name=b.source_role
        WHERE b.binding_id=$1::uuid AND b.scope=$2 AND b.version=1
        AND EXISTS(SELECT 1 FROM gregale_data_api.refresh_rotation_members x WHERE x.binding_id=b.binding_id AND x.scope=b.scope AND x.role_name=$3 AND x.role_oid=to_regrole($3)::oid)`,[bindingID,scope,role])).rows[0]
      if(!member)return false
      report.paused=member.paused;report.member_count=member.member_count
    }))return report
    if(!await check('source_role',async()=>Number(await servingRole(client,member.source_role,scope))===Number(member.role_oid)))return report
    await check('serving_grants',async()=>(await client.query("SELECT has_schema_privilege($1::name,'gregale_data_api','USAGE') AND has_function_privilege($1::name,'gregale_data_api.check_request()','EXECUTE') AND has_function_privilege($1::name,'gregale_data_api.refresh_status(text)','EXECUTE') AS valid",[member.source_role])).rows[0]?.valid===true)
    await check('sql_deadline',async()=>{if(member.sql_deadline!==sqlDeadline)return false;await deadlineMode(client,member.source_role,sqlDeadline)})
    await check('rpc_grants',async()=>{
      const snapshot=await inspectClient(client,['api'],{privilegeRole:member.source_role})
      const names=snapshot.functions.map(f=>f.name)
      const rpcs=(await client.query(`SELECT p.oid FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
        WHERE n.nspname='api' AND p.proname=ANY($1::text[]) AND p.proowner=(current_user::regrole)::oid AND p.prokind='f' AND NOT p.prosecdef AND obj_description(p.oid,'pg_proc')='@gregale:rpc' ORDER BY p.oid`,[names])).rows.map(r=>String(r.oid))
      return rpcs.length===names.length && JSON.stringify(rpcs)===JSON.stringify(member.rpcs.map(String))
    })
    await check('catalog_witness',async()=>(await client.query('SELECT gregale_data_api.refresh_catalog_witness($1::name) AS witness',[member.source_role])).rows[0].witness===member.catalog_witness)
    await check('serving_contract',async()=>fingerprint(await inspectClient(client,['api'],{privilegeRole:member.source_role}))===member.fingerprint)
    // A fresh restricted login cannot be in any existing policy's TO list.
    // Match the applicability bit used by the pinned catalog witness, even for
    // policies outside api. This does not evaluate arbitrary policy expressions.
    await check('rls_compatibility',async()=>!(await client.query('SELECT EXISTS(SELECT 1 FROM pg_policy WHERE $1::oid=ANY(polroles) AND NOT 0::oid=ANY(polroles)) AS incompatible',[member.role_oid])).rows[0].incompatible)
    await check('baseline_privileges',async()=>(await client.query(`SELECT has_schema_privilege($1::name,'api','USAGE')
      AND NOT EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='api' AND
        ((c.relkind IN ('r','p','v','m','f') AND (NOT has_table_privilege($1::name,c.oid,'SELECT') OR NOT has_table_privilege($1::name,c.oid,'INSERT') OR NOT has_table_privilege($1::name,c.oid,'UPDATE') OR NOT has_table_privilege($1::name,c.oid,'DELETE')))
        OR (c.relkind='S' AND (NOT has_sequence_privilege($1::name,c.oid,'SELECT') OR NOT has_sequence_privilege($1::name,c.oid,'USAGE')))))
      AND NOT EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname NOT IN ('api','information_schema') AND n.nspname !~ '^pg_' AND c.relkind IN ('r','p','v','m','f') AND has_table_privilege($1::name,c.oid,'SELECT,INSERT,UPDATE,DELETE'))
      AND NOT EXISTS(SELECT 1 FROM pg_shdepend WHERE refclassid='pg_authid'::regclass AND refobjid=$2::oid AND deptype='o')
      AND NOT EXISTS(SELECT 1 FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname IN ('api','public') AND p.prosecdef AND has_function_privilege($1::name,p.oid,'EXECUTE')) AS valid`,[member.source_role,member.role_oid])).rows[0]?.valid===true)
    return report
  } finally {
    await client.query('ROLLBACK')
    report.complete=report.checks.every(c=>c.status!=='unknown')
    report.ready=report.checks.every(c=>c.status==='passed')
  }
}

export async function main(env=process.env,args=process.argv.slice(2)) {
  const [action,role,bindingID]=args
  if(args.length!==3 || !['enroll','check-enrollment','preflight'].includes(action) || !env.MIGRATION_DATABASE_URL) throw new Error('Use refresh-rotation.mjs enroll|check-enrollment|preflight BINDING_ROLE BINDING_UUID with MIGRATION_DATABASE_URL')
  if(env.DATA_API_SQL_DEADLINE!==undefined && env.DATA_API_SQL_DEADLINE!=='1') throw new Error('Invalid SQL deadline setting')
  const client=new pg.Client({connectionString:databaseURL(env.MIGRATION_DATABASE_URL),connectionTimeoutMillis:limits.queryMs,query_timeout:limits.queryMs})
  try {
    await client.connect()
    const options={sqlDeadline:env.DATA_API_SQL_DEADLINE==='1',schemas:schemaNames(env.DATA_API_SCHEMAS)}
    const report=action==='preflight'?await preflightRefreshRotation(client,role,bindingID,options):await manageRefreshRotation(client,action,role,bindingID,options)
    process.stdout.write(JSON.stringify(report)+'\n')
    if(action==='preflight' && !report.ready)process.exitCode=1
  } finally { await client.end() }
}
if(process.argv[1] && import.meta.url===pathToFileURL(process.argv[1]).href) main().catch(()=>{process.stderr.write('Refresh rotation setup failed; check managed owner authority, installed hooks, binding policy and contract drift.\n');process.exitCode=1})

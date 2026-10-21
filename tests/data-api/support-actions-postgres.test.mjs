import test from 'node:test'
import assert from 'node:assert/strict'
import {createRequire} from 'node:module'
import {pathToFileURL} from 'node:url'
import {join} from 'node:path'
import {readFile} from 'node:fs/promises'
import {randomUUID,randomBytes} from 'node:crypto'
const directory=process.env.DATA_API_SUPPORT_CONSOLE_DIR
test('support actions PostgreSQL function enforces tenant, permission, version and atomic idempotent receipts',{skip:!directory||!process.env.DATA_API_TEST_DATABASE_URL,timeout:30000},async t=>{
 const pg=createRequire(join(directory,'package.json'))('pg'),{actionDatabase}=await import(pathToFileURL(join(directory,'actions.mjs')))
 const suffix=randomBytes(5).toString('hex'),database='support_actions_'+suffix,role='support_actor_'+suffix,password=randomBytes(16).toString('hex'),admin=new pg.Client({connectionString:process.env.DATA_API_TEST_DATABASE_URL});await admin.connect()
 let owner,actions
 t.after(async()=>{await actions?.close();await owner?.end();await admin.query('DROP DATABASE IF EXISTS "'+database+'" WITH (FORCE)');await admin.query('DROP ROLE IF EXISTS "'+role+'"');await admin.end()})
 await admin.query('CREATE ROLE "'+role+'" LOGIN NOSUPERUSER NOBYPASSRLS PASSWORD \''+password+'\'')
 await admin.query('CREATE DATABASE "'+database+'"')
 const ownerURL=new URL(process.env.DATA_API_TEST_DATABASE_URL);ownerURL.pathname='/'+database;owner=new pg.Client({connectionString:ownerURL.toString()});await owner.connect()
 await owner.query('CREATE SCHEMA api')
 const base=await readFile(new URL('../../cmd/gregale/templates/data-api-starter/migrations/examples/support/support.sql',import.meta.url),'utf8')
 await owner.query(base.replaceAll('gregale_data_api','"'+role+'"'))
 // The action caller deliberately gets no direct customer-table permissions.
 await owner.query('REVOKE ALL ON support_private.cases,support_private.replies FROM "'+role+'"')
 await owner.query(await readFile(join(directory,'migrations/support-actions.sql'),'utf8'))
 await owner.query(await readFile(join(directory,'migrations/action-recovery.sql'),'utf8'))
 await owner.query(await readFile(join(directory,'migrations/case-assignment.sql'),'utf8'))
 await owner.query(await readFile(join(directory,'migrations/case-priority.sql'),'utf8'))
 await owner.query(await readFile(join(directory,'migrations/internal-notes.sql'),'utf8'))
 await owner.query('GRANT USAGE ON SCHEMA support_actions TO "'+role+'"; GRANT EXECUTE ON FUNCTION support_actions.execute(text,integer,integer,text,uuid,timestamptz) TO "'+role+'"')
 await owner.query("INSERT INTO support_private.cases(tenant,title,status) VALUES('a','Case A','open'),('b','Case B','open')")
 const url=new URL(ownerURL);url.username=role;url.password=password;actions=await actionDatabase(url.toString())
 await owner.query('GRANT EXECUTE ON FUNCTION support_actions.register(text,integer,integer,text,uuid,timestamptz),support_actions.submitted(uuid),support_actions.recent(uuid) TO "'+role+'"')
 const databaseActions=actions,registered=new Map()
 actions={...databaseActions,async execute(claims,request){
  if(!registered.has(request.id)){request={...request,reviewExpires:request.reviewExpires??Date.now()+300000};await databaseActions.register(claims,request);registered.set(request.id,request.reviewExpires)}
  return databaseActions.execute(claims,{...request,reviewExpires:registered.get(request.id)})
 }}
 const claims={sub:'staff-a',tenant:'a',gregale_support_actions:['add_reply','close_case']}
 const reply={id:randomUUID(),action:'add_reply',caseId:1,version:1,body:'Private customer reply'}
 reply.reviewExpires=Date.now()+300000;await databaseActions.register(claims,reply);registered.set(reply.id,reply.reviewExpires)
 const [first,second]=await Promise.all([actions.execute(claims,reply),actions.execute(claims,reply)]);assert.deepEqual(first,second)
 assert.equal((await owner.query('SELECT count(*)::int AS count FROM support_private.replies')).rows[0].count,1)
 await assert.rejects(()=>actions.execute({...claims,gregale_support_actions:[]},{...reply,id:randomUUID()}),e=>e.status===403)
 await assert.rejects(()=>actions.execute({...claims,tenant:'b'},reply),e=>e.status===403)
 await assert.rejects(()=>actions.execute(claims,{...reply,body:'changed'}),e=>e.status===403)
 await assert.rejects(()=>actions.execute(claims,{id:randomUUID(),action:'close_case',caseId:2,version:1}),e=>e.status===409)
 await assert.rejects(()=>actions.execute(claims,{id:randomUUID(),action:'close_case',caseId:1,version:1}),e=>e.status===409)
 await assert.rejects(()=>actions.execute(claims,{id:randomUUID(),action:'close_case',caseId:1,version:2,reviewExpires:Date.now()-1}),e=>e.status===409)
 await owner.query("CREATE FUNCTION support_private.test_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'simulated unknown failure'; END $$; CREATE TRIGGER test_failure BEFORE INSERT ON support_private.replies FOR EACH ROW EXECUTE FUNCTION support_private.test_failure();")
 const unresolved={id:randomUUID(),action:'add_reply',caseId:1,version:2,body:'Unresolved reply'}
 await assert.rejects(()=>actions.execute(claims,unresolved),e=>e.status===503)
 await owner.query('DROP TRIGGER test_failure ON support_private.replies')
 const unknown=await actions.recent(claims,unresolved.id);assert.equal(unknown[0].state,'unresolved');assert.equal(unknown[0].result,null)
 assert.equal((await owner.query('SELECT count(*)::int AS count FROM support_private.replies')).rows[0].count,1)
 const close={id:randomUUID(),action:'close_case',caseId:1,version:2}
 const result=await actions.execute(claims,close);assert.equal(result.status,'closed');assert.equal(result.version,3);assert.deepEqual(await actions.execute(claims,close),result)
 const history=await actions.recent(claims);assert.equal(history.filter(r=>r.state==='completed').length,2);assert.ok(history.some(r=>r.state==='rejected'));assert.equal((await actions.recent({...claims,sub:'other'})).length,0);assert.equal((await actions.recent({...claims,tenant:'b'})).length,0)
 const receipts=(await owner.query('SELECT * FROM support_actions.receipts')).rows;assert.equal(receipts.length,2);assert.ok(receipts.every(r=>r.tenant==='a'&&r.subject==='staff-a'));assert.ok(!JSON.stringify(receipts).includes('Private customer reply'))
 await databaseActions.close()
 const restarted=await actionDatabase(url.toString());actions=restarted
 assert.equal((await restarted.recent(claims,reply.id))[0].state,'completed')
 assert.equal((await restarted.recent(claims,unresolved.id))[0].state,'unresolved')
 await owner.query("INSERT INTO support_private.cases(tenant,title,status) VALUES('a','Assignable case','open')")
 const assigning={...claims,gregale_support_actions:['assign_case','unassign_case']},assignment={id:randomUUID(),action:'assign_case',caseId:3,version:1,reviewExpires:Date.now()+300000}
 await restarted.register(assigning,assignment);const assigned=await restarted.execute(assigning,assignment);assert.equal(assigned.assignee,'staff-a');assert.deepEqual(await restarted.execute(assigning,assignment),assigned)
 const stale={id:randomUUID(),action:'unassign_case',caseId:3,version:1,reviewExpires:Date.now()+300000};await restarted.register(assigning,stale);await assert.rejects(()=>restarted.execute(assigning,stale),e=>e.status===409)
 const removal={...stale,id:randomUUID(),version:2};await restarted.register(assigning,removal);assert.equal((await restarted.execute(assigning,removal)).assignee,null)
 const assignments=(await owner.query('SELECT * FROM support_private.assignment_events')).rows;assert.equal(assignments.length,2);assert.ok(assignments.every(r=>r.tenant==='a'&&r.actor==='staff-a'));assert.equal(assignments[0].assignee,'staff-a');assert.equal(assignments[1].previous_assignee,'staff-a');assert.equal(assignments[1].assignee,null)
 const outside={...assignment,id:randomUUID(),caseId:2};await restarted.register(assigning,outside);await assert.rejects(()=>restarted.execute(assigning,outside),e=>e.status===409)
 const prioritizing={...claims,gregale_support_actions:['set_priority']},priorityRequest={id:randomUUID(),action:'set_priority',caseId:3,version:3,body:'urgent',reviewExpires:Date.now()+300000}
 await restarted.register(prioritizing,priorityRequest);const prioritized=await restarted.execute(prioritizing,priorityRequest);assert.equal(prioritized.priority,'urgent');assert.deepEqual(await restarted.execute(prioritizing,priorityRequest),prioritized)
 const clock=(await owner.query('SELECT extract(epoch FROM response_due_at-created_at)/60 AS minutes FROM support_private.cases WHERE id=3')).rows[0];assert.equal(Number(clock.minutes),60)
 assert.equal((await owner.query('SELECT count(*)::int AS count FROM support_private.priority_events')).rows[0].count,1)
 await owner.query("UPDATE support_private.response_policy SET minutes=30 WHERE priority='high'")
 const high={...priorityRequest,id:randomUUID(),body:'high',version:4};await restarted.register(prioritizing,high);await restarted.execute(prioritizing,high)
 assert.equal(Number((await owner.query('SELECT extract(epoch FROM response_due_at-created_at)/60 AS minutes FROM support_private.cases WHERE id=3')).rows[0].minutes),30)
 const reply3={id:randomUUID(),action:'add_reply',caseId:3,version:5,body:'First response',reviewExpires:Date.now()+300000};await restarted.register(claims,reply3);await restarted.execute(claims,reply3)
 assert.ok((await owner.query('SELECT first_response_at FROM support_private.cases WHERE id=3')).rows[0].first_response_at)
 const afterReply={...priorityRequest,id:randomUUID(),body:'low',version:6};await restarted.register(prioritizing,afterReply);await restarted.execute(prioritizing,afterReply)
 assert.equal(Number((await owner.query('SELECT extract(epoch FROM response_due_at-created_at)/60 AS minutes FROM support_private.cases WHERE id=3')).rows[0].minutes),30)
 await owner.query("INSERT INTO support_private.cases(tenant,title,status) VALUES('a','Notes case','open')")
 const noteClaims={...claims,gregale_internal_notes:true,gregale_support_actions:['add_note']},note={id:randomUUID(),action:'add_note',caseId:4,version:1,body:'Internal reasoning',reviewExpires:Date.now()+300000}
 await restarted.register(noteClaims,note);const noted=await restarted.execute(noteClaims,note);assert.ok(noted.note_id);assert.deepEqual(await restarted.execute(noteClaims,note),noted)
 assert.equal((await owner.query('SELECT count(*)::int AS count FROM support_private.notes')).rows[0].count,1)
 assert.equal((await owner.query('SELECT first_response_at FROM support_private.cases WHERE id=4')).rows[0].first_response_at,null)
 const forbidden={...note,id:randomUUID(),version:2};await restarted.register({...noteClaims,gregale_internal_notes:false},forbidden);await assert.rejects(()=>restarted.execute({...noteClaims,gregale_internal_notes:false},forbidden),e=>e.status===403)
 const noteHistory=await restarted.recent(noteClaims,note.id);assert.equal(noteHistory[0].state,'completed');assert.ok(!JSON.stringify(noteHistory).includes('Internal reasoning'))
 const caller=new pg.Client({connectionString:url.toString()});await caller.connect()
 await owner.query('GRANT SELECT ON support_private.assignment_events TO "'+role+'"')
 await caller.query("SELECT set_config('request.jwt.claims',$1,false)",[JSON.stringify({tenant:'b',gregale_read_only:true})]);assert.equal((await caller.query('SELECT * FROM support_private.assignment_events')).rows.length,0)
 await caller.query("SELECT set_config('request.jwt.claims',$1,false)",[JSON.stringify({tenant:'a',gregale_read_only:true})]);assert.equal((await caller.query('SELECT * FROM support_private.assignment_events')).rows.length,2)
 await owner.query('REVOKE SELECT ON support_private.assignment_events FROM "'+role+'"')
 await owner.query('GRANT SELECT ON support_private.notes TO "'+role+'"')
 await caller.query("SELECT set_config('request.jwt.claims',$1,false)",[JSON.stringify({tenant:'a',gregale_read_only:true})]);assert.equal((await caller.query('SELECT * FROM support_private.notes')).rows.length,0)
 await caller.query("SELECT set_config('request.jwt.claims',$1,false)",[JSON.stringify({tenant:'a',gregale_read_only:true,gregale_internal_notes:true})]);assert.equal((await caller.query('SELECT * FROM support_private.notes')).rows.length,1)
 await caller.query("SELECT set_config('request.jwt.claims',$1,false)",[JSON.stringify({tenant:'b',gregale_read_only:true,gregale_internal_notes:true})]);assert.equal((await caller.query('SELECT * FROM support_private.notes')).rows.length,0)
 await owner.query('REVOKE SELECT ON support_private.notes FROM "'+role+'"')
 await assert.rejects(()=>caller.query('UPDATE support_private.cases SET status=\'closed\''),e=>e.code==='42501')
 await assert.rejects(()=>caller.query('SELECT * FROM support_actions.receipts'),e=>e.code==='42501');await caller.end()
 await assert.rejects(()=>actionDatabase(ownerURL.toString()),/non-owner/)
})

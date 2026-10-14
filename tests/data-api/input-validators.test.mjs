import test from 'node:test'
import assert from 'node:assert/strict'
import { mkdtemp, writeFile, readFile, readdir, cp, mkdir, symlink, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { pathToFileURL } from 'node:url'
import { createRequire } from 'node:module'
import {validateRecord,keyedEdit,editTarget} from '../../cmd/gregale/templates/data-api/inspector-data.mjs'
import { execFileSync } from 'node:child_process'
import { generate, generateValidators, fingerprint, inputModel } from '../../cmd/gregale/templates/data-api/type-render.mjs'
async function compiled(t, snapshot, metadata = []) {
  const root = await mkdtemp(join(tmpdir(), 'gregale-input-validators-'))
  t.after(() => rm(root, { recursive: true, force: true }))
  const source = generateValidators(snapshot, metadata)
  await writeFile(join(root, 'database.ts'), source)
  await writeFile(join(root, 'package.json'), '{"type":"module"}')
  await writeFile(join(root, 'use.ts'), `import {inputValidators, DataAPIInputError, type DataAPIInputIssue, type TablesInsert} from './database.js';
const issue: DataAPIInputIssue = {code:'too_long',path:['title']};
const error = new DataAPIInputError(issue);
// @ts-expect-error Issue paths are readonly.
error.issues[0].path.push('changed');
// @ts-expect-error Issue codes are a stable union.
const unknownIssue: DataAPIInputIssue = {code:'custom_private_reason',path:[]};
void unknownIssue;
const parsed: TablesInsert<'entries'> = inputValidators.api.entries.insert({});
// @ts-expect-error Views have no write validators.
inputValidators.api.entry_view.insert({});
// @ts-expect-error Unknown relations are rejected.
inputValidators.api.missing.insert({});
void parsed;
import {validateInputRows} from './database.js';
const batch = validateInputRows([],inputValidators.api.entries.insert);
if (batch.ok) { const rows: readonly TablesInsert<'entries'>[] = batch.rows; void rows; }
else {
 // @ts-expect-error Failed batches never expose a partial payload.
 batch.rows;
}
// @ts-expect-error Batch parsers must be synchronous.
validateInputRows([],async () => ({}));
${snapshot.functions?.some(f => f.schema === 'api' && f.name === 'entries') ? `
import {rpcValidators, type Database} from './database.js';
import {createDataClient, validatedRpc} from '${resolve('sdk/data/dist/index.js')}';
const db = createDataClient<Database>({url:'https://example.test',accessToken:'token'}).schema('api');
const args = rpcValidators.api.entries.parse({});
const state: 'open' | 'closed' | null = args.state;
// @ts-expect-error RPC argument inference preserves enum values.
const invalidState: boolean = args.state;
// @ts-expect-error Unknown RPC names are rejected.
rpcValidators.api.missing.parse({});
const call = validatedRpc({},rpcValidators.api.entries,value => db.rpc('entries',value));
async function checkResult() {
 const outcome = await call;
 if (outcome.outcome === 'completed' && outcome.result.data !== null) {
  const value: number = outcome.result.data;
  // @ts-expect-error Result inference remains numeric.
  const invalid: string = outcome.result.data;
  void [value,invalid];
 }
}
void [state,invalidState,checkResult];` : ''}`)
  execFileSync(process.execPath, [resolve('sdk/data/node_modules/typescript/bin/tsc'), '--strict', '--target', 'ES2022', '--module', 'NodeNext', '--moduleResolution', 'NodeNext', '--outDir', join(root, 'dist'), join(root, 'database.ts'), join(root, 'use.ts')], { encoding: 'utf8' })
  return { source, ...(await import(pathToFileURL(join(root, 'dist/database.js')))) }
}
const column = (name, type, extra = {}) => ({ name, type, nullable: false, optional: false, insertable: true, updatable: true, ...extra })
const snapshot = { version: 3, schemas: ['api'], functions: [], enums: {}, composites: [{schema:'api',name:'address',fields:[{name:'city',type:'string',nullable:true}]}], tables: [
  {schema:'api',name:'entries',view:false,relationships:[],columns:[
    column('id','number',{optional:true,insertable:false,updatable:false}),column('title','string'),column('state','"open" | "closed"'),column('count','number'),
    column('tags','(string | null)[]',{optional:true,nullable:true}),column('payload','Json',{optional:true}),
    column('address','Database["api"]["CompositeTypes"]["address"]',{optional:true}),column('__proto__','string',{optional:true}),
    column('custom','unknown',{optional:true}),
  ]},
  {schema:'api',name:'entry_view',view:true,relationships:[],columns:[column('title','string')]},
] }
test('generated validators compile, retain types, validate writes and return detached JSON without leaking inputs', async t => {
  const { inputValidators, inputValidationWarnings, DataAPIInputError, formMetadata, source } = await compiled(t, snapshot, [
    {schema:'api',relation:'entries',name:'title',type:'varchar',typmod:7,checks:true},
    {schema:'api',relation:'entries',name:'count',type:'int2'},
  ])
  assert.deepEqual(formMetadata.api.entries.insert.find(f=>f.name==='title'),{name:'title',control:'text',required:true,nullable:false,maxLength:3,lengthUnit:'unicode_code_points',requiresReview:true})
  assert.equal(formMetadata.api.entries.update.find(f=>f.name==='title').required,false)
  assert.equal(formMetadata.api.entries.insert.some(f=>f.name==='id'),false)
  assert.equal(formMetadata.api.entry_view,undefined)
  assert.deepEqual(formMetadata.api.entries.insert.find(f=>f.name==='state').options,['open','closed'])
  assert.equal(formMetadata.api.entries.insert.find(f=>f.name==='address').control,'custom')
  assert.ok(source.startsWith(generate(snapshot)))
  assert.ok(source.includes(`// Schema fingerprint: ${fingerprint(snapshot)}`))
  assert.ok(inputValidationWarnings.some(w => w.reason === 'check_constraints_require_review'))
  assert.ok(inputValidationWarnings.some(w => w.column === 'custom' && w.reason === 'unsupported_type'))
  const parser = inputValidators.api.entries.insert, update = inputValidators.api.entries.update
  const valid = {title:'😀ab',state:'open',count:32767,tags:['x',null],payload:{nested:[1,true]},address:{city:null}}
  const result = parser(valid)
  assert.deepEqual(result,valid); assert.notEqual(result,valid); assert.notEqual(result.payload,valid.payload)
  assert.deepEqual(update({}),{})
  assert.deepEqual(update({title:'abc'}),{title:'abc'})
  for (const value of [{}, {...valid,id:4}, {...valid,extra:'secret'}, {...valid,title:'abcd'}, {...valid,title:null}, {...valid,state:'SECRET'}, {...valid,count:32768}, {...valid,count:0.5}, {...valid,count:NaN}, {...valid,count:Infinity}, {...valid,payload:null}, {...valid,tags:[4]}, {...valid,address:{}}, {...valid,title:undefined}]) {
    assert.throws(() => parser(value), error => error instanceof DataAPIInputError && error.code === 'invalid_input' && !error.message.includes('SECRET'))
  }
  const cyclic={};cyclic.self=cyclic
  assert.throws(()=>parser({...valid,payload:cyclic}),DataAPIInputError)
  let read=false;const getter={...valid};Object.defineProperty(getter,'title',{enumerable:true,get(){read=true;return 'abc'}})
  assert.throws(()=>parser(getter),DataAPIInputError);assert.equal(read,false)
  assert.throws(()=>parser({...valid,payload:new Date()}),DataAPIInputError)
  const proto=parser(JSON.parse('{"title":"abc","state":"open","count":1,"__proto__":"safe"}'))
  assert.equal(Object.getPrototypeOf(proto),Object.prototype);assert.equal(proto.__proto__,'safe')
  assert.equal(inputValidators.api.entry_view,undefined)
})

test('issues expose stable codes and known schema paths while withholding values and dynamic keys',async t=>{
  const {inputValidators,DataAPIInputError}=await compiled(t,snapshot,[
    {schema:'api',relation:'entries',name:'title',type:'varchar',typmod:7},
    {schema:'api',relation:'entries',name:'count',type:'int2'},
  ])
  const valid={title:'abc',state:'open',count:1}
  const missing={...valid};delete missing.title
  const cases=[
    [missing,'required',['title']],
    [{...valid,title:null},'null_not_allowed',['title']],
    [{...valid,title:4},'invalid_type',['title']],
    [{...valid,state:'PRIVATE_VALUE'},'invalid_enum',['state']],
    [{...valid,count:Infinity},'not_finite',['count']],
    [{...valid,count:1.5},'invalid_integer',['count']],
    [{...valid,count:32768},'out_of_range',['count']],
    [{...valid,title:'PRIVATE_VALUE'},'too_long',['title']],
    [{...valid,tags:['valid',4]},'invalid_type',['tags',1]],
    [{...valid,address:{city:4}},'invalid_type',['address','city']],
    [{...valid,address:{}},'required',['address','city']],
    [{...valid,PRIVATE_KEY:'PRIVATE_VALUE'},'unknown_field',[]],
    [{...valid,payload:{PRIVATE_KEY:undefined}},'invalid_json',['payload']],
    [{...valid,address:{PRIVATE_KEY:'PRIVATE_VALUE'}},'unknown_field',['address']],
  ]
  const cyclic={};cyclic.PRIVATE_KEY=cyclic
  cases.push([{...valid,payload:cyclic},'cyclic_input',['payload']])
  const getter={...valid};Object.defineProperty(getter,'title',{get(){throw new Error('PRIVATE_VALUE')},enumerable:true})
  cases.push([getter,'unsupported_object',['title']])
  const tags=['x'];Object.defineProperty(tags,0,{get(){throw new Error('PRIVATE_VALUE')},enumerable:true})
  cases.push([{...valid,tags},'unsupported_object',['tags']])
  for(const [input,code,path] of cases){
    assert.throws(()=>inputValidators.api.entries.insert(input),error=>{
      assert.ok(error instanceof DataAPIInputError)
      assert.equal(error.code,'invalid_input')
      assert.deepEqual(error.issues,[{code,path}])
      assert.ok(Object.isFrozen(error.issues));assert.ok(Object.isFrozen(error.issues[0]));assert.ok(Object.isFrozen(error.issues[0].path))
      assert.doesNotMatch(JSON.stringify(error)+String(error),/PRIVATE_KEY|PRIVATE_VALUE/)
      return true
    })
  }
  let deep={};for(let i=0;i<70;i++)deep={PRIVATE_KEY:deep}
  assert.throws(()=>inputValidators.api.entries.insert({...valid,payload:deep}),error=>error.issues[0].code==='input_too_complex'&&JSON.stringify(error.issues[0].path)==='["payload"]')
})

test('RPC parsers preserve defaults, nulls, nested shapes, namespaces and validatedRpc inference',async t=>{
  const extended={...snapshot,schemas:['api','other'],functions:[
    {schema:'api',name:'entries',args:[{name:'state',type:'"open" | "closed"',optional:false},{name:'tags',type:'(string | null)[]',optional:true},{name:'address',type:'Database["api"]["CompositeTypes"]["address"]',optional:true}],returns:'number | null'},
    {schema:'api',name:'nothing',args:[],returns:'undefined'},
    {schema:'api',name:'row_input',args:[{name:'row',type:`Database["api"]["Tables"]["records"]['Row']`,optional:false}],returns:'boolean | null'},
    {schema:'other',name:'entries',args:[{name:'count',type:'number',optional:false}],returns:'number | null'},
  ],tables:[...snapshot.tables,{schema:'api',name:'records',view:false,relationships:[],columns:[column('id','number'),column('name','string',{nullable:true})]}]}
  const {rpcValidators,inputValidators,DataAPIInputError,inputValidationWarnings}=await compiled(t,extended)
  const {validatedRpc}=await import('../../sdk/data/dist/index.js')
  assert.deepEqual(rpcValidators.api.entries.parse({state:'open'}),{state:'open'})
  assert.deepEqual(rpcValidators.api.entries.parse({state:null,address:null,tags:null}),{state:null,address:null,tags:null})
  assert.deepEqual(rpcValidators.other.entries.parse({count:3}),{count:3})
  assert.deepEqual(rpcValidators.api.nothing.parse({}),{})
  assert.throws(()=>rpcValidators.api.nothing.parse({PRIVATE_KEY:true}),error=>error.issues[0].code==='unknown_field')
  assert.deepEqual(rpcValidators.api.row_input.parse({row:{id:1,name:null}}),{row:{id:1,name:null}})
  assert.throws(()=>rpcValidators.api.row_input.parse({row:{id:1}}),error=>JSON.stringify(error.issues[0].path)==='["row","name"]')
  assert.equal(typeof inputValidators.api.entries.insert,'function')
  assert.ok(inputValidationWarnings.some(w=>w.function==='api.entries'&&w.reason==='rpc_database_rules_require_review'))
  const valid={state:'closed',tags:['a',null],address:{city:'Paris'}}
  const parsed=rpcValidators.api.entries.parse(valid);assert.deepEqual(parsed,valid);assert.notEqual(parsed.address,valid.address)
  for(const [value,code,path] of [[{},'required',['state']],[{state:'PRIVATE_VALUE'},'invalid_enum',['state']],[{state:'open',tags:[true]},'invalid_type',['tags',0]],[{state:'open',address:{city:4}},'invalid_type',['address','city']],[{state:'open',tags:undefined},'invalid_type',['tags']]]){
    let calls=0
    const result=await validatedRpc(value,rpcValidators.api.entries,async()=>{calls++;return 3})
    assert.equal(result.outcome,'validation_failed');assert.equal(calls,0)
    assert.throws(()=>rpcValidators.api.entries.parse(value),error=>error instanceof DataAPIInputError&&error.issues[0].code===code&&JSON.stringify(error.issues[0].path)===JSON.stringify(path))
  }
  const completed=await validatedRpc(valid,rpcValidators.api.entries,async args=>args.state)
  assert.deepEqual(completed,{outcome:'completed',result:'closed'})
})

test('batch validation checks every row, bounds safe reports and withholds partial payloads',async t=>{
 const {inputValidators,validateInputRows}=await compiled(t,snapshot,[{schema:'api',relation:'entries',name:'count',type:'int2'}])
 const valid={title:'abc',state:'open',count:1}
 const result=validateInputRows([{...valid,count:1.5},{...valid,state:'PRIVATE_VALUE'},valid],inputValidators.api.entries.insert,{maxErrors:1})
 assert.deepEqual(result,{ok:false,total:3,valid:1,invalid:2,errors:[{row:1,code:'invalid_integer',path:['count']}],truncated:true})
 assert.equal('rows' in result,false);assert.ok(Object.isFrozen(result));assert.ok(Object.isFrozen(result.errors))
 const checked=validateInputRows([valid],inputValidators.api.entries.insert)
 assert.equal(checked.ok,true);assert.deepEqual(checked.rows,[valid]);assert.notEqual(checked.rows[0],valid);assert.ok(Object.isFrozen(checked.rows))
 let calls=0
 const all=validateInputRows(Array.from({length:1000},()=>({})),value=>{calls++;return inputValidators.api.entries.insert(value)})
 assert.equal(calls,1000);assert.equal(all.invalid,1000);assert.equal(all.errors.length,100);assert.equal(all.truncated,true)
 const privateError=validateInputRows([valid],()=>{throw new Error('PRIVATE_VALUE')})
 assert.deepEqual(privateError.errors,[{row:1,code:'invalid_input',path:[]}]);assert.doesNotMatch(JSON.stringify(privateError),/PRIVATE_VALUE/)
 const asynchronous=validateInputRows([valid],()=>Promise.reject(new Error('PRIVATE_VALUE')))
 assert.equal(asynchronous.ok,false);await new Promise(resolve=>setImmediate(resolve))
 for(const maxErrors of [0,1001,1.5,NaN]) assert.throws(()=>validateInputRows([],inputValidators.api.entries.insert,{maxErrors}),RangeError)
 assert.throws(()=>validateInputRows(Array(1001),inputValidators.api.entries.insert),RangeError)
})

test('inspector descriptors and validation match generated parsers without catalog expressions',async t=>{
 const metadata=[{schema:'api',relation:'entries',name:'title',type:'varchar',typmod:7},{schema:'api',relation:'entries',name:'count',type:'int2'}]
 const {inputValidators}=await compiled(t,snapshot,metadata)
 const model=inputModel(snapshot,metadata),spec=model.tables.find(table=>table.table==='entries').spec
 assert.equal(model.tables.some(table=>table.table==='entry_view'),false)
 assert.match(model.revision,/^[a-f0-9]{64}$/)
 assert.notEqual(inputModel(snapshot,[{...metadata[0],typmod:9},metadata[1]]).revision,model.revision)
 const valid={title:'abc',state:'open',count:1}
 for(const input of [valid,{...valid,title:'abcd'},{...valid,count:1.5},{...valid,state:'PRIVATE_VALUE'},{...valid,tags:[1]}, {}, {...valid,PRIVATE_KEY:'PRIVATE_VALUE'}, {...valid,payload:{PRIVATE_KEY:undefined}}]){
  let result
  try{result={ok:true,value:inputValidators.api.entries.insert(input)}}catch(error){result={ok:false,issue:error.issues[0]}}
  assert.deepEqual(validateRecord(input,spec),result)
  if(!result.ok)assert.doesNotMatch(JSON.stringify(result),/PRIVATE_KEY|PRIVATE_VALUE/)
 }
})

test('catalog-derived validators match PostgreSQL lengths and integer bounds without changing contracts', {skip:!process.env.DATA_API_TEST_DATABASE_URL || !process.env.DATA_API_RUNTIME_DIR}, async t => {
  const runtime=pathToFileURL(process.env.DATA_API_RUNTIME_DIR+'/'), require=createRequire(new URL('package.json',runtime)), pg=require('pg')
  const {inspectBundle,inspect}=await import(new URL('types.mjs',runtime))
  const admin=new pg.Client({connectionString:process.env.DATA_API_TEST_DATABASE_URL});await admin.connect()
  const database=`input_validation_${process.pid}_${Date.now()}`
  await admin.query(`CREATE DATABASE "${database}"`)
  const url=new URL(process.env.DATA_API_TEST_DATABASE_URL);url.pathname=`/${database}`
  const db=new pg.Client({connectionString:url.toString()});await db.connect()
  t.after(async()=>{await db.end();await admin.query(`DROP DATABASE "${database}" WITH (FORCE)`);await admin.end()})
  await db.query(`CREATE SCHEMA api; CREATE TYPE api.status AS ENUM ('open','closed'); CREATE DOMAIN api.positive AS integer CHECK(VALUE>0);
    CREATE TABLE api.entries(id integer GENERATED ALWAYS AS IDENTITY, title varchar(3) NOT NULL, state api.status NOT NULL, count smallint NOT NULL, domain_value api.positive, body jsonb, CHECK(count>=0));
    CREATE VIEW api.entry_view AS SELECT title FROM api.entries;
    CREATE FUNCTION api.choose_state(chosen api.status,count_value integer DEFAULT 3) RETURNS integer LANGUAGE sql AS 'SELECT count_value';
    COMMENT ON FUNCTION api.choose_state(api.status,integer) IS '@gregale:rpc';
    CREATE TABLE api.keyed_rows(owner text NOT NULL,id integer NOT NULL,body text,version integer NOT NULL DEFAULT 1,PRIMARY KEY(owner,id));
    CREATE FUNCTION api.bump_keyed_version() RETURNS trigger LANGUAGE plpgsql AS 'BEGIN NEW.version:=OLD.version+1; RETURN NEW; END';
    CREATE TRIGGER bump_version BEFORE UPDATE ON api.keyed_rows FOR EACH ROW EXECUTE FUNCTION api.bump_keyed_version();
    COMMENT ON COLUMN api.keyed_rows.version IS '@gregale:version';`)
  const output=execFileSync(process.execPath,[new URL('types.mjs',runtime).pathname,'--validators'],{env:{...process.env,DATABASE_URL:url.toString(),DATA_API_SCHEMAS:'api'},encoding:'utf8'})
  assert.ok(output.includes('export const inputValidators = {'))
  assert.throws(()=>execFileSync(process.execPath,[new URL('types.mjs',runtime).pathname,'--validators','--snapshot'],{env:{...process.env,DATABASE_URL:url.toString(),DATA_API_SCHEMAS:'api'},stdio:'pipe'}))
  const {snapshot,metadata}=await inspectBundle(url.toString(),['api'],{validators:true})
  assert.deepEqual(snapshot,await inspect(url.toString(),['api']))
  assert.equal(snapshot.version,4)
  const keyed=snapshot.tables.find(table=>table.name==='keyed_rows')
  assert.deepEqual(keyed.primary_key,{columns:['owner','id'],filterable:true})
  assert.deepEqual(keyed.concurrency,{column:'version',kind:'integer_version'})
  const model=inputModel(snapshot,metadata).tables.find(table=>table.table==='keyed_rows')
  const edit=keyedEdit(model,{owner:'team,one',id:2,body:'old',version:1})
  const target=editTarget({edit,table:'keyed_rows',fingerprint:'a'.repeat(64)},'http://localhost')
  assert.equal(target.searchParams.get('owner'),'eq."team,one"');assert.equal(target.searchParams.get('id'),'eq.2');assert.equal(target.searchParams.get('version'),'eq.1')
  assert.equal(keyedEdit(model,{owner:'a',id:Number.MAX_SAFE_INTEGER+1,version:1}),null)
  await db.query("INSERT INTO api.keyed_rows(owner,id,body) VALUES('a',1,'one'),('b',1,'two')")
  const update=await db.query("UPDATE api.keyed_rows SET body='edited' WHERE owner='a' AND id=1 AND version=1 RETURNING version")
  assert.equal(update.rows[0].version,2)
  assert.equal((await db.query("UPDATE api.keyed_rows SET body='stale' WHERE owner='a' AND id=1 AND version=1 RETURNING version")).rowCount,0)
  assert.equal((await db.query("SELECT body FROM api.keyed_rows WHERE owner='b' AND id=1")).rows[0].body,'two')
  await db.query("COMMENT ON COLUMN api.keyed_rows.version IS NULL")
  const unannotated=await inspect(url.toString(),['api']);assert.equal(unannotated.tables.find(table=>table.name==='keyed_rows').concurrency,undefined)
  const {inputValidators,rpcValidators,inputValidationWarnings}=await compiled(t,snapshot,metadata)
  assert.deepEqual(rpcValidators.api.choose_state.parse({chosen:'open'}),{chosen:'open'})
  assert.deepEqual(rpcValidators.api.choose_state.parse({chosen:null,count_value:null}),{chosen:null,count_value:null})
  assert.equal((await db.query("SELECT api.choose_state('open') AS value")).rows[0].value,3)
  assert.throws(()=>rpcValidators.api.choose_state.parse({}),error=>error.issues[0].code==='required')
  assert.throws(()=>rpcValidators.api.choose_state.parse({chosen:'invalid'}),error=>error.issues[0].code==='invalid_enum')
  assert.ok(inputValidationWarnings.some(w=>w.column==='domain_value'&&w.reason==='domain_rules_require_review'))
  const insert=inputValidators.api.entries.insert
  for(const value of [{title:'😀ab',state:'open',count:32767},{title:'abc',state:'closed',count:0,body:{data:[1]}}]) {
    const row=insert(value);await db.query('INSERT INTO api.entries(title,state,count,body) VALUES($1,$2,$3,$4)',[row.title,row.state,row.count,row.body??null])
  }
  for(const value of [{title:'abcd',state:'open',count:1},{title:'abc',state:'invalid',count:1},{title:'abc',state:'open',count:32768}]) {
    assert.throws(()=>insert(value));await assert.rejects(()=>db.query('INSERT INTO api.entries(title,state,count) VALUES($1,$2,$3)',[value.title,value.state,value.count]))
  }
  // CHECK SQL is deliberately not interpreted: the database remains authoritative.
  assert.equal(insert({title:'abc',state:'open',count:-1}).count,-1)
  await assert.rejects(()=>db.query("INSERT INTO api.entries(title,state,count) VALUES('abc','open',-1)"))
})


test('team CSV preview consumes generated schema rules and bounds complete error counts', {skip:!process.env.DATA_API_TEST_DATABASE_URL||!process.env.DATA_API_RUNTIME_DIR},async t=>{
 const runtime=pathToFileURL(process.env.DATA_API_RUNTIME_DIR+'/'),require=createRequire(new URL('package.json',runtime)),pg=require('pg')
 const {inspectBundle}=await import(new URL('types.mjs',runtime))
 const admin=new pg.Client({connectionString:process.env.DATA_API_TEST_DATABASE_URL});await admin.connect()
 const database=`csv_validation_${process.pid}_${Date.now()}`;await admin.query(`CREATE DATABASE "${database}"`)
 const url=new URL(process.env.DATA_API_TEST_DATABASE_URL);url.pathname=`/${database}`
 const db=new pg.Client({connectionString:url.toString()});await db.connect()
 const root=await mkdtemp(join(tmpdir(),'gregale-team-csv-'))
 t.after(async()=>{await rm(root,{recursive:true,force:true});await db.end();await admin.query(`DROP DATABASE "${database}" WITH (FORCE)`);await admin.end()})
 const migrations=new URL('../../cmd/gregale/templates/data-api-teams/migrations/sql/',import.meta.url)
 for(const name of (await readdir(migrations)).filter(name=>name.endsWith('.sql')).sort()) await db.query(await readFile(new URL(name,migrations),'utf8'))
 // A future schema requirement must block the old import mapping before writes.
 await db.query('ALTER TABLE api.team_orders ADD COLUMN import_label text NOT NULL')
 const {snapshot,metadata}=await inspectBundle(url.toString(),['api'],{validators:true})
 await cp(new URL('../../cmd/gregale/templates/data-api-teams/client/src/',import.meta.url),join(root,'src'),{recursive:true})
 await writeFile(join(root,'src/database.types.ts'),generateValidators(snapshot,metadata))
 await writeFile(join(root,'package.json'),'{"type":"module"}')
 await mkdir(join(root,'node_modules/@gregale'),{recursive:true});await symlink(resolve('sdk/data'),join(root,'node_modules/@gregale/data'))
 execFileSync(process.execPath,[resolve('sdk/data/node_modules/typescript/bin/tsc'),'--strict','--target','ES2022','--module','NodeNext','--moduleResolution','NodeNext','--outDir',join(root,'dist'),join(root,'src/imports.ts')],{encoding:'utf8'})
 const {previewCsv}=await import(pathToFileURL(join(root,'dist/imports.js')))
 const checked=previewCsv('total,status\n1,draft\n2,submitted',{total:'total',status:'status'},{tenant:'team',subject:'alice'})
 assert.equal(checked.total,2);assert.equal(checked.invalid,2);assert.equal(checked.valid,0);assert.equal(checked.items.length,0)
 assert.deepEqual(checked.errors.map(error=>({row:error.row,code:error.code,path:error.path})),[{row:2,code:'required',path:['import_label']},{row:3,code:'required',path:['import_label']}])
 assert.equal((await db.query('SELECT count(*)::int AS count FROM api.team_orders')).rows[0].count,0)
 const legacy=previewCsv('total,status\n1,draft',{total:'total',status:'status'})
 assert.equal(legacy.items.length,1);assert.equal(legacy.invalid,0)
 const bounded=previewCsv('total,status\n'+'-1,draft\n'.repeat(300),{total:'total',status:'status'})
 assert.equal(bounded.invalid,300);assert.equal(bounded.errors.length,100);assert.equal(bounded.errorsTruncated,true)
})

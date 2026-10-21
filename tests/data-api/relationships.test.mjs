import test from 'node:test'
import assert from 'node:assert/strict'
import {relationshipLinks} from '../../cmd/gregale/templates/data-api/inspector-data.mjs'
const table=(name,names,relationships=[])=>({schema:'api',name,columns:names.map(name=>({name})),relationships})
const fk={foreignKeyName:'child_parent',columns:['tenant','parent_id'],referencedRelation:'parents',referencedColumns:['tenant','id']}
const parent=table('parents',['tenant','id']),child=table('children',['tenant','parent_id'],[fk]),snapshot={tables:[parent,child]}
test('relationship navigation includes every composite component and preserves literal string values',()=>{
 const links=relationshipLinks(snapshot,child,{tenant:'a,"b&x=1',parent_id:7})
 assert.deepEqual(links[0].predicates,[['tenant','eq.a,"b&x=1'],['id','eq.7']])
 assert.equal(links[0].table,'parents')
 assert.deepEqual(relationshipLinks(snapshot,parent,{tenant:'alice',id:7})[0].predicates,[['tenant','eq.alice'],['parent_id','eq.7']])
})
test('incomplete, nullable, unsafe and unexposed relationships never become unfiltered reads',()=>{
 for(const row of [{tenant:'a'},{tenant:null,parent_id:7},{tenant:'a',parent_id:Number.MAX_SAFE_INTEGER+1},{tenant:'a',parent_id:{}}])assert.deepEqual(relationshipLinks(snapshot,child,row),[])
 assert.deepEqual(relationshipLinks({tables:[child]},child,{tenant:'a',parent_id:7}),[])
 const malformed=table('children',['tenant','parent_id'],[{...fk,referencedColumns:[]}])
 assert.deepEqual(relationshipLinks({tables:[parent,malformed]},malformed,{tenant:'a',parent_id:7}),[])
})

import {validateViews,viewProblem} from '../../cmd/gregale/templates/data-api/inspector-data.mjs'
import {requestStore} from '../../cmd/gregale/templates/data-api/dev-requests.mjs'
import {mkdtemp,readFile,rm,symlink} from 'node:fs/promises'
import {join} from 'node:path'
import {tmpdir} from 'node:os'
const fingerprint='a'.repeat(64)
const definition=()=>({name:'Related rows',fingerprint,steps:[
 {schema:'api',table:'parents',columns:['tenant','id'],filter:{column:'tenant',operator:'eq',value:'team'},sort:null,via:null},
 {schema:'api',table:'children',columns:['parent_id'],filter:null,sort:{column:'parent_id',direction:'desc'},via:{foreignKey:'child_parent',direction:'children'}}
]})
test('portable views exclude identity and row scope and detect stale or missing dependencies',()=>{
 const view=definition();assert.equal(viewProblem(view,snapshot,fingerprint),null)
 assert.match(viewProblem(view,snapshot,'b'.repeat(64)),/Schema changed/)
 assert.match(viewProblem(view,{tables:[parent]},fingerprint),/unavailable/)
 const changed={tables:[parent,{...child,relationships:[]}]};assert.match(viewProblem(view,changed,fingerprint),/relationship/)
 for(const extra of [{token:'secret'},{identity:'alice'},{predicates:[['tenant','eq.alice']]},{rows:[]}])assert.throws(()=>validateViews({version:1,views:[{...view,...extra}]}))
 const scoped=definition();scoped.steps[1].predicates=[['parent_id','eq.5']];assert.throws(()=>validateViews({version:1,views:[scoped]}))
 const wrongRoot=definition();wrongRoot.steps[0].via={foreignKey:'child_parent',direction:'children'};assert.throws(()=>validateViews({version:1,views:[wrongRoot]}))
})
test('view files use atomic revisions, reject stale overwrites, and refuse symlinks',async t=>{
 const root=await mkdtemp(join(tmpdir(),'gregale-view-store-'));t.after(()=>rm(root,{recursive:true,force:true}))
 const path=join(root,'data-api.views.json'),store=requestStore(path,{validate:validateViews,empty:{version:1,views:[]}})
 const initial=await store.read();assert.deepEqual(initial.views,[])
 const saved=await store.save({version:1,views:[definition()]},initial.revision)
 assert.notEqual(saved.revision,initial.revision)
 await assert.rejects(store.save({version:1,views:[]},initial.revision),{status:409})
 assert.deepEqual(JSON.parse(await readFile(path,'utf8')),{version:1,views:[definition()]})
 const link=join(root,'link.json');await symlink(path,link)
 await assert.rejects(requestStore(link,{validate:validateViews,empty:{version:1,views:[]}}).read())
})

test('view size limits count UTF-8 bytes and missing-file defaults stay isolated',async t=>{
 const views=Array.from({length:35},(_,index)=>{const view=definition();view.name=`View ${index}`;view.steps[0].filter.value='😀'.repeat(512);return view})
 const value={version:1,views};assert.ok(JSON.stringify(value).length<65536)
 assert.throws(()=>validateViews(value))
 const root=await mkdtemp(join(tmpdir(),'gregale-view-default-'));t.after(()=>rm(root,{recursive:true,force:true}))
 const store=requestStore(join(root,'data-api.views.json'),{validate:validateViews,empty:{version:1,views:[]}})
 const empty=await store.read();empty.views.push(definition())
 assert.deepEqual((await store.read()).views,[])
})

test('support view metadata requires a named read-only identity and rejects credentials or unsafe modes',()=>{
 const support={name:'Support',fingerprint:'a'.repeat(64),mode:'read_only',identity:'support_a',steps:[{schema:'api',table:'notes',columns:['body'],filter:null,sort:null,via:null}]}
 assert.equal(validateViews({version:1,views:[support]}).views[0].identity,'support_a')
 for(const view of [{...support,mode:'write'},{...support,identity:''},{...support,token:'secret'},{...support,identity:undefined},{...support,mode:undefined}])assert.throws(()=>validateViews({version:1,views:[view]}))
})

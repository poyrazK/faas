import test from 'node:test'
import assert from 'node:assert/strict'
import http from 'node:http'
import {pathToFileURL} from 'node:url'
import {join} from 'node:path'
const runtime=process.env.DATA_API_RUNTIME_DIR
const module=runtime?await import(pathToFileURL(join(runtime,'server.mjs'))):null
const listen=server=>new Promise(resolve=>server.listen(0,'127.0.0.1',resolve))
test('signed read-only identities reject table writes and RPCs before upstream while retaining binding-role isolation',{skip:!runtime},async t=>{
 let upstreamCalls=0,claims
 const upstream=http.createServer((req,res)=>{upstreamCalls++;claims=JSON.parse(Buffer.from(req.headers.authorization.split('.')[1],'base64url'));res.writeHead(200,{'Content-Type':'application/json'});res.end('[]')})
 await listen(upstream);t.after(()=>new Promise(resolve=>upstream.close(resolve)))
 const config={role:'gregale_data_api',schemas:['api'],functions:[{schema:'api',name:'echo'}],secret:Buffer.alloc(32,1),fingerprint:'a'.repeat(64),origins:[]}
 const records=[],gateway=module.createServer(config,async token=>({sub:'agent',exp:Math.floor(Date.now()/1000)+60,role:'postgres',...(token==='support'?{gregale_read_only:true}:token==='invalid'?{gregale_read_only:'true'}:{})}),upstream.address().port,upstream.address().port,r=>records.push(r))
 await listen(gateway);t.after(()=>new Promise(resolve=>gateway.close(resolve)))
 const url=`http://127.0.0.1:${gateway.address().port}`,headers={Authorization:'Bearer support','Content-Type':'application/json'}
 for(const [method,path] of [['POST','/rest/v1/notes'],['PATCH','/rest/v1/notes?id=eq.1'],['DELETE','/rest/v1/notes?id=eq.1'],['POST','/rest/v1/rpc/echo'],['PATCH',`/__gregale/contract/${config.fingerprint}/rest/v1/notes?id=eq.1`]]){
  const response=await fetch(url+path,{method,headers,...(method==='POST'||method==='PATCH'?{body:'{}'}:{})});assert.equal(response.status,403);assert.equal((await response.json()).code,'read_only_identity')
 }
 assert.equal(upstreamCalls,0);assert.ok(records.some(r=>r.code==='read_only_identity'))
 for(const method of ['GET','HEAD'])assert.equal((await fetch(url+'/rest/v1/notes',{method,headers})).status,200)
 assert.equal(claims.role,'gregale_data_api');assert.equal(claims.gregale_read_only,true)
 assert.equal((await fetch(url+'/rest/v1/notes',{headers:{Authorization:'Bearer invalid'}})).status,401)
 assert.equal((await fetch(url+'/rest/v1/notes',{method:'POST',headers:{...headers,Authorization:'Bearer editor'},body:'{}'})).status,200)
 assert.equal(claims.role,'gregale_data_api')
})

test('private-base view relationships expose only complete readable projections',{skip:!runtime},async()=>{
 const {viewRelationships}=await import(pathToFileURL(join(runtime,'types.mjs')))
 const column=(schema,relation,id,number,name,kind='r')=>({schema,relation,id:String(id),number,name,kind})
 const columns=[column('hidden','cases',100,1,'tenant'),column('hidden','cases',100,2,'id'),column('hidden','replies',101,1,'tenant'),column('hidden','replies',101,2,'case_id'),column('api','support_cases',200,1,'tenant','v'),column('api','support_cases',200,2,'id','v'),column('api','support_replies',201,1,'tenant','v'),column('api','support_replies',201,2,'case_id','v')]
 const action=(id,names)=>`({QUERY :commandType 1 :setOperations <> :targetList (${names.map((_,index)=>`{TARGETENTRY :resno ${index+1} :resorigtbl ${id} :resorigcol ${index+1} :resjunk false}`).join(' ')})})`
 const views=[{schema:'api',relation:'support_cases',id:'200',action:action(100,['tenant','id'])},{schema:'api',relation:'support_replies',id:'201',action:action(101,['tenant','case_id'])}]
 const relations=[{schema:'hidden',relation:'replies',referenced_schema:'hidden',referenced_relation:'cases',name:'case_fk',columns:['tenant','case_id'],referenced_columns:['tenant','id'],one_to_one:false}]
 const result=viewRelationships(columns,relations,views,['api']);assert.equal(result.length,1);assert.equal(result[0].schema,'api');assert.equal(result[0].relation,'support_replies');assert.equal(result[0].referenced_relation,'support_cases');assert.equal(result[0].referenced_schema,'api')
 assert.equal(viewRelationships(columns.filter(c=>!(c.id==='101'&&c.number===1)),relations,views,['api']).length,0)
})

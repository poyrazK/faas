import test from 'node:test'
import assert from 'node:assert/strict'
import {mkdtemp,mkdir,writeFile,rm} from 'node:fs/promises'
import {tmpdir} from 'node:os'
import {join} from 'node:path'
import http from 'node:http'
import {browserExample} from '../dev-browser.mjs'

test('the browser example shares the private fragment capability and rejects foreign hosts',async t=>{
 const root=await mkdtemp(join(tmpdir(),'gregale-browser-'));t.after(()=>rm(root,{recursive:true,force:true}))
 await mkdir(join(root,'client/browser'),{recursive:true})
 await writeFile(join(root,'client/browser/index.html'),'<body><form id="session"></form><script src="client.js"></script></html>')
 await writeFile(join(root,'client/browser/client.js'),'document.body.dataset.loaded="yes"')
 let origin
 const state=()=>({url:origin})
 const server=http.createServer((req,res)=>{if(!example.handle(req,res)){res.writeHead(404);res.end()}})
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));origin=`http://127.0.0.1:${server.address().port}`
 const example=await browserExample(root,state,origin+'/__gregale/dev/#private-capability')
 t.after(async()=>{server.closeAllConnections();await new Promise(resolve=>server.close(resolve))})
 assert.equal(example.url,origin+'/__gregale/example/#private-capability')
 const response=await fetch(example.url);assert.equal(response.status,200);const html=await response.text();assert.match(html,/session.js/);assert.ok(!html.includes('private-capability'));assert.equal(response.headers.get('cache-control'),'no-store')
 const foreignStatus=await new Promise((resolve,reject)=>{const req=http.get(origin+'/__gregale/example/',{headers:{Host:'foreign.example'}},res=>{res.resume();res.on('end',()=>resolve(res.statusCode))});req.on('error',reject)})
 assert.equal(foreignStatus,403)
 assert.equal((await fetch(example.url+'',{method:'POST'})).status,404)
 assert.equal((await fetch(origin+'/__gregale/example/../../package.json')).status,404)
})

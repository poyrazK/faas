import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import {createServer} from 'node:http'
import {pathToFileURL} from 'node:url'
import test from 'node:test'
import {browserInspector} from './browser-inspector.mjs'
import {waitUntil} from './browser-wait.mjs'

test('an unchanged background session preserves identity changes, while schema changes invalidate the view', {skip:!process.env.DATA_API_CHROMIUM_BIN,timeout:45000},async t=>{
 const root=process.env.DATA_API_RUNTIME_DIR?pathToFileURL(process.env.DATA_API_RUNTIME_DIR+'/'):new URL('../../cmd/gregale/templates/data-api/',import.meta.url)
 const session={ready:true,status:'ready',fingerprint:'a'.repeat(64),input_model:{revision:'same',tables:[]},identities:{alice:{subject:'alice',token:'synthetic-alice'},bob:{subject:'bob',token:'synthetic-bob'}},snapshot:{tables:[{schema:'api',name:'notes',columns:[{name:'id',type:'Int'}]}],functions:[]}}
 const server=createServer(async(request,response)=>{
  if(request.url==='/session'){response.writeHead(200,{'Content-Type':'application/json'}).end(JSON.stringify(session));return}
  const name=request.url==='/'?'inspector.html':request.url.slice(1)
  if(!/^[a-z-]+\.(mjs|html)$/.test(name)){response.writeHead(404).end();return}
  try{response.writeHead(200,{'Content-Type':name.endsWith('.mjs')?'text/javascript':'text/html'}).end(await readFile(new URL(name,root)))}catch{response.end()}
 })
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve))
 t.after(async()=>{server.closeAllConnections();await new Promise(resolve=>server.close(resolve))})
 await browserInspector(`http://127.0.0.1:${server.address().port}/`,async evaluate=>{
  const until=expression=>waitUntil(evaluate,expression,async()=>'Inspector refresh did not complete')
  await until("document.querySelector('#identity').options.length===2&&[...document.querySelector('#data-filter').options].some(option=>option.value==='id')")
  await evaluate(`(()=>{const original=window.fetch;window.fetch=async(...args)=>{const response=await original(...args);if(String(args[0])==='session'&&!window.refreshHeld){const json=response.json.bind(response);response.json=async()=>{const value=await json();window.refreshHeld=true;await new Promise(resolve=>window.releaseRefresh=resolve);return value}}return response}})()`)
  await until('window.refreshHeld===true')
  await evaluate("document.querySelector('#identity').value='bob';document.querySelector('#identity').dispatchEvent(new Event('change'));document.querySelector('#data-filter').value='id';document.querySelector('#data-value').value='42'")
  await evaluate('(async()=>{window.releaseRefresh();await new Promise(resolve=>setTimeout(resolve,0))})()')
  assert.equal(await evaluate("document.querySelector('#identity').value"),'bob')
  assert.equal(await evaluate("document.querySelector('#data-filter').value"),'id')
  assert.equal(await evaluate("document.querySelector('#data-value').value"),'42')
  session.fingerprint='b'.repeat(64)
  await until("document.querySelector('#fingerprint').textContent.includes('"+session.fingerprint+"')")
  assert.equal(await evaluate("document.querySelector('#data-filter').value"),'')
  assert.equal(await evaluate("document.querySelector('#identity').value"),'bob')
 })
})

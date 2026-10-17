import assert from 'node:assert/strict'
import {browserInspector} from './browser-inspector.mjs'
import {waitUntil} from './browser-wait.mjs'
export async function browserSupportViews(url){
 const inspectorURL=new URL(url),state=await(await fetch(new URL('session',inspectorURL),{headers:{Authorization:'Bearer '+inspectorURL.hash.slice(1)}})).json()
 const ro={Authorization:'Bearer '+state.identities.support_a.token,'Accept-Profile':'api','Content-Profile':'api','Content-Type':'application/json'}
 let response=await fetch(new URL('/rest/v1/support_cases',inspectorURL),{headers:ro});assert.equal(response.status,200);let records=await response.json();assert.equal(records.length,1);assert.equal(records[0].tenant,'shop-a');assert.ok(!JSON.stringify(records).includes('private'))
 response=await fetch(new URL('/rest/v1/support_cases?select=internal_notes',inspectorURL),{headers:ro});assert.equal(response.ok,false)
 response=await fetch(new URL('/rest/v1/cases',inspectorURL),{headers:{...ro,'Accept-Profile':'support_private'}});assert.equal(response.ok,false)
 for(const method of ['POST','PATCH','DELETE']){response=await fetch(new URL('/rest/v1/support_cases',inspectorURL),{method,headers:ro,...(method!=='DELETE'?{body:'{}'}:{})});assert.equal(response.status,403);assert.equal((await response.json()).code,'read_only_identity')}
 assert.ok(state.snapshot.tables.every(table=>table.schema==='api'&&!table.columns.some(column=>column.name==='internal_notes')))
 await browserInspector(url,async evaluate=>{
  const until=expression=>waitUntil(evaluate,expression,'Support inspector action did not finish')
  await until("document.querySelector('#status').textContent.includes('ready')")
  await evaluate("document.querySelector('#identity').value='support_a';document.querySelector('#identity').dispatchEvent(new Event('change'));document.querySelector('#resource').value=JSON.stringify(['tables','api.support_cases']);document.querySelector('#resource').dispatchEvent(new Event('change'));document.querySelector('#data-read').click()")
  await until("!document.querySelector('#data-read').disabled&&document.querySelector('#data-rows').textContent.includes('Support case A')")
  assert.equal(await evaluate("document.querySelector('#data-insert').disabled"),true);assert.equal(await evaluate("document.querySelector('#bulk-review').disabled"),true);assert.equal(await evaluate("document.querySelector('#csv-import').disabled"),true);assert.equal(await evaluate("document.querySelectorAll('[data-bulk-row]').length"),0)
  await evaluate("document.querySelector('#method').value='POST';document.querySelector('#method').dispatchEvent(new Event('change'))")
  assert.equal(await evaluate("document.querySelector('#send').disabled"),true)
  await evaluate("for(const option of document.querySelector('#data-columns').options)option.selected=['tenant','id','title'].includes(option.value);document.querySelector('#data-columns').dispatchEvent(new Event('change'));document.querySelector('#data-filter').value='title';document.querySelector('#data-operator').value='ilike';document.querySelector('#data-value').value='*case*';document.querySelector('#data-read').click()")
  await until("!document.querySelector('#data-read').disabled&&document.querySelector('#data-rows').textContent.includes('Support case A')")
  await evaluate("document.querySelector('#view-read-only').checked=true;document.querySelector('#view-name').value='Support cases';document.querySelector('#view-save').click()")
  await until("document.querySelector('#view-status').textContent.startsWith('View saved')")
  await evaluate("document.querySelector('#identity').value='alice';document.querySelector('#identity').dispatchEvent(new Event('change'));document.querySelector('#view-load').click()")
  await until("!document.querySelector('#data-read').disabled&&document.querySelector('#data-rows').textContent.includes('Support case A')")
  assert.equal(await evaluate("document.querySelector('#identity').value"),'support_a')
  await evaluate("document.querySelector('#data-rows [data-relationship*=support_replies]').click()")
  await until("!document.querySelector('#data-read').disabled&&document.querySelector('#data-rows').textContent.includes('Support reply A')")
  assert.ok(!await evaluate("document.querySelector('#data-rows').textContent.includes('Support reply B')"))
  await evaluate("(()=>{window.supportExport=null;const create=URL.createObjectURL,click=HTMLAnchorElement.prototype.click;URL.createObjectURL=blob=>{window.supportBlob=blob;return create(blob)};HTMLAnchorElement.prototype.click=function(){if(this.download==='data-api.export.csv'){window.supportExport=window.supportBlob.text();return}return click.call(this)}})();document.querySelector('#data-export').click()")
  await until("document.querySelector('#export-stop').disabled&&window.supportExport!==null")
  const csv=await evaluate('window.supportExport');assert.ok(csv.includes('Support reply A'));assert.ok(!csv.includes('Support reply B'));assert.ok(!csv.includes('private'))
  await evaluate("document.querySelector('#identity').value='support_b';document.querySelector('#identity').dispatchEvent(new Event('change'));document.querySelector('#data-read').click()")
  await until("!document.querySelector('#data-read').disabled&&document.querySelector('#data-rows').textContent.includes('Support reply B')")
  assert.ok(!await evaluate("document.querySelector('#data-rows').textContent.includes('Support reply A')"))
 })
}

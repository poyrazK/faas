import {browserBulkHistory} from './browser-bulk-history.mjs'
import {browserBulkReconcile} from './browser-bulk-reconcile.mjs'
import {browserBulkPreflight} from './browser-bulk-preflight.mjs'
import {browserMatchingWrite} from './browser-matching-write.mjs'
import {browserBulkWrite} from './browser-bulk-write.mjs'
import {browserCsvExport} from './browser-csv-export.mjs'
import {browserCsvImport} from './browser-csv-import.mjs'
import assert from 'node:assert/strict'
import {browserInspector} from './browser-inspector.mjs'
import {waitUntil} from './browser-wait.mjs'
export async function browserDataBrowser(url){
 const inspectorURL=new URL(url),session=await (await fetch(new URL('session',inspectorURL),{headers:{Authorization:'Bearer '+inspectorURL.hash.slice(1)}})).json()
 let guidedArtifact
 const apiHeaders={Authorization:'Bearer '+session.identities.alice.token,'Content-Type':'application/json','Content-Profile':'api',Prefer:'return=representation'}

 await browserInspector(url,async evaluate=>{
  const until=expression=>waitUntil(evaluate,expression,async()=>'Data browser did not finish the requested action: '+await evaluate("document.querySelector('#data-status').textContent+' / '+document.querySelector('#access-status').textContent+' / '+document.querySelector('#access-results').textContent+' / '+document.querySelector('#csv-status').textContent+' / '+document.querySelector('#csv-progress').textContent+' / bulk: '+document.querySelector('#bulk-status').textContent+' / preflight: '+document.querySelector('#bulk-preflight-status').textContent"))
  await until("document.querySelector('#resource')?.options.length>0")
  await evaluate(`document.querySelector('#resource').value=JSON.stringify(['tables','api.notes']);document.querySelector('#resource').dispatchEvent(new Event('change'));
   document.querySelector('#identity').value='alice';document.querySelector('#identity').dispatchEvent(new Event('change'));
   window.dataBrowserPosts=0;const originalFetch=window.fetch;window.fetch=(url,options)=>{if(options?.method==='POST'&&String(url).includes('/rest/v1/notes'))window.dataBrowserPosts++;return originalFetch(url,options)};`)
  const fill=async(subject,body)=>evaluate(`(()=>{document.querySelector('[data-field=subject]').value=${JSON.stringify(subject)};document.querySelector('[data-field=subject]').dispatchEvent(new Event('input'));document.querySelector('[data-field=body]').value=${JSON.stringify(body)};document.querySelector('[data-field=body]').dispatchEvent(new Event('input'));document.querySelector('#data-review').click()})()`)
  await fill('alice','x'.repeat(129))
  await until("document.querySelector('#data-write-status').textContent.includes('Fix')")
  assert.equal(await evaluate('window.dataBrowserPosts'),0)
  await fill('alice','inspector-browser-created')
  await until("document.querySelector('#data-plan').textContent.includes('inspector-browser-created')")
  assert.equal(await evaluate("document.querySelector('#data-insert').disabled"),true)
  await evaluate("document.querySelector('#identity').value='bob';document.querySelector('#identity').dispatchEvent(new Event('change'))")
  assert.equal(await evaluate("document.querySelector('#data-plan').textContent"),'')
  assert.equal(await evaluate('window.dataBrowserPosts'),0)
  await evaluate("document.querySelector('#identity').value='alice';document.querySelector('#identity').dispatchEvent(new Event('change'))")
  await fill('bob','inspector-browser-forbidden')
  await evaluate("document.querySelector('#data-approved').checked=true;document.querySelector('#data-approved').dispatchEvent(new Event('change'));document.querySelector('#data-insert').click()")
  await until("document.querySelector('#data-write-status').textContent.includes('refused (403)')")
  assert.equal(await evaluate('window.dataBrowserPosts'),1)
  await fill('alice','inspector-browser-created')
  await evaluate("document.querySelector('#data-approved').checked=true;document.querySelector('#data-approved').dispatchEvent(new Event('change'));document.querySelector('#data-insert').click()")
  await until("document.querySelector('#data-write-status').textContent.includes('confirmed')")
  assert.equal(await evaluate('window.dataBrowserPosts'),2)
  await evaluate("document.querySelector('#data-filter').value='body';document.querySelector('#data-value').value='inspector-browser-created';document.querySelector('#data-read').click()")
  await until("document.querySelector('#data-rows').textContent.includes('inspector-browser-created')")
  const rows=await (await fetch(new URL('/rest/v1/notes?body=eq.inspector-browser-created',inspectorURL),{headers:apiHeaders})).json()
  assert.equal(rows.length,1);const row=rows[0]
  const prepareEdit=async()=>{
   await evaluate(`(()=>{document.querySelector('#data-rows button').click();const body=document.querySelector('#data-fields [data-field=body]');const mode=document.getElementById(body.id.replace('field','mode'));mode.value='value';mode.dispatchEvent(new Event('change'));body.value='inspector-browser-edited';body.dispatchEvent(new Event('input'));document.querySelector('#data-review').click()})()`)
   await until("document.querySelector('#data-plan').textContent.includes('expectedVersion')")
   assert.equal(await evaluate("Boolean(document.querySelector('#data-fields [data-field=id],#data-fields [data-field=version]'))"),false)
  }
  await prepareEdit()
  // Another write advances the reviewed version before the user's confirmation.
  const concurrent=await fetch(new URL(`/rest/v1/notes?id=eq.${row.id}&version=eq.${row.version}`,inspectorURL),{method:'PATCH',headers:apiHeaders,body:JSON.stringify({body:'inspector-browser-concurrent'})})
  assert.equal(concurrent.status,200)
  await evaluate("document.querySelector('#data-approved').checked=true;document.querySelector('#data-approved').dispatchEvent(new Event('change'));document.querySelector('#data-insert').click()")
  await until("document.querySelector('#data-write-status').textContent.includes('Read it again')")
  const afterConflict=await (await fetch(new URL(`/rest/v1/notes?id=eq.${row.id}`,inspectorURL),{headers:apiHeaders})).json()
  assert.equal(afterConflict[0].body,'inspector-browser-concurrent');assert.equal(afterConflict[0].version,row.version+1)
  await evaluate("document.querySelector('#data-value').value='inspector-browser-concurrent';document.querySelector('#data-read').click()")
  await until("document.querySelector('#data-rows').textContent.includes('inspector-browser-concurrent')")
  await prepareEdit()
  await evaluate("document.querySelector('#data-approved').checked=true;document.querySelector('#data-approved').dispatchEvent(new Event('change'));document.querySelector('#data-insert').click()")
  await until("document.querySelector('#data-write-status').textContent.startsWith('Update')&&document.querySelector('#data-write-status').textContent.includes('confirmed')")
  const saved=await (await fetch(new URL(`/rest/v1/notes?id=eq.${row.id}`,inspectorURL),{headers:apiHeaders})).json()
  assert.equal(saved[0].body,'inspector-browser-edited');assert.equal(saved[0].version,row.version+2)
  await evaluate("document.querySelector('#identity').value='bob';document.querySelector('#identity').dispatchEvent(new Event('change'));document.querySelector('#data-filter').value='body';document.querySelector('#data-value').value='inspector-browser-edited';document.querySelector('#data-read').click()")
  await until("document.querySelector('#data-status').textContent.startsWith('0 visible')")
  assert.equal(await evaluate("document.querySelector('#data-rows').textContent.includes('inspector-browser-edited')"),false)
  assert.equal(await evaluate('window.dataBrowserPosts'),2)
  const child=await fetch(new URL('/rest/v1/comments',inspectorURL),{method:'POST',headers:apiHeaders,body:JSON.stringify({subject:'alice',note_id:row.id,body:'inspector-related-child'})})
  assert.equal(child.status,201);const childRow=(await child.json())[0]
  await evaluate("document.querySelector('#identity').value='alice';document.querySelector('#identity').dispatchEvent(new Event('change'));document.querySelector('#data-filter').value='body';document.querySelector('#data-value').value='inspector-browser-edited';document.querySelector('#data-read').click()")
  await until("document.querySelector('#data-rows').textContent.includes('inspector-browser-edited')")
  await evaluate("document.querySelector('#data-rows [data-relationship*=comments]').click()")
  await until("document.querySelector('#data-rows').textContent.includes('inspector-related-child')")
  assert.equal(await evaluate("document.querySelector('#resource').value"),JSON.stringify(['tables','api.comments']))
  await until("!document.querySelector('#view-save').disabled")
  await evaluate("for(const option of document.querySelector('#data-columns').options)option.selected=option.value==='body';document.querySelector('#data-sort').value='id';document.querySelector('#data-direction').value='desc';document.querySelector('#view-name').value='Notes and comments';document.querySelector('#view-save').click()")
  await until("document.querySelector('#view-status').textContent.startsWith('View saved')")
  const savedViews=await (await fetch(new URL('views',inspectorURL),{headers:{Authorization:'Bearer '+inspectorURL.hash.slice(1)}})).json()
  const definition=savedViews.views.find(view=>view.name==='Notes and comments')
  assert.equal(definition.steps.length,2)
  assert.equal(definition.steps[1].via.direction,'children')
  assert.deepEqual(definition.steps[1].columns,['body'])
  assert.deepEqual(definition.steps[1].sort,{column:'id',direction:'desc'})
  assert.deepEqual(Object.keys(definition).sort(),['fingerprint','name','steps'])
  assert.deepEqual(Object.keys(definition.steps[1]).sort(),['columns','filter','schema','sort','table','via'])
  assert.equal(JSON.stringify(definition).includes('predicates'),false)
  await evaluate("(()=>{const create=URL.createObjectURL.bind(URL),click=HTMLAnchorElement.prototype.click;URL.createObjectURL=blob=>{window.lifecycleExport=blob.text();return create(blob)};HTMLAnchorElement.prototype.click=function(){if(this.download==='data-api.lifecycle.json'){window.lifecycleFilename=this.download;return}return click.call(this)};document.querySelector('#access-lifecycle').click()})()")
  await until("document.querySelector('#access-status').textContent.startsWith('Lifecycle scaffold exported')")
  const lifecycle=await evaluate("window.lifecycleExport.then(text=>JSON.parse(text))")
  assert.equal(lifecycle.version,5);assert.equal(lifecycle.status,'needs_review');assert.ok(lifecycle.mutations.every(check=>check.identity===null));assert.ok(lifecycle.fixtures.every(fixture=>fixture.keys===null))
  assert.equal(await evaluate('window.lifecycleFilename'),'data-api.lifecycle.json')
  await evaluate("document.querySelector('#access-generate').click()")
  await until("document.querySelector('#access-status').textContent.startsWith('Scaffold ready')")
  const scaffold=await evaluate("JSON.parse(document.querySelector('#access-manifest').value)")
  assert.equal(scaffold.status,'needs_review');assert.ok(scaffold.fixtures.every(fixture=>fixture.keys===null));assert.ok(scaffold.checks.every(check=>check.steps.every(step=>step.rows===null)))
  scaffold.status='reviewed';scaffold.fixtures[0].keys={id:row.id,subject:'alice'};scaffold.fixtures[1].keys={id:childRow.id,subject:'alice',note_id:row.id}
  for(const check of scaffold.checks)for(const step of check.steps)step.rows=check.identity==='alice'?1:0
  const saveChecks=async manifest=>{
   await evaluate(`document.querySelector('#access-manifest').value=${JSON.stringify(JSON.stringify(manifest))};document.querySelector('#access-save').click()`)
   await until("document.querySelector('#access-status').textContent.startsWith('Checks saved')")
  }
  await saveChecks(scaffold)
  await evaluate("document.querySelector('#access-run').click()")
  await until("document.querySelector('#access-status').textContent.startsWith('Saved access checks passed')")
  const accessResults=await evaluate("JSON.parse(document.querySelector('#access-results').textContent)")
  assert.equal(accessResults.length,4);assert.ok(accessResults.every(result=>result.passed));assert.ok(!JSON.stringify(accessResults).includes('inspector-browser-edited'))
  scaffold.checks.find(check=>check.identity==='bob').steps[1].rows=1
  await saveChecks(scaffold);await evaluate("document.querySelector('#access-run').click()")
  await until("document.querySelector('#access-status').textContent.startsWith('Saved access checks failed')")
  const failed=await evaluate("JSON.parse(document.querySelector('#access-results').textContent).filter(result=>!result.passed)")
  assert.equal(failed.length,1);assert.equal(failed[0].step,2);assert.equal(failed[0].identity,'bob')
  scaffold.checks.find(check=>check.identity==='bob').steps[1].rows=0;await saveChecks(scaffold)
  guidedArtifact={manifest:scaffold,fixtureSQL:`INSERT INTO api.notes(id,subject,body) OVERRIDING SYSTEM VALUE VALUES (${row.id},'alice','inspector-browser-edited');
INSERT INTO api.comments(id,subject,note_id,body) OVERRIDING SYSTEM VALUE VALUES (${childRow.id},'alice',${row.id},'inspector-related-child');
SELECT pg_catalog.setval('api.notes_id_seq',${row.id});
SELECT pg_catalog.setval('api.comments_id_seq',${childRow.id});
`}
  await evaluate("document.querySelector('#identity').value='bob';document.querySelector('#identity').dispatchEvent(new Event('change'));document.querySelector('#view-load').click()")
  await until("document.querySelector('#data-status').textContent.startsWith('0 visible')")
  assert.equal(await evaluate("document.querySelector('#resource').value"),JSON.stringify(['tables','api.notes']))
  await evaluate("document.querySelector('#identity').value='alice';document.querySelector('#identity').dispatchEvent(new Event('change'));document.querySelector('#view-load').click()")
  await until("document.querySelector('#data-rows').textContent.includes('inspector-browser-edited')")
  await evaluate("document.querySelector('#data-rows [data-relationship*=comments]').click()")
  await until("document.querySelector('#data-rows').textContent.includes('inspector-related-child')")
  assert.deepEqual(await evaluate("[...document.querySelectorAll('#data-rows th')].map(cell=>cell.textContent)"),['body','Actions'])
  assert.equal(await evaluate("document.querySelector('#data-sort').value"),'id')
  await evaluate("document.querySelector('#data-rows [data-relationship]').click()")
  await until("document.querySelector('#data-rows').textContent.includes('inspector-browser-edited')")
  await evaluate("document.querySelector('#data-back').click()")
  await until("document.querySelector('#data-rows').textContent.includes('inspector-related-child')")
  await evaluate("document.querySelector('#identity').value='bob';document.querySelector('#identity').dispatchEvent(new Event('change'));document.querySelector('#data-read').click()")
  await until("document.querySelector('#data-status').textContent.startsWith('0 visible')")
  assert.equal(await evaluate("document.querySelector('#data-back').disabled"),true)
  assert.equal(await evaluate("document.querySelector('#data-scope').textContent"),'')
  const latest=await (await fetch(new URL('views',inspectorURL),{headers:{Authorization:'Bearer '+inspectorURL.hash.slice(1)}})).json()
  const stale={...definition,name:'Stale relationship workflow',fingerprint:'d'.repeat(64)}
  const saveStale=await fetch(new URL('views',inspectorURL),{method:'PUT',headers:{Authorization:'Bearer '+inspectorURL.hash.slice(1),'Content-Type':'application/json','If-Match':latest.revision},body:JSON.stringify({version:1,views:[definition,stale]})})
  assert.equal(saveStale.status,200)
  await evaluate("document.querySelector('#view-reload').click()")
  await until("document.querySelector('#view-status').textContent==='Views reloaded.'")
  await evaluate("(()=>{window.staleViewReads=0;const previousFetch=window.fetch;window.fetch=(url,options)=>{if(String(url).includes('/rest/v1/')&&(!options?.method||options.method==='GET'))window.staleViewReads++;return previousFetch(url,options)};document.querySelector('#data-views').value='Stale relationship workflow';document.querySelector('#view-load').click()})()")
  await until("document.querySelector('#view-status').textContent.startsWith('Schema changed')")
  assert.equal(await evaluate('window.staleViewReads'),0)
  await browserCsvImport(evaluate,until,inspectorURL,apiHeaders)
  await browserCsvExport(evaluate,until,inspectorURL,apiHeaders)
  await browserBulkWrite(evaluate,until,inspectorURL,apiHeaders)
  await browserMatchingWrite(evaluate,until,inspectorURL,apiHeaders)
  await browserBulkPreflight(evaluate,until,inspectorURL,apiHeaders)
  await browserBulkReconcile(evaluate,until,inspectorURL,apiHeaders)
  await browserBulkHistory(evaluate,until,inspectorURL)
 })
 return guidedArtifact
}

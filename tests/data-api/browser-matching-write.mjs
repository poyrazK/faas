import assert from 'node:assert/strict'
export async function browserMatchingWrite(evaluate,until,inspectorURL,apiHeaders){
 const ready=async()=>{await evaluate("document.querySelector('#bulk-preflight').click()");await until("document.querySelector('#bulk-stop').disabled&&document.querySelector('#bulk-preflight-status').textContent.includes('rows ready.')")}
 const marker='inspector-matching-source'
 const seeded=await fetch(new URL('/rest/v1/notes',inspectorURL),{method:'POST',headers:apiHeaders,body:JSON.stringify(Array.from({length:105},()=>({subject:'alice',body:marker})))})
 assert.equal(seeded.status,201);const rows=await seeded.json()
 const filter=()=>"document.querySelector('#data-filter').value='body';document.querySelector('#data-operator').value='eq';document.querySelector('#data-value').value='inspector-matching-source';"
 await evaluate("document.querySelector('#resource').value=JSON.stringify(['tables','api.notes']);document.querySelector('#resource').dispatchEvent(new Event('change'));document.querySelector('#identity').value='alice';document.querySelector('#identity').dispatchEvent(new Event('change'));document.querySelector('#bulk-scope').value='matching';document.querySelector('#bulk-scope').dispatchEvent(new Event('input'));document.querySelector('#bulk-operation').value='update';document.querySelector('#bulk-operation').dispatchEvent(new Event('input'));document.querySelector('#bulk-changes').value=JSON.stringify({body:'inspector-matching-updated'});document.querySelector('#bulk-changes').dispatchEvent(new Event('input'));document.querySelector('#bulk-limit').value='100';"+filter()+"document.querySelector('#bulk-review').click()")
 await until("document.querySelector('#bulk-stop').disabled&&document.querySelector('#bulk-status').textContent.includes('More rows match')")
 assert.equal(await evaluate("document.querySelector('#bulk-run').disabled"),true);assert.equal(await evaluate("document.querySelector('#bulk-preview').textContent"),'')
 await evaluate("document.querySelector('#identity').value='bob';document.querySelector('#identity').dispatchEvent(new Event('change'));"+filter()+"document.querySelector('#bulk-limit').value='105';document.querySelector('#bulk-review').click()")
 await until("document.querySelector('#bulk-stop').disabled&&document.querySelector('#bulk-status').textContent.includes('No rows match')")
 assert.equal(await evaluate("document.querySelector('#bulk-run').disabled"),true)
 await evaluate("document.querySelector('#identity').value='alice';document.querySelector('#identity').dispatchEvent(new Event('change'));"+filter()+"document.querySelector('#bulk-review').click()")
 await until("document.querySelector('#bulk-stop').disabled&&document.querySelector('#bulk-preview').textContent.includes('expectedVersion')")
 let preview=await evaluate("JSON.parse(document.querySelector('#bulk-preview').textContent)")
 assert.equal(preview.count,105);assert.equal(preview.source,'matching');assert.equal(preview.rows.length,105)
 assert.ok(preview.rows.every(row=>row.changes.body.before===marker))
 await ready()
 const inserted=await fetch(new URL('/rest/v1/notes',inspectorURL),{method:'POST',headers:apiHeaders,body:JSON.stringify({subject:'alice',body:marker})});assert.equal(inserted.status,201);const newRow=(await inserted.json())[0]
 const changed=await fetch(new URL(`/rest/v1/notes?id=eq.${rows[0].id}&subject=eq.alice`,inspectorURL),{method:'PATCH',headers:apiHeaders,body:JSON.stringify({body:'inspector-matching-concurrent'})});assert.equal(changed.status,200)
 await evaluate("document.querySelector('#bulk-approved').checked=true;document.querySelector('#bulk-approved').dispatchEvent(new Event('change'));document.querySelector('#bulk-run').click()")
 await until("document.querySelector('#bulk-stop').disabled&&document.querySelector('#bulk-status').textContent.startsWith('Finished:')")
 const results=await evaluate("JSON.parse(document.querySelector('#bulk-results').textContent)")
 await evaluate("document.querySelector('#bulk-report').click()")
 const matchingReport=JSON.parse(await evaluate('window.bulkReports.at(-1)'));assert.equal(matchingReport.source,'matching');assert.equal(matchingReport.total,105);assert.equal(matchingReport.summary.confirmed,104);assert.equal(matchingReport.summary.conflict,1);assert.equal(matchingReport.reconciliation.conflicts.length,1);assert.ok(!JSON.stringify(matchingReport).includes(marker))
 assert.equal(results.filter(row=>row.outcome==='confirmed').length,104);assert.equal(results.filter(row=>row.outcome==='conflict').length,1)
 let fresh=await(await fetch(new URL(`/rest/v1/notes?id=eq.${newRow.id}`,inspectorURL),{headers:{...apiHeaders,'Accept-Profile':'api'}})).json();assert.equal(fresh[0].body,marker)
 await evaluate("document.querySelector('#bulk-operation').value='delete';document.querySelector('#bulk-operation').dispatchEvent(new Event('input'));document.querySelector('#bulk-review').click()")
 await until("document.querySelector('#bulk-stop').disabled&&document.querySelector('#bulk-preview').textContent.includes('expectedVersion')")
 preview=await evaluate("JSON.parse(document.querySelector('#bulk-preview').textContent)");assert.equal(preview.count,1);assert.equal(preview.rows[0].key.id,newRow.id)
 await ready();await evaluate("document.querySelector('#bulk-approved').checked=true;document.querySelector('#bulk-approved').dispatchEvent(new Event('change'));document.querySelector('#bulk-run').click()")
 await until("document.querySelector('#bulk-stop').disabled&&document.querySelector('#bulk-status').textContent.startsWith('Finished:')")
 assert.equal(await evaluate("JSON.parse(document.querySelector('#bulk-results').textContent)[0].outcome"),'confirmed')
 fresh=await(await fetch(new URL(`/rest/v1/notes?id=eq.${newRow.id}`,inspectorURL),{headers:{...apiHeaders,'Accept-Profile':'api'}})).json();assert.equal(fresh.length,0)
 // Cancel a pending matching read: no approved preview or mutation is produced.
 await evaluate("(()=>{window.matchingOriginalFetch=window.fetch;window.matchingReads=0;window.fetch=(url,options)=>String(url).includes('/rest/v1/')&&(!options?.method||options.method==='GET')?new Promise((resolve,reject)=>{window.matchingReads++;options.signal.addEventListener('abort',()=>reject(new DOMException('Stopped','AbortError')),{once:true})}):window.matchingOriginalFetch(url,options)})();document.querySelector('#bulk-review').click()")
 await until('window.matchingReads===1');await evaluate("document.querySelector('#bulk-stop').click()")
 await until("document.querySelector('#bulk-stop').disabled&&document.querySelector('#bulk-status').textContent.includes('cancelled')")
 assert.equal(await evaluate("document.querySelector('#bulk-preview').textContent"),'');assert.equal(await evaluate("document.querySelector('#bulk-run').disabled"),true)
 await evaluate('window.fetch=window.matchingOriginalFetch')
}

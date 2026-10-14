import assert from 'node:assert/strict'
export async function browserBulkReconcile(evaluate,until,inspectorURL,apiHeaders){
 const created=await fetch(new URL('/rest/v1/notes',inspectorURL),{method:'POST',headers:apiHeaders,body:JSON.stringify({subject:'alice',body:'inspector-reconcile-source'})});assert.equal(created.status,201)
 await evaluate("document.querySelector('#resource').value=JSON.stringify(['tables','api.notes']);document.querySelector('#resource').dispatchEvent(new Event('change'));document.querySelector('#identity').value='alice';document.querySelector('#identity').dispatchEvent(new Event('change'));document.querySelector('#data-filter').value='body';document.querySelector('#data-operator').value='eq';document.querySelector('#data-value').value='inspector-reconcile-source';document.querySelector('#bulk-scope').value='selected';document.querySelector('#bulk-scope').dispatchEvent(new Event('input'));document.querySelector('#bulk-operation').value='update';document.querySelector('#bulk-operation').dispatchEvent(new Event('input'));document.querySelector('#bulk-changes').value=JSON.stringify({body:'inspector-reconcile-applied'});document.querySelector('#bulk-changes').dispatchEvent(new Event('input'));document.querySelector('#data-read').click()")
 await until("!document.querySelector('#data-read').disabled&&document.querySelector('#data-status').textContent.startsWith('1 visible')")
 await evaluate("document.querySelector('[data-bulk-row]').checked=true;document.querySelector('[data-bulk-row]').dispatchEvent(new Event('change'));document.querySelector('#bulk-review').click();document.querySelector('#bulk-preflight').click()")
 await until("document.querySelector('#bulk-stop').disabled&&document.querySelector('#bulk-preflight-status').textContent.includes('rows ready.')")
 await evaluate("(()=>{window.reconcileOriginalFetch=window.fetch;window.reconcileWrites=0;window.fetch=async(url,options)=>{if(options?.method==='PATCH'){window.reconcileWrites++;const response=await window.reconcileOriginalFetch(url,options);await response.body?.cancel();throw new TypeError('Simulated lost response')}return window.reconcileOriginalFetch(url,options)}})();document.querySelector('#bulk-approved').checked=true;document.querySelector('#bulk-approved').dispatchEvent(new Event('change'));document.querySelector('#bulk-run').click()")
 await until("document.querySelector('#bulk-stop').disabled&&document.querySelector('#bulk-status').textContent.startsWith('Finished:')")
 assert.equal(await evaluate("JSON.parse(document.querySelector('#bulk-results').textContent)[0].outcome"),'uncertain')
 await evaluate("document.querySelector('#bulk-report').click()")
 const report=JSON.parse(await evaluate('window.bulkReports.at(-1)'))
 await evaluate("window.fetch=window.reconcileOriginalFetch;document.querySelector('#bulk-reconcile').click()")
 await until("document.querySelector('#bulk-stop').disabled&&document.querySelector('#bulk-reconcile-rows').textContent.includes('potentially_applied')")
 assert.ok(await evaluate("document.querySelector('#bulk-reconcile-rows').textContent.includes('inspector-reconcile-applied')"))
 assert.equal(await evaluate('window.reconcileWrites'),1)
 await evaluate("document.querySelector('#bulk-report').click()")
 const after=JSON.parse(await evaluate('window.bulkReports.at(-1)'));assert.equal(after.run_id,report.run_id);assert.equal(after.summary.uncertain,1)
 // Losing visibility does not establish that an uncertain update or delete committed.
 const parsed=await created.json(),id=parsed[0].id
 const removed=await fetch(new URL(`/rest/v1/notes?id=eq.${id}&subject=eq.alice`,inspectorURL),{method:'DELETE',headers:apiHeaders});assert.equal(removed.status,200)
 await evaluate("document.querySelector('#bulk-reconcile').click()")
 await until("document.querySelector('#bulk-stop').disabled&&document.querySelector('#bulk-reconcile-rows').textContent.includes('unavailable')")
 assert.equal(await evaluate("document.querySelectorAll('[data-reconcile-index]').length"),0);assert.equal(await evaluate("document.querySelector('#bulk-reconcile-preview').disabled"),true)
 assert.equal(await evaluate('window.reconcileWrites'),1)
}

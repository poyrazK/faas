import assert from 'node:assert/strict'
// Browser steps poll a DOM expression until it holds. A wall-clock deadline
// keeps the budget independent of how long each CDP evaluate takes on a
// loaded CI runner, and the failure names the step that never settled.
// message is a string or an async function that reads page state.
export async function waitUntil(evaluate,expression,message,timeoutMs=30000){
 const deadline=Date.now()+timeoutMs
 while(Date.now()<deadline){if(await evaluate(expression))return;await new Promise(resolve=>setTimeout(resolve,30))}
 assert.fail((typeof message==='function'?await message():message)+' within '+timeoutMs+' ms: '+expression)
}

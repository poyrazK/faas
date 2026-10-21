import assert from 'node:assert/strict'
import http from 'node:http'
import {readFile} from 'node:fs/promises'
import {join} from 'node:path'
import {browserInspector} from './browser-inspector.mjs'
import {waitUntil} from './browser-wait.mjs'
export async function browserNoteEditor(directory){
 const server=http.createServer(async(req,res)=>{try{const name=req.url==='/'?'index.html':req.url==='/client.js'?'client.js':null;if(!name){res.statusCode=404;res.end();return}res.setHeader('Content-Type',name.endsWith('html')?'text/html; charset=utf-8':'text/javascript');res.end(await readFile(join(directory,'client/browser',name)))}catch{res.statusCode=500;res.end()}})
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve))
 try{await browserInspector(`http://127.0.0.1:${server.address().port}/`,async evaluate=>{
  for(let i=0;i<100;i++){if(await evaluate("Boolean(document.querySelector('#note-form [name=body]'))"))break;await new Promise(resolve=>setTimeout(resolve,30))}
  await evaluate(`window.editorCalls=[];window.editorConflict=false;const originalFetch=window.fetch;
   window.fetch=async(url,options)=>{
    if(!String(url).startsWith('https://notes.example'))return originalFetch(url,options);
    window.editorCalls.push({url:String(url),body:JSON.parse(options.body)});
    return window.editorConflict?new Response(JSON.stringify({code:'update_conflict',message:'Conflict',details:'',hint:''}),{status:409,headers:{'Content-Type':'application/json'}}):new Response(JSON.stringify({id:1,body:'changed',priority:2,version:2}),{status:200,headers:{'Content-Type':'application/json'}});
   };
   document.querySelector('#session [name=url]').value='https://notes.example';document.querySelector('#session [name=subject]').value='alice';document.querySelector('#session [name=token]').value='test-token';
   document.querySelector('#note-form [name=body]').value='x'.repeat(129);document.querySelector('#save-note').click();`)
  const until=expression=>waitUntil(evaluate,expression,'Editor feedback did not finish')
  await until("document.querySelector('#insert-body-error').textContent.includes('too long')")
  assert.equal(await evaluate('window.editorCalls.length'),0)
  await evaluate(`document.querySelector('#edit-form [name=id]').value='1';document.querySelector('#edit-form [name=version]').value='1';document.querySelector('#edit-form [name=body]').value='x'.repeat(129);document.querySelector('#edit-form [name=priority]').value='2';document.querySelector('#session [name=token]').value='test-token';document.querySelector('#edit-form button').click()`)
  await until("document.querySelector('#update-body-error').textContent.includes('too long')")
  assert.equal(await evaluate('window.editorCalls.length'),0)
  await evaluate("document.querySelector('#edit-form [name=body]').value='changed';document.querySelector('#edit-form button').click()")
  await until("document.querySelector('#edit-result').textContent.startsWith('Saved note 1')")
  const call=await evaluate('window.editorCalls[0]');assert.deepEqual(call.body,{body:'changed',priority:2});assert.ok(call.url.includes('version=eq.1'));assert.ok(call.url.includes('id=eq.1'))
  await evaluate("window.editorConflict=true;document.querySelector('#session [name=token]').value='test-token';document.querySelector('#edit-form button').click()")
  await until("document.querySelector('#edit-result').textContent.includes('Reload before saving')")
  assert.equal(await evaluate('window.editorCalls.length'),2)
 })}finally{server.closeAllConnections();await new Promise(resolve=>server.close(resolve))}
}

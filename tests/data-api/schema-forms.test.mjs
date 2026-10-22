import test from 'node:test'
import assert from 'node:assert/strict'
import {mkdtemp,writeFile,readFile,cp,rm,access} from 'node:fs/promises'
import {tmpdir} from 'node:os'
import {join,resolve} from 'node:path'
import http from 'node:http'
import {execFile} from 'node:child_process'
import {promisify} from 'node:util'
import {browserInspector} from './browser-inspector.mjs'
import {generateValidators} from '../../cmd/gregale/templates/data-api/type-render.mjs'
const execute=promisify(execFile)
const browser=process.env.DATA_API_CHROMIUM_BIN??'/usr/bin/chromium'
const browserReady=await access(browser).then(()=>true,()=>false)
test('generated form fields render accessible controls safely and keep ownership outside the editor',{skip:!browserReady,timeout:30000},async t=>{
 const root=await mkdtemp(join(tmpdir(),'gregale-schema-forms-'));t.after(()=>rm(root,{recursive:true,force:true}))
 const column=(name,type,extra={})=>({name,type,nullable:false,optional:false,insertable:true,updatable:true,...extra})
 const snapshot={version:3,schemas:['api'],tables:[{schema:'api',name:'notes',view:false,relationships:[],columns:[column('id','number',{insertable:false,updatable:false,optional:true}),column('subject','string'),column('body','string'),column('priority','number',{optional:true}),column('version','number',{optional:true})]}],functions:[],enums:{},composites:[]}
 await writeFile(join(root,'database.types.ts'),generateValidators(snapshot,[{schema:'api',relation:'notes',name:'body',type:'varchar',typmod:7},{schema:'api',relation:'notes',name:'priority',type:'int4'}]))
 await writeFile(join(root,'package.json'),'{"type":"module"}')
 await cp(new URL('../../cmd/gregale/templates/data-api-starter/client/src/schema-form.ts',import.meta.url),join(root,'schema-form.ts'))
 await execute(process.execPath,[resolve('sdk/data/node_modules/typescript/bin/tsc'),'--strict','--target','ES2022','--module','NodeNext','--outDir',join(root,'dist'),join(root,'schema-form.ts')])
 const html=`<!doctype html><meta charset="utf-8"><div id="insert"></div><div id="update"></div><pre id="status">waiting</pre><script type="module">
import {renderNoteFields,noteFormFields} from './schema-form.js';
import {inputValidators,formMetadata} from './database.types.js';
try {
 renderNoteFields(document.querySelector('#insert'),'insert');renderNoteFields(document.querySelector('#update'),'update');
 const body=document.querySelector('#insert [name=body]'),priority=document.querySelector('#update [name=priority]');
 if(!body.required || body.hasAttribute('maxlength') || !body.getAttribute('aria-describedby')) throw Error('body');
 if(document.querySelector('#update [name=body]').required || priority.type!=='number' || priority.step!=='1') throw Error('update');
 if(document.querySelector('[name=subject],[name=id],[name=version]')) throw Error('ownership');
 inputValidators.api.notes.insert({subject:'alice',body:'😀ab'});
 let rejected=false;try{inputValidators.api.notes.insert({subject:'alice',body:'abcd'})}catch(error){rejected=error.issues[0].code==='too_long'}
 if(!rejected || !document.querySelector('#insert').textContent.includes('3 characters')) throw Error('length');
 const enumField=formMetadata.api.notes.update.find(field=>field.name==='priority');enumField.control='select';enumField.options=['<img src=x onerror=alert(1)>'];
 renderNoteFields(document.querySelector('#update'),'update');
 const choice=document.querySelector('#update select');
 if(!choice || choice.options[1].textContent!=='<img src=x onerror=alert(1)>' || document.querySelector('#update img')) throw Error('enum');
 document.querySelector('#status').textContent='passed';
}catch(error){document.querySelector('#status').textContent='failed: '+error.message;}
</script>`
 const server=http.createServer(async(req,res)=>{try{if(req.url==='/'){res.setHeader('Content-Type','text/html');res.end(html)}else if(['/schema-form.js','/database.types.js'].includes(req.url)){res.setHeader('Content-Type','text/javascript');res.end(await readFile(join(root,'dist',req.url.slice(1))))}else{res.statusCode=404;res.end()}}catch{res.statusCode=500;res.end()}})
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));t.after(()=>new Promise(resolve=>server.close(resolve)))
 const previous=process.env.DATA_API_CHROMIUM_BIN;process.env.DATA_API_CHROMIUM_BIN=browser
 try{await browserInspector(`http://127.0.0.1:${server.address().port}/`,async evaluate=>{
  for(let index=0;index<100;index++){
   const status=await evaluate("document.querySelector('#status')?.textContent")
   if(status==='passed')return
   assert.ok(!status?.startsWith('failed'),status)
   await new Promise(resolve=>setTimeout(resolve,30))
  }
  assert.fail('Schema form did not finish rendering')
 })}finally{if(previous===undefined)delete process.env.DATA_API_CHROMIUM_BIN;else process.env.DATA_API_CHROMIUM_BIN=previous}
})

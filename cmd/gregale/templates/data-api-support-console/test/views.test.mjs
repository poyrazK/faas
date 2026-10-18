import test from 'node:test'
import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import {validateView,viewLink,linkedView,preferenceKey,loadViews} from '../public/views.mjs'
import {validateConsole,dataTarget} from '../queries.mjs'
const definition=JSON.parse(await readFile(new URL('../support-console.json',import.meta.url)))
const view={name:'Open cases',resource:'cases',search:'delivery & delay',filter:'open',sort:'id',direction:'asc'}
test('share links round trip only approved settings and regenerate fixed data requests',()=>{
 validateConsole(definition)
 const url=new URL(viewLink('https://support.example',view,definition))
 assert.equal(url.pathname,'/');assert.equal(url.search,'')
 assert.deepEqual(linkedView(url.hash,definition),view)
 const params=new URLSearchParams(Object.entries(view).filter(([key])=>key!=='name'))
 assert.equal(dataTarget({api:'https://api.example',fingerprint:'a'.repeat(64)},definition,params).resource.id,'cases')
 for(const change of [{scope:{}},{tenant:'other'},{token:'secret'},{sort:'internal_notes'},{resource:'private'},{filter:'invalid'},{search:'x'.repeat(129)}])assert.throws(()=>validateView({...view,...change},definition))
 assert.throws(()=>linkedView('#view={}&view={}',definition))
 assert.throws(()=>validateConsole({...definition,views:[{...view,sort:'internal_notes'}]}))
})
test('personal preferences are bounded, configuration only, and subject/tenant scoped',()=>{
 const entries=new Map(),storage={getItem:key=>entries.get(key)}
 const a=preferenceKey({subject:'a',tenant:'one'}),b=preferenceKey({subject:'b',tenant:'one'}),other=preferenceKey({subject:'a',tenant:'two'})
 entries.set(a,JSON.stringify([view]));assert.deepEqual(loadViews(storage,a,definition),[view]);assert.deepEqual(loadViews(storage,b,definition),[]);assert.deepEqual(loadViews(storage,other,definition),[])
 entries.set(a,JSON.stringify([{...view,token:'secret'}]));assert.deepEqual(loadViews(storage,a,definition),[])
 entries.set(a,JSON.stringify(Array(21).fill(view)));assert.deepEqual(loadViews(storage,a,definition),[])
})

test('saved personal queues validate resource and preserve identity-relative meaning',()=>{
 const approved={...definition,queues:{resource:'cases',assignee:'assignee',status:'status'}}
 const queue={...view,queue:'mine'}
 assert.deepEqual(linkedView(new URL(viewLink('https://support.example',queue,approved)).hash,approved),queue)
 assert.throws(()=>validateView({...view,queue:'foreign'},approved))
 assert.throws(()=>validateView({...view,resource:'replies',sort:'id',filter:'',queue:'mine'},approved))
})

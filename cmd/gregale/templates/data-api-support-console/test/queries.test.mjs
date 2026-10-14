import test from 'node:test'
import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import {dataTarget,validateConsole} from '../queries.mjs'
import {settings} from '../auth.mjs'
const definition=JSON.parse(await readFile(new URL('../support-console.json',import.meta.url)))
test('approved queries intersect relationship keys, search and filters while preserving pinned projection',()=>{
 validateConsole(definition)
 const config={api:'https://data.example',fingerprint:'a'.repeat(64)},params=new URLSearchParams({resource:'replies',search:'a%_\\*',scope:JSON.stringify({source:'cases',relationship:'replies',values:['shop-a',7]}),sort:'body',direction:'desc',offset:'100',limit:'100'})
 const {target}=dataTarget(config,definition,params)
 assert.equal(target.searchParams.get('select'),'"tenant","id","case_id","body"')
 assert.deepEqual(target.searchParams.getAll('tenant'),['eq.shop-a']);assert.equal(target.searchParams.get('case_id'),'eq.7')
 assert.equal(target.searchParams.get('body'),'ilike.*a\\%\\_\\\\\\**');assert.equal(target.searchParams.get('order'),'"body".desc,"tenant".asc,"id".asc')
 assert.ok(target.pathname.includes(config.fingerprint))
})
test('configuration rejects arbitrary names, incomplete relationships and unapproved query values',()=>{
 for(const resource of [{...definition.resources[0],table:'notes;drop'},{...definition.resources[0],keys:['unknown']},{...definition.resources[0],relationships:[{id:'replies',target:'replies',from:['tenant'],to:['tenant','case_id']}]},{...definition.resources[0],columns:['tenant','id','id']}])assert.throws(()=>validateConsole({...definition,resources:[resource,definition.resources[1]]}))
 assert.throws(()=>dataTarget({api:'https://data.example',fingerprint:'a'.repeat(64)},definition,new URLSearchParams({resource:'replies',scope:JSON.stringify({source:'cases',relationship:'replies',values:['shop-a',9007199254740992]})})))
})
test('issuer configuration preserves a trailing slash for exact OIDC issuer validation',()=>{
 const value=settings({SUPPORT_ORIGIN:'https://support.example',SUPPORT_OIDC_ISSUER:'https://issuer.example/',SUPPORT_OIDC_CLIENT_ID:'console',SUPPORT_DATA_AUDIENCE:'data',SUPPORT_DATA_API_URL:'https://data.example',SUPPORT_SCHEMA_FINGERPRINT:'a'.repeat(64),SUPPORT_SESSION_KEY:Buffer.alloc(32,1).toString('base64url')})
 assert.equal(value.issuer,'https://issuer.example/')
})

import test from 'node:test'
import assert from 'node:assert/strict'
import {startDemo} from '../demo.mjs'
import {seal,unseal,settings} from '../auth.mjs'
import {dataTarget,validateConsole} from '../queries.mjs'
export async function signIn(demo,agent='a'){
 const login=await fetch(demo.url+'/auth/login',{redirect:'manual'});assert.equal(login.status,303)
 const loginCookie=login.headers.getSetCookie()[0].split(';')[0],target=new URL(login.headers.get('location'));target.searchParams.set('agent',agent)
 const authorized=await fetch(target,{redirect:'manual'});assert.equal(authorized.status,303)
 const callback=await fetch(authorized.headers.get('location'),{headers:{Cookie:loginCookie},redirect:'manual'})
 return {callback,loginCookie,cookie:callback.headers.getSetCookie().find(value=>value.startsWith('support_session='))?.split(';')[0]}
}
test('OIDC code/PKCE sign-in produces an encrypted HttpOnly session and approved tenant reads',async t=>{
 const demo=await startDemo({port:0});t.after(demo.close)
 assert.equal((await fetch(demo.url+'/api/data?resource=cases')).status,401)
 assert.equal((await fetch(demo.url+'/api/config')).status,401)
 const {callback,cookie}=await signIn(demo);assert.equal(callback.status,303);assert.ok(callback.headers.getSetCookie().some(value=>value.includes('HttpOnly')&&value.includes('SameSite=Lax')))
 const session=unseal(cookie.split('=')[1],demo.config.key,'session');assert.ok(session.token);assert.ok(!cookie.includes(session.token))
 const headers={Cookie:cookie},profile=await(await fetch(demo.url+'/api/session',{headers})).json();assert.deepEqual(Object.keys(profile),['action_recovery','actions','audit_admin','subject','staff','tenant','expires']);assert.equal(profile.tenant,'shop-a')
 const response=await fetch(demo.url+'/api/data?resource=cases&limit=100',{headers});assert.equal(response.status,200);const rows=await response.json();assert.equal(rows.length,100);assert.ok(rows.every(row=>row.tenant==='shop-a'));assert.ok(!JSON.stringify(rows).includes('private'))
 assert.ok(demo.data.calls.every(call=>call.method==='GET'));assert.ok(demo.data.calls[0].query.get('select').includes('"title"'))
 const searched=await(await fetch(demo.url+'/api/data?resource=cases&search=case%201&filter=open',{headers})).json();assert.ok(searched.length>0);assert.ok(searched.every(row=>row.status==='open'&&row.title.toLowerCase().includes('case 1')))
 const scope=encodeURIComponent(JSON.stringify({source:'cases',relationship:'replies',values:['shop-a',1]}))
 const related=await(await fetch(demo.url+'/api/data?resource=replies&scope='+scope,{headers})).json();assert.equal(related.length,2)
 const foreign=encodeURIComponent(JSON.stringify({source:'cases',relationship:'replies',values:['shop-b',121]}))
 assert.deepEqual(await(await fetch(demo.url+'/api/data?resource=replies&scope='+foreign,{headers})).json(),[])
 const other=await signIn(demo,'b');assert.equal(other.callback.status,303);const otherRows=await(await fetch(demo.url+'/api/data?resource=cases',{headers:{Cookie:other.cookie}})).json();assert.equal(otherRows.length,1);assert.equal(otherRows[0].tenant,'shop-b')
})
test('unknown columns, filters, scopes, resources and write methods never reach upstream',async t=>{
 const demo=await startDemo({port:0});t.after(demo.close);const {cookie}=await signIn(demo),headers={Cookie:cookie}
 for(const query of ['resource=private','resource=cases&select=internal_notes','resource=cases&sort=internal_notes','resource=cases&resource=replies','resource=cases&limit=101','resource=cases&offset=-1','resource=cases&filter=unapproved','resource=cases&scope=%7B%7D'])assert.equal((await fetch(demo.url+'/api/data?'+query,{headers})).status,400)
 for(const method of ['POST','PATCH','DELETE','PUT'])assert.equal((await fetch(demo.url+'/api/data?resource=cases',{method,headers})).status,405)
 assert.equal(demo.data.calls.length,0)
})
test('writable, wrong-audience, expired and tampered sessions are refused before data access',async t=>{
 const demo=await startDemo({port:0});t.after(demo.close)
 for(const claims of [{gregale_read_only:false,aud:'support-data-demo'},{gregale_read_only:true,aud:'wrong-audience'},{gregale_read_only:true,aud:'support-data-demo',tenant:''}]){
  const token=await demo.provider.sign({sub:'agent-a',tenant:'shop-a',...claims}),value=seal({kind:'session',token,expires:Date.now()+60000},demo.config.key)
  assert.equal((await fetch(demo.url+'/api/data?resource=cases',{headers:{Cookie:'support_session='+value}})).status,401)
 }
 const signed=await signIn(demo),value=signed.cookie.split('=')[1],tampered=value.slice(0,40)+(value[40]==='A'?'B':'A')+value.slice(41)
 assert.equal((await fetch(demo.url+'/api/data?resource=cases',{headers:{Cookie:'support_session='+tampered}})).status,401)
 const original=unseal(value,demo.config.key,'session'),expired=seal({...original,expires:Date.now()-1},demo.config.key)
 assert.equal((await fetch(demo.url+'/api/data?resource=cases',{headers:{Cookie:'support_session='+expired}})).status,401)
 assert.equal(demo.data.calls.length,0)
})
test('bad callback state, mismatched nonce and writable tokens cannot create staff sessions',async t=>{
 const demo=await startDemo({port:0});t.after(demo.close)
 const login=await fetch(demo.url+'/auth/login',{redirect:'manual'}),cookie=login.headers.getSetCookie()[0].split(';')[0]
 assert.equal((await fetch(demo.url+'/auth/callback?code=bad&state=bad',{headers:{Cookie:cookie},redirect:'manual'})).status,400)
 for(const providerOptions of [{nonceOverride:'wrong'},{readOnly:false}]){
  const invalid=await startDemo({port:0,providerOptions});t.after(invalid.close);const result=await signIn(invalid);assert.equal(result.callback.status,502);assert.equal(result.cookie,undefined);assert.equal(invalid.data.calls.length,0)
 }
})
test('logout checks origin and public assets never expose access tokens',async t=>{
 const demo=await startDemo({port:0});t.after(demo.close);const {cookie}=await signIn(demo)
 const denied=await fetch(demo.url+'/auth/logout',{method:'POST',headers:{Cookie:cookie,Origin:'https://attacker.example'}});assert.equal(denied.status,403)
 const loggedOut=await fetch(demo.url+'/auth/logout',{method:'POST',headers:{Cookie:cookie,Origin:demo.url}});assert.equal(loggedOut.status,200);assert.ok(loggedOut.headers.getSetCookie().some(value=>value.includes('support_session=')&&value.includes('Max-Age=0')))
 const publicConfig=await fetch(demo.url+'/api/config',{headers:{Cookie:cookie,Origin:'https://attacker.example'}});assert.equal(publicConfig.status,403)
 const index=await(await fetch(demo.url+'/')).text();assert.ok(index.includes('Sign in with your organization'));assert.ok(!index.includes('Bearer'))
})
test('configuration requires HTTPS, a pinned contract and a full-size session key',async()=>{
 const config={SUPPORT_ORIGIN:'https://support.example',SUPPORT_OIDC_ISSUER:'https://issuer.example',SUPPORT_OIDC_CLIENT_ID:'console',SUPPORT_DATA_AUDIENCE:'data',SUPPORT_DATA_API_URL:'https://data.example',SUPPORT_SCHEMA_FINGERPRINT:'a'.repeat(64),SUPPORT_SESSION_KEY:Buffer.alloc(32,1).toString('base64url')}
 assert.equal(settings(config).secure,true)
 for(const override of [{SUPPORT_ORIGIN:'http://support.example'},{SUPPORT_DATA_API_URL:'https://user:pass@data.example'},{SUPPORT_SESSION_KEY:'short'},{SUPPORT_SCHEMA_FINGERPRINT:'bad'}])assert.throws(()=>settings({...config,...override}))
})

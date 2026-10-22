import {auditSettings} from './audit.mjs'
import {randomBytes,createCipheriv,createDecipheriv} from 'node:crypto'
import {jwtVerify,createRemoteJWKSet} from 'jose'
export function endpoint(value){
 const url=new URL(value)
 if(url.username||url.password||url.hash||url.protocol!=='https:'&&!(url.protocol==='http:'&&['localhost','127.0.0.1','[::1]'].includes(url.hostname)))throw new Error('Use HTTPS endpoints or loopback HTTP for development.')
 return url
}
export function settings(env){
 const origin=endpoint(env.SUPPORT_ORIGIN),issuer=endpoint(env.SUPPORT_OIDC_ISSUER),api=endpoint(env.SUPPORT_DATA_API_URL)
 if(origin.pathname!=='/'||origin.search||issuer.search||api.search||api.pathname!=='/'||!env.SUPPORT_OIDC_CLIENT_ID||!env.SUPPORT_DATA_AUDIENCE||!/^[a-f0-9]{64}$/.test(env.SUPPORT_SCHEMA_FINGERPRINT??''))throw new Error('Configure origin, issuer, client, API audience and pinned schema.')
 const key=Buffer.from(env.SUPPORT_SESSION_KEY??'','base64url');if(key.length!==32||key.toString('base64url')!==env.SUPPORT_SESSION_KEY)throw new Error('Use a 32-byte base64url session key.')
 if(env.SUPPORT_ACTION_DATABASE_URL&&!/^postgres(?:ql)?:/.test(env.SUPPORT_ACTION_DATABASE_URL))throw new Error("Configure a PostgreSQL action database URL.")
 return {actionDatabase:env.SUPPORT_ACTION_DATABASE_URL,audit:auditSettings(env),origin:origin.origin,issuer:env.SUPPORT_OIDC_ISSUER,api:api.origin,clientId:env.SUPPORT_OIDC_CLIENT_ID,clientSecret:env.SUPPORT_OIDC_CLIENT_SECRET,audience:env.SUPPORT_DATA_AUDIENCE,fingerprint:env.SUPPORT_SCHEMA_FINGERPRINT,key,scopes:env.SUPPORT_OIDC_SCOPES??'openid profile',audienceParameter:env.SUPPORT_OIDC_AUDIENCE_PARAMETER??'',secure:origin.protocol==='https:',port:Number(env.PORT??8080)}
}
export function seal(value,key){
 const iv=randomBytes(12),cipher=createCipheriv('aes-256-gcm',key,iv);cipher.setAAD(Buffer.from('gregale-support-console-v1'))
 const encrypted=Buffer.concat([cipher.update(JSON.stringify(value)),cipher.final()]);return Buffer.concat([iv,cipher.getAuthTag(),encrypted]).toString('base64url')
}
export function unseal(value,key,kind){
 try{if(typeof value!=='string'||value.length>3800)return null;const bytes=Buffer.from(value,'base64url');if(bytes.length<29)return null;const decipher=createDecipheriv('aes-256-gcm',key,bytes.subarray(0,12));decipher.setAAD(Buffer.from('gregale-support-console-v1'));decipher.setAuthTag(bytes.subarray(12,28));const parsed=JSON.parse(Buffer.concat([decipher.update(bytes.subarray(28)),decipher.final()]).toString());return parsed.kind===kind&&parsed.expires>Date.now()?parsed:null}catch{return null}
}
export function cookie(name,value,config,maxAge){return `${name}=${value}; Path=/; HttpOnly; SameSite=Lax; Max-Age=${maxAge}${config.secure?'; Secure':''}`}
export function cookies(header){const values=new Map();for(const pair of (header??'').split(';')){const index=pair.indexOf('=');if(index>0)values.set(pair.slice(0,index).trim(),pair.slice(index+1).trim())}return values}
export async function oidc(config){
 const response=await fetch(config.issuer.replace(/\/$/,'')+'/.well-known/openid-configuration',{redirect:'error',signal:AbortSignal.timeout(10000)})
 const body=await boundedText(response,65536);if(!response.ok)throw new Error('Cannot discover staff identity provider.')
 const metadata=JSON.parse(body);if(metadata.issuer!==config.issuer)throw new Error('Identity provider issuer does not match.')
 for(const name of ['authorization_endpoint','token_endpoint','jwks_uri']){const url=endpoint(metadata[name]);if(config.issuer.startsWith('https:')&&url.protocol!=='https:')throw new Error('Identity endpoints must not downgrade HTTPS.')}
 const keys=createRemoteJWKSet(endpoint(metadata.jwks_uri),{timeoutDuration:10000})
 const access=async token=>{
  if(typeof token!=='string'||token.length>8000)throw new Error('Invalid staff session.')
  const {payload}=await jwtVerify(token,keys,{issuer:config.issuer,audience:config.audience,algorithms:['RS256','ES256'],requiredClaims:['sub','exp']})
  if(payload.gregale_read_only!==true||typeof payload.tenant!=='string'||!payload.tenant||payload.tenant.length>256||typeof payload.sub!=='string'||!payload.sub)throw new Error('Staff read-only access is required.')
  return payload
 }
 const identity=async(token,nonce)=>{const {payload}=await jwtVerify(token,keys,{issuer:config.issuer,audience:config.clientId,algorithms:['RS256','ES256'],requiredClaims:['sub','exp']});if(payload.nonce!==nonce||Array.isArray(payload.aud)&&payload.aud.length>1&&payload.azp!==config.clientId||payload.azp!==undefined&&payload.azp!==config.clientId)throw new Error('Invalid staff sign-in nonce or authorized party.');return payload}
 return {metadata,access,identity}
}
export async function boundedText(response,limit){
 const reader=response.body?.getReader(),chunks=[];let bytes=0
 try{if(reader)for(;;){const {done,value}=await reader.read();if(done)break;bytes+=value.length;if(bytes>limit)throw new Error('Response exceeds bounds.');chunks.push(value)}}finally{if(reader){await reader.cancel().catch(()=>{});reader.releaseLock()}}
 return Buffer.concat(chunks.map(chunk=>Buffer.from(chunk))).toString('utf8')
}

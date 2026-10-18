import {validateBulkHistory,retainBulkHistory,emptyHistory,historyLimits} from './bulk-history.mjs'
import {validateGuidedAccess,runGuidedAccess} from './guided-access.mjs'
import { validateViews } from './inspector-data.mjs'
import { dirname, join } from 'node:path'
import { readFile } from 'node:fs/promises'
import { requestStore, requestLimit } from './dev-requests.mjs'
import { randomBytes, timingSafeEqual } from 'node:crypto'

// A fragment capability keeps local test identities out of unauthenticated responses.
export async function inspector(state, requestsPath, identities) {
  const store = requestStore(requestsPath, { identities })
  const views = requestStore(join(dirname(requestsPath), 'data-api.views.json'), { validate: validateViews, empty: { version: 1, views: [] } })
  const access = requestStore(join(dirname(requestsPath), 'data-api.access.json'), { validate: value=>validateGuidedAccess(value,identities), empty: { version: 3, status: 'empty' } })
  const history=requestStore(join(dirname(requestsPath),'data-api.bulk-history.json'),{validate:validateBulkHistory,empty:emptyHistory(),maxBytes:historyLimits.bytes})
  let accessRunning=false
  const key = randomBytes(32).toString('hex')
  const assets = new Map(await Promise.all(['inspector.html', 'inspector.mjs', 'inspector-data.mjs', 'csv-import.mjs', 'csv-export.mjs', 'bulk-write.mjs', 'bulk-history.mjs', 'guided-access.mjs', 'guided-mutations.mjs', 'request-replay.mjs'].map(async name => [name, await readFile(new URL(name, import.meta.url))])))
  const prefix = '/__gregale/dev/'
  return {
    url: origin => `${origin}${prefix}#${key}`,
    handle(req, res) {
      const path = req.url.split('?')[0]
      if (!path.startsWith(prefix)) return false
      const origin = state().url
      const headers = { 'Cache-Control': 'no-store', 'Referrer-Policy': 'no-referrer', 'X-Content-Type-Options': 'nosniff', 'Content-Security-Policy': "default-src 'none'; script-src 'self'; connect-src 'self'; style-src 'unsafe-inline'; base-uri 'none'; frame-ancestors 'none'" }
      const send = (status, body, type = 'application/json') => { res.writeHead(status, { ...headers, 'Content-Type': type }); res.end(body) }
      if (!origin || req.headers.host !== new URL(origin).host || (req.headers.origin && req.headers.origin !== origin) || req.headers['sec-fetch-site'] === 'cross-site') { send(403, '{}'); return true }
      if (path === `${prefix}session` || path === `${prefix}requests` || path === `${prefix}views` || path === `${prefix}access` || path === `${prefix}access-run` || path === `${prefix}bulk-history`) {
        const provided = Buffer.from(req.headers.authorization ?? '')
        const expected = Buffer.from(`Bearer ${key}`)
        if (provided.length !== expected.length || !timingSafeEqual(provided, expected)) send(401, '{}')
        else if (path === `${prefix}session`) {
          if (req.method !== 'GET') send(405, '{}')
          else send(200, JSON.stringify(state()))
        } else if(path===`${prefix}access-run`){
          if(req.method!=='POST')send(405,'{}')
          else if(accessRunning||!state().ready)send(409,JSON.stringify({error:'Wait until the API is ready and the current check run finishes.'}))
          else{
            accessRunning=true
            const controller=new AbortController(),timer=setTimeout(()=>controller.abort(),120000)
            const closed=()=>controller.abort();res.once('close',closed)
            ;(async()=>{
              const {revision,...manifest}=await access.read(true),current=state()
              const results=await runGuidedAccess(manifest,{url:current.url,identities:current.identities,snapshot:current.snapshot,fingerprint:current.fingerprint,signal:controller.signal})
              send(200,JSON.stringify({status:results.every(result=>result.passed)?'passed':'failed',results}))
            })().catch(()=>send(400,JSON.stringify({error:'Cannot run access checks; save a valid reviewed manifest and verify local API readiness.'}))).finally(()=>{clearTimeout(timer);res.removeListener('close',closed);accessRunning=false})
          }
        } else if (!['GET', 'PUT'].includes(req.method)) send(405, '{}')
        else {
          const activeStore = path===`${prefix}bulk-history`?history:path === `${prefix}views` ? views : path===`${prefix}access` ? access : store
          const operation = async () => {
            if (req.method === 'GET') {const value=await activeStore.read();if(path===`${prefix}bulk-history`){const {revision,...rest}=value;return {...retainBulkHistory(rest),revision}}return value}
            if (req.headers['content-type'] !== 'application/json') { const error = new Error('Use application/json.'); error.status = 415; throw error }
            const chunks = []; let size = 0
            req.setTimeout(10000, () => req.destroy())
            for await (const chunk of req) {
              size += chunk.length
              if (size > (path===`${prefix}bulk-history`?historyLimits.bytes:requestLimit)) { const error = new Error('Request collection exceeds 64 KiB.'); error.status = 413; throw error }
              chunks.push(chunk)
            }
            req.setTimeout(0)
            const value=JSON.parse(Buffer.concat(chunks).toString('utf8'));return activeStore.save(path===`${prefix}bulk-history`?retainBulkHistory(value):value, req.headers['if-match'])
          }
          operation().then(value => send(200, JSON.stringify(value)), error => send(error.status ?? 400, JSON.stringify({ error: error.message === 'Saved requests changed; reload the collection before saving.' ? error.message : 'Cannot load or save requests; check the collection format, size and file type.' })))
        }
      } else {
        if (req.method !== 'GET') { send(405, '{}'); return true }
        const name = path === prefix ? 'inspector.html' : path.slice(prefix.length)
        if (!assets.has(name)) send(404, '{}')
        else send(200, assets.get(name), name.endsWith('.html') ? 'text/html; charset=utf-8' : 'text/javascript; charset=utf-8')
      }
      return true
    }
  }
}

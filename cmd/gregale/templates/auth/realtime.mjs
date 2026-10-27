import { timingSafeEqual } from 'node:crypto'
import { limits } from './config.mjs'
import { inSession } from './authz.mjs'

// Managed realtime channel authorization (ADR-1190 phase 5). A realtime
// endpoint created with `gregale realtime create APP --auth AUTH_APP` calls
// POST /realtime/authorize-channel here with a callback token sealed in this
// app. The callback carries the verified token's subject and session_id.
// Reuse the same session-aware authorization function as the Data API.
// Permission "presence" (setting presence state and sending signals) is its
// own action, realtime:presence, so reading a channel does not grant it.

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i

// Channels cannot contain "/", so they name resources with ":":
// org:<uuid>:<kind>:<name> authorizes as org/<uuid>/<kind>/<name>.
export function channelResource(channel) {
  if (typeof channel !== 'string') return null
  const [scope, org, kind, ...rest] = channel.split(':')
  const name = rest.join(':')
  if (scope !== 'org' || !uuid.test(org ?? '') || !/^[a-z][a-z0-9_-]{0,63}$/.test(kind ?? '') || name === '') return null
  return `org/${org}/${kind}/${name}`
}

export function callbackTokens(env) {
  return Object.entries(env).filter(([k, v]) => /^AUTH_REALTIME_CALLBACK_TOKEN_[A-Z0-9]{1,16}$/.test(k) && typeof v === 'string' && v.length >= 32).map(([, v]) => Buffer.from(v))
}

function tokenMatches(tokens, header) {
  const presented = Buffer.from(/^Bearer (\S+)$/.exec(header ?? '')?.[1] ?? '')
  return presented.length > 0 && tokens.some(token => token.length === presented.length && timingSafeEqual(token, presented))
}

export function createRealtimeAuthorizer({ pool, tokens }) {
  return (req, res) => {
    const reply = status => { res.writeHead(status, { 'Cache-Control': 'no-store' }); res.end() }
    if (tokens.length === 0) return reply(404)
    if (req.method !== 'POST') return reply(405)
    if (!tokenMatches(tokens, req.headers.authorization)) { req.resume(); return reply(401) }
    const chunks = []
    let size = 0
    req.on('data', chunk => { size += chunk.length; if (size > limits.authzBodyBytes) { reply(413); req.destroy() } else chunks.push(chunk) })
    req.on('end', async () => {
      if (res.writableEnded) return
      let event
      try { event = JSON.parse(Buffer.concat(chunks).toString('utf8')) } catch { return reply(400) }
      const activity = event?.permission === 'read_activity'
      const action = activity ? 'realtime:activity' : event?.permission === 'presence' ? 'realtime:presence' : 'realtime:read'
      const resource = channelResource(activity ? event.activity_parent_channel || event.channel : event?.channel)
      if (event?.type !== 'realtime.authorize_channel' || typeof event.principal !== 'string' || !uuid.test(event.principal) ||
          typeof event.session_id !== 'string' || !uuid.test(event.session_id) || !resource) return reply(403)
      try {
        const { rows: [{ allowed }] } = await inSession(pool, { sub: event.principal, session_id: event.session_id }, client =>
          client.query('SELECT gregale_auth.authorize($1, $2) AS allowed', [action, resource]))
        reply(allowed ? 204 : 403)
      } catch (err) {
        console.error(JSON.stringify({ level: 'error', event: 'realtime_authorize_failed', sqlstate: err?.code ?? null }))
        reply(503)
      }
    })
  }
}

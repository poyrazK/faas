import { addressBucket, budgetKey, goLower } from './attempts.mjs'
import { limits } from './config.mjs'
import { networkLabel } from './securitylog.mjs'
import { normalizePhone } from './sms.mjs'

// Message budgets (ADR-1190). Every sign-in code, magic link, password reset
// and confirmation costs the owner money, and each may land in a stranger's
// inbox or phone. GoTrue's own limits are per instance and kept in memory,
// so a snapshot restore resets them. The front counts each request that may
// send one in the database instead: per recipient (an email address or
// phone number, or the signed-in user for a re-authentication code or a
// phone second-factor challenge) and, when the owner sets it, per network
// (an IPv4 address or IPv6 /64), in limits.messageWindowSeconds. A spent
// recipient budget still lets through a request from a network the account
// signed in from recently (security.mjs), up to limits.trustedBudgetMultiplier
// times the budget, so a stranger asking for the person's links from
// elsewhere cannot keep them from signing in, nor can that network send
// without end. A
// request is counted before GoTrue
// sees it, so a parallel burst cannot all pass, and given back when GoTrue
// refuses it (an error status) or never answers: then nothing was sent. A
// signed-in user's budget is keyed by a token the front has not verified;
// GoTrue refuses a forged one and its count is given back, so it holds a
// budget only while in flight. An email budget is keyed by the inbox, so
// plus-addressing and Gmail's dots do not each get a fresh budget.

// messageBudgets are the budgets for the owner's settings (securityConfig).
export function messageBudgets(security) {
  return {
    recipient: { limit: security?.messagesPerRecipient ?? limits.messagesPerRecipientDefault, seconds: limits.messageWindowSeconds },
    // Off unless the owner sets it (securityConfig).
    network: security?.messagesPerNetwork ? { limit: security.messagesPerNetwork, seconds: limits.messageWindowSeconds } : null,
  }
}

// Paths whose JSON body names the email address or phone number GoTrue
// sends to: sign-in codes and links, sign-up confirmation, password reset,
// resent messages and an email or phone change.
const bodyRoutes = new Set(['POST /otp', 'POST /magiclink', 'POST /signup', 'POST /recover', 'POST /resend', 'PUT /user'])

// sendRoute names what a request to GoTrue (a path below /auth/v1) may
// send: 'body' (the body names the recipient), 'reauthenticate' (a code to
// the signed-in user), a factor ID (a phone factor's challenge texts its
// number) or null.
export function sendRoute(method, path) {
  // GoTrue's router serves a path with trailing slashes as the path.
  const route = path.split('?')[0].replace(/\/+$/, '')
  // An email or phone change also mails the current address or number,
  // so it counts against the signed-in user too.
  if (method === 'PUT' && route === '/user') return { kind: 'body', user: true }
  if (bodyRoutes.has(`${method} ${route}`)) return { kind: 'body' }
  if (method === 'GET' && route === '/reauthenticate') return { kind: 'reauthenticate' }
  const challenge = method === 'POST' && /^\/factors\/([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})\/challenge$/i.exec(route)
  return challenge ? { kind: 'challenge', factor: challenge[1].toLowerCase() } : null
}

// bodyRecipients reads the addresses a body would make GoTrue send to, as
// GoTrue v2.197.0 reads them: Go's JSON decoding matches field names without
// regard to case, so a body naming a field twice is refused ('ambiguous')
// rather than guessed. A body that is not a JSON object is left to GoTrue,
// which refuses it without sending.
export function bodyRecipients(raw) {
  let body
  try { body = JSON.parse(raw.toString('utf8')) } catch { return [] }
  if (!body || typeof body !== 'object' || Array.isArray(body)) return []
  const recipients = []
  for (const name of ['email', 'phone']) {
    const fields = Object.entries(body).filter(([key]) => key.toLowerCase() === name)
    if (fields.length > 1) throw new Error('ambiguous')
    const value = fields[0]?.[1]
    if (typeof value !== 'string' || value.trim() === '') continue
    recipients.push(name === 'email' ? `email:${goLower(value.trim())}` : `phone:${normalizePhone(value.trim())}`)
  }
  return recipients
}

// inbox is the mailbox an email recipient reaches: without a +tag, and
// without dots for Gmail, which ignores them.
export function inbox(recipient) {
  if (!recipient.startsWith('email:')) return recipient
  const address = recipient.slice('email:'.length), at = address.lastIndexOf('@')
  if (at < 1) return recipient
  let local = address.slice(0, at).split('+')[0]
  const domain = address.slice(at + 1)
  if (domain === 'gmail.com' || domain === 'googlemail.com') local = local.replaceAll('.', '')
  return `email:${local || address.slice(0, at)}@${domain === 'googlemail.com' ? 'gmail.com' : domain}`
}

// createSends counts one request against the network and each recipient.
// take returns the entries it counted, for release when GoTrue refuses the
// request, or, when that overspent a budget, the budget's name, the count
// before this request and the seconds until it reopens; a refused request
// gives back what it counted, so refusals do not prolong a wait.
export function createSends({ attempts, pool, budgets, trusted = null, events = null }) {
  return {
    // recipients lists who a request may send to: from its body, or the
    // signed-in user for a re-authentication or a phone factor's challenge.
    async recipients(route, body, claims) {
      const user = typeof claims?.sub === 'string' && /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(claims.sub) ? claims.sub.toLowerCase() : null
      if (route.kind === 'body') {
        const named = bodyRecipients(body)
        return route.user && user && named.length ? [...named, `user:${user}`] : named
      }
      if (!user) return []
      if (route.kind === 'reauthenticate') return [`user:${user}`]
      const { rows: [factor] } = await pool.query('SELECT factor_type FROM auth.mfa_factors WHERE id = $1 AND user_id = $2', [route.factor, user])
      return factor?.factor_type === 'phone' ? [`user:${user}`] : []
    },
    async take(recipients, address) {
      if (recipients.length === 0) return { taken: [] }
      const entries = [...(budgets.network ? [{ name: 'network', key: budgetKey('message-network', addressBucket(address)), budget: budgets.network }] : []),
        ...recipients.map(recipient => ({ name: 'recipient', recipient, key: budgetKey('message-recipient', inbox(recipient)), budget: budgets.recipient }))]
      const taken = []
      try {
        for (const entry of entries) {
          const { count, wait } = await attempts.take(entry)
          taken.push(entry)
          // A trusted network passes a spent recipient budget, up to a
          // ceiling, so it cannot be used to send without end.
          if (wait === 0) continue
          const passes = entry.recipient && trusted && count <= entry.budget.limit * limits.trustedBudgetMultiplier && await trusted(entry.recipient, address)
          if (!passes) {
            await attempts.release(taken)
            return { refused: { name: entry.name, key: entry.key, recipient: entry.recipient ?? null, budget: entry.budget, count: count - 1, wait } }
          }
          events?.record('trusted_pass', { subject: entry.recipient, address, alike: [entry.recipient, 'messages', networkLabel(address)], detail: { budget: 'message_recipient' } })
        }
      } catch (err) {
        if (taken.length) await attempts.release(taken).catch(() => {})
        throw err
      }
      return { taken }
    },
    release: taken => taken.length ? attempts.release(taken) : Promise.resolve(),
  }
}

import { limits } from './config.mjs'

// SMS country allowlist (ADR-1190). Texting costs the customer money and
// attracts SMS pumping, where bots make an app text premium numbers in
// countries it does not serve. AUTH_SMS_COUNTRIES lists the E.164 country
// calling codes the app may text; the front refuses every other number
// before GoTrue sends anything. E.164 calling codes are prefix-free, so a
// prefix match names exactly one country code ('1' is all of North America).

export function smsCountries(env) {
  const value = env.AUTH_SMS_COUNTRIES ?? ''
  if (value === '') return null
  const codes = value.split(',')
  if (codes.length > limits.smsCountries || !codes.every(code => /^[1-9][0-9]{0,2}$/.test(code))) {
    throw new Error(`AUTH_SMS_COUNTRIES must list 1 to ${limits.smsCountries} country calling codes such as 1,44,49`)
  }
  return [...new Set(codes)]
}

// smsSendPath reports whether a request may make GoTrue text a number it
// names: phone sign-in and sign-up, a resent code, a phone change and a
// phone MFA enrolment. A phone MFA challenge texts the enrolled number, which
// was checked at enrolment.
export function smsSendPath(method, url) {
  const path = url.split('?')[0]
  if (method === 'POST') return ['/auth/v1/otp', '/auth/v1/signup', '/auth/v1/resend', '/auth/v1/factors'].includes(path)
  return method === 'PUT' && path === '/auth/v1/user'
}

// phoneAllowed normalises a number exactly as GoTrue v2.197.0 does
// (formatPhoneNumber, then ^[1-9][0-9]{1,14}$). A number GoTrue would refuse
// is left for GoTrue to refuse; it is never texted.
export function phoneAllowed(phone, countries) {
  if (typeof phone !== 'string') return true
  const digits = (phone.startsWith('+') ? phone.slice(1) : phone).replaceAll(' ', '')
  if (!/^[1-9][0-9]{1,14}$/.test(digits)) return true
  return countries.some(code => digits.startsWith(code))
}

// bodyAllowed checks every key GoTrue would read as the phone number: Go's
// JSON decoding matches field names without regard to case.
export function bodyAllowed(raw, countries) {
  let body
  try { body = JSON.parse(raw.toString('utf8')) } catch { return true }
  if (!body || typeof body !== 'object' || Array.isArray(body)) return true
  return Object.entries(body).every(([key, value]) => key.toLowerCase() !== 'phone' || phoneAllowed(value, countries))
}

// baseURL returns an Auth app's origin: HTTPS, or HTTP on a loopback host,
// without credentials, a query or a fragment.
export function baseURL(value: string): string {
  const url = new URL(value)
  if (url.username || url.password || url.search || url.hash) throw new Error('Auth URL must not contain credentials, a query or a fragment')
  const loopback = ['localhost', '127.0.0.1', '[::1]'].includes(url.hostname)
  if (url.protocol !== 'https:' && !(url.protocol === 'http:' && loopback)) throw new Error('Auth requires HTTPS')
  return url.origin
}

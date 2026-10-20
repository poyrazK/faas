// Reading an access token's claims in the browser, without verifying them:
// the Auth app and backends verify; clients only decide what to show.

/** The organization a session acts in, from its token's org_* claims. */
export type ActiveOrganization = Readonly<{ id: string; slug: string; role: string; permissions: readonly string[] }>

/** claims reads an access token's claims without verifying them. */
export function claims(token: string): Record<string, unknown> {
  try {
    const part = token.split('.')[1].replace(/-/g, '+').replace(/_/g, '/')
    const value = JSON.parse(atob(part + '='.repeat((4 - part.length % 4) % 4)))
    return value && typeof value === 'object' ? value : {}
  } catch { return {} }
}

/** aal reads the assurance level of an access token without verifying it. */
export function aal(token: string): 'aal1' | 'aal2' {
  return claims(token).aal === 'aal2' ? 'aal2' : 'aal1'
}

export function activeOrganization(token: string): ActiveOrganization | null {
  const c = claims(token)
  if (typeof c.org_id !== 'string' || typeof c.org_slug !== 'string' || typeof c.org_role !== 'string') return null
  const permissions = Array.isArray(c.org_permissions) ? c.org_permissions.filter((p): p is string => typeof p === 'string') : []
  return { id: c.org_id, slug: c.org_slug, role: c.org_role, permissions }
}

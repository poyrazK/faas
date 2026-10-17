# @gregale/auth

Client for a Gregale application Auth app ([ADR-1190](../../docs/adr/1190-application-auth.md)):
sign-up and sign-in, sessions with automatic refresh, the hosted pages, and
organizations, invitations and authorization checks. It has no dependencies.

```ts
import { createAuthClient, resource } from '@gregale/auth'
import { createDataClient } from '@gregale/data'

const auth = createAuthClient({ url: 'https://shop-auth.gregale.app' })

// After a redirect from the hosted pages, a magic link or OAuth:
await auth.sessionFromUrl()

// Or send users to the hosted sign-in page:
location.href = auth.hostedSignInUrl({ redirectTo: 'https://shop.example.com/app' })

const data = createDataClient({ url: 'https://shop-data.gregale.app', accessToken: () => auth.getAccessToken() })

const [org] = await auth.organizations.list()
const canWrite = await auth.authorize('write', resource(org.id, 'doc', 42))
```

In a backend, verify tokens and check permissions with `@gregale/auth/server`
(`createVerifier`, `verify`, `authorize`, `bearerToken`): it checks the ES256
signature against the Auth app's keys, the issuer, the audience and the
expiry, with the same rules as the Go and Python SDKs. See
[Application Auth](../../docs/application-auth.md#in-your-backend).

Sessions are stored in `localStorage` in browsers and in memory elsewhere; pass
`store` to use your own, or `store: null` for memory only. Refresh tokens
rotate, so the client refreshes one request at a time and signs out when the
server rejects the refresh token. Failed requests throw `AuthError` with the
HTTP `status` and a stable `code`.

`signOut({ scope: 'global' })` ends every session of the user. Authorization
checks and row-level security honour sign-out on the next request; edge-only
JWT rules honour it when the access token expires.

When the Auth app's OAuth server is on, `connectedApps.list()` shows the
third-party apps the user allowed and `connectedApps.revoke(clientId)` ends
their access; they must ask the user again.

When the Auth app allows anonymous users, `signInAnonymously()` starts a
session without an account. Make the user permanent, keeping its data, with
`updateUser({ email, password })` or `linkIdentity({ provider, redirectTo })`
(identity linking must be on); `unlinkIdentity(id)` removes a sign-in method.

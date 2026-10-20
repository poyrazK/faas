# GregaleAuth for Swift

Native client for a Gregale application Auth app on iOS 15+ and macOS 12+
([ADR-1190](../../docs/adr/1190-application-auth.md)). No dependencies.

```swift
// Package.swift: .package(path: "…/sdk/swift"), then "GregaleAuth"
import GregaleAuth

let auth = try AuthClient(url: URL(string: "https://shop-auth.gregale.app")!)

// Email and password, or a code the user types from their email:
try await auth.signIn(email: email, password: password)
try await auth.signInWithOTP(email: email)
try await auth.verifyOTP(email: email, code: code)

// Sign in with Apple: pass the identity token and the raw nonce you hashed
// into the request.
try await auth.signInWithIdToken(provider: "apple", idToken: identityToken, nonce: rawNonce)

// OAuth or the hosted sign-in page (passkeys, SSO, CAPTCHA) in the system
// browser sheet, returning to a universal link (iOS 17.4+, macOS 14.4+):
try await auth.signInWithBrowser(provider: "github", callback: URL(string: "https://shop.example.com/auth/callback")!)

let token = try await auth.accessToken()          // for your API and @gregale/data
let allowed = try await auth.authorize(action: "write", resource: AuthClient.resource(org: orgID, kind: "doc", name: "42"))

for await change in await auth.changes() {        // signedIn, tokenRefreshed, signedOut, mfaVerified
    render(change.session)
}
```

- **Sessions** live in the Keychain (`kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly`,
  never synced) under the Auth app's origin; pass `store:` for your own
  `SessionStore`, or `MemorySessionStore()`. Refresh tokens rotate, so the
  client refreshes one request at a time and signs out when the Auth app
  rejects the refresh token.
- **Returns to the app** must be HTTPS universal links of your app that the
  Auth app allows: add the URL with `gregale auth config NAME --redirect-urls`.
  Custom URL schemes are refused because another app can claim them. OAuth
  uses PKCE; the verifier is kept in the store, so a return after the app
  was suspended still completes. Handle links the system opens yourself
  (an emailed sign-in or confirmation link) with `session(from: url)`.
- **Second factors:** `mfa.enrollTOTP`, `mfa.challenge`, `mfa.verify`,
  `mfa.verifyRecoveryCode` and `mfa.assuranceLevel()`; a user with a verified
  authenticator app or phone signs in at aal1 until they complete one.
- **Organizations:** `organizations.list/create`, `organizations.setActive`
  (with `--org-claims on`) and `organizations.active()`; also `sessions()`,
  `revokeSession`, `signOut(scope:)` and `deleteAccount()`.
- **Sign in with Apple / Google** needs the provider on in the Auth app with
  your app's bundle ID (or Android/iOS client ID) as its client ID.
- Passkeys on the device are not wrapped natively yet; the hosted sign-in
  page in `signInWithBrowser` offers them.

Failed requests throw `AuthError` with the HTTP `status` and a stable `code`.

## Tests

```sh
swift test                                   # unit tests against a stubbed Auth app
GREGALE_AUTH_KEYCHAIN_TEST=1 swift test      # also the login Keychain on macOS
GREGALE_AUTH_LIVE_URL=http://127.0.0.1:18200 GREGALE_AUTH_LIVE_OUTBOX=… GREGALE_AUTH_LIVE_CALLBACK=… swift test --filter LiveTests
```

The live test needs an Auth app with autoconfirm, organization claims,
self-deletion and a `doc:write` policy on, the callback among its redirect
URLs, and an outbox URL serving the sent emails as JSON.

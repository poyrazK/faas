# Gregale Auth for Kotlin and Android

Native client for a Gregale application Auth app
([ADR-1190](../../docs/adr/1190-application-auth.md)):

- `auth` (`dev.gregale:auth`): Kotlin/JVM, also on Android 8+ (API 26). It
  depends only on kotlinx-coroutines and kotlinx-serialization-json and talks
  HTTP through `HttpURLConnection`; pass your own `Transport` to use OkHttp.
- `auth-android` (`dev.gregale:auth-android`): `KeystoreSessionStore` and the
  browser hand-off. It is built only when the Android SDK is present
  (`ANDROID_HOME` or `local.properties`).

```kotlin
val url = "https://shop-auth.gregale.app"
val auth = AuthClient(url, KeystoreSessionStore(context, url))

// Email and password, or a code the user types from their email:
auth.signIn(email, password)
auth.signInWithEmailOtp(email)
auth.verifyEmailOtp(email, code)

// Google through Credential Manager: pass its ID token.
auth.signInWithIdToken("google", idToken)

// OAuth or the hosted sign-in page (passkeys, SSO, CAPTCHA) in the browser,
// returning to an App Link; the activity it opens completes it:
auth.startBrowserSignIn(activity, redirectTo = "https://shop.example.com/auth/callback", provider = "github")
override fun onNewIntent(intent: Intent) { super.onNewIntent(intent); lifecycleScope.launch { auth.completeBrowserSignIn(intent) } }

val token = auth.accessToken()                        // for your API and @gregale/data
val allowed = auth.authorize("write", AuthClient.resource(orgId, "doc", "42"))
auth.changes.collect { render(it.session) }           // SIGNED_IN, TOKEN_REFRESHED, SIGNED_OUT, MFA_VERIFIED
```

- **Sessions:** `KeystoreSessionStore` seals the session with AES-GCM under a
  non-exportable Android Keystore key in app-private preferences; a backup
  restored on another device reads as signed out. Without a store the session
  lives in memory. Refresh tokens rotate, so one refresh runs at a time and a
  rejected refresh token signs the user out.
- **Returns to the app** must be HTTPS App Links that the Auth app allows:
  `gregale auth config NAME --redirect-urls https://…`. OAuth and emailed links
  sent with `redirectTo` use PKCE; the verifier is kept in the store, so a
  return after the process was killed still completes.
- **Second factors** (`mfa.enrollTotp`, `challenge`, `verify`,
  `verifyRecoveryCode`, `assuranceLevel`), **organizations** (`list`, `create`,
  `setActive` with `--org-claims on`, `active`), `sessions()`,
  `revokeSession`, `signOut(scope)` and `deleteAccount()` work as in
  `@gregale/auth`.
- Passkeys on the device are not wrapped natively yet; the hosted sign-in
  page in the browser offers them.

Failed requests throw `AuthError` with the HTTP `status` and a stable `code`.

## Tests

```sh
./gradlew :auth:test
GREGALE_AUTH_LIVE_URL=http://127.0.0.1:18200 GREGALE_AUTH_LIVE_OUTBOX=… GREGALE_AUTH_LIVE_CALLBACK=… ./gradlew :auth:test --tests 'dev.gregale.auth.LiveTest'
```

The live test needs an Auth app with autoconfirm, organization claims,
self-deletion and a `doc:write` policy on, the callback among its redirect
URLs, and an outbox URL serving the sent emails as JSON; without
`GREGALE_AUTH_LIVE_URL` it returns at once.

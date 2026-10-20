package dev.gregale.auth

import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import java.net.URI
import java.net.URLDecoder
import java.net.URLEncoder
import java.security.MessageDigest
import java.security.SecureRandom
import java.util.Base64

/**
 * Client for a Gregale application Auth app (ADR-1190): sign-up and sign-in,
 * sessions with single-flight refresh, second factors, organizations and
 * authorization checks. On Android, pass a `KeystoreSessionStore` from
 * gregale-auth-android to keep the session between launches.
 *
 *     val auth = AuthClient("https://shop-auth.gregale.app", store)
 *     auth.signIn(email, password)
 *     auth.changes.collect { render(it.session) }
 */
public class AuthClient(
    url: String,
    private val store: SessionStore = MemorySessionStore(),
    private val transport: Transport = UrlConnectionTransport(),
    private val refreshMarginSeconds: Long = 60,
    private val now: () -> Long = { System.currentTimeMillis() / 1000 },
) {
    /** The Auth app's origin. */
    public val url: String = origin(url)

    private val refreshLock = Mutex()
    private val events = MutableSharedFlow<AuthChange>(extraBufferCapacity = 64)

    /** Each change of the stored session: sign-in, refresh, second step and sign-out. */
    public val changes: SharedFlow<AuthChange> = events.asSharedFlow()

    // MARK: Sessions

    private suspend fun save(session: Session, event: AuthEvent): Session {
        store.save(SESSION_KEY, encode(session))
        events.emit(AuthChange(event, session))
        return session
    }

    private suspend fun clear() {
        store.remove(SESSION_KEY)
        events.emit(AuthChange(AuthEvent.SIGNED_OUT, null))
    }

    private suspend fun storedSession(): Session? = store.load(SESSION_KEY)?.let(::decode)

    /** The stored session, refreshed first when its access token is about to expire. */
    public suspend fun currentSession(): Session? {
        val session = storedSession() ?: return null
        return if (session.expiresAt - now() <= refreshMarginSeconds) refreshSession() else session
    }

    /**
     * Refreshes the session. One refresh runs at a time: refresh tokens
     * rotate, so a caller that waited gets the session the first one made.
     * A refresh token the Auth app rejects signs the user out.
     */
    public suspend fun refreshSession(): Session? {
        val seen = storedSession()?.refreshToken
        return refreshLock.withLock {
            val current = storedSession() ?: return@withLock null
            if (seen != null && current.refreshToken != seen) return@withLock current
            try {
                save(parseSession(call("POST", "/auth/v1/token", listOf("grant_type" to "refresh_token"), body { put("refresh_token", current.refreshToken) })), AuthEvent.TOKEN_REFRESHED)
            } catch (error: AuthError) {
                if (error.status in 400..499) clear()
                throw error
            }
        }
    }

    /** A valid access token for other clients and your own API calls. */
    public suspend fun accessToken(): String = currentSession()?.accessToken ?: throw AuthError.notAuthenticated

    // MARK: Sign-up and sign-in

    public data class SignUpResult(val session: Session?, val user: User?)

    /**
     * Creates an account. Without autoconfirm the user confirms their address
     * first: with the emailed code (verifyEmailOtp with type "signup") or the
     * link, which returns to [redirectTo] for [sessionFromUrl].
     */
    public suspend fun signUp(email: String, password: String, data: JsonObject? = null, redirectTo: String? = null, captchaToken: String? = null): SignUpResult {
        val fields = buildJsonObject {
            put("email", email)
            put("password", password)
            if (data != null) put("data", data)
            if (redirectTo != null) pkceFields().forEach { (key, value) -> put(key, value) }
            captcha(captchaToken)
        }
        val response = call("POST", "/auth/v1/signup", redirect(redirectTo), fields)
        val session = try { parseSession(response) } catch (_: AuthError) { null }
        if (session != null) return SignUpResult(save(session, AuthEvent.SIGNED_IN), session.user)
        return SignUpResult(null, runCatching { User.from(parse(response)) }.getOrNull())
    }

    public suspend fun signIn(email: String, password: String, captchaToken: String? = null): Session =
        save(parseSession(call("POST", "/auth/v1/token", listOf("grant_type" to "password"), body { put("email", email); put("password", password); captcha(captchaToken) })), AuthEvent.SIGNED_IN)

    /** Emails a sign-in code (and a link that returns to [redirectTo]); finish with [verifyEmailOtp]. */
    public suspend fun signInWithEmailOtp(email: String, createUser: Boolean = false, redirectTo: String? = null, captchaToken: String? = null) {
        val fields = buildJsonObject {
            put("email", email)
            put("create_user", createUser)
            if (redirectTo != null) pkceFields().forEach { (key, value) -> put(key, value) }
            captcha(captchaToken)
        }
        call("POST", "/auth/v1/otp", redirect(redirectTo), fields)
    }

    /** Texts a code to [phone], with its country code (+447700900123). */
    public suspend fun signInWithPhoneOtp(phone: String, createUser: Boolean = false, captchaToken: String? = null) {
        call("POST", "/auth/v1/otp", body = body { put("phone", phone); put("channel", "sms"); put("create_user", createUser); captcha(captchaToken) })
    }

    /** Completes an emailed code; [type] is "email" (sign-in), "signup", "recovery", "invite" or "email_change". */
    public suspend fun verifyEmailOtp(email: String, code: String, type: String = "email"): Session =
        save(parseSession(call("POST", "/auth/v1/verify", body = body { put("type", type); put("email", email); put("token", code.trim()) })), AuthEvent.SIGNED_IN)

    public suspend fun verifyPhoneOtp(phone: String, code: String): Session =
        save(parseSession(call("POST", "/auth/v1/verify", body = body { put("type", "sms"); put("phone", phone); put("token", code.trim()) })), AuthEvent.SIGNED_IN)

    /** Starts a session without an account when the Auth app allows anonymous users. */
    public suspend fun signInAnonymously(captchaToken: String? = null): Session =
        save(parseSession(call("POST", "/auth/v1/signup", body = body { captcha(captchaToken) })), AuthEvent.SIGNED_IN)

    /**
     * Signs in with an ID token from the platform's own sign-in, such as
     * Google's Credential Manager (provider "google") or Sign in with Apple.
     * The provider must be on in the Auth app with your app's client ID.
     */
    public suspend fun signInWithIdToken(provider: String, idToken: String, nonce: String? = null, accessToken: String? = null): Session {
        val fields = body {
            put("provider", provider)
            put("id_token", idToken)
            if (nonce != null) put("nonce", nonce)
            if (accessToken != null) put("access_token", accessToken)
        }
        return save(parseSession(call("POST", "/auth/v1/token", listOf("grant_type" to "id_token"), fields)), AuthEvent.SIGNED_IN)
    }

    /**
     * The URL that starts OAuth with [provider] and returns to [redirectTo]
     * with a one-time code (PKCE). Open it in a Custom Tab or the browser and
     * pass the App Link the app receives to [sessionFromUrl]. [redirectTo]
     * must be an HTTPS URL the Auth app allows.
     */
    public suspend fun oauthUrl(provider: String, redirectTo: String, scopes: List<String> = emptyList()): String {
        val query = mutableListOf("provider" to provider, "redirect_to" to redirectTo, "code_challenge" to pkceChallenge(), "code_challenge_method" to "s256")
        if (scopes.isNotEmpty()) query += "scopes" to scopes.joinToString(" ")
        return link("/auth/v1/authorize", query)
    }

    /** The hosted sign-in page (passkeys, SSO and CAPTCHA included), which returns to [redirectTo] with the session in the fragment. */
    public fun hostedSignInUrl(redirectTo: String? = null): String = link("/ui/sign-in", redirect(redirectTo))

    /** The hosted account page: email address, password, second factors, passkeys and sessions. */
    public fun hostedAccountUrl(redirectTo: String? = null): String = link("/ui/account", redirect(redirectTo))

    /**
     * Completes a return from the hosted pages, an emailed link or OAuth: a
     * session in the fragment, or a one-time code exchanged with this
     * device's PKCE verifier. Null when the URL carries neither; a returned
     * error throws.
     */
    public suspend fun sessionFromUrl(returnUrl: String): Session? {
        val uri = URI(returnUrl)
        val params = params(uri.rawQuery) + params(uri.rawFragment)
        val error = params["error_code"] ?: params["error"]
        if (error != null) {
            val status = (params["status"] ?: params["code"])?.toIntOrNull() ?: 400
            throw AuthError(status, if (CODE.matches(error)) error else "http_$status")
        }
        val access = params["access_token"]
        val refresh = params["refresh_token"]
        if (access != null && refresh != null) {
            val expiresAt = params["expires_at"]?.toLongOrNull() ?: (now() + (params["expires_in"]?.toLongOrNull() ?: 0))
            return save(Session(access, refresh, expiresAt), AuthEvent.SIGNED_IN)
        }
        return params["code"]?.let { exchangeCode(it) }
    }

    /** Exchanges a one-time code from a PKCE return for a session. */
    public suspend fun exchangeCode(code: String): Session {
        val verifier = store.load(VERIFIER_KEY) ?: throw AuthError(400, "pkce_verifier_missing")
        val response = call("POST", "/auth/v1/token", listOf("grant_type" to "pkce"), body { put("auth_code", code); put("code_verifier", verifier) })
        store.remove(VERIFIER_KEY)
        return save(parseSession(response), AuthEvent.SIGNED_IN)
    }

    // A fresh verifier per flow, kept in the store so a return after the
    // process was killed still completes.
    private suspend fun pkceChallenge(): String {
        val verifier = base64Url(ByteArray(32).also { random.nextBytes(it) })
        store.save(VERIFIER_KEY, verifier)
        return base64Url(MessageDigest.getInstance("SHA-256").digest(verifier.toByteArray(Charsets.US_ASCII)))
    }

    private suspend fun pkceFields(): Map<String, JsonElement> =
        mapOf("code_challenge" to JsonPrimitive(pkceChallenge()), "code_challenge_method" to JsonPrimitive("s256"))

    // MARK: The signed-in user

    public suspend fun user(): User = User.from(parse(call("GET", "/auth/v1/user", token = accessToken())))

    /** Changes the email address (confirmed by email), the password or user metadata. */
    public suspend fun updateUser(email: String? = null, password: String? = null, data: JsonObject? = null): User {
        val fields = body {
            if (email != null) put("email", email)
            if (password != null) put("password", password)
            if (data != null) put("data", data)
        }
        return User.from(parse(call("PUT", "/auth/v1/user", body = fields, token = accessToken())))
    }

    /** Emails a recovery code and link; verifyEmailOtp(type = "recovery") signs the user in to choose a new password. */
    public suspend fun resetPassword(email: String, redirectTo: String? = null, captchaToken: String? = null) {
        call("POST", "/auth/v1/recover", redirect(redirectTo ?: "$url/ui/reset-password"), body { put("email", email); captcha(captchaToken) })
    }

    public enum class SignOutScope(internal val value: String) {
        /** This session. */
        LOCAL("local"),
        /** Every session of the user. */
        GLOBAL("global"),
        /** Every session but this one; this one stays signed in. */
        OTHERS("others"),
    }

    /** Clears the stored session even when the Auth app cannot be reached (except for OTHERS), then rethrows that failure. */
    public suspend fun signOut(scope: SignOutScope = SignOutScope.LOCAL) {
        val session = storedSession()
        if (session != null) {
            try {
                call("POST", "/auth/v1/logout", listOf("scope" to scope.value), token = session.accessToken)
            } catch (error: AuthError) {
                if (error.status >= 500) {
                    if (scope != SignOutScope.OTHERS) clear()
                    throw error
                }
            } catch (error: Exception) {
                if (scope != SignOutScope.OTHERS) clear()
                throw error
            }
        }
        if (scope != SignOutScope.OTHERS) clear()
    }

    /** The signed-in user's own sessions, most recently active first. */
    public suspend fun sessions(): List<UserSession> =
        (parse(authz("GET", "/sessions"))["sessions"] as? JsonArray).orEmpty().mapNotNull { element ->
            val s = element as? JsonObject ?: return@mapNotNull null
            UserSession(s.string("id") ?: "", s.string("created_at") ?: "", s.string("last_active_at") ?: "", s.string("user_agent"), s.string("ip"), (s["current"] as? JsonPrimitive)?.content == "true")
        }

    /** Ends one of the user's sessions; end this one with [signOut]. */
    public suspend fun revokeSession(id: String) {
        authz("DELETE", "/sessions/${escape(id)}")
    }

    /**
     * Deletes the account when the Auth app allows it. The session must come
     * from a sign-in in the last ten minutes, else this throws
     * reauthentication_required. Returns how many organizations went with it.
     */
    public suspend fun deleteAccount(): Int {
        val deleted = parse(authz("DELETE", "/account")).int("organizations_deleted") ?: 0
        clear()
        return deleted
    }

    /** Whether the signed-in user may do [action] on [resource]; see [resource]. */
    public suspend fun authorize(action: String, resource: String): Boolean =
        (parse(authz("POST", "/authorize", body { put("action", action); put("resource", resource) }))["allowed"] as? JsonPrimitive)?.content == "true"

    // MARK: Second factors

    /** Second factors. A user who enrolled one signs in at aal1 and reaches aal2 by verifying a challenge or a recovery code. */
    public val mfa: Mfa = Mfa()

    public inner class Mfa internal constructor() {
        public suspend fun listFactors(): List<Factor> = user().factors

        /** Starts enrolling an authenticator app; show the secret or QR code, then challenge and verify to activate it. */
        public suspend fun enrollTotp(friendlyName: String? = null): TotpEnrollment {
            val json = parse(call("POST", "/auth/v1/factors", body = body { put("factor_type", "totp"); if (friendlyName != null) put("friendly_name", friendlyName) }, token = accessToken()))
            val totp = json["totp"].asObject()
            return TotpEnrollment(json.string("id") ?: "", totp.string("secret") ?: "", totp.string("uri") ?: "", totp.string("qr_code") ?: "")
        }

        public suspend fun enrollPhone(phone: String, friendlyName: String? = null): Factor =
            Factor.from(parse(call("POST", "/auth/v1/factors", body = body { put("factor_type", "phone"); put("phone", phone); if (friendlyName != null) put("friendly_name", friendlyName) }, token = accessToken())))
                ?: throw AuthError.invalidSession

        /** Creates a challenge; for a phone factor this texts the code. */
        public suspend fun challenge(factorId: String): FactorChallenge {
            val json = parse(call("POST", "/auth/v1/factors/${escape(factorId)}/challenge", body = JsonObject(emptyMap()), token = accessToken()))
            return FactorChallenge(json.string("id") ?: "", json.string("type"))
        }

        /** Verifies a challenge; the stored session becomes aal2. */
        public suspend fun verify(factorId: String, challengeId: String, code: String): Session =
            save(parseSession(call("POST", "/auth/v1/factors/${escape(factorId)}/verify", body = body { put("challenge_id", challengeId); put("code", code.trim()) }, token = accessToken())), AuthEvent.MFA_VERIFIED)

        /** Uses one recovery code in place of a second factor. */
        public suspend fun verifyRecoveryCode(code: String): Session =
            save(parseSession(call("POST", "/auth/v1/factors/recovery-codes/verify", body = body { put("code", code.trim()) }, token = accessToken())), AuthEvent.MFA_VERIFIED)

        public suspend fun unenroll(factorId: String) {
            call("DELETE", "/auth/v1/factors/${escape(factorId)}", token = accessToken())
        }

        /** The session's level, and AAL2 as next when the user has a verified authenticator app or phone to complete. */
        public suspend fun assuranceLevel(): AssuranceLevels {
            val session = currentSession() ?: return AssuranceLevels(null, null)
            val pending = listFactors().any { it.status == "verified" && (it.factorType == "totp" || it.factorType == "phone") }
            return AssuranceLevels(session.assuranceLevel, if (pending) AssuranceLevel.AAL2 else session.assuranceLevel)
        }
    }

    // MARK: Organizations

    public val organizations: Organizations = Organizations()

    public inner class Organizations internal constructor() {
        public suspend fun list(): List<Organization> =
            (parse(authz("GET", "/orgs"))["organizations"] as? JsonArray).orEmpty().mapNotNull { (it as? JsonObject)?.let(Organization::from) }

        public suspend fun create(slug: String, name: String): Organization =
            Organization.from(parse(authz("POST", "/orgs", body { put("slug", slug); put("name", name) })))

        /**
         * Makes [org] (one the user belongs to) the session's active
         * organization, or none with null, and refreshes the session so its
         * token carries it. Needs organization claims on in the Auth app.
         */
        public suspend fun setActive(org: String?): ActiveOrganization? {
            authz("POST", "/active-organization", JsonObject(mapOf("org" to (org?.let(::JsonPrimitive) ?: JsonNull))))
            return refreshSession()?.activeOrganization
        }

        /** The session's active organization, read from its token. */
        public suspend fun active(): ActiveOrganization? = currentSession()?.activeOrganization
    }

    // MARK: HTTP

    private suspend fun authz(method: String, path: String, body: JsonObject? = null): String = call(method, "/authz/v1$path", body = body, token = accessToken())

    private suspend fun call(method: String, path: String, query: List<Pair<String, String>> = emptyList(), body: JsonObject? = null, token: String? = null): String {
        val headers = buildMap {
            if (body != null) put("Content-Type", "application/json")
            if (token != null) put("Authorization", "Bearer $token")
        }
        val response = transport.send(HttpRequest(method, link(path, query), headers, body?.toString()))
        if (response.status !in 200..299) throw error(response.status, response.body)
        return response.body
    }

    private fun link(path: String, query: List<Pair<String, String>>): String =
        url + path + if (query.isEmpty()) "" else "?" + query.joinToString("&") { (key, value) -> "${escape(key)}=${escape(value)}" }

    private fun redirect(target: String?): List<Pair<String, String>> = if (target == null) emptyList() else listOf("redirect_to" to target)

    private fun kotlinx.serialization.json.JsonObjectBuilder.captcha(token: String?) {
        if (token != null) put("gotrue_meta_security", buildJsonObject { put("captcha_token", token) })
    }

    public companion object {
        private const val SESSION_KEY = "session"
        private const val VERIFIER_KEY = "pkce"
        private val CODE = Regex("^[a-z0-9_]{1,64}$")
        private val random = SecureRandom()

        /** The resource name authorize() and gregale_auth.authorize() expect. */
        public fun resource(org: String, kind: String, name: String): String = "org/$org/$kind/$name"

        internal fun origin(value: String): String {
            val invalid = AuthError(400, "invalid_auth_url")
            val uri = try { URI(value) } catch (_: Exception) { throw invalid }
            val scheme = uri.scheme?.lowercase() ?: throw invalid
            val host = uri.host ?: throw invalid
            if (uri.rawUserInfo != null || uri.rawQuery != null || uri.rawFragment != null || !(uri.rawPath.isNullOrEmpty() || uri.rawPath == "/")) throw invalid
            val loopback = host in setOf("localhost", "127.0.0.1", "[::1]")
            if (!(scheme == "https" || (scheme == "http" && loopback))) throw invalid
            return "$scheme://$host" + if (uri.port == -1) "" else ":${uri.port}"
        }

        internal fun error(status: Int, body: String): AuthError {
            val json = try { Json.parseToJsonElement(body) as? JsonObject } catch (_: Exception) { null }
            val code = json?.let { it.string("error_code") ?: it.string("code") ?: it.string("error") }
            return AuthError(status, if (code != null && CODE.matches(code)) code else "http_$status")
        }

        internal fun parse(body: String): JsonObject = try { Json.parseToJsonElement(body) as? JsonObject } catch (_: Exception) { null } ?: throw AuthError.invalidSession

        internal fun parseSession(body: String): Session {
            val json = try { Json.parseToJsonElement(body) as? JsonObject } catch (_: Exception) { null } ?: throw AuthError.invalidSession
            val access = json.string("access_token") ?: throw AuthError.invalidSession
            val refresh = json.string("refresh_token") ?: throw AuthError.invalidSession
            val expiresAt = json.long("expires_at") ?: json.long("expires_in")?.let { System.currentTimeMillis() / 1000 + it } ?: throw AuthError.invalidSession
            // A user object the SDK cannot read must not lose the session.
            val user = (json["user"] as? JsonObject)?.let { runCatching { User.from(it) }.getOrNull() }
            return Session(access, refresh, expiresAt, user)
        }

        private fun encode(session: Session): String = buildJsonObject {
            put("accessToken", session.accessToken)
            put("refreshToken", session.refreshToken)
            put("expiresAt", session.expiresAt)
        }.toString()

        private fun decode(value: String): Session? = try {
            val json = Json.parseToJsonElement(value) as JsonObject
            Session(json.string("accessToken")!!, json.string("refreshToken")!!, json.long("expiresAt")!!)
        } catch (_: Exception) {
            null
        }

        private fun body(build: kotlinx.serialization.json.JsonObjectBuilder.() -> Unit): JsonObject = buildJsonObject(build)

        private fun params(raw: String?): Map<String, String> =
            raw.orEmpty().split('&').filter { it.isNotEmpty() }.associate { part ->
                val (key, value) = part.split('=', limit = 2).let { it[0] to it.getOrElse(1) { "" } }
                URLDecoder.decode(key, "UTF-8") to URLDecoder.decode(value, "UTF-8")
            }

        internal fun escape(value: String): String = URLEncoder.encode(value, "UTF-8").replace("+", "%20").replace("*", "%2A").replace("%7E", "~")

        private fun base64Url(bytes: ByteArray): String = Base64.getUrlEncoder().withoutPadding().encodeToString(bytes)
    }
}

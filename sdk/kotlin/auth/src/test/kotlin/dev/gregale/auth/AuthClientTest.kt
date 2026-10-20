package dev.gregale.auth

import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonArray
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import java.net.URI
import java.net.URLDecoder
import java.security.MessageDigest
import java.util.Base64
import java.util.Collections
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

/** Routes requests by "METHOD /path" and records them. */
class FakeAuthApp(private val routes: Map<String, (Call) -> Pair<Int, JsonObject?>>) : Transport {
    class Call(val key: String, val query: Map<String, String>, val headers: Map<String, String>, val body: JsonObject)

    val calls: MutableList<Call> = Collections.synchronizedList(mutableListOf())
    fun count(key: String) = calls.count { it.key == key }

    override suspend fun send(request: HttpRequest): HttpResponse {
        val uri = URI(request.url)
        val query = uri.rawQuery.orEmpty().split('&').filter { it.isNotEmpty() }.associate { it.substringBefore('=') to URLDecoder.decode(it.substringAfter('='), "UTF-8") }
        val body = request.body?.let { AuthClient.parse(it) } ?: JsonObject(emptyMap())
        val call = Call("${request.method} ${uri.path}", query, request.headers, body)
        calls += call
        val (status, json) = routes[call.key]?.invoke(call) ?: (404 to buildJsonObject { put("code", "not_found") })
        return HttpResponse(status, json?.toString() ?: "")
    }
}

fun jwt(vararg claims: Pair<String, Any>): String {
    val json = buildJsonObject {
        for ((key, value) in claims) when (value) {
            is String -> put(key, value)
            is List<*> -> put(key, buildJsonArray { value.forEach { add(JsonPrimitive(it as String)) } })
        }
    }
    return "eyJhbGciOiJFUzI1NiJ9.${Base64.getUrlEncoder().withoutPadding().encodeToString(json.toString().toByteArray())}.sig"
}

fun tokens(n: Int, vararg claims: Pair<String, Any> = arrayOf("sub" to "u1"), expiresIn: Long = 600) = buildJsonObject {
    put("access_token", jwt(*claims))
    put("refresh_token", "refresh-$n")
    put("expires_in", expiresIn)
    put("user", buildJsonObject { put("id", "u1"); put("email", "a@example.test") })
}

fun json(build: kotlinx.serialization.json.JsonObjectBuilder.() -> Unit) = buildJsonObject(build)

class AuthClientTest {
    private val origin = "https://shop-auth.gregale.app"

    @Test
    fun rejectsUnsafeAuthUrls() {
        for (value in listOf("http://shop-auth.gregale.app", "https://user:pw@shop-auth.gregale.app", "https://shop-auth.gregale.app/?x=1", "https://shop-auth.gregale.app/#x", "https://shop-auth.gregale.app/auth")) {
            assertFailsWith<AuthError>(value) { AuthClient(value) }
        }
        assertEquals("http://localhost:8080", AuthClient("http://localhost:8080/").url)
        assertEquals(origin, AuthClient("$origin/").url)
    }

    @Test
    fun signsInStoresTheSessionAndAuthenticatesCalls() = runBlocking {
        val app = FakeAuthApp(mapOf(
            "POST /auth/v1/token" to { call -> if (call.body["password"] == JsonPrimitive("right")) 200 to tokens(1) else 400 to json { put("error_code", "invalid_credentials"); put("code", 400) } },
            "GET /authz/v1/orgs" to { _ -> 200 to json { put("organizations", buildJsonArray { add(json { put("id", "o1"); put("slug", "acme"); put("name", "Acme"); put("role", "owner") }) }) } },
            "POST /authz/v1/authorize" to { call -> 200 to json { put("allowed", call.body["action"] == JsonPrimitive("read")) } },
        ))
        val auth = AuthClient(origin, transport = app)
        assertEquals(AuthError(400, "invalid_credentials"), assertFailsWith<AuthError> { auth.signIn("a@example.test", "wrong") })
        val signedIn = async(start = CoroutineStart.UNDISPATCHED) { auth.changes.first() }
        val session = auth.signIn("a@example.test", "right")
        assertEquals(AuthEvent.SIGNED_IN, signedIn.await().event)
        assertEquals("refresh-1", session.refreshToken)
        assertEquals("a@example.test", session.user?.email)
        assertEquals("password", app.calls.last().query["grant_type"])

        assertEquals("acme", auth.organizations.list().single().slug)
        assertEquals("Bearer ${session.accessToken}", app.calls.last().headers["Authorization"])
        assertTrue(auth.authorize("read", AuthClient.resource("o1", "doc", "42")))
        assertFalse(auth.authorize("write", AuthClient.resource("o1", "doc", "42")))
        assertEquals(JsonPrimitive("org/o1/doc/42"), app.calls.last().body["resource"])
    }

    @Test
    fun refreshesOnceForConcurrentCallersAndSignsOutOnRejection() = runBlocking {
        val store = MemorySessionStore()
        val app = FakeAuthApp(mapOf(
            "POST /auth/v1/token" to { call -> if (call.body["refresh_token"] == JsonPrimitive("refresh-0")) 200 to tokens(1) else 400 to json { put("error_code", "refresh_token_not_found") } },
        ))
        val auth = AuthClient(origin, store, app)
        store.save("session", json { put("accessToken", jwt("sub" to "u1")); put("refreshToken", "refresh-0"); put("expiresAt", System.currentTimeMillis() / 1000 + 5) }.toString())
        val seen = (1..4).map { async { auth.accessToken() } }.awaitAll()
        assertEquals(1, seen.toSet().size)
        assertEquals(1, app.count("POST /auth/v1/token"))
        assertEquals("refresh-1", auth.currentSession()?.refreshToken)

        val signedOut = async(start = CoroutineStart.UNDISPATCHED) { auth.changes.first() }
        assertEquals("refresh_token_not_found", assertFailsWith<AuthError> { auth.refreshSession() }.code)
        assertEquals(AuthEvent.SIGNED_OUT, signedOut.await().event)
        assertNull(store.load("session"))
        assertEquals("not_authenticated", assertFailsWith<AuthError> { auth.accessToken() }.code)
    }

    @Test
    fun signUpWaitingForConfirmationAndCodes() = runBlocking {
        val app = FakeAuthApp(mapOf(
            "POST /auth/v1/signup" to { _ -> 200 to json { put("id", "u3"); put("email", "c@example.test") } },
            "POST /auth/v1/otp" to { _ -> 200 to json {} },
            "POST /auth/v1/verify" to { call -> if (call.body["token"] == JsonPrimitive("123456")) 200 to tokens(2) else 403 to json { put("error_code", "otp_expired") } },
        ))
        val auth = AuthClient(origin, transport = app)
        val callback = "https://shop.example/auth/callback?next=/a&b=c"
        val result = auth.signUp("c@example.test", "long password", json { put("name", "C") }, callback, "cap")
        assertNull(result.session)
        assertEquals("u3", result.user?.id)
        val signup = app.calls.last()
        assertEquals(callback, signup.query["redirect_to"])
        assertEquals(json { put("name", "C") }, signup.body["data"])
        assertEquals(JsonPrimitive("s256"), signup.body["code_challenge_method"])
        assertEquals(json { put("captcha_token", "cap") }, signup.body["gotrue_meta_security"])

        auth.signInWithEmailOtp("c@example.test")
        assertEquals(json { put("email", "c@example.test"); put("create_user", false) }, app.calls.last().body)
        assertEquals("otp_expired", assertFailsWith<AuthError> { auth.verifyEmailOtp("c@example.test", "000000") }.code)
        assertEquals("refresh-2", auth.verifyEmailOtp("c@example.test", " 123456 ", "signup").refreshToken)
        assertEquals(json { put("type", "signup"); put("email", "c@example.test"); put("token", "123456") }, app.calls.last().body)

        auth.signInWithPhoneOtp("+15550100", createUser = true)
        assertEquals(json { put("phone", "+15550100"); put("channel", "sms"); put("create_user", true) }, app.calls.last().body)
    }

    @Test
    fun oauthWithPkceAndReturnUrls() = runBlocking {
        val store = MemorySessionStore()
        val app = FakeAuthApp(mapOf(
            "POST /auth/v1/token" to { call -> if (call.query["grant_type"] == "pkce" && call.body["auth_code"] == JsonPrimitive("c0de")) 200 to tokens(3) else 400 to json { put("error_code", "bad_code_verifier") } },
        ))
        val auth = AuthClient(origin, store, app)
        val callback = "https://shop.example/auth/callback"
        val start = URI(auth.oauthUrl("github", callback, listOf("read:user", "user:email")))
        val query = start.rawQuery.split('&').associate { it.substringBefore('=') to URLDecoder.decode(it.substringAfter('='), "UTF-8") }
        assertEquals("/auth/v1/authorize", start.path)
        assertEquals("github", query["provider"])
        assertEquals(callback, query["redirect_to"])
        assertEquals("read:user user:email", query["scopes"])
        assertEquals("s256", query["code_challenge_method"])
        val verifier = assertNotNull(store.load("pkce"))
        assertEquals(Base64.getUrlEncoder().withoutPadding().encodeToString(MessageDigest.getInstance("SHA-256").digest(verifier.toByteArray())), query["code_challenge"])

        assertEquals("refresh-3", auth.sessionFromUrl("$callback?code=c0de")?.refreshToken)
        assertEquals(JsonPrimitive(verifier), app.calls.last().body["code_verifier"])
        assertNull(store.load("pkce"))
        assertEquals("pkce_verifier_missing", assertFailsWith<AuthError> { auth.sessionFromUrl("$callback?code=again") }.code)
    }

    @Test
    fun sessionInTheFragmentAndReturnedErrors() = runBlocking {
        val app = FakeAuthApp(emptyMap())
        val auth = AuthClient(origin, transport = app, now = { 1_000_000 })
        val token = jwt("sub" to "u2", "aal" to "aal2", "org_id" to "o1", "org_slug" to "acme", "org_role" to "admin", "org_permissions" to listOf("doc:write"))
        val session = assertNotNull(auth.sessionFromUrl("https://shop.example/cb#access_token=$token&refresh_token=r1&expires_in=600&token_type=bearer"))
        assertEquals("r1", session.refreshToken)
        assertEquals(1_000_600, session.expiresAt)
        assertEquals(AssuranceLevel.AAL2, session.assuranceLevel)
        assertEquals(ActiveOrganization("o1", "acme", "admin", listOf("doc:write")), session.activeOrganization)
        assertNull(auth.sessionFromUrl("https://shop.example/cb"))
        assertEquals("otp_expired", assertFailsWith<AuthError> { auth.sessionFromUrl("https://shop.example/cb#error=access_denied&error_code=otp_expired&error_description=Email+link+is+invalid") }.code)
        assertEquals(0, app.calls.size)
    }

    @Test
    fun secondStepAndOrganizations() = runBlocking {
        val orgToken = jwt("sub" to "u1", "aal" to "aal2", "org_id" to "o2", "org_slug" to "beta", "org_role" to "owner", "org_permissions" to listOf("*"))
        val factors = json {
            put("id", "u1")
            put("factors", buildJsonArray {
                add(json { put("id", "f1"); put("factor_type", "totp"); put("status", "verified"); put("friendly_name", "Phone") })
                add(json { put("id", "rc"); put("factor_type", "recovery_code"); put("status", "verified") })
            })
        }
        val app = FakeAuthApp(mapOf(
            "POST /auth/v1/token" to { call -> if (call.query["grant_type"] == "password") 200 to tokens(1, "sub" to "u1", "aal" to "aal1") else 200 to json { put("access_token", orgToken); put("refresh_token", "refresh-9"); put("expires_in", 600) } },
            "GET /auth/v1/user" to { _ -> 200 to factors },
            "POST /auth/v1/factors/f1/challenge" to { _ -> 200 to json { put("id", "c1"); put("type", "totp") } },
            "POST /auth/v1/factors/f1/verify" to { call -> if (call.body["code"] == JsonPrimitive("123456")) 200 to tokens(2, "sub" to "u1", "aal" to "aal2") else 422 to json { put("error_code", "mfa_verification_failed") } },
            "POST /authz/v1/active-organization" to { _ -> 200 to json { put("organization", json { put("id", "o2") }) } },
        ))
        val auth = AuthClient(origin, transport = app)
        auth.signIn("a@example.test", "pw")
        assertEquals(AssuranceLevels(AssuranceLevel.AAL1, AssuranceLevel.AAL2), auth.mfa.assuranceLevel())
        val challenge = auth.mfa.challenge("f1")
        assertEquals("mfa_verification_failed", assertFailsWith<AuthError> { auth.mfa.verify("f1", challenge.id, "000000") }.code)
        val verified = async(start = CoroutineStart.UNDISPATCHED) { auth.changes.first() }
        assertEquals(AssuranceLevel.AAL2, auth.mfa.verify("f1", challenge.id, "123456").assuranceLevel)
        assertEquals(AuthEvent.MFA_VERIFIED, verified.await().event)
        assertEquals(AssuranceLevel.AAL2, auth.mfa.assuranceLevel().current)

        assertEquals("beta", auth.organizations.setActive("o2")?.slug)
        assertEquals(json { put("org", "o2") }, app.calls.first { it.key == "POST /authz/v1/active-organization" }.body)
        assertEquals("o2", auth.organizations.active()?.id)
        auth.organizations.setActive(null)
        assertEquals(JsonObject(mapOf("org" to JsonNull)), app.calls.last { it.key == "POST /authz/v1/active-organization" }.body)
    }

    @Test
    fun signOutClearsTheSessionEvenWhenTheServerFails() = runBlocking {
        val store = MemorySessionStore()
        val app = FakeAuthApp(mapOf(
            "POST /auth/v1/token" to { _ -> 200 to tokens(1) },
            "POST /auth/v1/logout" to { call -> if (call.query["scope"] == "others") 204 to null else 503 to null },
        ))
        val auth = AuthClient(origin, store, app)
        auth.signIn("a@example.test", "pw")
        auth.signOut(AuthClient.SignOutScope.OTHERS)
        assertNotNull(store.load("session"))
        assertEquals(503, assertFailsWith<AuthError> { auth.signOut(AuthClient.SignOutScope.GLOBAL) }.status)
        assertNull(store.load("session"))
    }

    @Test
    fun hostedPagesAndEscaping() {
        val auth = AuthClient(origin)
        assertEquals("$origin/ui/sign-in?redirect_to=https%3A%2F%2Fshop.example%2Fapp%3Ftab%3D1%26x%3Da%2Bb%20c", auth.hostedSignInUrl("https://shop.example/app?tab=1&x=a+b c"))
        assertEquals("$origin/ui/account", auth.hostedAccountUrl())
    }
}

package dev.gregale.auth

import kotlinx.coroutines.delay
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.put
import java.net.HttpURLConnection
import java.net.URI
import java.nio.ByteBuffer
import java.util.UUID
import javax.crypto.Mac
import javax.crypto.spec.SecretKeySpec
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertNotEquals
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

/**
 * Runs against a real Auth app when GREGALE_AUTH_LIVE_URL is set: autoconfirm,
 * organization claims, self-deletion and a doc:write policy on,
 * GREGALE_AUTH_LIVE_CALLBACK an allowed redirect URL, and
 * GREGALE_AUTH_LIVE_OUTBOX serving the sent emails as JSON.
 */
class LiveTest {
    private val env = System.getenv()

    @Test
    fun againstARealAuthApp() = runBlocking {
        val live = env["GREGALE_AUTH_LIVE_URL"] ?: return@runBlocking println("skipped: set GREGALE_AUTH_LIVE_URL, GREGALE_AUTH_LIVE_OUTBOX and GREGALE_AUTH_LIVE_CALLBACK")
        val outbox = env.getValue("GREGALE_AUTH_LIVE_OUTBOX")
        val callback = env.getValue("GREGALE_AUTH_LIVE_CALLBACK")
        val emailedOrigin = env["GREGALE_AUTH_LIVE_EMAIL_ORIGIN"]
        val auth = AuthClient(live)
        val email = "kotlin-${UUID.randomUUID().toString().take(8)}@example.test"
        val password = "correct horse battery staple"

        val signedUp = auth.signUp(email, password, json { put("plan", "free") })
        assertNotNull(signedUp.session, "autoconfirm signs in at once")
        assertEquals(email, signedUp.user?.email)
        assertEquals("invalid_credentials", assertFailsWith<AuthError> { auth.signIn(email, "wrong password") }.code)
        assertEquals(JsonPrimitive("pro"), auth.updateUser(data = json { put("plan", "pro") }).userMetadata?.get("plan"))
        val before = auth.currentSession()
        val after = auth.refreshSession()
        assertNotEquals(before?.refreshToken, after?.refreshToken, "refresh tokens rotate")

        // An emailed code, typed into the app.
        auth.signOut()
        auth.signInWithEmailOtp(email)
        val code = assertNotNull(Regex("""Code: (\d{6})""").find(latestEmail(outbox, email))?.groupValues?.get(1))
        assertEquals(JsonPrimitive(email), auth.verifyEmailOtp(email, code).claims["email"])

        // An emailed link returning to the app with a PKCE code. GoTrue sends
        // one email a minute per address, so a new person signs up by link.
        auth.signOut()
        val newcomer = "kotlin-link-${UUID.randomUUID().toString().take(8)}@example.test"
        auth.signInWithEmailOtp(newcomer, createUser = true, redirectTo = callback)
        var link = assertNotNull(Regex("""(https?://\S+/verify\?\S+)""").find(latestEmail(outbox, newcomer))?.groupValues?.get(1))
        if (emailedOrigin != null) link = link.replace(emailedOrigin, live)
        val returned = redirect(link)
        assertTrue(returned.startsWith(callback), "returned to $returned")
        assertTrue(URI(returned).rawQuery.orEmpty().contains("code="), "a PKCE return carries a code: $returned")
        assertEquals(JsonPrimitive(newcomer), auth.sessionFromUrl(returned)?.claims?.get("email"))
        auth.signOut()
        auth.signIn(email, password)

        // A second step.
        val enrollment = auth.mfa.enrollTotp("Work phone")
        assertEquals(AssuranceLevel.AAL2, auth.mfa.verify(enrollment.id, auth.mfa.challenge(enrollment.id).id, totp(enrollment.secret)).assuranceLevel)
        auth.signOut()
        assertEquals(AssuranceLevel.AAL1, auth.signIn(email, password).assuranceLevel)
        assertEquals(AssuranceLevels(AssuranceLevel.AAL1, AssuranceLevel.AAL2), auth.mfa.assuranceLevel())
        val wrong = auth.mfa.challenge(enrollment.id)
        assertEquals("mfa_verification_failed", assertFailsWith<AuthError> { auth.mfa.verify(enrollment.id, wrong.id, "000000") }.code)
        assertEquals(AssuranceLevel.AAL2, auth.mfa.verify(enrollment.id, auth.mfa.challenge(enrollment.id).id, totp(enrollment.secret)).assuranceLevel)

        // Organizations, the active one in the token, and authorization.
        val org = auth.organizations.create("kotlin-${UUID.randomUUID().toString().take(8)}", "Kotlin Co")
        assertEquals(ActiveOrganization(org.id, org.slug, "owner", listOf("*")), auth.organizations.setActive(org.id))
        assertEquals(AssuranceLevel.AAL2, auth.currentSession()?.assuranceLevel, "switching keeps the second step")
        assertEquals(org.id, auth.organizations.active()?.id)
        assertTrue(auth.authorize("write", AuthClient.resource(org.id, "doc", "1")))
        assertFalse(auth.authorize("write", AuthClient.resource("00000000-0000-4000-8000-000000000000", "doc", "1")))
        assertNull(auth.organizations.setActive(null))

        assertTrue(auth.sessions().any { it.current })
        assertEquals(1, auth.deleteAccount())
        assertNull(auth.currentSession())
        assertEquals("invalid_credentials", assertFailsWith<AuthError> { auth.signIn(email, password) }.code)
    }

    // The newest email to address, waiting briefly for it to arrive.
    private suspend fun latestEmail(outbox: String, address: String): String {
        repeat(50) {
            val emails = Json.parseToJsonElement(URI(outbox).toURL().readText()) as JsonArray
            val mail = emails.lastOrNull { (it as JsonObject).string("to") == address } as JsonObject?
            if (mail != null) return mail.string("text") ?: ""
            delay(100)
        }
        error("no email to $address")
    }

    // Opens a link without following its redirect, as the app's App Link would receive it.
    private fun redirect(link: String): String {
        val connection = URI(link).toURL().openConnection() as HttpURLConnection
        connection.instanceFollowRedirects = false
        try {
            return assertNotNull(connection.getHeaderField("Location"), "no redirect from $link (${connection.responseCode})")
        } finally {
            connection.disconnect()
        }
    }
}

/** RFC 6238 code for an authenticator app's base32 secret. */
fun totp(secret: String, epochSeconds: Long = System.currentTimeMillis() / 1000): String {
    val alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"
    val bytes = mutableListOf<Byte>()
    var buffer = 0
    var bits = 0
    for (character in secret.uppercase()) {
        val index = alphabet.indexOf(character)
        if (index < 0) continue
        buffer = (buffer shl 5 or index) and 0xffff
        bits += 5
        if (bits >= 8) {
            bytes += ((buffer shr (bits - 8)) and 0xff).toByte()
            bits -= 8
        }
    }
    val mac = Mac.getInstance("HmacSHA1").apply { init(SecretKeySpec(bytes.toByteArray(), "HmacSHA1")) }.doFinal(ByteBuffer.allocate(8).putLong(epochSeconds / 30).array())
    val offset = mac.last().toInt() and 0x0f
    val value = ((mac[offset].toInt() and 0x7f) shl 24 or ((mac[offset + 1].toInt() and 0xff) shl 16) or ((mac[offset + 2].toInt() and 0xff) shl 8) or (mac[offset + 3].toInt() and 0xff)) % 1_000_000
    return value.toString().padStart(6, '0')
}

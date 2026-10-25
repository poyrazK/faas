package dev.gregale.auth

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.booleanOrNull
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.intOrNull
import kotlinx.serialization.json.longOrNull
import java.util.Base64

/** A signed-in session. The client refreshes it shortly before [expiresAt] (Unix seconds). */
public data class Session(
    val accessToken: String,
    val refreshToken: String,
    val expiresAt: Long,
    val user: User? = null,
) {
    /** The access token's claims, read without verifying them. */
    val claims: JsonObject get() = Claims.decode(accessToken)

    /** [AssuranceLevel.AAL2] once the user has completed a second step. */
    val assuranceLevel: AssuranceLevel get() = if (claims.string("aal") == "aal2") AssuranceLevel.AAL2 else AssuranceLevel.AAL1

    /** The organization this session acts in, from its token's org_* claims. */
    val activeOrganization: ActiveOrganization? get() = Claims.activeOrganization(claims)
}

public data class User(
    val id: String,
    val email: String?,
    val phone: String?,
    val isAnonymous: Boolean,
    val appMetadata: JsonObject?,
    val userMetadata: JsonObject?,
    val factors: List<Factor>,
) {
    internal companion object {
        fun from(json: JsonObject): User = User(
            id = json.string("id") ?: throw AuthError.invalidSession,
            email = json.string("email")?.ifEmpty { null },
            phone = json.string("phone")?.ifEmpty { null },
            isAnonymous = (json["is_anonymous"] as? JsonPrimitive)?.booleanOrNull ?: false,
            appMetadata = json["app_metadata"] as? JsonObject,
            userMetadata = json["user_metadata"] as? JsonObject,
            factors = (json["factors"] as? JsonArray).orEmpty().mapNotNull { (it as? JsonObject)?.let(Factor::from) },
        )
    }
}

/** A failed request. [code] is the stable server code, or `http_<status>`. */
public class AuthError(public val status: Int, public val code: String) : Exception(code) {
    override fun equals(other: Any?): Boolean = other is AuthError && other.status == status && other.code == code
    override fun hashCode(): Int = status * 31 + code.hashCode()
    override fun toString(): String = "AuthError($status, $code)"

    internal companion object {
        val notAuthenticated = AuthError(401, "not_authenticated")
        val invalidSession = AuthError(502, "invalid_session")
    }
}

public enum class AuthEvent { SIGNED_IN, TOKEN_REFRESHED, SIGNED_OUT, MFA_VERIFIED }

/** One change of the stored session, from [AuthClient.changes]. */
public data class AuthChange(val event: AuthEvent, val session: Session?)

public enum class AssuranceLevel { AAL1, AAL2 }

public data class AssuranceLevels(val current: AssuranceLevel?, val next: AssuranceLevel?)

/** A second factor: an authenticator app, a phone or recovery codes. */
public data class Factor(val id: String, val factorType: String, val status: String, val friendlyName: String?, val phone: String?) {
    internal companion object {
        fun from(json: JsonObject): Factor? {
            val id = json.string("id") ?: return null
            return Factor(id, json.string("factor_type") ?: "", json.string("status") ?: "", json.string("friendly_name"), json.string("phone"))
        }
    }
}

/** Show [secret] or the [qrCode] (an SVG document), then challenge and verify to activate it. */
public data class TotpEnrollment(val id: String, val secret: String, val uri: String, val qrCode: String)

public data class FactorChallenge(val id: String, val type: String?)

public data class Organization(val id: String, val slug: String, val name: String, val role: String?) {
    internal companion object {
        fun from(json: JsonObject): Organization =
            Organization(json.string("id") ?: "", json.string("slug") ?: "", json.string("name") ?: "", json.string("role"))
    }
}

public data class ActiveOrganization(val id: String, val slug: String, val role: String, val permissions: List<String>)

/** One of the signed-in user's own sessions; [current] is the one asking. */
public data class UserSession(val id: String, val createdAt: String, val lastActiveAt: String, val userAgent: String?, val ip: String?, val current: Boolean)

internal fun JsonObject.string(key: String): String? = (this[key] as? JsonPrimitive)?.takeIf { it.isString }?.contentOrNull

internal fun JsonObject.long(key: String): Long? = (this[key] as? JsonPrimitive)?.longOrNull

internal fun JsonObject.int(key: String): Int? = (this[key] as? JsonPrimitive)?.intOrNull

internal object Claims {
    /** Reads an access token's claims without verifying them: clients only decide what to show. */
    fun decode(token: String): JsonObject {
        val part = token.split('.').getOrNull(1) ?: return JsonObject(emptyMap())
        return try {
            Json.parseToJsonElement(String(Base64.getUrlDecoder().decode(part.trimEnd('=')), Charsets.UTF_8)) as? JsonObject ?: JsonObject(emptyMap())
        } catch (_: IllegalArgumentException) {
            JsonObject(emptyMap())
        }
    }

    fun activeOrganization(claims: JsonObject): ActiveOrganization? {
        val id = claims.string("org_id") ?: return null
        val slug = claims.string("org_slug") ?: return null
        val role = claims.string("org_role") ?: return null
        val permissions = (claims["org_permissions"] as? JsonArray).orEmpty().mapNotNull { (it as? JsonPrimitive)?.takeIf(JsonPrimitive::isString)?.content }
        return ActiveOrganization(id, slug, role, permissions)
    }
}

internal fun JsonElement?.asObject(): JsonObject = this as? JsonObject ?: JsonObject(emptyMap())

package dev.gregale.auth.android

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Base64
import dev.gregale.auth.SessionStore
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.security.GeneralSecurityException
import java.security.KeyStore
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

/**
 * Keeps the session in app-private SharedPreferences, sealed with AES-GCM
 * under a non-exportable Android Keystore key. A backup restored to another
 * device cannot be opened there and reads as signed out.
 *
 *     val auth = AuthClient(url, KeystoreSessionStore(context, url))
 */
public class KeystoreSessionStore(context: Context, private val authUrl: String) : SessionStore {
    private val preferences = context.applicationContext.getSharedPreferences(PREFERENCES, Context.MODE_PRIVATE)

    override suspend fun load(key: String): String? = withContext(Dispatchers.IO) {
        val sealed = preferences.getString(name(key), null) ?: return@withContext null
        try {
            open(sealed)
        } catch (_: GeneralSecurityException) {
            preferences.edit().remove(name(key)).apply()
            null
        } catch (_: IllegalArgumentException) {
            preferences.edit().remove(name(key)).apply()
            null
        }
    }

    override suspend fun save(key: String, value: String) {
        withContext(Dispatchers.IO) { preferences.edit().putString(name(key), seal(value)).commit() }
    }

    override suspend fun remove(key: String) {
        withContext(Dispatchers.IO) { preferences.edit().remove(name(key)).commit() }
    }

    private fun name(key: String) = "$authUrl#$key"

    private fun secretKey(): SecretKey {
        val keyStore = KeyStore.getInstance(KEYSTORE).apply { load(null) }
        (keyStore.getKey(ALIAS, null) as? SecretKey)?.let { return it }
        val generator = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, KEYSTORE)
        generator.init(
            KeyGenParameterSpec.Builder(ALIAS, KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                .setKeySize(256)
                .build(),
        )
        return generator.generateKey()
    }

    private fun seal(value: String): String {
        val cipher = Cipher.getInstance(TRANSFORMATION).apply { init(Cipher.ENCRYPT_MODE, secretKey()) }
        return Base64.encodeToString(cipher.iv + cipher.doFinal(value.toByteArray(Charsets.UTF_8)), Base64.NO_WRAP)
    }

    private fun open(sealed: String): String {
        val bytes = Base64.decode(sealed, Base64.NO_WRAP)
        val cipher = Cipher.getInstance(TRANSFORMATION).apply { init(Cipher.DECRYPT_MODE, secretKey(), GCMParameterSpec(128, bytes, 0, IV_BYTES)) }
        return String(cipher.doFinal(bytes, IV_BYTES, bytes.size - IV_BYTES), Charsets.UTF_8)
    }

    private companion object {
        const val PREFERENCES = "dev.gregale.auth"
        const val KEYSTORE = "AndroidKeyStore"
        const val ALIAS = "dev.gregale.auth.session"
        const val TRANSFORMATION = "AES/GCM/NoPadding"
        const val IV_BYTES = 12
    }
}

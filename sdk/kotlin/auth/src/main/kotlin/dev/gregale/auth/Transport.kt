package dev.gregale.auth

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.io.IOException
import java.net.HttpURLConnection
import java.net.URI
import java.util.concurrent.ConcurrentHashMap

public class HttpRequest(public val method: String, public val url: String, public val headers: Map<String, String>, public val body: String?)

public class HttpResponse(public val status: Int, public val body: String, public val headers: Map<String, String> = emptyMap())

/** Sends the client's requests; replace it to use OkHttp or to test. */
public fun interface Transport {
    public suspend fun send(request: HttpRequest): HttpResponse
}

/** HttpURLConnection on the IO dispatcher: on the JVM and Android without dependencies. Redirects are not followed. */
public class UrlConnectionTransport(private val timeoutMillis: Int = 30_000) : Transport {
    override suspend fun send(request: HttpRequest): HttpResponse = withContext(Dispatchers.IO) {
        val connection = URI(request.url).toURL().openConnection() as HttpURLConnection
        try {
            connection.requestMethod = request.method
            connection.instanceFollowRedirects = false
            connection.connectTimeout = timeoutMillis
            connection.readTimeout = timeoutMillis
            for ((name, value) in request.headers) connection.setRequestProperty(name, value)
            if (request.body != null) {
                connection.doOutput = true
                connection.outputStream.use { it.write(request.body.toByteArray(Charsets.UTF_8)) }
            }
            val status = connection.responseCode
            val stream = try {
                if (status >= 400) connection.errorStream else connection.inputStream
            } catch (_: IOException) {
                null
            }
            val body = stream?.use { String(it.readBytes(), Charsets.UTF_8) } ?: ""
            val headers = connection.headerFields.filterKeys { it != null }.mapValues { it.value.joinToString(", ") }
            HttpResponse(status, body, headers)
        } finally {
            connection.disconnect()
        }
    }
}

/** Where the client keeps the session (key "session") and a pending PKCE verifier (key "pkce"). */
public interface SessionStore {
    public suspend fun load(key: String): String?
    public suspend fun save(key: String, value: String)
    public suspend fun remove(key: String)
}

/** Keeps everything in memory: signed out on every launch. */
public class MemorySessionStore : SessionStore {
    private val values = ConcurrentHashMap<String, String>()
    override suspend fun load(key: String): String? = values[key]
    override suspend fun save(key: String, value: String) { values[key] = value }
    override suspend fun remove(key: String) { values.remove(key) }
}

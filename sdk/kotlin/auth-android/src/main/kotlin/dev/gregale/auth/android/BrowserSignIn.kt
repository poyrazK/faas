package dev.gregale.auth.android

import android.app.Activity
import android.content.Context
import android.content.Intent
import android.net.Uri
import dev.gregale.auth.AuthClient
import dev.gregale.auth.Session

/**
 * Opens OAuth with [provider], or the hosted sign-in page without one
 * (passkeys, SSO and CAPTCHA included), in the browser. [redirectTo] is an
 * HTTPS App Link of your app that the Auth app allows; the activity it opens
 * finishes with [completeBrowserSignIn]. To use a Custom Tab instead, open
 * [AuthClient.oauthUrl] or [AuthClient.hostedSignInUrl] yourself.
 */
public suspend fun AuthClient.startBrowserSignIn(context: Context, redirectTo: String, provider: String? = null) {
    val url = if (provider != null) oauthUrl(provider, redirectTo) else hostedSignInUrl(redirectTo)
    val intent = Intent(Intent.ACTION_VIEW, Uri.parse(url)).addCategory(Intent.CATEGORY_BROWSABLE)
    if (context !is Activity) intent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
    context.startActivity(intent)
}

/** Completes a browser sign-in or an emailed link from the App Link intent; null when it carries no session. */
public suspend fun AuthClient.completeBrowserSignIn(intent: Intent): Session? {
    val data = intent.data ?: return null
    if (intent.action != Intent.ACTION_VIEW) return null
    return sessionFromUrl(data.toString())
}

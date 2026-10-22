#if canImport(AuthenticationServices) && (os(iOS) || os(macOS))
import AuthenticationServices

@available(iOS 17.4, macOS 14.4, *)
extension AuthClient {
    /// Signs in through the system browser sheet: OAuth with provider, or the
    /// hosted sign-in page without one (passwords, links, passkeys,
    /// organization SSO and CAPTCHA as the Auth app has them on).
    ///
    /// callback is an HTTPS universal link of your app (an associated
    /// domain) that the Auth app allows as a redirect URL. The sheet returns
    /// it to the app without loading it; an OAuth code is exchanged with this
    /// device's PKCE verifier.
    @MainActor @discardableResult
    public func signInWithBrowser(provider: String? = nil, callback: URL, anchor: ASPresentationAnchor? = nil, ephemeral: Bool = false) async throws -> Session {
        guard let host = callback.host, callback.scheme == "https" else { throw AuthError(status: 400, code: "invalid_callback") }
        let start: URL
        if let provider {
            start = try await oauthURL(provider: provider, redirectTo: callback)
        } else {
            start = hostedSignInURL(redirectTo: callback)
        }
        let context = PresentationContext(anchor: anchor ?? ASPresentationAnchor())
        let returned: URL = try await withCheckedThrowingContinuation { continuation in
            let session = ASWebAuthenticationSession(url: start, callback: .https(host: host, path: callback.path)) { url, error in
                // The context is held weakly by the session; keep it until the end.
                withExtendedLifetime(context) {
                    if let url {
                        continuation.resume(returning: url)
                    } else if let error = error as? ASWebAuthenticationSessionError, error.code == .canceledLogin {
                        continuation.resume(throwing: AuthError(status: 499, code: "web_authentication_cancelled"))
                    } else {
                        continuation.resume(throwing: error ?? AuthError(status: 500, code: "web_authentication_failed"))
                    }
                }
            }
            session.presentationContextProvider = context
            session.prefersEphemeralWebBrowserSession = ephemeral
            if !session.start() { continuation.resume(throwing: AuthError(status: 500, code: "web_authentication_failed")) }
        }
        guard let session = try await session(from: returned) else { throw AuthError(status: 400, code: "no_session_returned") }
        return session
    }
}

@MainActor
private final class PresentationContext: NSObject, ASWebAuthenticationPresentationContextProviding {
    let anchor: ASPresentationAnchor

    init(anchor: ASPresentationAnchor) {
        self.anchor = anchor
    }

    nonisolated func presentationAnchor(for session: ASWebAuthenticationSession) -> ASPresentationAnchor {
        MainActor.assumeIsolated { anchor }
    }
}
#endif

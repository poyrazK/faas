import CryptoKit
import Foundation

/// Client for a Gregale application Auth app (ADR-1190): sign-up and sign-in,
/// sessions kept in the Keychain with single-flight refresh, second factors,
/// organizations and authorization checks.
///
///     let auth = try AuthClient(url: URL(string: "https://shop-auth.gregale.app")!)
///     let session = try await auth.signIn(email: email, password: password)
///     for await change in await auth.changes() { … }
public actor AuthClient {
    /// The Auth app's origin.
    public nonisolated let url: URL
    private let http: URLSession
    private let store: SessionStore
    private let refreshMargin: TimeInterval
    private var refreshing: Task<Session?, Error>?
    private var listeners: [UUID: AsyncStream<AuthChange>.Continuation] = [:]

    private static let sessionKey = "session", verifierKey = "pkce"

    /// - Parameters:
    ///   - url: The Auth app's origin: HTTPS, or HTTP on a loopback host.
    ///   - store: Defaults to the Keychain.
    ///   - refreshMargin: Refresh this long before the access token expires.
    public init(url: URL, store: SessionStore? = nil, urlSession: URLSession = .shared, refreshMargin: TimeInterval = 60) throws {
        let origin = try Self.origin(url)
        self.url = origin
        http = urlSession
        self.refreshMargin = refreshMargin
        #if canImport(Security)
        self.store = store ?? KeychainSessionStore(account: origin.absoluteString)
        #else
        self.store = store ?? MemorySessionStore()
        #endif
    }

    static func origin(_ url: URL) throws -> URL {
        let invalid = AuthError(status: 400, code: "invalid_auth_url")
        guard let parts = URLComponents(url: url, resolvingAgainstBaseURL: false), let scheme = parts.scheme?.lowercased(), let host = parts.host, !host.isEmpty,
              parts.user == nil, parts.password == nil, parts.query == nil, parts.fragment == nil, parts.path.isEmpty || parts.path == "/" else { throw invalid }
        let loopback = ["localhost", "127.0.0.1", "::1"].contains(host)
        guard scheme == "https" || (scheme == "http" && loopback) else { throw invalid }
        var origin = URLComponents()
        origin.scheme = scheme
        origin.host = host
        origin.port = parts.port
        guard let value = origin.url else { throw invalid }
        return value
    }

    // MARK: Sessions

    /// Each change of the stored session: sign-in, refresh, second step and sign-out.
    public func changes() -> AsyncStream<AuthChange> {
        let id = UUID()
        var continuation: AsyncStream<AuthChange>.Continuation?
        let stream = AsyncStream<AuthChange> { continuation = $0 }
        listeners[id] = continuation
        continuation?.onTermination = { [weak self] _ in Task { await self?.removeListener(id) } }
        return stream
    }

    private func removeListener(_ id: UUID) {
        listeners[id] = nil
    }

    private func emit(_ event: AuthEvent, _ session: Session?) {
        for listener in listeners.values { listener.yield(AuthChange(event: event, session: session)) }
    }

    private static let encoder: JSONEncoder = {
        let encoder = JSONEncoder()
        encoder.dateEncodingStrategy = .secondsSince1970
        return encoder
    }()

    private static let decoder: JSONDecoder = {
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .secondsSince1970
        return decoder
    }()

    private func save(_ session: Session, _ event: AuthEvent) async throws -> Session {
        try await store.save(Self.encoder.encode(session), for: Self.sessionKey)
        emit(event, session)
        return session
    }

    private func clear() async throws {
        try await store.remove(Self.sessionKey)
        emit(.signedOut, nil)
    }

    private func storedSession() async throws -> Session? {
        guard let data = try await store.load(Self.sessionKey) else { return nil }
        return try? Self.decoder.decode(Session.self, from: data)
    }

    /// The stored session, refreshed first when its access token is about to expire.
    public func currentSession() async throws -> Session? {
        guard let session = try await storedSession() else { return nil }
        return session.expiresAt.timeIntervalSinceNow <= refreshMargin ? try await refreshSession() : session
    }

    /// Refreshes the session. One refresh runs at a time: refresh tokens
    /// rotate, so two refreshes with one token would revoke the session. A
    /// refresh token the Auth app rejects signs the user out.
    @discardableResult
    public func refreshSession() async throws -> Session? {
        if let refreshing { return try await refreshing.value }
        let task = Task { try await self.performRefresh() }
        refreshing = task
        defer { refreshing = nil }
        return try await task.value
    }

    private func performRefresh() async throws -> Session? {
        guard let current = try await storedSession() else { return nil }
        do {
            let data = try await call("POST", "/auth/v1/token", query: [("grant_type", "refresh_token")], body: ["refresh_token": .string(current.refreshToken)])
            return try await save(Self.parseSession(data), .tokenRefreshed)
        } catch let error as AuthError where (400..<500).contains(error.status) {
            try await clear()
            throw error
        }
    }

    /// A valid access token for other clients and your own API calls.
    public func accessToken() async throws -> String {
        guard let session = try await currentSession() else { throw AuthError.notAuthenticated }
        return session.accessToken
    }

    // MARK: Sign-up and sign-in

    public struct SignUpResult: Sendable {
        /// Nil while the email address awaits confirmation.
        public let session: Session?
        public let user: User?
    }

    /// Creates an account. Without autoconfirm the user must confirm their
    /// address first: with the emailed code (verifyOTP with type .signup) or
    /// the link, which returns to redirectTo for session(from:).
    public func signUp(email: String, password: String, data: [String: JSONValue]? = nil, redirectTo: URL? = nil, captchaToken: String? = nil) async throws -> SignUpResult {
        var body: [String: JSONValue] = ["email": .string(email), "password": .string(password)]
        if let data { body["data"] = .object(data) }
        if redirectTo != nil { body.merge(try await pkceFields()) { $1 } }
        let response = try await call("POST", "/auth/v1/signup", query: redirect(redirectTo), body: .object(body.merging(captcha(captchaToken)) { $1 }))
        if let session = try? Self.parseSession(response) {
            return SignUpResult(session: try await save(session, .signedIn), user: session.user)
        }
        return SignUpResult(session: nil, user: try? Self.decoder.decode(User.self, from: response))
    }

    @discardableResult
    public func signIn(email: String, password: String, captchaToken: String? = nil) async throws -> Session {
        let data = try await call("POST", "/auth/v1/token", query: [("grant_type", "password")], body: .object(["email": .string(email), "password": .string(password)].merging(captcha(captchaToken)) { $1 }))
        return try await save(Self.parseSession(data), .signedIn)
    }

    /// Emails a sign-in code (and a link that returns to redirectTo). Finish
    /// with verifyOTP(email:code:). createUser lets a new address sign up.
    public func signInWithOTP(email: String, createUser: Bool = false, redirectTo: URL? = nil, captchaToken: String? = nil) async throws {
        var body: [String: JSONValue] = ["email": .string(email), "create_user": .bool(createUser)]
        if redirectTo != nil { body.merge(try await pkceFields()) { $1 } }
        _ = try await call("POST", "/auth/v1/otp", query: redirect(redirectTo), body: .object(body.merging(captcha(captchaToken)) { $1 }))
    }

    /// Texts a code to phone, with its country code (+447700900123).
    public func signInWithOTP(phone: String, createUser: Bool = false, captchaToken: String? = nil) async throws {
        _ = try await call("POST", "/auth/v1/otp", body: .object(["phone": .string(phone), "channel": "sms", "create_user": .bool(createUser)].merging(captcha(captchaToken)) { $1 }))
    }

    public enum EmailOTPType: String, Sendable {
        case email, signup, recovery, invite, emailChange = "email_change"
    }

    /// Completes an emailed code: a sign-in code (.email), a sign-up
    /// confirmation, a password recovery or an email change.
    @discardableResult
    public func verifyOTP(email: String, code: String, type: EmailOTPType = .email) async throws -> Session {
        let data = try await call("POST", "/auth/v1/verify", body: ["type": .string(type.rawValue), "email": .string(email), "token": .string(code.trimmingCharacters(in: .whitespaces))])
        return try await save(Self.parseSession(data), .signedIn)
    }

    @discardableResult
    public func verifyOTP(phone: String, code: String) async throws -> Session {
        let data = try await call("POST", "/auth/v1/verify", body: ["type": "sms", "phone": .string(phone), "token": .string(code.trimmingCharacters(in: .whitespaces))])
        return try await save(Self.parseSession(data), .signedIn)
    }

    /// Starts a session without an account when the Auth app allows anonymous
    /// users; updateUser(email:password:) makes it permanent.
    @discardableResult
    public func signInAnonymously(captchaToken: String? = nil) async throws -> Session {
        let data = try await call("POST", "/auth/v1/signup", body: .object(captcha(captchaToken)))
        return try await save(Self.parseSession(data), .signedIn)
    }

    /// Signs in with an ID token from the platform's own sign-in, such as
    /// Sign in with Apple (provider "apple", with the raw nonce you hashed into
    /// the request) or Google. The provider must be on in the Auth app with
    /// your app's client ID.
    @discardableResult
    public func signInWithIdToken(provider: String, idToken: String, nonce: String? = nil, accessToken: String? = nil) async throws -> Session {
        var body: [String: JSONValue] = ["provider": .string(provider), "id_token": .string(idToken)]
        if let nonce { body["nonce"] = .string(nonce) }
        if let accessToken { body["access_token"] = .string(accessToken) }
        let data = try await call("POST", "/auth/v1/token", query: [("grant_type", "id_token")], body: .object(body))
        return try await save(Self.parseSession(data), .signedIn)
    }

    /// The URL that starts OAuth with provider and returns to redirectTo with
    /// a one-time code (PKCE); open it in ASWebAuthenticationSession or let
    /// signInWithBrowser do it, then pass the return URL to session(from:).
    /// redirectTo must be the site URL or under an allowed redirect URL: an
    /// HTTPS universal link or App Link.
    public func oauthURL(provider: String, redirectTo: URL, scopes: [String] = []) async throws -> URL {
        var query = [("provider", provider), ("redirect_to", redirectTo.absoluteString), ("code_challenge", try await pkceChallenge()), ("code_challenge_method", "s256")]
        if !scopes.isEmpty { query.append(("scopes", scopes.joined(separator: " "))) }
        return Self.url(url, "/auth/v1/authorize", query)
    }

    /// The hosted sign-in page (passwords, links, OAuth, passkeys, SSO and
    /// CAPTCHA as the Auth app has them on), which returns to redirectTo with
    /// the session in the URL fragment.
    public nonisolated func hostedSignInURL(redirectTo: URL? = nil) -> URL {
        Self.url(url, "/ui/sign-in", redirectTo.map { [("redirect_to", $0.absoluteString)] } ?? [])
    }

    /// The hosted account page: email address, password, second factors, passkeys and sessions.
    public nonisolated func hostedAccountURL(redirectTo: URL? = nil) -> URL {
        Self.url(url, "/ui/account", redirectTo.map { [("redirect_to", $0.absoluteString)] } ?? [])
    }

    /// Completes a return from the hosted pages, an emailed link or OAuth:
    /// a session in the fragment, or a one-time code exchanged with this
    /// device's PKCE verifier. Nil when the URL carries neither; a returned
    /// error throws.
    @discardableResult
    public func session(from returnURL: URL) async throws -> Session? {
        let parts = URLComponents(url: returnURL, resolvingAgainstBaseURL: false)
        var params: [String: String] = [:]
        for item in parts?.queryItems ?? [] { params[item.name] = item.value ?? "" }
        if let fragment = parts?.percentEncodedFragment {
            var fragmentParts = URLComponents()
            fragmentParts.percentEncodedQuery = fragment
            for item in fragmentParts.queryItems ?? [] { params[item.name] = item.value ?? "" }
        }
        if let code = params["error_code"] ?? params["error"] {
            let status = Int(params["status"] ?? params["code"] ?? "") ?? 400
            throw AuthError(status: status, code: code.range(of: "^[a-z0-9_]{1,64}$", options: .regularExpression) != nil ? code : "http_\(status)")
        }
        if let access = params["access_token"], let refresh = params["refresh_token"] {
            let expiresAt = Double(params["expires_at"] ?? "").map { Date(timeIntervalSince1970: $0) } ?? Date().addingTimeInterval(Double(params["expires_in"] ?? "") ?? 0)
            return try await save(Session(accessToken: access, refreshToken: refresh, expiresAt: expiresAt), .signedIn)
        }
        if let code = params["code"] { return try await exchangeCode(code) }
        return nil
    }

    /// Exchanges a one-time code from a PKCE return for a session.
    @discardableResult
    public func exchangeCode(_ code: String) async throws -> Session {
        guard let verifier = try await store.load(Self.verifierKey).flatMap({ String(data: $0, encoding: .utf8) }) else { throw AuthError(status: 400, code: "pkce_verifier_missing") }
        let data = try await call("POST", "/auth/v1/token", query: [("grant_type", "pkce")], body: ["auth_code": .string(code), "code_verifier": .string(verifier)])
        try await store.remove(Self.verifierKey)
        return try await save(Self.parseSession(data), .signedIn)
    }

    // A fresh verifier per flow, kept in the store so a return after the app
    // was suspended or relaunched still completes.
    private func pkceChallenge() async throws -> String {
        var generator = SystemRandomNumberGenerator()
        let verifier = Base64URL.encode(Data((0..<32).map { _ in UInt8.random(in: .min ... .max, using: &generator) }))
        try await store.save(Data(verifier.utf8), for: Self.verifierKey)
        return Base64URL.encode(Data(SHA256.hash(data: Data(verifier.utf8))))
    }

    private func pkceFields() async throws -> [String: JSONValue] {
        ["code_challenge": .string(try await pkceChallenge()), "code_challenge_method": "s256"]
    }

    // MARK: The signed-in user

    public func user() async throws -> User {
        try Self.decoder.decode(User.self, from: try await call("GET", "/auth/v1/user", token: try await accessToken()))
    }

    /// Changes the email address (confirmed by email), the password or user metadata.
    public func updateUser(email: String? = nil, password: String? = nil, data: [String: JSONValue]? = nil) async throws -> User {
        var body: [String: JSONValue] = [:]
        if let email { body["email"] = .string(email) }
        if let password { body["password"] = .string(password) }
        if let data { body["data"] = .object(data) }
        return try Self.decoder.decode(User.self, from: try await call("PUT", "/auth/v1/user", body: .object(body), token: try await accessToken()))
    }

    /// Emails a recovery code and link; verifyOTP(email:code:type: .recovery)
    /// signs the user in to choose a new password with updateUser(password:).
    public func resetPassword(email: String, redirectTo: URL? = nil, captchaToken: String? = nil) async throws {
        let target = redirectTo ?? Self.url(url, "/ui/reset-password", [])
        _ = try await call("POST", "/auth/v1/recover", query: redirect(target), body: .object(["email": .string(email)].merging(captcha(captchaToken)) { $1 }))
    }

    public enum SignOutScope: String, Sendable {
        /// This session.
        case local
        /// Every session of the user.
        case global
        /// Every session but this one; this one stays signed in.
        case others
    }

    /// Clears the stored session even when the Auth app cannot be reached
    /// (except for .others), then rethrows that failure.
    public func signOut(scope: SignOutScope = .local) async throws {
        if let session = try await storedSession() {
            do {
                _ = try await call("POST", "/auth/v1/logout", query: [("scope", scope.rawValue)], token: session.accessToken)
            } catch let error as AuthError where error.status < 500 {
                // Already signed out on the server.
            } catch {
                if scope != .others { try await clear() }
                throw error
            }
        }
        if scope != .others { try await clear() }
    }

    /// The signed-in user's own sessions, most recently active first.
    public func sessions() async throws -> [UserSession] {
        struct List: Decodable { let sessions: [UserSession] }
        return try Self.decoder.decode(List.self, from: try await authz("GET", "/sessions")).sessions
    }

    /// Ends one of the user's sessions; end this one with signOut().
    public func revokeSession(_ id: String) async throws {
        _ = try await authz("DELETE", "/sessions/\(Self.escape(id))")
    }

    /// Deletes the account when the Auth app allows it. The session must come
    /// from a sign-in in the last ten minutes, else this throws
    /// reauthentication_required. Returns how many organizations went with it.
    @discardableResult
    public func deleteAccount() async throws -> Int {
        struct Deleted: Decodable { let organizations_deleted: Int }
        let deleted = try Self.decoder.decode(Deleted.self, from: try await authz("DELETE", "/account"))
        try await clear()
        return deleted.organizations_deleted
    }

    /// Whether the signed-in user may do action on resource; see resource(org:kind:name:).
    public func authorize(action: String, resource: String) async throws -> Bool {
        struct Answer: Decodable { let allowed: Bool }
        return try Self.decoder.decode(Answer.self, from: try await authz("POST", "/authorize", body: ["action": .string(action), "resource": .string(resource)])).allowed
    }

    /// The resource name authorize() and gregale_auth.authorize() expect.
    public static func resource(org: String, kind: String, name: String) -> String {
        "org/\(org)/\(kind)/\(name)"
    }

    // MARK: Second factors

    /// Second factors. A user who enrolled one signs in at aal1 and reaches
    /// aal2 by verifying a challenge or a recovery code.
    public nonisolated var mfa: MFA { MFA(client: self) }

    public struct MFA: Sendable {
        let client: AuthClient

        public func listFactors() async throws -> [Factor] { try await client.user().factors ?? [] }

        /// Starts enrolling an authenticator app; show totp.secret or the QR
        /// code, then challenge and verify to activate it.
        public func enrollTOTP(friendlyName: String? = nil) async throws -> TOTPEnrollment {
            var body: [String: JSONValue] = ["factor_type": "totp"]
            if let friendlyName { body["friendly_name"] = .string(friendlyName) }
            return try AuthClient.decoder.decode(TOTPEnrollment.self, from: try await client.authed("POST", "/auth/v1/factors", body: .object(body)))
        }

        public func enrollPhone(_ phone: String, friendlyName: String? = nil) async throws -> Factor {
            var body: [String: JSONValue] = ["factor_type": "phone", "phone": .string(phone)]
            if let friendlyName { body["friendly_name"] = .string(friendlyName) }
            return try AuthClient.decoder.decode(Factor.self, from: try await client.authed("POST", "/auth/v1/factors", body: .object(body)))
        }

        /// Creates a challenge; for a phone factor this texts the code.
        public func challenge(factorId: String) async throws -> FactorChallenge {
            try AuthClient.decoder.decode(FactorChallenge.self, from: try await client.authed("POST", "/auth/v1/factors/\(AuthClient.escape(factorId))/challenge", body: [:]))
        }

        /// Verifies a challenge; the stored session becomes aal2.
        @discardableResult
        public func verify(factorId: String, challengeId: String, code: String) async throws -> Session {
            try await client.saveVerified(try await client.authed("POST", "/auth/v1/factors/\(AuthClient.escape(factorId))/verify", body: ["challenge_id": .string(challengeId), "code": .string(code.trimmingCharacters(in: .whitespaces))]))
        }

        /// Uses one recovery code in place of a second factor.
        @discardableResult
        public func verifyRecoveryCode(_ code: String) async throws -> Session {
            try await client.saveVerified(try await client.authed("POST", "/auth/v1/factors/recovery-codes/verify", body: ["code": .string(code.trimmingCharacters(in: .whitespaces))]))
        }

        public func unenroll(factorId: String) async throws {
            _ = try await client.authed("DELETE", "/auth/v1/factors/\(AuthClient.escape(factorId))")
        }

        /// The session's level, and aal2 as next when the user has a verified
        /// authenticator app or phone to complete.
        public func assuranceLevel() async throws -> (current: AssuranceLevel?, next: AssuranceLevel?) {
            guard let session = try await client.currentSession() else { return (nil, nil) }
            let factors = try await listFactors().filter { $0.status == "verified" && ($0.factorType == "totp" || $0.factorType == "phone") }
            return (session.assuranceLevel, factors.isEmpty ? session.assuranceLevel : .aal2)
        }
    }

    fileprivate func authed(_ method: String, _ path: String, body: JSONValue? = nil) async throws -> Data {
        try await call(method, path, body: body, token: try await accessToken())
    }

    fileprivate func saveVerified(_ data: Data) async throws -> Session {
        try await save(Self.parseSession(data), .mfaVerified)
    }

    // MARK: Organizations

    public nonisolated var organizations: Organizations { Organizations(client: self) }

    public struct Organizations: Sendable {
        let client: AuthClient

        public func list() async throws -> [Organization] {
            struct List: Decodable { let organizations: [Organization] }
            return try AuthClient.decoder.decode(List.self, from: try await client.authz("GET", "/orgs")).organizations
        }

        public func create(slug: String, name: String) async throws -> Organization {
            try AuthClient.decoder.decode(Organization.self, from: try await client.authz("POST", "/orgs", body: ["slug": .string(slug), "name": .string(name)]))
        }

        /// Makes org (one the user belongs to) the session's active
        /// organization, or none with nil, and refreshes the session so its
        /// token carries it. Needs organization claims on in the Auth app.
        @discardableResult
        public func setActive(_ org: String?) async throws -> ActiveOrganization? {
            _ = try await client.authz("POST", "/active-organization", body: ["org": org.map(JSONValue.string) ?? .null])
            return try await client.refreshSession()?.activeOrganization
        }

        /// The session's active organization, read from its token.
        public func active() async throws -> ActiveOrganization? {
            try await client.currentSession()?.activeOrganization
        }
    }

    // MARK: HTTP

    fileprivate func authz(_ method: String, _ path: String, body: JSONValue? = nil) async throws -> Data {
        try await call(method, "/authz/v1\(path)", body: body, token: try await accessToken())
    }

    private func call(_ method: String, _ path: String, query: [(String, String)] = [], body: JSONValue? = nil, token: String? = nil) async throws -> Data {
        var request = URLRequest(url: Self.url(url, path, query))
        request.httpMethod = method
        if let body {
            request.setValue("application/json", forHTTPHeaderField: "Content-Type")
            request.httpBody = try Self.encoder.encode(body)
        }
        if let token { request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization") }
        let (data, response) = try await http.data(for: request)
        let status = (response as? HTTPURLResponse)?.statusCode ?? 0
        guard (200..<300).contains(status) else { throw Self.error(status: status, data: data) }
        return data
    }

    private func redirect(_ target: URL?) -> [(String, String)] {
        target.map { [("redirect_to", $0.absoluteString)] } ?? []
    }

    private func captcha(_ token: String?) -> [String: JSONValue] {
        guard let token else { return [:] }
        return ["gotrue_meta_security": ["captcha_token": .string(token)]]
    }

    static func error(status: Int, data: Data) -> AuthError {
        if case .object(let body)? = try? JSONDecoder().decode(JSONValue.self, from: data),
           let code = body["error_code"]?.stringValue ?? body["code"]?.stringValue ?? body["error"]?.stringValue,
           code.range(of: "^[a-z0-9_]{1,64}$", options: .regularExpression) != nil {
            return AuthError(status: status, code: code)
        }
        return AuthError(status: status, code: "http_\(status)")
    }

    static func parseSession(_ data: Data) throws -> Session {
        struct Tokens: Decodable {
            let access_token: String
            let refresh_token: String
            let expires_in: Double?
            let expires_at: Double?
        }
        struct WithUser: Decodable { let user: User? }
        guard let tokens = try? JSONDecoder().decode(Tokens.self, from: data) else { throw AuthError.invalidSession }
        guard let expiresAt = tokens.expires_at.map({ Date(timeIntervalSince1970: $0) }) ?? tokens.expires_in.map({ Date().addingTimeInterval($0) }) else { throw AuthError.invalidSession }
        // A user object the SDK cannot read must not lose the session.
        let user = (try? JSONDecoder().decode(WithUser.self, from: data))?.user
        return Session(accessToken: tokens.access_token, refreshToken: tokens.refresh_token, expiresAt: expiresAt, user: user)
    }

    private static let unreserved = CharacterSet(charactersIn: "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~")

    static func escape(_ value: String) -> String {
        value.addingPercentEncoding(withAllowedCharacters: unreserved) ?? value
    }

    // URLComponents leaves & = + unescaped in query values; build it by hand.
    static func url(_ origin: URL, _ path: String, _ query: [(String, String)]) -> URL {
        var value = origin.absoluteString.hasSuffix("/") ? String(origin.absoluteString.dropLast()) + path : origin.absoluteString + path
        if !query.isEmpty { value += "?" + query.map { "\(escape($0.0))=\(escape($0.1))" }.joined(separator: "&") }
        return URL(string: value)!
    }
}

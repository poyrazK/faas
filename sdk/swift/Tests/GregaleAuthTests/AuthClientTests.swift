import CryptoKit
import Foundation
import XCTest
@testable import GregaleAuth

/// Routes requests to handlers by "METHOD /path" and records them.
final class Stub: URLProtocol, @unchecked Sendable {
    struct Call {
        let key: String
        let query: [String: String]
        let headers: [String: String]
        let body: [String: JSONValue]
    }

    typealias Handler = @Sendable (Call) -> (Int, JSONValue?)
    private static let lock = NSLock()
    nonisolated(unsafe) private static var routes: [String: Handler] = [:]
    nonisolated(unsafe) private static var recorded: [Call] = []

    static func reset(_ routes: [String: Handler]) {
        lock.withLock { self.routes = routes; recorded = [] }
    }

    static var calls: [Call] { lock.withLock { recorded } }
    static func count(_ key: String) -> Int { calls.filter { $0.key == key }.count }

    static func session() -> URLSession {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [Stub.self]
        return URLSession(configuration: configuration)
    }

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func stopLoading() {}

    override func startLoading() {
        let url = request.url!
        var query: [String: String] = [:]
        for item in URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems ?? [] { query[item.name] = item.value }
        var body: [String: JSONValue] = [:]
        if let data = request.httpBody ?? request.httpBodyStream.map(Self.read), case .object(let object)? = try? JSONDecoder().decode(JSONValue.self, from: data) { body = object }
        let call = Call(key: "\(request.httpMethod ?? "GET") \(url.path)", query: query, headers: request.allHTTPHeaderFields ?? [:], body: body)
        let handler = Self.lock.withLock { () -> Handler? in
            Self.recorded.append(call)
            return Self.routes[call.key]
        }
        let (status, json) = handler?(call) ?? (404, ["code": "not_found"])
        let response = HTTPURLResponse(url: url, statusCode: status, httpVersion: "HTTP/1.1", headerFields: ["Content-Type": "application/json"])!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        if let json { client?.urlProtocol(self, didLoad: try! JSONEncoder().encode(json)) }
        client?.urlProtocolDidFinishLoading(self)
    }

    private static func read(_ stream: InputStream) -> Data {
        stream.open()
        defer { stream.close() }
        var data = Data()
        var buffer = [UInt8](repeating: 0, count: 4096)
        while stream.hasBytesAvailable {
            let n = stream.read(&buffer, maxLength: buffer.count)
            if n <= 0 { break }
            data.append(buffer, count: n)
        }
        return data
    }
}

func jwt(_ claims: [String: JSONValue]) -> String {
    "eyJhbGciOiJFUzI1NiJ9.\(Base64URL.encode(try! JSONEncoder().encode(JSONValue.object(claims)))).sig"
}

func tokens(_ n: Int, _ claims: [String: JSONValue] = ["sub": "u1"], expiresIn: Double = 600, user: Bool = true) -> JSONValue {
    var body: [String: JSONValue] = ["access_token": .string(jwt(claims)), "refresh_token": .string("refresh-\(n)"), "expires_in": .number(expiresIn)]
    if user { body["user"] = ["id": "u1", "email": "a@example.test"] }
    return .object(body)
}

final class AuthClientTests: XCTestCase {
    let origin = URL(string: "https://shop-auth.gregale.app")!

    func client(_ routes: [String: Stub.Handler], store: SessionStore = MemorySessionStore()) throws -> AuthClient {
        Stub.reset(routes)
        return try AuthClient(url: origin, store: store, urlSession: Stub.session())
    }

    func testRejectsUnsafeAuthURLs() {
        for value in ["http://shop-auth.gregale.app", "https://user:pw@shop-auth.gregale.app", "https://shop-auth.gregale.app/?x=1", "https://shop-auth.gregale.app/#x", "https://shop-auth.gregale.app/auth"] {
            XCTAssertThrowsError(try AuthClient(url: URL(string: value)!, store: MemorySessionStore()), value)
        }
        XCTAssertEqual(try AuthClient(url: URL(string: "http://localhost:8080/")!, store: MemorySessionStore()).url.absoluteString, "http://localhost:8080")
        XCTAssertEqual(try AuthClient(url: origin, store: MemorySessionStore()).url.absoluteString, "https://shop-auth.gregale.app")
    }

    func testSignsInStoresTheSessionAndAuthenticatesCalls() async throws {
        let auth = try client([
            "POST /auth/v1/token": { call in call.body["password"] == "right" ? (200, tokens(1)) : (400, ["error_code": "invalid_credentials", "code": .number(400)]) },
            "GET /authz/v1/orgs": { _ in (200, ["organizations": [["id": "o1", "slug": "acme", "name": "Acme", "role": "owner"]]]) },
            "POST /authz/v1/authorize": { call in (200, ["allowed": .bool(call.body["action"] == "read")]) },
        ])
        let changes = await auth.changes()
        do {
            _ = try await auth.signIn(email: "a@example.test", password: "wrong")
            XCTFail("wrong password accepted")
        } catch let error as AuthError {
            XCTAssertEqual(error, AuthError(status: 400, code: "invalid_credentials"))
        }
        let session = try await auth.signIn(email: "a@example.test", password: "right")
        XCTAssertEqual(session.refreshToken, "refresh-1")
        XCTAssertEqual(session.user?.email, "a@example.test")
        XCTAssertEqual(Stub.calls.last?.query["grant_type"], "password")
        var iterator = changes.makeAsyncIterator()
        let change = await iterator.next()
        XCTAssertEqual(change?.event, .signedIn)

        let orgs = try await auth.organizations.list()
        XCTAssertEqual(orgs.first?.slug, "acme")
        XCTAssertEqual(Stub.calls.last?.headers["Authorization"], "Bearer \(session.accessToken)")
        let canRead = try await auth.authorize(action: "read", resource: AuthClient.resource(org: "o1", kind: "doc", name: "42"))
        let canWrite = try await auth.authorize(action: "write", resource: AuthClient.resource(org: "o1", kind: "doc", name: "42"))
        XCTAssertTrue(canRead)
        XCTAssertFalse(canWrite)
        XCTAssertEqual(Stub.calls.last?.body["resource"], "org/o1/doc/42")
    }

    func testRefreshesOnceForConcurrentCallersAndSignsOutOnRejection() async throws {
        let store: SessionStore = MemorySessionStore()
        let auth = try client([
            "POST /auth/v1/token": { call in call.body["refresh_token"] == "refresh-0" ? (200, tokens(1)) : (400, ["error_code": "refresh_token_not_found"]) },
        ], store: store)
        // An access token about to expire.
        try await store.save(JSONEncoder().encode(["accessToken": jwt(["sub": "u1"]), "refreshToken": "refresh-0", "expiresAt": "\(Date().timeIntervalSince1970 + 5)"].mapValues { $0 }) .replacingExpiresAt(), for: "session")
        async let first = auth.accessToken()
        async let second = auth.accessToken()
        let tokensSeen = try await [first, second]
        XCTAssertEqual(Set(tokensSeen).count, 1)
        XCTAssertEqual(Stub.count("POST /auth/v1/token"), 1)
        let refreshed = try await auth.currentSession()
        XCTAssertEqual(refreshed?.refreshToken, "refresh-1")

        // The server no longer knows refresh-1: the user is signed out.
        let changes = await auth.changes()
        do {
            try await auth.refreshSession()
            XCTFail("rejected refresh succeeded")
        } catch let error as AuthError {
            XCTAssertEqual(error.code, "refresh_token_not_found")
        }
        let stored = try await store.load("session")
        XCTAssertNil(stored)
        var iterator = changes.makeAsyncIterator()
        let change = await iterator.next()
        XCTAssertEqual(change?.event, .signedOut)
        do {
            _ = try await auth.accessToken()
            XCTFail("signed-out client returned a token")
        } catch let error as AuthError {
            XCTAssertEqual(error.code, "not_authenticated")
        }
    }

    func testSignUpWaitingForConfirmationAndCodes() async throws {
        let auth = try client([
            "POST /auth/v1/signup": { _ in (200, ["id": "u3", "email": "c@example.test"]) },
            "POST /auth/v1/otp": { _ in (200, [:]) },
            "POST /auth/v1/verify": { call in call.body["token"] == "123456" ? (200, tokens(2)) : (403, ["error_code": "otp_expired"]) },
        ])
        let callback = URL(string: "https://shop.example/auth/callback?next=/a&b=c")!
        let result = try await auth.signUp(email: "c@example.test", password: "long password", data: ["name": "C"], redirectTo: callback, captchaToken: "cap")
        XCTAssertNil(result.session)
        XCTAssertEqual(result.user?.id, "u3")
        let signup = Stub.calls.last!
        XCTAssertEqual(signup.query["redirect_to"], callback.absoluteString)
        XCTAssertEqual(signup.body["data"], ["name": "C"])
        XCTAssertEqual(signup.body["code_challenge_method"], "s256")
        XCTAssertEqual(signup.body["gotrue_meta_security"], ["captcha_token": "cap"])

        try await auth.signInWithOTP(email: "c@example.test")
        XCTAssertEqual(Stub.calls.last?.body, ["email": "c@example.test", "create_user": false])
        do {
            _ = try await auth.verifyOTP(email: "c@example.test", code: "000000")
            XCTFail("wrong code accepted")
        } catch let error as AuthError {
            XCTAssertEqual(error.code, "otp_expired")
        }
        let session = try await auth.verifyOTP(email: "c@example.test", code: " 123456 ", type: .signup)
        XCTAssertEqual(session.refreshToken, "refresh-2")
        XCTAssertEqual(Stub.calls.last?.body, ["type": "signup", "email": "c@example.test", "token": "123456"])

        try await auth.signInWithOTP(phone: "+15550100", createUser: true)
        XCTAssertEqual(Stub.calls.last?.body, ["phone": "+15550100", "channel": "sms", "create_user": true])
    }

    func testOAuthWithPKCEAndReturnURLs() async throws {
        let store: SessionStore = MemorySessionStore()
        let auth = try client([
            "POST /auth/v1/token": { call in
                guard call.query["grant_type"] == "pkce", call.body["auth_code"] == "c0de" else { return (400, ["error_code": "bad_code_verifier"]) }
                return (200, tokens(3))
            },
        ], store: store)
        let callback = URL(string: "https://shop.example/auth/callback")!
        let start = try await auth.oauthURL(provider: "github", redirectTo: callback, scopes: ["read:user", "user:email"])
        let query = Dictionary(uniqueKeysWithValues: URLComponents(url: start, resolvingAgainstBaseURL: false)!.queryItems!.map { ($0.name, $0.value ?? "") })
        XCTAssertEqual(start.path, "/auth/v1/authorize")
        XCTAssertEqual(query["provider"], "github")
        XCTAssertEqual(query["redirect_to"], callback.absoluteString)
        XCTAssertEqual(query["scopes"], "read:user user:email")
        XCTAssertEqual(query["code_challenge_method"], "s256")
        let verifier = try await store.load("pkce").flatMap { String(data: $0, encoding: .utf8) }
        XCTAssertEqual(query["code_challenge"], Base64URL.encode(Data(SHA256.hash(data: Data(verifier!.utf8)))))

        let session = try await auth.session(from: URL(string: "https://shop.example/auth/callback?code=c0de")!)
        XCTAssertEqual(session?.refreshToken, "refresh-3")
        XCTAssertEqual(Stub.calls.last?.body["code_verifier"], .string(verifier!))
        let leftover = try await store.load("pkce")
        XCTAssertNil(leftover)
        do {
            _ = try await auth.session(from: URL(string: "https://shop.example/auth/callback?code=again")!)
            XCTFail("a code without a verifier was exchanged")
        } catch let error as AuthError {
            XCTAssertEqual(error.code, "pkce_verifier_missing")
        }
    }

    func testSessionInTheFragmentAndReturnedErrors() async throws {
        let auth = try client([:])
        let token = jwt(["sub": "u2", "aal": "aal2", "org_id": "o1", "org_slug": "acme", "org_role": "admin", "org_permissions": ["doc:write"]])
        let session = try await auth.session(from: URL(string: "https://shop.example/cb#access_token=\(token)&refresh_token=r1&expires_in=600&token_type=bearer")!)
        XCTAssertEqual(session?.refreshToken, "r1")
        XCTAssertEqual(session?.assuranceLevel, .aal2)
        XCTAssertEqual(session?.activeOrganization, ActiveOrganization(id: "o1", slug: "acme", role: "admin", permissions: ["doc:write"]))
        XCTAssertEqual(session.map { Int($0.expiresAt.timeIntervalSinceNow.rounded()) }, 600)
        let none = try await auth.session(from: URL(string: "https://shop.example/cb")!)
        XCTAssertNil(none)
        do {
            _ = try await auth.session(from: URL(string: "https://shop.example/cb#error=access_denied&error_code=otp_expired&error_description=Email+link+is+invalid")!)
            XCTFail("an error return was accepted")
        } catch let error as AuthError {
            XCTAssertEqual(error.code, "otp_expired")
        }
        XCTAssertEqual(Stub.calls.count, 0)
    }

    func testSecondStepAndOrganizations() async throws {
        let factors: JSONValue = ["id": "u1", "factors": [["id": "f1", "factor_type": "totp", "status": "verified", "friendly_name": "Phone"], ["id": "rc", "factor_type": "recovery_code", "status": "verified"]]]
        let orgToken = jwt(["sub": "u1", "aal": "aal2", "org_id": "o2", "org_slug": "beta", "org_role": "owner", "org_permissions": ["*"]])
        let refreshed: JSONValue = ["access_token": .string(orgToken), "refresh_token": "refresh-9", "expires_in": .number(600)]
        let token: Stub.Handler = { call in call.query["grant_type"] == "password" ? (200, tokens(1, ["sub": "u1", "aal": "aal1"])) : (200, refreshed) }
        let verify: Stub.Handler = { call in call.body["code"] == "123456" ? (200, tokens(2, ["sub": "u1", "aal": "aal2"])) : (422, ["error_code": "mfa_verification_failed"]) }
        let user: Stub.Handler = { _ in (200, factors) }
        let challenge: Stub.Handler = { _ in (200, ["id": "c1", "type": "totp"]) }
        let activate: Stub.Handler = { _ in (200, ["organization": ["id": "o2", "slug": "beta", "role": "owner"]]) }
        let auth = try client(["POST /auth/v1/token": token, "GET /auth/v1/user": user, "POST /auth/v1/factors/f1/challenge": challenge, "POST /auth/v1/factors/f1/verify": verify, "POST /authz/v1/active-organization": activate])
        _ = try await auth.signIn(email: "a@example.test", password: "pw")
        var level = try await auth.mfa.assuranceLevel()
        XCTAssertEqual(level.current, AssuranceLevel.aal1)
        XCTAssertEqual(level.next, AssuranceLevel.aal2)
        let started = try await auth.mfa.challenge(factorId: "f1")
        let changes = await auth.changes()
        let verified = try await auth.mfa.verify(factorId: "f1", challengeId: started.id, code: "123456")
        XCTAssertEqual(verified.assuranceLevel, AssuranceLevel.aal2)
        var iterator = changes.makeAsyncIterator()
        let change = await iterator.next()
        XCTAssertEqual(change?.event, .mfaVerified)
        level = try await auth.mfa.assuranceLevel()
        XCTAssertEqual(level.current, AssuranceLevel.aal2)

        let active = try await auth.organizations.setActive("o2")
        XCTAssertEqual(active?.slug, "beta")
        XCTAssertEqual(Stub.calls.first { $0.key == "POST /authz/v1/active-organization" }?.body, ["org": "o2"])
        let current = try await auth.organizations.active()
        XCTAssertEqual(current?.id, "o2")
        _ = try await auth.organizations.setActive(String?.none)
        XCTAssertEqual(Stub.calls.last { $0.key == "POST /authz/v1/active-organization" }?.body, ["org": .null])
    }

    func testSignOutClearsTheSessionEvenWhenTheServerFails() async throws {
        let store: SessionStore = MemorySessionStore()
        let auth = try client([
            "POST /auth/v1/token": { _ in (200, tokens(1)) },
            "POST /auth/v1/logout": { call in call.query["scope"] == "others" ? (204, nil) : (503, nil) },
        ], store: store)
        _ = try await auth.signIn(email: "a@example.test", password: "pw")
        try await auth.signOut(scope: .others)
        let kept = try await store.load("session")
        XCTAssertNotNil(kept)
        do {
            try await auth.signOut(scope: .global)
            XCTFail("a failed sign-out did not throw")
        } catch let error as AuthError {
            XCTAssertEqual(error.status, 503)
        }
        let cleared = try await store.load("session")
        XCTAssertNil(cleared)
    }

    func testHostedPagesAndEscaping() throws {
        let auth = try AuthClient(url: origin, store: MemorySessionStore())
        let back = URL(string: "https://shop.example/app?tab=1&x=a+b")!
        XCTAssertEqual(auth.hostedSignInURL(redirectTo: back).absoluteString, "https://shop-auth.gregale.app/ui/sign-in?redirect_to=https%3A%2F%2Fshop.example%2Fapp%3Ftab%3D1%26x%3Da%2Bb")
        XCTAssertEqual(auth.hostedAccountURL().absoluteString, "https://shop-auth.gregale.app/ui/account")
    }

    func testKeychainStore() async throws {
        guard ProcessInfo.processInfo.environment["GREGALE_AUTH_KEYCHAIN_TEST"] == "1" else { throw XCTSkip("set GREGALE_AUTH_KEYCHAIN_TEST=1 to use the login keychain") }
        let store: SessionStore = KeychainSessionStore(service: "dev.gregale.auth.tests", account: UUID().uuidString)
        try await store.save(Data("one".utf8), for: "session")
        try await store.save(Data("two".utf8), for: "session")
        let value = try await store.load("session")
        XCTAssertEqual(value, Data("two".utf8))
        try await store.remove("session")
        let removed = try await store.load("session")
        XCTAssertNil(removed)
        try await store.remove("session")
    }
}

private extension Data {
    // The test writes expiresAt as a string to keep the literal simple; the
    // store holds seconds since 1970 as a number.
    func replacingExpiresAt() throws -> Data {
        var object = try JSONSerialization.jsonObject(with: self) as! [String: Any]
        object["expiresAt"] = Double(object["expiresAt"] as! String)!
        return try JSONSerialization.data(withJSONObject: object)
    }
}

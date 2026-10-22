import CryptoKit
import Foundation
import XCTest
@testable import GregaleAuth

/// Runs against a real Auth app when GREGALE_AUTH_LIVE_URL is set:
/// autoconfirm, organization claims, self-deletion and a doc:write policy on,
/// GREGALE_AUTH_LIVE_CALLBACK an allowed redirect URL, and
/// GREGALE_AUTH_LIVE_OUTBOX serving the sent emails as JSON.
final class LiveTests: XCTestCase {
    func testAgainstARealAuthApp() async throws {
        let env = ProcessInfo.processInfo.environment
        guard let live = env["GREGALE_AUTH_LIVE_URL"].flatMap(URL.init(string:)), let outbox = env["GREGALE_AUTH_LIVE_OUTBOX"].flatMap(URL.init(string:)),
              let callback = env["GREGALE_AUTH_LIVE_CALLBACK"].flatMap(URL.init(string:)) else {
            throw XCTSkip("set GREGALE_AUTH_LIVE_URL, GREGALE_AUTH_LIVE_OUTBOX and GREGALE_AUTH_LIVE_CALLBACK")
        }
        let emailedOrigin = env["GREGALE_AUTH_LIVE_EMAIL_ORIGIN"]
        let auth = try AuthClient(url: live, store: MemorySessionStore())
        let email = "swift-\(UUID().uuidString.prefix(8).lowercased())@example.test", password = "correct horse battery staple"

        let signedUp = try await auth.signUp(email: email, password: password, data: ["plan": "free"])
        XCTAssertNotNil(signedUp.session, "autoconfirm signs in at once")
        XCTAssertEqual(signedUp.user?.email, email)
        XCTAssertEqual(signedUp.user?.userMetadata?["plan"], "free")
        do {
            _ = try await auth.signIn(email: email, password: "wrong password")
            XCTFail("a wrong password was accepted")
        } catch let error as AuthError {
            XCTAssertEqual(error.code, "invalid_credentials")
        }
        let updated = try await auth.updateUser(data: ["plan": "pro"])
        XCTAssertEqual(updated.userMetadata?["plan"], "pro")
        let before = try await auth.currentSession()
        let after = try await auth.refreshSession()
        XCTAssertNotNil(after)
        XCTAssertNotEqual(before?.refreshToken, after?.refreshToken, "refresh tokens rotate")

        // An emailed code, typed into the app.
        try await auth.signOut()
        try await auth.signInWithOTP(email: email)
        let codeMail = try await latestEmail(outbox, to: email)
        let code = try XCTUnwrap(codeMail.firstMatch(#"Code: (\d{6})"#), "no code in: \(codeMail)")
        let viaCode = try await auth.verifyOTP(email: email, code: code)
        XCTAssertEqual(viaCode.claims["email"]?.stringValue, email)

        // The link in the same kind of email, returning to the app with a PKCE
        // code. GoTrue sends one email a minute per address, so a new person
        // signs up by link.
        try await auth.signOut()
        let newcomer = "swift-link-\(UUID().uuidString.prefix(8).lowercased())@example.test"
        try await auth.signInWithOTP(email: newcomer, createUser: true, redirectTo: callback)
        let linkMail = try await latestEmail(outbox, to: newcomer)
        var link = try XCTUnwrap(linkMail.firstMatch(#"(https?://\S+/verify\?\S+)"#), "no link in: \(linkMail)")
        if let emailedOrigin { link = link.replacingOccurrences(of: emailedOrigin, with: live.absoluteString) }
        let returned = try await redirect(from: URL(string: link)!)
        XCTAssertTrue(returned.absoluteString.hasPrefix(callback.absoluteString), "returned to \(returned)")
        XCTAssertNotNil(URLComponents(url: returned, resolvingAgainstBaseURL: false)?.queryItems?.first { $0.name == "code" }, "a PKCE return carries a code: \(returned)")
        let viaLink = try await auth.session(from: returned)
        XCTAssertEqual(viaLink?.claims["email"]?.stringValue, newcomer)
        try await auth.signOut()
        _ = try await auth.signIn(email: email, password: password)

        // A second step.
        let enrollment = try await auth.mfa.enrollTOTP(friendlyName: "Work phone")
        let first = try await auth.mfa.challenge(factorId: enrollment.id)
        let enrolled = try await auth.mfa.verify(factorId: enrollment.id, challengeId: first.id, code: totp(enrollment.totp.secret))
        XCTAssertEqual(enrolled.assuranceLevel, AssuranceLevel.aal2)
        try await auth.signOut()
        let aal1 = try await auth.signIn(email: email, password: password)
        XCTAssertEqual(aal1.assuranceLevel, AssuranceLevel.aal1)
        let level = try await auth.mfa.assuranceLevel()
        XCTAssertEqual(level.current, AssuranceLevel.aal1)
        XCTAssertEqual(level.next, AssuranceLevel.aal2)
        let wrongChallenge = try await auth.mfa.challenge(factorId: enrollment.id)
        do {
            try await auth.mfa.verify(factorId: enrollment.id, challengeId: wrongChallenge.id, code: "000000")
            XCTFail("a wrong code was accepted")
        } catch let error as AuthError {
            XCTAssertEqual(error.code, "mfa_verification_failed")
        }
        let second = try await auth.mfa.challenge(factorId: enrollment.id)
        let aal2 = try await auth.mfa.verify(factorId: enrollment.id, challengeId: second.id, code: totp(enrollment.totp.secret))
        XCTAssertEqual(aal2.assuranceLevel, AssuranceLevel.aal2)

        // Organizations, the active one in the token, and authorization.
        let org = try await auth.organizations.create(slug: "swift-\(UUID().uuidString.prefix(8).lowercased())", name: "Swift Co")
        let active = try await auth.organizations.setActive(org.id)
        XCTAssertEqual(active, ActiveOrganization(id: org.id, slug: org.slug, role: "owner", permissions: ["*"]))
        let current = try await auth.currentSession()
        XCTAssertEqual(current?.assuranceLevel, AssuranceLevel.aal2, "switching keeps the second step")
        let fromToken = try await auth.organizations.active()
        XCTAssertEqual(fromToken?.id, org.id)
        let canWrite = try await auth.authorize(action: "write", resource: AuthClient.resource(org: org.id, kind: "doc", name: "1"))
        let elsewhere = try await auth.authorize(action: "write", resource: AuthClient.resource(org: "00000000-0000-4000-8000-000000000000", kind: "doc", name: "1"))
        XCTAssertTrue(canWrite)
        XCTAssertFalse(elsewhere)
        let cleared = try await auth.organizations.setActive(String?.none)
        XCTAssertNil(cleared)

        let sessions = try await auth.sessions()
        XCTAssertTrue(sessions.contains { $0.current })

        let organizationsDeleted = try await auth.deleteAccount()
        XCTAssertEqual(organizationsDeleted, 1)
        let afterDeletion = try await auth.currentSession()
        XCTAssertNil(afterDeletion)
        do {
            _ = try await auth.signIn(email: email, password: password)
            XCTFail("a deleted account signed in")
        } catch let error as AuthError {
            XCTAssertEqual(error.code, "invalid_credentials")
        }
    }

    private struct Email: Decodable {
        let to: String
        let subject: String
        let text: String
    }

    // The newest email to address, waiting briefly for it to arrive.
    private func latestEmail(_ outbox: URL, to address: String, after previous: String? = nil) async throws -> String {
        for _ in 0..<50 {
            let (data, _) = try await URLSession.shared.data(from: outbox)
            if let mail = try JSONDecoder().decode([Email].self, from: data).last(where: { $0.to == address }), mail.text != previous {
                return mail.text
            }
            try await Task.sleep(nanoseconds: 100_000_000)
        }
        throw AuthError(status: 504, code: "no_email")
    }

    // Opens a link without following its redirect, as the app's universal
    // link would receive it.
    private func redirect(from link: URL) async throws -> URL {
        final class Stop: NSObject, URLSessionTaskDelegate {
            func urlSession(_ session: URLSession, task: URLSessionTask, willPerformHTTPRedirection response: HTTPURLResponse, newRequest request: URLRequest) async -> URLRequest? { nil }
        }
        let (_, response) = try await URLSession.shared.data(for: URLRequest(url: link), delegate: Stop())
        let location = try XCTUnwrap((response as? HTTPURLResponse)?.value(forHTTPHeaderField: "Location"), "no redirect from \(link)")
        return try XCTUnwrap(URL(string: location))
    }
}

/// RFC 6238 code for an authenticator app's base32 secret.
func totp(_ secret: String, at date: Date = Date()) -> String {
    let alphabet = Array("ABCDEFGHIJKLMNOPQRSTUVWXYZ234567")
    var bytes: [UInt8] = []
    var buffer = 0, bits = 0
    for character in secret.uppercased() {
        guard let index = alphabet.firstIndex(of: character) else { continue }
        buffer = (buffer << 5 | index) & 0xffff
        bits += 5
        if bits >= 8 {
            bytes.append(UInt8((buffer >> (bits - 8)) & 0xff))
            bits -= 8
        }
    }
    var counter = UInt64(date.timeIntervalSince1970 / 30).bigEndian
    let mac = Array(HMAC<Insecure.SHA1>.authenticationCode(for: Data(bytes: &counter, count: 8), using: SymmetricKey(data: bytes)))
    let offset = Int(mac[mac.count - 1] & 0x0f)
    let value = (UInt32(mac[offset] & 0x7f) << 24 | UInt32(mac[offset + 1]) << 16 | UInt32(mac[offset + 2]) << 8 | UInt32(mac[offset + 3])) % 1_000_000
    return String(format: "%06d", value)
}

private extension String {
    func firstMatch(_ pattern: String) -> String? {
        guard let regex = try? NSRegularExpression(pattern: pattern), let match = regex.firstMatch(in: self, range: NSRange(startIndex..., in: self)),
              let range = Range(match.range(at: 1), in: self) else { return nil }
        return String(self[range])
    }
}

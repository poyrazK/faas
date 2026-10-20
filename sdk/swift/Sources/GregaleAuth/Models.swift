import Foundation

/// A signed-in session. Access tokens are short-lived; the client refreshes
/// them shortly before `expiresAt`.
public struct Session: Codable, Sendable, Equatable {
    public let accessToken: String
    public let refreshToken: String
    public let expiresAt: Date
    public let user: User?

    public init(accessToken: String, refreshToken: String, expiresAt: Date, user: User? = nil) {
        self.accessToken = accessToken
        self.refreshToken = refreshToken
        self.expiresAt = expiresAt
        self.user = user
    }

    /// The access token's claims, read without verifying them.
    public var claims: [String: JSONValue] { Claims.decode(accessToken) }

    /// aal2 once the user has completed a second step.
    public var assuranceLevel: AssuranceLevel { claims["aal"]?.stringValue == "aal2" ? .aal2 : .aal1 }

    /// The organization this session acts in, from its token's org_* claims.
    public var activeOrganization: ActiveOrganization? { Claims.activeOrganization(claims) }
}

public struct User: Codable, Sendable, Equatable {
    public let id: String
    public let email: String?
    public let phone: String?
    public let isAnonymous: Bool?
    public let appMetadata: [String: JSONValue]?
    public let userMetadata: [String: JSONValue]?
    public let factors: [Factor]?

    enum CodingKeys: String, CodingKey {
        case id, email, phone, factors
        case isAnonymous = "is_anonymous", appMetadata = "app_metadata", userMetadata = "user_metadata"
    }
}

/// A failed request. `code` is the stable server code, or `http_<status>`.
public struct AuthError: Error, Equatable, LocalizedError, Sendable {
    public let status: Int
    public let code: String

    public init(status: Int, code: String) {
        self.status = status
        self.code = code
    }

    public var errorDescription: String? { code }

    static let notAuthenticated = AuthError(status: 401, code: "not_authenticated")
    static let invalidSession = AuthError(status: 502, code: "invalid_session")
}

public enum AuthEvent: String, Sendable {
    case signedIn = "SIGNED_IN", tokenRefreshed = "TOKEN_REFRESHED", signedOut = "SIGNED_OUT", mfaVerified = "MFA_VERIFIED"
}

/// One change of the stored session, from `AuthClient.changes()`.
public struct AuthChange: Sendable {
    public let event: AuthEvent
    public let session: Session?
}

public enum AssuranceLevel: String, Codable, Sendable {
    case aal1, aal2
}

/// A second factor: an authenticator app, a phone or recovery codes.
public struct Factor: Codable, Sendable, Equatable {
    public let id: String
    public let factorType: String
    public let status: String
    public let friendlyName: String?
    public let phone: String?

    enum CodingKeys: String, CodingKey {
        case id, status, phone
        case factorType = "factor_type", friendlyName = "friendly_name"
    }
}

public struct TOTPEnrollment: Codable, Sendable {
    public struct TOTP: Codable, Sendable {
        /// The QR code as an SVG document; render it or show `secret`.
        public let qrCode: String
        public let secret: String
        public let uri: String
        enum CodingKeys: String, CodingKey { case secret, uri, qrCode = "qr_code" }
    }
    public let id: String
    public let totp: TOTP
}

public struct FactorChallenge: Codable, Sendable {
    public let id: String
    public let type: String?
}

public struct Organization: Codable, Sendable, Equatable {
    public let id: String
    public let slug: String
    public let name: String
    public let role: String?
}

public struct ActiveOrganization: Sendable, Equatable {
    public let id: String
    public let slug: String
    public let role: String
    public let permissions: [String]
}

/// One of the signed-in user's own sessions; `current` is the one asking.
public struct UserSession: Codable, Sendable, Equatable {
    public let id: String
    public let createdAt: String
    public let lastActiveAt: String
    public let userAgent: String?
    public let ip: String?
    public let current: Bool

    enum CodingKeys: String, CodingKey {
        case id, ip, current
        case createdAt = "created_at", lastActiveAt = "last_active_at", userAgent = "user_agent"
    }
}

/// Any JSON value, for metadata and claims.
public enum JSONValue: Codable, Sendable, Equatable {
    case string(String), number(Double), bool(Bool), object([String: JSONValue]), array([JSONValue]), null

    public init(from decoder: Decoder) throws {
        let container = try decoder.singleValueContainer()
        if container.decodeNil() { self = .null }
        else if let value = try? container.decode(Bool.self) { self = .bool(value) }
        else if let value = try? container.decode(Double.self) { self = .number(value) }
        else if let value = try? container.decode(String.self) { self = .string(value) }
        else if let value = try? container.decode([JSONValue].self) { self = .array(value) }
        else { self = .object(try container.decode([String: JSONValue].self)) }
    }

    public func encode(to encoder: Encoder) throws {
        var container = encoder.singleValueContainer()
        switch self {
        case .string(let value): try container.encode(value)
        case .number(let value): try container.encode(value)
        case .bool(let value): try container.encode(value)
        case .object(let value): try container.encode(value)
        case .array(let value): try container.encode(value)
        case .null: try container.encodeNil()
        }
    }

    public var stringValue: String? { if case .string(let value) = self { return value } else { return nil } }
    public var boolValue: Bool? { if case .bool(let value) = self { return value } else { return nil } }
}

extension JSONValue: ExpressibleByStringLiteral, ExpressibleByBooleanLiteral, ExpressibleByDictionaryLiteral, ExpressibleByArrayLiteral {
    public init(stringLiteral value: String) { self = .string(value) }
    public init(booleanLiteral value: Bool) { self = .bool(value) }
    public init(dictionaryLiteral elements: (String, JSONValue)...) { self = .object(Dictionary(uniqueKeysWithValues: elements)) }
    public init(arrayLiteral elements: JSONValue...) { self = .array(elements) }
}

enum Claims {
    /// Reads an access token's claims without verifying them: the Auth app
    /// and backends verify; clients only decide what to show.
    static func decode(_ token: String) -> [String: JSONValue] {
        let parts = token.split(separator: ".")
        guard parts.count >= 2, let data = Base64URL.decode(String(parts[1])),
              case .object(let claims)? = try? JSONDecoder().decode(JSONValue.self, from: data) else { return [:] }
        return claims
    }

    static func activeOrganization(_ claims: [String: JSONValue]) -> ActiveOrganization? {
        guard let id = claims["org_id"]?.stringValue, let slug = claims["org_slug"]?.stringValue, let role = claims["org_role"]?.stringValue else { return nil }
        var permissions: [String] = []
        if case .array(let values)? = claims["org_permissions"] { permissions = values.compactMap(\.stringValue) }
        return ActiveOrganization(id: id, slug: slug, role: role, permissions: permissions)
    }
}

enum Base64URL {
    static func encode(_ data: Data) -> String {
        data.base64EncodedString().replacingOccurrences(of: "+", with: "-").replacingOccurrences(of: "/", with: "_").replacingOccurrences(of: "=", with: "")
    }

    static func decode(_ value: String) -> Data? {
        var base64 = value.replacingOccurrences(of: "-", with: "+").replacingOccurrences(of: "_", with: "/")
        base64 += String(repeating: "=", count: (4 - base64.count % 4) % 4)
        return Data(base64Encoded: base64)
    }
}

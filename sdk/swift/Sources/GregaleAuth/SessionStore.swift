import Foundation
#if canImport(Security)
import Security
#endif

/// Where the client keeps the session (key "session") and a pending PKCE
/// verifier (key "pkce") between launches.
public protocol SessionStore: Sendable {
    func load(_ key: String) async throws -> Data?
    func save(_ data: Data, for key: String) async throws
    func remove(_ key: String) async throws
}

/// Keeps everything in memory: signed out on every launch.
public actor MemorySessionStore: SessionStore {
    private var values: [String: Data] = [:]

    public init() {}

    public func load(_ key: String) -> Data? { values[key] }
    public func save(_ data: Data, for key: String) { values[key] = data }
    public func remove(_ key: String) { values[key] = nil }
}

#if canImport(Security)
/// The default store: generic-password Keychain items readable after the
/// device's first unlock, never synced or restored to another device.
public struct KeychainSessionStore: SessionStore {
    public let service: String
    /// Distinguishes several Auth apps in one app; the client passes its origin.
    public let account: String

    public init(service: String = "dev.gregale.auth", account: String) {
        self.service = service
        self.account = account
    }

    private func query(_ key: String) -> [String: Any] {
        [kSecClass as String: kSecClassGenericPassword, kSecAttrService as String: service, kSecAttrAccount as String: "\(account)#\(key)"]
    }

    public func load(_ key: String) throws -> Data? {
        var query = query(key)
        query[kSecReturnData as String] = true
        query[kSecMatchLimit as String] = kSecMatchLimitOne
        var item: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &item)
        if status == errSecItemNotFound { return nil }
        guard status == errSecSuccess else { throw KeychainError(status: status) }
        return item as? Data
    }

    public func save(_ data: Data, for key: String) throws {
        let attributes: [String: Any] = [kSecValueData as String: data, kSecAttrAccessible as String: kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly]
        var status = SecItemUpdate(query(key) as CFDictionary, attributes as CFDictionary)
        if status == errSecItemNotFound {
            status = SecItemAdd(query(key).merging(attributes) { $1 } as CFDictionary, nil)
        }
        guard status == errSecSuccess else { throw KeychainError(status: status) }
    }

    public func remove(_ key: String) throws {
        let status = SecItemDelete(query(key) as CFDictionary)
        guard status == errSecSuccess || status == errSecItemNotFound else { throw KeychainError(status: status) }
    }
}

public struct KeychainError: Error, Equatable {
    public let status: OSStatus
}
#endif

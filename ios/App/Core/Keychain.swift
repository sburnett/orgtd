import Foundation
import Security

/// The SSH private key, kept in the Keychain (never on disk). Only the
/// app reads it — the share extension never talks to git — so it needs
/// no shared access group.
enum Keychain {
    private static let service = "orgtd.ssh-deploy-key"
    private static let account = "default"

    private static var query: [String: Any] {
        [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
        ]
    }

    static func load() -> Data? {
        var q = query
        q[kSecReturnData as String] = true
        q[kSecMatchLimit as String] = kSecMatchLimitOne
        var out: CFTypeRef?
        guard SecItemCopyMatching(q as CFDictionary, &out) == errSecSuccess else { return nil }
        return out as? Data
    }

    static func save(_ key: Data) throws {
        delete()
        var q = query
        q[kSecValueData as String] = key
        q[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
        let status = SecItemAdd(q as CFDictionary, nil)
        guard status == errSecSuccess else {
            throw NSError(domain: NSOSStatusErrorDomain, code: Int(status), userInfo: [NSLocalizedDescriptionKey: "Couldn't save the SSH key to the Keychain (\(status))"])
        }
    }

    static func delete() {
        SecItemDelete(query as CFDictionary)
    }
}

import Foundation
import Mobilecore

struct CoreError: LocalizedError {
    var message: String
    var errorDescription: String? { message }
}

/// Wraps the gomobile-generated Mobilecore functions: runs them off the
/// main actor, one at a time, and decodes their JSON results.
actor CoreClient {
    let groupDir: String

    init(groupDir: URL = AppGroup.containerURL) {
        self.groupDir = groupDir.path
        _ = try? CoreClient.check { MobilecoreSetTimeZone(TimeZone.current.identifier, $0) }
    }

    /// Calls a Mobilecore function that reports failure through an
    /// NSError out-parameter, turning that into a Swift throw.
    private static func check<T>(_ call: (NSErrorPointer) -> T) throws -> T {
        var error: NSError?
        let result = call(&error)
        if let error { throw CoreError(message: error.localizedDescription) }
        return result
    }

    private func decode<T: Decodable>(_ type: T.Type, _ call: (NSErrorPointer) -> String) throws -> T {
        let json = try CoreClient.check(call)
        return try JSONDecoder().decode(T.self, from: Data(json.utf8))
    }

    private var now: Int64 { Int64(Date().timeIntervalSince1970) }

    // MARK: Setup

    static func generateKey() throws -> (publicKey: String, privateKey: Data) {
        let kp = try check { MobilecoreGenerateSSHKey($0) }
        guard let kp, let priv = kp.privateKeyPEM else { throw CoreError(message: "Key generation returned nothing") }
        return (kp.publicKey, priv)
    }

    static func publicKey(for privateKey: Data) -> String? {
        try? check { MobilecorePublicKeyFor(privateKey, $0) }
    }

    /// Reads the server's host key, for the user to confirm before cloning.
    func scanHostKey(url: String) throws -> HostKeyInfo {
        try decode(HostKeyInfo.self) { MobilecoreScanHostKey(url, $0) }
    }

    func clone(url: String, key: Data, hostKey: HostKeyInfo) throws {
        let json = String(decoding: try JSONEncoder().encode(hostKey), as: UTF8.self)
        _ = AppGroup.repoURL // pre-create it with its file protection
        _ = try CoreClient.check { MobilecoreClone(groupDir, url, key, json, $0) }
    }

    func removeClone() throws {
        _ = try CoreClient.check { MobilecoreRemoveClone(groupDir, $0) }
    }

    // MARK: Git

    /// Imports share-extension captures and rebuilds from what was last
    /// fetched; never touches the network.
    func refreshLocal(author: Author) throws {
        _ = try CoreClient.check { MobilecoreRefreshLocal(groupDir, author.name, author.email, $0) }
    }

    func refresh(key: Data?, author: Author) throws {
        _ = try CoreClient.check { MobilecoreRefresh(groupDir, key, author.name, author.email, $0) }
    }

    func sync(key: Data?, author: Author) throws {
        _ = try CoreClient.check { MobilecoreSync(groupDir, key, author.name, author.email, $0) }
    }

    // MARK: Capture

    @discardableResult
    func capture(_ draft: CaptureDraft, author: Author) throws -> String {
        let json = try draft.json()
        return try CoreClient.check { MobilecoreCapture(groupDir, json, author.name, author.email, $0) }
    }

    func update(_ draft: CaptureDraft, author: Author) throws {
        let json = try draft.json()
        _ = try CoreClient.check { MobilecoreUpdatePendingCapture(groupDir, json, author.name, author.email, $0) }
    }

    func delete(captureID: String, author: Author) throws {
        _ = try CoreClient.check { MobilecoreDeletePendingCapture(groupDir, captureID, author.name, author.email, $0) }
    }

    func pendingCapture(id: String) throws -> CaptureDraft {
        try decode(CaptureDraft.self) { MobilecorePendingCapture(groupDir, id, $0) }
    }

    func parseDate(_ input: String) throws -> String {
        let now = self.now
        return try CoreClient.check { MobilecoreParseDate(input, now, $0) }
    }

    // MARK: Read views

    func status() throws -> StatusResult {
        try decode(StatusResult.self) { MobilecoreStatus(groupDir, $0) }
    }

    func agenda() throws -> AgendaResult {
        let now = self.now
        return try decode(AgendaResult.self) { MobilecoreAgenda(groupDir, now, $0) }
    }

    func inbox() throws -> InboxResult {
        try decode(InboxResult.self) { MobilecoreInbox(groupDir, $0) }
    }

    func outline(showClosed: Bool) throws -> OutlineResult {
        let now = self.now
        return try decode(OutlineResult.self) { MobilecoreOutline(groupDir, showClosed, now, $0) }
    }

    func search(_ query: String) throws -> SearchResult {
        try decode(SearchResult.self) { MobilecoreSearch(groupDir, query, $0) }
    }

    func tags() throws -> [String] {
        try decode([String].self) { MobilecoreTags(groupDir, $0) }
    }
}

struct Author: Equatable {
    var name: String
    var email: String
}

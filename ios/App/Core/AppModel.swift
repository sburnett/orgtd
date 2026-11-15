import Foundation
import Observation
import SwiftUI

/// App-wide state: settings, sync status, and the loaded read views. All
/// Go work goes through `core` (off the main actor); this type only
/// publishes results.
@MainActor
@Observable
final class AppModel {
    let core = CoreClient()

    // Settings (UserDefaults; the key itself is in the Keychain).
    var repoURL: String { didSet { defaults.set(repoURL, forKey: "repoURL") } }
    var authorName: String { didSet { defaults.set(authorName, forKey: "authorName") } }
    var authorEmail: String { didSet { defaults.set(authorEmail, forKey: "authorEmail") } }
    var showClosed: Bool { didSet { defaults.set(showClosed, forKey: "showClosed"); Task { await reloadOutline() } } }

    private(set) var publicKey: String?
    private(set) var status = StatusResult.empty
    private(set) var agenda: AgendaResult?
    private(set) var inbox: InboxResult?
    private(set) var outline: OutlineResult?
    private(set) var tags: [String] = []
    private(set) var busy: String?        // what's running, for the status line
    var lastError: String?                // the latest failure, shown until dismissed or replaced

    private let defaults = UserDefaults.standard

    init() {
        repoURL = defaults.string(forKey: "repoURL") ?? ""
        authorName = defaults.string(forKey: "authorName") ?? ""
        authorEmail = defaults.string(forKey: "authorEmail") ?? ""
        showClosed = defaults.bool(forKey: "showClosed")
        if let key = Keychain.load() {
            publicKey = CoreClient.publicKey(for: key)
        }
    }

    var author: Author {
        Author(name: authorName.isEmpty ? "orgtd iOS" : authorName,
               email: authorEmail.isEmpty ? "orgtd-ios@localhost" : authorEmail)
    }

    var hasKey: Bool { publicKey != nil }
    var isSetUp: Bool { hasKey && status.cloned }

    // MARK: Keywords and colors

    func keyword(_ name: String) -> Keyword? { status.keywords.first { $0.name == name } }

    func keywordColor(_ name: String) -> Color {
        Color(orgColor: keyword(name)?.color ?? "") ?? .secondary
    }

    var tagColor: Color { Color(orgColor: status.tagColor) ?? .teal }
    var dateColor: Color { Color(orgColor: status.dateColor) ?? .pink }

    // MARK: Setup

    func generateKey() throws {
        let (pub, priv) = try CoreClient.generateKey()
        try Keychain.save(priv)
        publicKey = pub
    }

    /// The server's host key, read by `checkServer` and waiting for the
    /// user to compare its fingerprint and trust it.
    var serverHostKey: HostKeyInfo?

    var trimmedRepoURL: String { repoURL.trimmingCharacters(in: .whitespaces) }

    func checkServer() async {
        serverHostKey = nil
        let url = trimmedRepoURL
        await run("Contacting server…") {
            let key = try await self.core.scanHostKey(url: url)
            self.serverHostKey = key
        }
    }

    /// Clones, trusting `serverHostKey` from now on.
    func clone() async {
        guard let key = Keychain.load(), let hostKey = serverHostKey else { return }
        let url = trimmedRepoURL
        await run("Cloning…") {
            try await self.core.clone(url: url, key: key, hostKey: hostKey)
        }
        serverHostKey = nil
        await reloadAll()
    }

    /// Removes the clone (captures are kept) so another repository can be
    /// set up.
    func removeClone() async {
        await run("Removing…") { try await self.core.removeClone() }
        agenda = nil
        inbox = nil
        outline = nil
        await reloadStatus()
    }

    func forgetKey() {
        Keychain.delete()
        publicKey = nil
    }

    // MARK: Git

    /// Fetch, fold in share-extension captures, and rebuild — never pushes.
    func refresh() async {
        guard isSetUp else { await reloadStatus(); return }
        await run("Fetching…") {
            try await self.core.refresh(key: Keychain.load(), author: self.author)
        }
        await reloadAll()
    }

    /// Refresh, then push the pending captures to origin/mobile.
    func sync() async {
        guard isSetUp else { return }
        await run("Syncing…") {
            try await self.core.sync(key: Keychain.load(), author: self.author)
        }
        await reloadAll()
    }

    // MARK: Captures

    func save(_ draft: CaptureDraft) async -> Bool {
        let ok = await run("Saving…") {
            if draft.id == nil {
                try await self.core.capture(draft, author: self.author)
            } else {
                try await self.core.update(draft, author: self.author)
            }
        }
        await reloadAll()
        return ok
    }

    func deleteCapture(id: String) async {
        await run("Deleting…") { try await self.core.delete(captureID: id, author: self.author) }
        await reloadAll()
    }

    func pendingCapture(id: String) async -> CaptureDraft? {
        do { return try await core.pendingCapture(id: id) } catch {
            lastError = error.localizedDescription
            return nil
        }
    }

    func parseDate(_ input: String) async throws -> String {
        try await core.parseDate(input)
    }

    func search(_ query: String) async -> [Entry] {
        (try? await core.search(query).entries) ?? []
    }

    // MARK: Loading

    func reloadStatus() async {
        if let s = try? await core.status() { status = s }
    }

    func reloadOutline() async {
        guard status.cloned else { return }
        do { outline = try await core.outline(showClosed: showClosed) } catch { lastError = error.localizedDescription }
    }

    func reloadAll() async {
        await reloadStatus()
        guard status.cloned else { return }
        do {
            agenda = try await core.agenda()
            inbox = try await core.inbox()
            outline = try await core.outline(showClosed: showClosed)
            tags = try await core.tags()
            TagsCache.save(tags)
        } catch {
            lastError = error.localizedDescription
        }
    }

    /// Runs `work`, recording what's happening in `busy` and any failure
    /// in `lastError`. Returns whether it succeeded.
    @discardableResult
    private func run(_ label: String, _ work: @escaping () async throws -> Void) async -> Bool {
        busy = label
        defer { busy = nil }
        do {
            try await work()
            lastError = nil
            return true
        } catch {
            lastError = error.localizedDescription
            return false
        }
    }
}

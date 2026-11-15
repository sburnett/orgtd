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
    private(set) var busy: String?        // what's running, for the Sync button
    var lastError: String?                // the latest failure, shown until dismissed or replaced
    /// A failure of something the user just did (a state change, a
    /// delete), shown as an alert right away rather than only on the
    /// Sync button's icon.
    var actionError: String?

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

    /// The keywords a capture can start with: the configured, not-done ones.
    var captureKeywords: [KeywordOption] {
        status.keywords.filter { !$0.done }.map { KeywordOption(name: $0.name, color: $0.color) }
    }

    /// A blank capture, starting with the keyword last captured with.
    func newCapture() -> CaptureDraft {
        CaptureDraft(keyword: KeywordsCache.last(in: captureKeywords))
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

    // Automatic fetches (on returning to the foreground) are rationed: at
    // most one a minute, and after a failure to reach the server, one
    // after 1, 5, 15, then every 30 minutes. Pull-to-refresh and Sync are
    // the user asking, so they always try. Nothing ever retries on its
    // own, and nothing runs in the background.
    private static let minAutoFetchInterval: TimeInterval = 60
    private static let offlineBackoff: [TimeInterval] = [60, 5 * 60, 15 * 60, 30 * 60]
    private var nextAutoFetch = Date.distantPast
    private var consecutiveFailures = 0

    /// The latest network failure was the server being unreachable
    /// (offline, away from its network), not a real problem.
    var isOffline: Bool {
        (lastError ?? status.lastError).contains("can't reach the git server")
    }

    /// On returning to the foreground: always fold in share-extension
    /// captures and rebuild (no network); fetch too if the back-off allows.
    func onForeground() async {
        guard isSetUp else { await reloadStatus(); return }
        // Local first, so the views show what's on the phone right away
        // instead of waiting out a fetch (up to the connect timeout when
        // the server can't be reached).
        await run("Updating…") {
            try await self.core.refreshLocal(author: self.author)
        }
        await reloadAll()
        if Date() >= nextAutoFetch {
            await refresh()
        }
    }

    /// Fetch, fold in share-extension captures, and rebuild — never pushes.
    func refresh() async {
        guard isSetUp else { await reloadStatus(); return }
        let ok = await run("Fetching…") {
            try await self.core.refresh(key: Keychain.load(), author: self.author)
        }
        noteNetworkResult(ok)
        await reloadAll()
    }

    /// Refresh, then push the pending captures to origin/mobile.
    func sync() async {
        guard isSetUp else { return }
        let ok = await run("Syncing…") {
            try await self.core.sync(key: Keychain.load(), author: self.author)
        }
        noteNetworkResult(ok)
        await reloadAll()
    }

    private func noteNetworkResult(_ ok: Bool) {
        if ok || !isOffline {
            consecutiveFailures = 0
            nextAutoFetch = Date().addingTimeInterval(Self.minAutoFetchInterval)
        } else {
            let delays = Self.offlineBackoff
            nextAutoFetch = Date().addingTimeInterval(delays[min(consecutiveFailures, delays.count - 1)])
            consecutiveFailures += 1
        }
    }

    // MARK: Captures

    func save(_ draft: CaptureDraft) async -> Bool {
        if draft.id == nil { KeywordsCache.remember(draft.keyword) }
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
        let ok = await run("Deleting…") { try await self.core.delete(captureID: id, author: self.author) }
        if !ok { actionError = lastError }
        await reloadAll()
    }

    /// Changes an entry's TODO keyword ("" for none), committed locally
    /// at once and pushed on the next Sync.
    func setState(of entry: Entry, to keyword: String) async {
        let ok = await run("Saving…") {
            try await self.core.setState(entryID: entry.id, keyword: keyword, author: self.author)
        }
        if !ok { actionError = lastError }
        await reloadAll()
    }

    /// Saves an edit form for `entry`; only fields that changed are
    /// written. Returns whether it worked (the form shows lastError
    /// otherwise).
    func edit(_ entry: Entry, to form: CaptureDraft) async -> Bool {
        let ok = await run("Saving…") {
            try await self.core.editEntry(entryID: entry.id, form: form, author: self.author)
        }
        await reloadAll()
        return ok
    }

    /// Every configured keyword, done ones included (editing can set any).
    var allKeywords: [KeywordOption] {
        status.keywords.map { KeywordOption(name: $0.name, color: $0.color) }
    }

    /// Forgets the dropped state changes once they've been shown.
    func dismissDropped() async {
        try? await core.dismissDropped()
        await reloadStatus()
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
            KeywordsCache.save(captureKeywords)
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

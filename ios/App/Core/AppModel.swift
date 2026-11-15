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
    private(set) var outline: OutlineResult?
    private(set) var tags: [String] = []
    private(set) var busy: String?        // what's running, for the Sync button
    var lastError: String?                // the latest failure, shown until dismissed or replaced
    /// A failure of something the user just did (a state change, a
    /// delete), shown as an alert right away rather than only on the
    /// Sync button's icon.
    var actionError: String?

    /// The tab showing.
    var tab: AppTab = .outline
    /// An entry ID for the Outline to reveal (expand to, open and scroll
    /// to); it clears it once done.
    var focus: String?

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

    /// Whether the Outline shows the entry `id` (not one in a file it
    /// leaves out, or closed and hidden).
    func outlineShows(_ id: String) -> Bool {
        guard let outline else { return false }
        func find(_ entries: [Entry]) -> Bool {
            entries.contains { $0.id == id || find($0.children ?? []) }
        }
        return (outline.files + outline.reference).contains { find($0.entries) }
    }

    /// Switches to the Outline with `entry` revealed and its details open.
    func showInOutline(_ entry: Entry) {
        focus = entry.id
        tab = .outline
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

    /// A blank capture (or new outline entry), starting with the keyword
    /// last used for one.
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

    /// Saves an edit to a still-pending capture (made here or from the
    /// share sheet), which lives only in the queue until it's merged.
    func updateCapture(_ draft: CaptureDraft) async -> Bool {
        let ok = await run("Saving…") {
            try await self.core.update(draft, author: self.author)
        }
        await reloadAll()
        return ok
    }

    /// Deletes `entry` with everything under it, committed locally at
    /// once — or, for a capture not merged yet, takes it out of the
    /// queue. Like `addEntry`, `deleted` runs in the turn that publishes
    /// the reloaded outline, for a view to renumber the IDs it holds. A
    /// failure is shown as an alert.
    func deleteEntry(_ entry: Entry, deleted: () -> Void) async {
        let ok = await run("Deleting…") {
            if let id = entry.captureId {
                try await self.core.delete(captureID: id, author: self.author)
            } else {
                try await self.core.deleteEntry(entryID: entry.id, author: self.author)
            }
        }
        if ok, let o = try? await core.outline(showClosed: showClosed) {
            outline = o
            deleted()
        }
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

    /// Adds a new entry to `file` at `place`, committed locally at once.
    /// Entry IDs are index paths, so adding one renumbers the siblings
    /// after it: `placed` gets the new entry's ID in the same main-actor
    /// turn that publishes the reloaded outline, so a view can renumber
    /// the IDs it holds without a frame showing the wrong ones. Returns
    /// whether it worked (the form shows lastError otherwise).
    func addEntry(_ draft: CaptureDraft, file: String, at place: EntryPlace, placed: (String) -> Void) async -> Bool {
        KeywordsCache.remember(draft.keyword)
        var newID = ""
        let ok = await run("Saving…") {
            let core = self.core, author = self.author
            switch place {
            case .first(let parent):
                newID = try await core.addEntry(draft, file: file, parentID: parent, last: false, author: author)
            case .last(let parent):
                newID = try await core.addEntry(draft, file: file, parentID: parent, last: true, author: author)
            }
        }
        if ok, let o = try? await core.outline(showClosed: showClosed) {
            outline = o
            placed(newID)
        }
        await reloadAll()
        return ok
    }

    /// Moves `entry`, with its subtree, one level deeper (delta 1: under
    /// the entry above it) or shallower (-1: after its parent), as
    /// desktop ">>"/"<<" do, committed locally at once. Like `addEntry`,
    /// `placed` gets the entry's new ID in the turn that publishes the
    /// reloaded outline. A failure is shown as an alert.
    func shiftEntry(_ entry: Entry, by delta: Int, placed: (String) -> Void) async {
        var newID = ""
        let ok = await run("Saving…") {
            newID = try await self.core.shiftEntry(entryID: entry.id, delta: delta, author: self.author)
        }
        if ok, let o = try? await core.outline(showClosed: showClosed) {
            outline = o
            placed(newID)
        }
        if !ok { actionError = lastError }
        await reloadAll()
    }

    /// Moves `entry`, with its subtree, among its siblings: right before
    /// the sibling `anchor` or, with `after`, right after its subtree.
    /// Like `shiftEntry`, `placed` gets the entry's new ID in the turn
    /// that publishes the reloaded outline. A failure is shown as an
    /// alert.
    func moveEntry(_ entry: Entry, beside anchor: Entry, after: Bool, placed: (String) -> Void) async {
        var newID = ""
        let ok = await run("Saving…") {
            newID = try await self.core.moveEntry(entryID: entry.id, anchorID: anchor.id, after: after, author: self.author)
        }
        if ok, let o = try? await core.outline(showClosed: showClosed) {
            outline = o
            placed(newID)
        }
        if !ok { actionError = lastError }
        await reloadAll()
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

/// MainTabs' tabs.
enum AppTab: Hashable {
    case outline, agenda
}

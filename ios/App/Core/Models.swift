import Foundation

// Codable mirrors of mobilecore/dto.go. Field names are a contract with
// the Go side — change both together.

struct Entry: Codable, Identifiable, Hashable {
    var id: String
    var file: String
    var ancestors: [String]
    var level: Int
    var keyword: String
    var done: Bool
    var priority: String
    var title: String
    var tags: [String]
    var scheduled: String
    var deadline: String
    var closed: String
    var properties: [Property]
    var body: String
    var children: [Entry]?
    var pending: Bool?
    var captureId: String?
    var pendingState: Bool?
    var pendingNew: Bool?
    var scheduledInput: String?
    var deadlineInput: String?

    var isPending: Bool { pending ?? false }
    /// Its state was changed on this phone and isn't merged yet.
    var hasPendingState: Bool { pendingState ?? false }
    /// It was added on this phone (in the outline) and isn't merged yet.
    var isNew: Bool { pendingNew ?? false }
    /// Reference material has no TODO state.
    var isReference: Bool { file.hasPrefix("reference/") }

    /// The IDs of this entry's ancestors, outermost first: "work.org:0.2.1"
    /// has ancestors "work.org:0" and "work.org:0.2".
    var ancestorIDs: [String] {
        guard let colon = id.lastIndex(of: ":") else { return [] }
        let file = id[..<colon]
        let parts = id[id.index(after: colon)...].split(separator: ".")
        guard parts.count > 1 else { return [] }
        return (1..<parts.count).map { n in file + ":" + parts.prefix(n).joined(separator: ".") }
    }

    /// "work.org › Project › Parent", for showing where an entry lives.
    var place: String {
        ([file] + ancestors).joined(separator: " › ")
    }
}

struct Property: Codable, Hashable {
    var key: String
    var value: String
}

struct AgendaItem: Codable, Hashable, Identifiable {
    var entry: Entry
    var label: String
    var date: String
    var repeater: String
    var missed: Int
    var id: String { entry.id + "|" + label }
}

struct AgendaSection: Codable, Hashable, Identifiable {
    var name: String
    var items: [AgendaItem]?
    var id: String { name }
}

struct AgendaResult: Codable {
    var today: String
    var sections: [AgendaSection]
}

struct OutlineFile: Codable, Hashable, Identifiable {
    var file: String
    var entries: [Entry]
    var id: String { file }
}

struct OutlineResult: Codable {
    var files: [OutlineFile]
    var reference: [OutlineFile]
}

struct SearchResult: Codable {
    var query: String
    var entries: [Entry]
}

struct Keyword: Codable, Hashable {
    var name: String
    var color: String
    var bold: Bool
    var done: Bool
}

/// A state change made on the phone that couldn't be applied because its
/// entry changed on the desktop first.
struct DroppedChange: Codable, Hashable {
    var title: String
    var from: String
    var to: String
    var reason: String
    /// A new entry, dropped because the entry it went under changed.
    var added: Bool?
    /// A demote (1) or promote (-1), rather than a keyword change.
    var move: Int?
}

/// A server's SSH host key (mobilecore's hostKeyJSON).
struct HostKeyInfo: Codable, Equatable {
    var host: String
    var key: String
    var fingerprint: String
}

struct StatusResult: Codable {
    var cloned: Bool
    var repoURL: String
    var baseBranch: String
    var hostKey: HostKeyInfo
    var inboxFile: String
    var lastFetch: String
    var lastPush: String
    var lastError: String
    var lastErrorAt: String
    var pending: Int
    var unpushed: Int
    var keywords: [Keyword]
    var tagColor: String
    var dateColor: String
    var configErrors: [String]
    var pendingStates: Int
    var dropped: [DroppedChange]

    static let empty = StatusResult(cloned: false, repoURL: "", baseBranch: "", hostKey: HostKeyInfo(host: "", key: "", fingerprint: ""), inboxFile: "inbox.org", lastFetch: "", lastPush: "", lastError: "", lastErrorAt: "", pending: 0, unpushed: 0, keywords: [], tagColor: "", dateColor: "", configErrors: [], pendingStates: 0, dropped: [])
}

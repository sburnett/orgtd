import Foundation

/// One capture, in the JSON shape of Go's capture.Capture (the queue's
/// on-disk format). Dates are "YYYY-MM-DD" or "YYYY-MM-DD HH:MM" strings.
struct CaptureDraft: Codable, Equatable {
    var id: String?
    var createdAt: Date = Date()
    var keyword: String = ""
    var title: String = ""
    var body: String = ""
    var tags: [String] = []
    var scheduled: String = ""
    var deadline: String = ""

    enum CodingKeys: String, CodingKey {
        case id, createdAt, keyword, title, body, tags, scheduled, deadline
    }

    init(id: String? = nil, createdAt: Date = Date(), keyword: String = "", title: String = "", body: String = "", tags: [String] = [], scheduled: String = "", deadline: String = "") {
        self.id = id
        self.createdAt = createdAt
        self.keyword = keyword
        self.title = title
        self.body = body
        self.tags = tags
        self.scheduled = scheduled
        self.deadline = deadline
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decodeIfPresent(String.self, forKey: .id)
        if let s = try c.decodeIfPresent(String.self, forKey: .createdAt), let d = CaptureDraft.parseDate(s) {
            createdAt = d
        }
        keyword = try c.decodeIfPresent(String.self, forKey: .keyword) ?? ""
        title = try c.decodeIfPresent(String.self, forKey: .title) ?? ""
        body = try c.decodeIfPresent(String.self, forKey: .body) ?? ""
        tags = try c.decodeIfPresent([String].self, forKey: .tags) ?? []
        scheduled = try c.decodeIfPresent(String.self, forKey: .scheduled) ?? ""
        deadline = try c.decodeIfPresent(String.self, forKey: .deadline) ?? ""
    }

    func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encodeIfPresent(id, forKey: .id)
        // With the device's UTC offset, so CREATED shows local wall time.
        try c.encode(CaptureDraft.formatter.string(from: createdAt), forKey: .createdAt)
        try c.encode(keyword, forKey: .keyword)
        try c.encode(title, forKey: .title)
        try c.encode(body, forKey: .body)
        try c.encode(tags, forKey: .tags)
        try c.encode(scheduled, forKey: .scheduled)
        try c.encode(deadline, forKey: .deadline)
    }

    static var formatter: ISO8601DateFormatter {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime]
        f.timeZone = .current
        return f
    }

    static func parseDate(_ s: String) -> Date? {
        if let d = formatter.date(from: s) { return d }
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return f.date(from: s)
    }

    func json() throws -> String {
        String(decoding: try JSONEncoder().encode(self), as: UTF8.self)
    }

    var trimmedTitle: String { title.trimmingCharacters(in: .whitespacesAndNewlines) }

    /// Writes this capture to the incoming directory for the app to pick
    /// up — the share extension's only write. Atomic, one file per
    /// capture, named by its ID.
    func writeIncoming(to dir: URL = AppGroup.incomingURL) throws {
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        var draft = self
        let id = draft.id ?? UUID().uuidString
        draft.id = id
        let data = try JSONEncoder().encode(draft)
        try data.write(to: dir.appendingPathComponent(id + ".json"), options: .atomic)
    }
}

/// "YYYY-MM-DD" (or with " HH:MM") <-> Date, for the capture sheet's
/// date pickers.
enum OrgDate {
    private static func formatter(_ format: String) -> DateFormatter {
        let f = DateFormatter()
        f.locale = Locale(identifier: "en_US_POSIX")
        f.timeZone = .current
        f.dateFormat = format
        return f
    }

    static func string(from date: Date, withTime: Bool = false) -> String {
        formatter(withTime ? "yyyy-MM-dd HH:mm" : "yyyy-MM-dd").string(from: date)
    }

    static func date(from s: String) -> Date? {
        formatter("yyyy-MM-dd HH:mm").date(from: s) ?? formatter("yyyy-MM-dd").date(from: s)
    }

    /// "Wed Nov 18" style, for showing a stored date.
    static func display(_ s: String) -> String {
        guard let d = date(from: s) else { return s }
        let f = DateFormatter()
        f.setLocalizedDateFormatFromTemplate(s.count > 10 ? "EEE MMM d HH:mm" : "EEE MMM d")
        return f.string(from: d)
    }
}

/// The cached tag list the app writes for the share extension.
enum TagsCache {
    static func load() -> [String] {
        guard let data = try? Data(contentsOf: AppGroup.tagsCacheURL) else { return [] }
        return (try? JSONDecoder().decode([String].self, from: data)) ?? []
    }

    static func save(_ tags: [String]) {
        guard let data = try? JSONEncoder().encode(tags) else { return }
        try? data.write(to: AppGroup.tagsCacheURL, options: .atomic)
    }
}

/// A keyword the capture form offers: a configured, not-done keyword and
/// its config color.
struct KeywordOption: Codable, Hashable, Identifiable {
    var name: String
    var color: String
    var id: String { name }
}

/// The keyword list the app writes for the share extension (which can't
/// read the config itself), and the keyword last captured with, which
/// new captures start with.
enum KeywordsCache {
    private static var url: URL { AppGroup.containerURL.appendingPathComponent("keywords-cache.json") }

    static func load() -> [KeywordOption] {
        guard let data = try? Data(contentsOf: url) else { return [] }
        return (try? JSONDecoder().decode([KeywordOption].self, from: data)) ?? []
    }

    static func save(_ keywords: [KeywordOption]) {
        guard let data = try? JSONEncoder().encode(keywords) else { return }
        try? data.write(to: url, options: .atomic)
    }

    private static var lastURL: URL { AppGroup.containerURL.appendingPathComponent("last-keyword") }

    /// The last keyword used, if it's still one of `options`; "" otherwise.
    static func last(in options: [KeywordOption]) -> String {
        let name = (try? String(contentsOf: lastURL, encoding: .utf8)) ?? ""
        return options.contains { $0.name == name } ? name : ""
    }

    static func remember(_ keyword: String) {
        try? keyword.write(to: lastURL, atomically: true, encoding: .utf8)
    }
}

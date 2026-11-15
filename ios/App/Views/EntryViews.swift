import SwiftUI

/// One entry as a list row: keyword, title, tags, and optional detail
/// (an agenda date, where it lives).
struct EntryRow: View {
    @Environment(AppModel.self) private var model
    let entry: Entry
    var detail: String? = nil
    var showPlace = false

    var body: some View {
        VStack(alignment: .leading, spacing: 3) {
            HStack(alignment: .firstTextBaseline, spacing: 6) {
                if !entry.keyword.isEmpty {
                    KeywordBadge(name: entry.keyword)
                }
                Text(entry.title)
                    .strikethrough(entry.done)
                    .foregroundStyle(entry.done ? .secondary : .primary)
            }
            if !entry.tags.isEmpty || detail != nil {
                HStack(spacing: 8) {
                    if let detail {
                        Text(detail).foregroundStyle(model.dateColor)
                    }
                    if !entry.tags.isEmpty {
                        Text(entry.tags.map { ":" + $0 }.joined() + ":")
                            .foregroundStyle(model.tagColor)
                    }
                }
                .font(.caption)
            }
            if showPlace {
                Text(entry.place).font(.caption2).foregroundStyle(.secondary).lineLimit(1)
            }
        }
    }
}

struct KeywordBadge: View {
    @Environment(AppModel.self) private var model
    let name: String

    var body: some View {
        Text(name)
            .font(.caption.monospaced())
            .fontWeight(model.keyword(name)?.bold == true ? .bold : .regular)
            .foregroundStyle(model.keywordColor(name))
    }
}

/// Read-only detail: keyword, title, tags, dates, properties, body with
/// tappable links.
struct EntryDetailView: View {
    @Environment(AppModel.self) private var model
    let entry: Entry

    var body: some View {
        List {
            Section {
                VStack(alignment: .leading, spacing: 6) {
                    if !entry.keyword.isEmpty || !entry.priority.isEmpty {
                        HStack {
                            if !entry.keyword.isEmpty { KeywordBadge(name: entry.keyword) }
                            if !entry.priority.isEmpty { Text("[#\(entry.priority)]").font(.caption.monospaced()) }
                        }
                    }
                    Text(OrgText.attributed(entry.title))
                        .font(.title3.weight(.semibold))
                        .strikethrough(entry.done)
                    if !entry.tags.isEmpty {
                        Text(entry.tags.map { ":" + $0 }.joined() + ":")
                            .font(.callout)
                            .foregroundStyle(model.tagColor)
                    }
                    Text(entry.place).font(.caption).foregroundStyle(.secondary)
                }
                .textSelection(.enabled)
            }
            if entry.isPending {
                Section {
                    Label("Captured on this phone; not merged into \(model.status.baseBranch.isEmpty ? "main" : model.status.baseBranch) yet", systemImage: "clock")
                        .font(.callout)
                }
            }
            let dates = [("Scheduled", entry.scheduled), ("Deadline", entry.deadline), ("Closed", entry.closed)].filter { !$0.1.isEmpty }
            if !dates.isEmpty {
                Section("Dates") {
                    ForEach(dates, id: \.0) { label, value in
                        LabeledContent(label) {
                            Text(value).foregroundStyle(model.dateColor)
                        }
                    }
                }
            }
            if !entry.body.isEmpty {
                Section("Notes") {
                    Text(OrgText.attributed(entry.body))
                        .font(.body)
                        .textSelection(.enabled)
                }
            }
            if !entry.properties.isEmpty {
                Section("Properties") {
                    ForEach(entry.properties, id: \.key) { p in
                        VStack(alignment: .leading) {
                            Text(p.key).font(.caption.monospaced()).foregroundStyle(.secondary)
                            Text(OrgText.attributed(p.value)).font(.callout).textSelection(.enabled)
                        }
                    }
                }
            }
            if let children = entry.children, !children.isEmpty {
                Section("Children") {
                    ForEach(children) { child in
                        NavigationLink(value: child) { EntryRow(entry: child) }
                    }
                }
            }
        }
        .navigationTitle(entry.title)
        .navigationBarTitleDisplayMode(.inline)
    }
}

/// Org markup → AttributedString: [[url][description]] and [[url]] links
/// and bare URLs become tappable links; everything else is plain text.
enum OrgText {
    private static let linkRegex = try! NSRegularExpression(pattern: #"\[\[([^\]]+)\](?:\[([^\]]*)\])?\]"#)

    static func attributed(_ text: String) -> AttributedString {
        var out = AttributedString()
        let ns = text as NSString
        var cursor = 0
        for m in linkRegex.matches(in: text, range: NSRange(location: 0, length: ns.length)) {
            if m.range.location > cursor {
                out += plain(ns.substring(with: NSRange(location: cursor, length: m.range.location - cursor)))
            }
            let target = ns.substring(with: m.range(at: 1))
            let label = m.range(at: 2).location != NSNotFound ? ns.substring(with: m.range(at: 2)) : target
            var piece = AttributedString(label)
            if let url = URL(string: target), url.scheme != nil {
                piece.link = url
            }
            out += piece
            cursor = m.range.location + m.range.length
        }
        if cursor < ns.length {
            out += plain(ns.substring(from: cursor))
        }
        return out
    }

    /// Plain text with bare URLs linked.
    private static func plain(_ s: String) -> AttributedString {
        var a = AttributedString(s)
        guard let detector = try? NSDataDetector(types: NSTextCheckingResult.CheckingType.link.rawValue) else { return a }
        for m in detector.matches(in: s, range: NSRange(s.startIndex..., in: s)) {
            guard let url = m.url, let r = Range(m.range, in: s),
                  let lower = AttributedString.Index(r.lowerBound, within: a),
                  let upper = AttributedString.Index(r.upperBound, within: a) else { continue }
            a[lower..<upper].link = url
        }
        return a
    }
}

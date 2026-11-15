import SwiftUI

/// One entry as a compact list row, the same in every list (inbox,
/// outline, agenda, search): keyword, title on one line (truncated — the
/// detail has it whole), an optional "(N)" hidden-count, tags on the
/// right when the whole line fits (otherwise the title gets the room),
/// and a clock for a capture that's still pending. `subtitle` adds a
/// small second line (an agenda date, where a search result lives).
struct CompactEntryRow: View {
    @Environment(AppModel.self) private var model
    let entry: Entry
    var hiddenCount: Int? = nil
    var subtitle: Text? = nil

    var body: some View {
        VStack(alignment: .leading, spacing: 1) {
            ViewThatFits(in: .horizontal) {
                line(withTags: true)
                line(withTags: false)
            }
            if let subtitle {
                subtitle
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
        }
        .padding(.vertical, subtitle == nil ? 0 : 3)
    }

    private func line(withTags: Bool) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: 5) {
            if !entry.keyword.isEmpty {
                Text(entry.keyword)
                    .font(.caption.monospaced())
                    .fontWeight(model.keyword(entry.keyword)?.bold == true ? .bold : .regular)
                    .foregroundStyle(model.keywordColor(entry.keyword))
                    .fixedSize()
            }
            Text(entry.title)
                .font(.body)
                .strikethrough(entry.done)
                .foregroundStyle(entry.done ? .secondary : .primary)
                .lineLimit(1)
                .truncationMode(.tail)
            if let hiddenCount {
                Text("(\(hiddenCount))")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .fixedSize()
            }
            Spacer(minLength: 4)
            if withTags && !entry.tags.isEmpty {
                Text(entry.tags.map { ":" + $0 }.joined() + ":")
                    .font(.caption)
                    .foregroundStyle(model.tagColor)
                    .fixedSize()
            }
            if entry.isPending {
                Image(systemName: "clock.badge")
                    .font(.caption)
                    .foregroundStyle(.orange)
                    .accessibilityLabel("Pending")
            }
        }
    }
}

extension View {
    /// The shared spacing of every entry list: compact rows and sections.
    func compactEntryList() -> some View {
        environment(\.defaultMinListRowHeight, 32)
            .listSectionSpacing(.compact)
    }

    /// The shared insets of a (non-tree) entry row.
    func compactEntryRowInsets() -> some View {
        listRowInsets(EdgeInsets(top: 0, leading: 16, bottom: 0, trailing: 12))
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

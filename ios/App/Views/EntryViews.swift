import SwiftUI

/// One entry as a compact list row, the same in every list (outline,
/// agenda, search): keyword, title, an optional "(N)"
/// hidden-count, tags on the right when they fit beside the whole title
/// (otherwise the title gets the room), and a clock for anything still
/// pending. The title is cut to one line, or — `expanded`, for an open
/// row — wraps onto as many as it needs. `subtitle` adds a small second
/// line (an agenda date, where a search result lives).
///
/// Opening a row only changes how many lines the same title view may
/// use: the line's views and the tags-fit decision stay the same (see
/// HeadlineLayout), so nothing cross-fades, and the first line doesn't
/// move — it has fixed padding rather than being centred.
struct CompactEntryRow: View {
    @Environment(AppModel.self) private var model
    let entry: Entry
    var hiddenCount: Int? = nil
    var subtitle: Text? = nil
    var expanded = false

    var body: some View {
        VStack(alignment: .leading, spacing: 1) {
            HeadlineLayout {
                if !entry.keyword.isEmpty {
                    Text(entry.keyword)
                        .font(.caption.monospaced())
                        .fontWeight(model.keyword(entry.keyword)?.bold == true ? .bold : .regular)
                        .foregroundStyle(model.keywordColor(entry.keyword))
                        .fixedSize()
                        .layoutValue(key: HeadlineRole.self, value: .lead)
                }
                Text(entry.title)
                    .font(.body)
                    .strikethrough(entry.done)
                    .foregroundStyle(entry.done ? .secondary : .primary)
                    .lineLimit(expanded ? nil : 1)
                    .truncationMode(.tail)
                    .layoutValue(key: HeadlineRole.self, value: .title)
                if let hiddenCount {
                    Text("(\(hiddenCount))")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .fixedSize()
                        .layoutValue(key: HeadlineRole.self, value: .afterTitle)
                }
                if !entry.tags.isEmpty {
                    Text(entry.tags.map { ":" + $0 }.joined() + ":")
                        .font(.caption)
                        .foregroundStyle(model.tagColor)
                        .fixedSize()
                        .layoutValue(key: HeadlineRole.self, value: .tags)
                }
                // Like the desktop outline's compact planning dates; rows
                // with a subtitle (agenda, search) already show their date.
                if subtitle == nil, !entry.isReference, let due = OrgDate.compact(entry.deadline) {
                    Text("D:" + due)
                        .font(.caption.monospacedDigit())
                        .foregroundStyle(model.dateColor)
                        .fixedSize()
                        .accessibilityLabel("Deadline \(OrgDate.display(entry.deadlineInput ?? due))")
                        .layoutValue(key: HeadlineRole.self, value: .trail)
                }
                if entry.isPending || entry.hasPendingState {
                    Image(systemName: "clock.badge")
                        .font(.caption)
                        .foregroundStyle(.orange)
                        .accessibilityLabel("Pending")
                        .layoutValue(key: HeadlineRole.self, value: .trail)
                }
            }
            .clipped() // tags that don't fit are parked outside the line
            if let subtitle {
                subtitle
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
        }
        .padding(.vertical, subtitle == nil ? 5 : 4)
    }
}

/// What each piece of a headline line is, for HeadlineLayout.
enum HeadlineRole: LayoutValueKey {
    static let defaultValue = Part.title
    enum Part { case lead, title, afterTitle, tags, trail }
}

/// Lays out a headline line: lead (keyword) and title from the left, the
/// after-title pieces right after the title, tags and trail at the right
/// edge, all on the title's first baseline. The tags are shown only if
/// they fit beside the title's whole single-line width — decided the same
/// way however many lines the title may use, so opening a row never
/// changes which pieces show. The title gets the rest of the width,
/// truncating or wrapping within it.
struct HeadlineLayout: Layout {
    var spacing: CGFloat = 5
    var gap: CGFloat = 8 // between the title block and the tags

    private struct Plan {
        var titleWidth: CGFloat
        var showTags: Bool
        var height: CGFloat
        var baseline: CGFloat
    }

    private func plan(_ width: CGFloat, _ subviews: Subviews) -> Plan {
        func ideal(_ part: HeadlineRole.Part) -> CGFloat {
            subviews.filter { $0[HeadlineRole.self] == part }
                .reduce(0) { $0 + $1.sizeThatFits(.unspecified).width + spacing }
        }
        let fixed = ideal(.lead) + ideal(.afterTitle) + ideal(.trail)
        let tags = ideal(.tags)
        let title = subviews.first { $0[HeadlineRole.self] == .title }
        let titleIdeal = title?.sizeThatFits(.unspecified).width ?? 0
        let showTags = tags > 0 && titleIdeal + fixed + gap + tags <= width
        let titleWidth = max(0, width - fixed - (showTags ? gap + tags : 0))
        var height: CGFloat = 0
        var baseline: CGFloat = 0
        if let title {
            let d = title.dimensions(in: ProposedViewSize(width: titleWidth, height: nil))
            height = d.height
            baseline = d[.firstTextBaseline]
        }
        // Lift the line if a smaller piece's baseline sits lower than the
        // title's would allow (never in practice; keeps it safe).
        for v in subviews where v[HeadlineRole.self] != .title {
            let d = v.dimensions(in: .unspecified)
            baseline = max(baseline, d[.firstTextBaseline])
        }
        for v in subviews {
            let d = v[HeadlineRole.self] == .title
                ? v.dimensions(in: ProposedViewSize(width: titleWidth, height: nil))
                : v.dimensions(in: .unspecified)
            height = max(height, baseline - d[.firstTextBaseline] + d.height)
        }
        return Plan(titleWidth: titleWidth, showTags: showTags, height: height, baseline: baseline)
    }

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        let width = proposal.width ?? 10_000
        return CGSize(width: width, height: plan(width, subviews).height)
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        let p = plan(bounds.width, subviews)
        func put(_ v: LayoutSubview, x: CGFloat, size: ProposedViewSize) {
            let d = v.dimensions(in: size)
            v.place(at: CGPoint(x: x, y: bounds.minY + p.baseline - d[.firstTextBaseline]), proposal: size)
        }
        var x = bounds.minX
        for v in subviews where v[HeadlineRole.self] == .lead {
            put(v, x: x, size: .unspecified)
            x += v.sizeThatFits(.unspecified).width + spacing
        }
        for v in subviews where v[HeadlineRole.self] == .title {
            let size = ProposedViewSize(width: p.titleWidth, height: nil)
            put(v, x: x, size: size)
            // Pieces that follow the title start after its last line's
            // text when it's one line, or after its block when it wraps.
            x += min(v.sizeThatFits(size).width, p.titleWidth) + spacing
        }
        for v in subviews where v[HeadlineRole.self] == .afterTitle {
            put(v, x: x, size: .unspecified)
            x += v.sizeThatFits(.unspecified).width + spacing
        }
        var right = bounds.maxX
        for v in subviews.reversed() where v[HeadlineRole.self] == .trail {
            let w = v.sizeThatFits(.unspecified).width
            right -= w
            put(v, x: right, size: .unspecified)
            right -= spacing
        }
        for v in subviews.reversed() where v[HeadlineRole.self] == .tags {
            let w = v.sizeThatFits(.unspecified).width
            if p.showTags {
                right -= w
                put(v, x: right, size: .unspecified)
                right -= spacing
            } else {
                put(v, x: bounds.maxX + 1_000, size: .unspecified) // parked out of sight
            }
        }
    }
}

/// The height of an entry's one-line row, and of the box its line sits
/// in when the row is open (see EntryListRow).
let entryLineHeight: CGFloat = 32

/// The height of an EntryListRow's headline — taller than one line when
/// an open row's title wraps — so the outline can size the chevron
/// beside it to match.
struct HeadlineHeightKey: PreferenceKey {
    static let defaultValue: CGFloat = 0
    static func reduce(value: inout CGFloat, nextValue: () -> CGFloat) {
        value = max(value, nextValue())
    }
}

extension View {
    /// The shared spacing of every entry list: compact rows and sections.
    func compactEntryList() -> some View {
        environment(\.defaultMinListRowHeight, entryLineHeight)
            .listSectionSpacing(.compact)
    }

    /// The shared insets of a (non-tree) entry row.
    func compactEntryRowInsets() -> some View {
        listRowInsets(EdgeInsets(top: 0, leading: 16, bottom: 0, trailing: 12))
    }
}

/// A list row's background: the list's own — or, while the row's entry
/// is open, a step lighter (the secondary background) and tinted, so the
/// entry and its details read as one block, unlike the rows (its
/// children) below it. On a plain list's black (dark mode) a tint alone
/// barely shows. The details and form boxes inside go a step lighter
/// again (`entryPanelBackground`). In the outline it also draws `guides`:
/// a thin vertical line at each x (from the row's leading edge), one per
/// level of nesting, which join up across rows into the tree's branches.
/// `plain` is for a plain-style list (the outline), whose rows sit on the
/// screen's own background rather than a grouped list's card.
/// `openLeading` keeps the open highlight off the row's first points —
/// in the outline, its indentation and chevron, so the highlight covers
/// the entry alone and doesn't read as selecting its whole subtree.
struct EntryRowBackground: View {
    var isOpen: Bool
    var guides: [CGFloat] = []
    var plain = false
    var openLeading: CGFloat = 0

    var body: some View {
        ZStack {
            Color(plain ? .systemBackground : .secondarySystemGroupedBackground)
            if isOpen {
                ZStack {
                    Color(.secondarySystemBackground)
                    Color.accentColor.opacity(0.12)
                }
                .padding(.leading, openLeading)
            }
            if !guides.isEmpty {
                Canvas { ctx, size in
                    for x in guides {
                        ctx.fill(Path(CGRect(x: x - 0.5, y: 0, width: 1, height: size.height)),
                                 with: .color(Color(.tertiaryLabel)))
                    }
                }
            }
        }
    }
}

/// The box an open row's details or edit form sits in: a step lighter
/// than the open row's own background (see EntryRowBackground), with its
/// text fields a step back down.
let entryPanelBackground = Color(.tertiarySystemBackground)

extension View {
    /// The row background of an entry row (see EntryRowBackground). It
    /// must be on the row itself, not nested inside it, to take effect.
    func entryRowBackground(isOpen: Bool, guides: [CGFloat] = [], plain: Bool = false, openLeading: CGFloat = 0) -> some View {
        listRowBackground(EntryRowBackground(isOpen: isOpen, guides: guides, plain: plain, openLeading: openLeading))
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

/// An entry's row in any list: the compact line, which a tap opens into
/// the entry's details right where it is — in context, under its parent
/// and above its children — and which the pencil there turns into its
/// edit form. `open` is the list's one open row (opening another closes
/// it); `key` tells this row apart (the agenda can show an entry twice).
/// The edit form also deletes the entry (after a confirmation;
/// `deleted` runs once it's gone). A long press runs `longPress`, if given.
struct EntryListRow: View {
    @Environment(AppModel.self) private var model
    let entry: Entry
    let key: String
    @Binding var open: String?
    var hiddenCount: Int? = nil
    var subtitle: Text? = nil
    var deleted: () -> Void = {}
    /// Runs on a long press of the line (the Outline's way into
    /// rearranging); nil for none.
    var longPress: (() -> Void)? = nil
    /// Whether a tap on the line opens and closes the details.
    var tappable = true
    /// Whether the details have the state menu and pencil — not where
    /// swipe actions offer them (the Outline).
    var detailActions = true
    /// Runs on a tap of the line instead of opening the details, if given.
    var tapped: (() -> Void)? = nil
    @State private var editing = false

    private var isOpen: Bool { open == key }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            // The line's first line keeps its place when the details open
            // below it (and the title wraps): it has fixed padding, never
            // centring — the list centres a row shorter than its minimum
            // height, but not a taller one.
            // Identical open or closed — nothing about it may change, or
            // SwiftUI cross-fades it — and kept out of the open/close
            // animation, so only the details below appear.
            CompactEntryRow(entry: entry, hiddenCount: hiddenCount, subtitle: subtitle, expanded: isOpen)
                .frame(maxWidth: .infinity, alignment: .topLeading)
                .background { GeometryReader { Color.clear.preference(key: HeadlineHeightKey.self, value: $0.size.height) } }
                .transaction { $0.animation = nil }
                .contentShape(Rectangle())
                .onTapGesture {
                    if let tapped { tapped() } else if tappable { toggle() }
                }
                .gesture(LongPressGesture().onEnded { _ in longPress?() }, including: longPress == nil ? .subviews : .all)
                .accessibilityAddTraits(tappable ? .isButton : [])
                .accessibilityHint(tapped != nil ? "Shows it in the outline" : tappable ? (isOpen ? "Hides details" : "Shows details") : "")
            if isOpen {
                Spacer().frame(height: 4)
                Group {
                    if editing {
                        InlineEntryEditor(entry: entry, deleted: deleted) { withAnimation(.snappy(duration: 0.2)) { editing = false } }
                    } else {
                        InlineEntryDetails(entry: entry, edit: detailActions ? { withAnimation(.snappy(duration: 0.2)) { editing = true } } : nil)
                    }
                }
                .transition(.opacity)
            }
        }
        .padding(.bottom, isOpen ? 8 : 0)
        .onChange(of: isOpen) { _, nowOpen in
            if !nowOpen { editing = false }
        }
    }

    private func toggle() {
        // A tap on the line doesn't throw away an edit in progress.
        guard !editing else { return }
        withAnimation(.snappy(duration: 0.2)) { open = isOpen ? nil : key }
    }
}

/// An open row's details: dates, notes (links tappable), tags,
/// properties (always shown), pending status, and — given `edit` (a
/// list with no swipe actions for them) — the state menu and pencil.
struct InlineEntryDetails: View {
    @Environment(AppModel.self) private var model
    let entry: Entry
    let edit: (() -> Void)?

    /// Whether there's nothing to show but the title.
    private var isEmpty: Bool {
        entry.scheduled.isEmpty && entry.deadline.isEmpty && entry.closed.isEmpty && entry.body.isEmpty
            && entry.tags.isEmpty && entry.properties.isEmpty
            && !entry.isPending && !entry.isNew && !entry.hasPendingState
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            let dates = [("Scheduled", entry.scheduled), ("Deadline", entry.deadline), ("Closed", entry.closed)].filter { !$0.1.isEmpty }
            if !dates.isEmpty {
                VStack(alignment: .leading, spacing: 2) {
                    ForEach(dates, id: \.0) { label, value in
                        (Text(label + " ").foregroundColor(.secondary) + Text(value).foregroundColor(model.dateColor))
                            .font(.footnote)
                    }
                }
            }
            if !entry.body.isEmpty {
                Text(OrgText.attributed(entry.body))
                    .font(.callout)
                    .textSelection(.enabled)
                    .fixedSize(horizontal: false, vertical: true)
            }
            if !entry.tags.isEmpty {
                Text(entry.tags.map { ":" + $0 }.joined() + ":")
                    .font(.footnote)
                    .foregroundStyle(model.tagColor)
            }
            if entry.isPending {
                SwiftUI.Label("Captured on this phone; not merged yet", systemImage: "clock")
                    .font(.footnote).foregroundStyle(.secondary)
            } else if entry.isNew {
                SwiftUI.Label("Added on this phone; not merged yet", systemImage: "clock")
                    .font(.footnote).foregroundStyle(.secondary)
            } else if entry.hasPendingState {
                SwiftUI.Label("Changed on this phone; not merged yet", systemImage: "clock")
                    .font(.footnote).foregroundStyle(.secondary)
            }
            if !entry.properties.isEmpty {
                VStack(alignment: .leading, spacing: 4) {
                    ForEach(entry.properties, id: \.key) { p in
                        (Text(p.key + "  ").font(.caption.monospaced()).foregroundColor(.secondary)
                            + Text(OrgText.attributed(p.value)).font(.caption))
                            .textSelection(.enabled)
                    }
                }
            }
            if isEmpty && edit == nil {
                Text("No details").font(.footnote).foregroundStyle(.secondary)
            }
            if let edit {
                HStack(spacing: 12) {
                    if !entry.isReference {
                        StateMenu(entry: entry) {
                            HStack(spacing: 4) {
                                if entry.keyword.isEmpty {
                                    Text("No state").font(.footnote).foregroundStyle(.secondary)
                                } else {
                                    KeywordBadge(name: entry.keyword)
                                }
                                Image(systemName: "chevron.up.chevron.down").font(.caption2).foregroundStyle(.secondary)
                            }
                            .padding(.horizontal, 10)
                            .padding(.vertical, 5)
                            .background(Capsule().fill(Color.secondary.opacity(0.12)))
                        }
                        .accessibilityLabel("State")
                    }
                    Spacer()
                    Button(action: edit) {
                        Image(systemName: "pencil")
                            .font(.body.weight(.semibold))
                            .frame(width: 36, height: 30)
                    }
                    .buttonStyle(.bordered)
                    .accessibilityLabel("Edit")
                }
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(12)
        .background(RoundedRectangle(cornerRadius: 12).fill(entryPanelBackground))
    }
}

/// An open row's edit form — the capture form's fields, in place, and a
/// delete button. Only fields that change are written back
/// (mobilecore.EditEntry); a still-pending capture is edited as a
/// capture. `deleted` runs once a delete is made, for the list to
/// renumber the IDs it holds.
struct InlineEntryEditor: View {
    @Environment(AppModel.self) private var model
    let entry: Entry
    var deleted: () -> Void = {}
    let done: () -> Void
    @State private var confirmingDelete = false
    @State private var draft: CaptureDraft
    @State private var loading: Bool
    @State private var saving = false
    @State private var error: String?

    init(entry: Entry, deleted: @escaping () -> Void = {}, done: @escaping () -> Void) {
        self.entry = entry
        self.deleted = deleted
        self.done = done
        _loading = State(initialValue: entry.captureId != nil)
        _draft = State(initialValue: CaptureDraft(
            keyword: entry.keyword,
            title: entry.title,
            body: entry.body,
            tags: entry.tags,
            scheduled: entry.scheduledInput ?? "",
            deadline: entry.deadlineInput ?? ""
        ))
    }

    var body: some View {
        InlineForm(draft: $draft, keywords: entry.isReference ? nil : entry.captureId != nil ? model.captureKeywords : model.allKeywords,
                   loading: loading, saving: saving, error: error, cancel: done,
                   delete: { confirmingDelete = true }) {
            Task { await save() }
        }
        .confirmationDialog("Delete “\(entry.title)”?", isPresented: $confirmingDelete, titleVisibility: .visible) {
            Button("Delete", role: .destructive) {
                Task { await model.deleteEntry(entry) { withAnimation(.snappy(duration: 0.2)) { deleted() } } }
            }
        } message: {
            Text(entry.deleteMessage)
        }
        .task {
            // A pending capture is still only in the queue: edit it there.
            guard let id = entry.captureId else { return }
            if let capture = await model.pendingCapture(id: id) { draft = capture }
            loading = false
        }
    }

    private func save() async {
        saving = true
        defer { saving = false }
        let ok = entry.captureId != nil ? await model.updateCapture(draft) : await model.edit(entry, to: draft)
        if ok {
            done()
        } else {
            error = model.lastError
        }
    }
}

/// The fields of an inline form (the capture form's, laid out for a list
/// row) with Cancel and Save under them. `keywords` nil hides the keyword
/// picker (reference material has no state). The title takes the focus
/// when `focusTitle` is set.
struct InlineForm: View {
    @Environment(AppModel.self) private var model
    @Binding var draft: CaptureDraft
    let keywords: [KeywordOption]?
    var loading = false
    let saving: Bool
    let error: String?
    var focusTitle = false
    let cancel: () -> Void
    /// Shows a delete button (an existing entry's form), which runs this.
    var delete: (() -> Void)? = nil
    let save: () -> Void
    @FocusState private var titleFocused: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            if loading {
                ProgressView().frame(maxWidth: .infinity)
            } else {
                if let keywords {
                    KeywordPicker(keyword: $draft.keyword, options: keywords)
                }
                TextField("Title", text: $draft.title, axis: .vertical)
                    .focused($titleFocused)
                    .inlineField()
                TextField("Notes", text: $draft.body, axis: .vertical)
                    .lineLimit(2...10)
                    .inlineField()
                VStack(alignment: .leading, spacing: 6) {
                    TagField(tags: $draft.tags, knownTags: model.tags)
                }
                .inlineField()
                DateField(label: "Scheduled", value: $draft.scheduled, parseDate: { try await model.parseDate($0) })
                DateField(label: "Deadline", value: $draft.deadline, parseDate: { try await model.parseDate($0) })
            }
            if let error {
                Text(error).font(.footnote).foregroundStyle(.red)
            }
            HStack {
                Button("Cancel", action: cancel)
                    .buttonStyle(.bordered)
                if let delete {
                    Button(action: delete) {
                        Image(systemName: "trash")
                    }
                    .buttonStyle(.bordered)
                    .tint(.red)
                    .disabled(saving || loading || model.busy != nil)
                    .accessibilityLabel("Delete")
                }
                Spacer()
                Button("Save", action: save)
                    .buttonStyle(.borderedProminent)
                    .disabled(draft.trimmedTitle.isEmpty || saving || loading)
            }
        }
        .padding(12)
        .background(RoundedRectangle(cornerRadius: 12).fill(entryPanelBackground))
        .onAppear { if focusTitle { titleFocused = true } }
    }
}

extension View {
    /// A text field's look inside an inline editor.
    func inlineField() -> some View {
        padding(.horizontal, 10)
            .padding(.vertical, 7)
            .background(RoundedRectangle(cornerRadius: 8).fill(Color(.secondarySystemBackground)))
    }
}

/// A menu of every configured keyword (and None) that sets `entry`'s
/// state, labelled by `label`.
struct StateMenu<Label: View>: View {
    @Environment(AppModel.self) private var model
    let entry: Entry
    @ViewBuilder var label: () -> Label
    @State private var closing: ClosingRequest?

    var body: some View {
        Menu {
            StateMenuItems(entry: entry) { keyword in
                if model.closes(entry, to: keyword) {
                    closing = ClosingRequest(entry: entry, keyword: keyword)
                } else {
                    Task { await model.setState(of: entry, to: keyword) }
                }
            }
        } label: {
            label()
        }
        .disabled(entry.isReference || model.busy != nil)
        .closingNotePrompt($closing)
    }
}

/// The items of a state menu; `choose` gets the picked keyword.
struct StateMenuItems: View {
    @Environment(AppModel.self) private var model
    let entry: Entry
    let choose: (String) -> Void

    var body: some View {
        ForEach(model.status.keywords, id: \.name) { k in
            Button {
                choose(k.name)
            } label: {
                if k.name == entry.keyword {
                    SwiftUI.Label(k.name, systemImage: "checkmark")
                } else {
                    Text(k.name)
                }
            }
            .disabled(k.name == entry.keyword)
        }
        Button {
            choose("")
        } label: {
            if entry.keyword.isEmpty {
                SwiftUI.Label("None", systemImage: "checkmark")
            } else {
                Text("None")
            }
        }
        .disabled(entry.keyword.isEmpty)
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

extension Entry {
    /// What deleting the entry does, for a delete confirmation.
    var deleteMessage: String {
        let n = descendants ?? 0
        let under = n == 0 ? "" : " This also deletes the \(n == 1 ? "entry" : "\(n) entries") under it."
        if captureId != nil {
            return "It was captured on this phone and isn't merged yet, so it's gone for good." + under
        }
        return "It's removed from \(file) when you merge." + under
    }
}

/// Closing `entry` with `keyword` (a done state), waiting on a closing
/// note (see ClosingNotePrompt).
struct ClosingRequest: Identifiable {
    let entry: Entry
    let keyword: String
    var id: String { entry.id + "|" + keyword }
}

/// Asks for an optional closing note before closing an entry, as desktop
/// "r" does: confirming logs the note (or none, left empty) and sets the
/// state; Cancel leaves the state as it was. `closed` runs once it's set.
struct ClosingNotePrompt: ViewModifier {
    @Environment(AppModel.self) private var model
    @Binding var request: ClosingRequest?
    var closed: () -> Void = {}
    @State private var note = ""

    func body(content: Content) -> some View {
        content.alert("Closing note", isPresented: Binding(get: { request != nil }, set: { if !$0 { request = nil } }),
                      presenting: request) { r in
            TextField("Optional", text: $note)
            Button("Cancel", role: .cancel) { note = "" }
            Button("Mark \(r.keyword)") {
                let note = note
                self.note = ""
                Task { await model.setState(of: r.entry, to: r.keyword, note: note) }
                closed()
            }
        } message: { r in
            Text("“\(r.entry.title)” — add a note, or leave it empty.")
        }
    }
}

extension View {
    /// Asks for a closing note while `request` is set (see
    /// ClosingNotePrompt).
    func closingNotePrompt(_ request: Binding<ClosingRequest?>, closed: @escaping () -> Void = {}) -> some View {
        modifier(ClosingNotePrompt(request: request, closed: closed))
    }
}

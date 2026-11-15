import SwiftUI

/// The outline: each file's headlines as an expandable tree, a separate
/// Reference section, and search. A row's chevron expands or collapses
/// its children; tapping the row opens its details (and edit form) in
/// place. Long-pressing a row enters arranging, until Done: there,
/// dragging a row moves it among its siblings, swiping it right or left
/// moves it one level deeper or shallower (as desktop ">>" and "<<"
/// do), and every list visible starts and ends with a "New entry" row,
/// which opens a form for the new entry right where it will go. A file
/// with no entries showing always has a "New entry" row. Picking a
/// search result opens the outline at that entry.
struct OutlineView: View {
    @Environment(AppModel.self) private var model
    @State private var query = ""
    @State private var results: [Entry] = []
    @State private var expanded: Set<String> = []
    @State private var target: String?
    @State private var open: String?
    /// Whether rows are being moved and added (see above).
    @State private var arranging = false

    var body: some View {
        @Bindable var model = model
        ScrollViewReader { proxy in
            List {
                if !query.isEmpty {
                    if results.isEmpty {
                        ContentUnavailableView.search(text: query)
                    }
                    ForEach(results) { entry in
                        Button {
                            reveal(entry)
                        } label: {
                            CompactEntryRow(entry: entry, subtitle: Text(entry.place))
                        }
                        .buttonStyle(.plain)
                        .compactEntryRowInsets()
                    }
                } else if let outline = model.outline {
                    ForEach(outline.files) { file in
                        Section(file.file) { nodes(file.entries, file: file.file) }
                    }
                    if !outline.reference.isEmpty {
                        ForEach(outline.reference) { file in
                            Section("Reference · \(file.file.replacingOccurrences(of: "reference/", with: ""))") {
                                nodes(file.entries, file: file.file)
                            }
                        }
                    }
                }
            }
            .onChange(of: target) { _, id in
                guard let id else { return }
                withAnimation { proxy.scrollTo(id, anchor: .center) }
                target = nil
            }
        }
        .compactEntryList()
        .navigationTitle(arranging ? "Arrange" : "Outline")
        .sensoryFeedback(.impact(weight: .medium), trigger: arranging) { _, now in now }
        .searchable(text: $query, prompt: "Search titles, tags, notes")
        .task(id: query) {
            results = query.isEmpty ? [] : await model.search(query)
        }
        .toolbar {
            if arranging {
                ToolbarItem(placement: .topBarTrailing) {
                    Button("Done") {
                        withAnimation(.snappy(duration: 0.2)) {
                            arranging = false
                            open = nil
                        }
                    }
                    .fontWeight(.semibold)
                }
            } else {
                browsingToolbar
            }
        }
        .refreshable { await model.refresh() }
        .task(id: model.focus) {
            guard let id = model.focus else { return }
            model.focus = nil
            reveal(id)
        }
    }

    /// The toolbar outside arranging: show closed, expand/collapse all,
    /// and Sync.
    @ToolbarContentBuilder
    private var browsingToolbar: some ToolbarContent {
            ToolbarItemGroup(placement: .topBarTrailing) {
                Button {
                    model.showClosed.toggle()
                } label: {
                    Image(systemName: model.showClosed ? "eye" : "eye.slash")
                }
                .accessibilityLabel(model.showClosed ? "Hide closed items" : "Show closed items")

                // Collapses everything if anything is open; otherwise
                // opens everything. The icon shows what a tap will do.
                let expandable = allExpandable
                let anyOpen = !expanded.isDisjoint(with: expandable)
                Button {
                    withAnimation(.snappy(duration: 0.2)) {
                        expanded = anyOpen ? [] : expandable
                    }
                } label: {
                    Image(systemName: anyOpen ? "rectangle.compress.vertical" : "rectangle.expand.vertical")
                }
                .accessibilityLabel(anyOpen ? "Collapse all" : "Expand all")
                .disabled(!query.isEmpty || expandable.isEmpty)
            }
            // Sync in a capsule of its own, as on every other tab, so it
            // sits in exactly the same spot: grouped with the buttons above
            // it would share their capsule and be inset differently.
            if #available(iOS 26.0, *) {
                ToolbarSpacer(.fixed, placement: .topBarTrailing)
            }
            ToolbarItem(placement: .topBarTrailing) { SyncButton() }
    }

    /// Every entry in the outline that has children, i.e. everything
    /// "expand all" opens.
    private var allExpandable: Set<String> {
        guard let outline = model.outline else { return [] }
        var ids = Set<String>()
        func walk(_ entries: [Entry]) {
            for e in entries {
                guard let children = e.children, !children.isEmpty else { continue }
                ids.insert(e.id)
                walk(children)
            }
        }
        for file in outline.files + outline.reference { walk(file.entries) }
        return ids
    }

    /// The rows currently visible in `file`'s top-level `entries`: every
    /// entry whose ancestors are all expanded, in outline order, with the
    /// "New entry" rows (see TreeRow.visible).
    private func nodes(_ entries: [Entry], file: String) -> some View {
        let rows = TreeRow.visible(entries, file: file, arranging: arranging, expanded: expanded)
        return ForEach(rows) { row in
            switch row.kind {
            case .entry(let entry):
                TreeRowView(entry: entry, depth: row.depth, isExpanded: expanded.contains(entry.id), arranging: arranging, open: $open,
                            arrange: startArranging,
                            deleted: { renumber(Renumbering(file: file, removed: entry.id, inserted: nil)) }) {
                    withAnimation(.snappy(duration: 0.2)) {
                        if expanded.contains(entry.id) { expanded.remove(entry.id) } else { expanded.insert(entry.id) }
                    }
                }
                // Swipe right: deeper (">>"); swipe left: shallower ("<<").
                .swipeActions(edge: .leading, allowsFullSwipe: true) {
                    if arranging && entry.indexPath.last != 0 {
                        Button { shift(entry, by: 1) } label: {
                            Label("Indent", systemImage: "arrow.right.to.line")
                        }
                        .tint(.indigo)
                    }
                }
                .swipeActions(edge: .trailing, allowsFullSwipe: true) {
                    if arranging && entry.indexPath.count > 1 {
                        Button { shift(entry, by: -1) } label: {
                            Label("Outdent", systemImage: "arrow.left.to.line")
                        }
                        .tint(.indigo)
                    }
                }
                .moveDisabled(!arranging)
            case .add(let slot):
                AddEntryRow(slot: slot, depth: row.depth, open: $open) { newID in
                    renumber(Renumbering(file: slot.file, removed: nil, inserted: newID))
                    expanded.formUnion(Entry.ancestorIDs(of: newID))
                    // Arranging, the next thing is likely another move or
                    // addition, not the new entry's details.
                    open = arranging ? nil : newID
                }
                .moveDisabled(true)
            }
        }
        .onMove(perform: arranging ? { from, to in move(from, to: to, in: rows) } : nil)
    }

    /// Enters arranging, closing any open details or form.
    private func startArranging() {
        withAnimation(.snappy(duration: 0.2)) {
            arranging = true
            open = nil
        }
    }

    /// A drag of `rows[from]` to just before `rows[to]` (or the end):
    /// moves the entry among its siblings, before the first one at or
    /// after the drop, or after the last. A drop outside its parent's
    /// list does nothing, and the row goes back.
    private func move(_ from: IndexSet, to: Int, in rows: [TreeRow]) {
        guard let i = from.first, case .entry(let entry) = rows[i].kind else { return }
        let path = entry.indexPath
        func sibling(_ k: Int) -> Entry? {
            guard case .entry(let e) = rows[k].kind, e.indexPath.count == path.count,
                  e.indexPath.dropLast() == path.dropLast() else { return nil }
            return e
        }
        let siblingRows = rows.indices.filter { sibling($0) != nil }
        guard siblingRows.count > 1, let first = siblingRows.first, let last = siblingRows.last else { return }
        // The parent's list: from its first sibling to the end of its
        // last one's subtree, and the "New entry" rows around it.
        var lo = first, hi = last + 1
        while hi < rows.count, rows[hi].depth > rows[last].depth { hi += 1 }
        if lo > 0, case .add = rows[lo - 1].kind { lo -= 1 }
        if hi < rows.count, case .add = rows[hi].kind { hi += 1 }
        guard (lo...hi).contains(to) else { return }
        let order = siblingRows.compactMap(sibling)
        guard let k = order.firstIndex(where: { $0.id == entry.id }) else { return }
        let anchor: Entry, after: Bool
        if let next = siblingRows.first(where: { $0 >= to && $0 != i }).flatMap(sibling) {
            (anchor, after) = (next, false)
            if k + 1 < order.count && order[k + 1].id == anchor.id { return } // where it already is
        } else {
            guard let end = order.last(where: { $0.id != entry.id }) else { return }
            (anchor, after) = (end, true)
            if k == order.count - 1 { return }
        }
        Task {
            await model.moveEntry(entry, beside: anchor, after: after) { newID in
                withAnimation(.snappy(duration: 0.2)) {
                    renumber(Renumbering(file: entry.file, removed: entry.id, inserted: newID))
                }
            }
        }
    }

    /// Moves `entry` one level, keeping the same rows expanded (and its
    /// details open, if they were) under their new IDs, and its new
    /// parent expanded so it stays in sight.
    private func shift(_ entry: Entry, by delta: Int) {
        Task {
            await model.shiftEntry(entry, by: delta) { newID in
                withAnimation(.snappy(duration: 0.2)) {
                    let wasOpen = open == entry.id
                    renumber(Renumbering(file: entry.file, removed: entry.id, inserted: newID))
                    expanded.formUnion(Entry.ancestorIDs(of: newID))
                    if wasOpen { open = newID }
                }
            }
        }
    }

    /// Entry IDs are index paths, so adding, moving or deleting an entry
    /// renumbers others: this carries the expanded rows, and the open
    /// one, over (dropping a deleted entry's).
    private func renumber(_ r: Renumbering) {
        expanded = Set(expanded.compactMap(r.apply))
        if let o = open { open = r.apply(o) }
    }

    /// Leaves search, expands the entry's ancestors, and scrolls to it.
    private func reveal(_ entry: Entry) { reveal(entry.id) }

    /// Leaves search and arranging, expands the entry `id`'s ancestors,
    /// opens its details, and scrolls to it.
    private func reveal(_ id: String) {
        arranging = false
        expanded.formUnion(Entry.ancestorIDs(of: id))
        open = id
        query = ""
        Task { @MainActor in
            // Let the outline rows exist before scrolling to one.
            try? await Task.sleep(for: .milliseconds(150))
            target = id
        }
    }
}

/// Where a new entry goes: `place` in `file`.
struct AddSlot: Hashable {
    let file: String
    let place: EntryPlace

    /// Distinct from every entry ID (those never start with "+"). A
    /// parent's ID has a ":" after the file name, so a file's top level
    /// can't collide with an entry's children.
    var key: String {
        switch place {
        case .first(let parent): "+first:" + (parent ?? file)
        case .last(let parent): "+last:" + (parent ?? file)
        }
    }
}

/// How one change to a file's tree renumbers its entry IDs ("work.org:0.2"
/// is index path [0, 2]): an entry (with its subtree) taken out at
/// `removed`, if any, and an entry — the same one (a move), or a new one
/// — put in at `inserted`, its ID once the change is made, if any (none
/// for a delete).
struct Renumbering {
    let file: String
    let removed: String?
    let inserted: String?

    /// `id` as it is after the change, or nil if it was deleted. Other
    /// files' IDs, and keys that aren't entry IDs, come back as they were.
    func apply(_ id: String) -> String? {
        guard let (f, path) = Entry.parse(id), f == file else { return id }
        let at = inserted.flatMap(Entry.parse)?.path
        var p = path
        if let removed, let (_, r) = Entry.parse(removed) {
            if p.starts(with: r) {
                guard let at else { return nil } // deleted
                return Entry.id(file, at + p.dropFirst(r.count)) // in the moved subtree
            }
            // Later siblings of the removed entry, and their subtrees, move up.
            if p.count >= r.count, p.prefix(r.count - 1).elementsEqual(r.dropLast()), p[r.count - 1] > r[r.count - 1] {
                p[r.count - 1] -= 1
            }
        }
        // The inserted entry's later siblings, and their subtrees, move down.
        if let at {
            let parent = at.dropLast()
            if p.count >= at.count, p.prefix(parent.count).elementsEqual(parent), p[parent.count] >= at[parent.count] {
                p[parent.count] += 1
            }
        }
        return Entry.id(file, p)
    }
}

extension Entry {
    /// The entry's index path: "work.org:0.2" → [0, 2].
    var indexPath: [Int] { Entry.parse(id)?.path ?? [] }

    /// An entry ID's file and index path, or nil if it isn't one.
    static func parse(_ id: String) -> (file: String, path: [Int])? {
        guard let colon = id.lastIndex(of: ":") else { return nil }
        let parts = id[id.index(after: colon)...].split(separator: ".", omittingEmptySubsequences: false)
        let path = parts.compactMap { Int($0) }
        guard !path.isEmpty, path.count == parts.count else { return nil }
        return (String(id[..<colon]), path)
    }

    static func id<S: Sequence>(_ file: String, _ path: S) -> String where S.Element == Int {
        file + ":" + path.map(String.init).joined(separator: ".")
    }

    /// The IDs of `id`'s ancestors, outermost first.
    static func ancestorIDs(of id: String) -> [String] {
        guard let (file, path) = parse(id) else { return [] }
        return (1..<max(path.count, 1)).map { Entry.id(file, path.prefix($0)) }
    }
}

/// One visible line of the tree: an entry, or a new entry's row.
struct TreeRow: Identifiable {
    enum Kind {
        case entry(Entry)
        case add(AddSlot)
    }

    let kind: Kind
    let depth: Int

    var id: String {
        switch kind {
        case .entry(let e): e.id
        case .add(let slot): slot.key
        }
    }

    /// The visible rows of `file`'s list `entries` (the children of
    /// `parent`, or the top level): each entry, then, if it's expanded,
    /// its children's list. Arranging, each list starts and ends with a
    /// "New entry" row; a file with no entries showing has just one.
    static func visible(_ entries: [Entry], file: String, arranging: Bool, expanded: Set<String>, parent: String? = nil, depth: Int = 0) -> [TreeRow] {
        func add(_ p: EntryPlace) -> TreeRow { TreeRow(kind: .add(AddSlot(file: file, place: p)), depth: depth) }
        if entries.isEmpty {
            return parent == nil ? [add(.last(parent: nil))] : []
        }
        var rows: [TreeRow] = arranging ? [add(.first(parent: parent))] : []
        for e in entries {
            rows.append(TreeRow(kind: .entry(e), depth: depth))
            if let children = e.children, !children.isEmpty, expanded.contains(e.id) {
                rows += visible(children, file: file, arranging: arranging, expanded: expanded, parent: e.id, depth: depth + 1)
            }
        }
        if arranging { rows.append(add(.last(parent: parent))) }
        return rows
    }
}

/// The leading part every tree line shares: the row's inset, its depth's
/// indentation, and the column where a chevron goes.
private let treeRowInset: CGFloat = 6
private let treeIndent: CGFloat = 14
private let chevronWidth: CGFloat = 28
/// The space between the chevron's tile and the entry beside it, so a
/// tap near the boundary isn't ambiguous.
private let chevronGap: CGFloat = 6

/// A row at `depth`'s guide lines: one under each ancestor's chevron, so
/// every expanded entry's subtree hangs from a line that runs down past
/// its last row (a new child's form included).
private func treeGuides(depth: Int) -> [CGFloat] {
    (0..<depth).map { d in treeRowInset + CGFloat(d) * treeIndent + chevronWidth / 2 }
}

/// A chevron's button: a faint rounded tile behind the arrow, darker
/// while pressed, so its tap target reads as separate from the entry's
/// own (which opens details) beside it.
private struct ChevronButtonStyle: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .background {
                RoundedRectangle(cornerRadius: 7, style: .continuous)
                    .fill(configuration.isPressed ? Color(.systemFill) : Color(.tertiarySystemFill))
                    .padding(.vertical, 4)
                    .padding(.horizontal, 1)
            }
    }
}

/// One line of the tree: indentation, a chevron for an entry with
/// children, then the shared EntryListRow (whose details open in place).
private struct TreeRowView: View {
    @Environment(AppModel.self) private var model
    let entry: Entry
    let depth: Int
    let isExpanded: Bool
    /// Arranging: taps don't open details, a long press starts a drag
    /// (not `arrange`), and a grip shows the row can be dragged.
    let arranging: Bool
    @Binding var open: String?
    let arrange: () -> Void
    let deleted: () -> Void
    let toggle: () -> Void

    /// The headline's height beside the chevron, which grows to cover it.
    @State private var headlineHeight: CGFloat = entryLineHeight

    private var children: [Entry] { entry.children ?? [] }

    var body: some View {
        HStack(alignment: .top, spacing: chevronGap) {
            if children.isEmpty {
                // A full line tall, like the chevron: a row shorter than
                // the list's minimum height is centred, and stops being
                // centred when its details open, which would nudge the
                // title up.
                Color.clear.frame(width: chevronWidth, height: entryLineHeight)
            } else {
                Button(action: toggle) {
                    Image(systemName: "chevron.right")
                        .font(.footnote.weight(.semibold))
                        .foregroundStyle(.secondary)
                        .rotationEffect(.degrees(isExpanded ? 90 : 0))
                        .animation(.snappy(duration: 0.2), value: isExpanded) // rotates for children, not for details
                        // The arrow stays on the first line; the button
                        // runs down the whole (possibly wrapped) title.
                        .frame(width: chevronWidth, height: entryLineHeight)
                        .frame(height: max(entryLineHeight, headlineHeight), alignment: .top)
                        .contentShape(Rectangle())
                }
                .buttonStyle(ChevronButtonStyle())
                .accessibilityLabel(isExpanded ? "Collapse" : "Expand")
                .transaction { $0.animation = nil } // still while the row's details open
            }
            EntryListRow(entry: entry, key: entry.id, open: $open, hiddenCount: children.isEmpty || isExpanded ? nil : Self.descendants(children),
                         deleted: deleted, longPress: arranging ? nil : arrange, tappable: !arranging)
            if arranging {
                Image(systemName: "line.3.horizontal")
                    .font(.footnote.weight(.semibold))
                    .foregroundStyle(.tertiary)
                    .frame(height: entryLineHeight)
                    .accessibilityHidden(true)
            }
        }
        .onPreferenceChange(HeadlineHeightKey.self) { headlineHeight = $0 }
        .padding(.leading, CGFloat(depth) * treeIndent)
        .listRowInsets(EdgeInsets(top: 0, leading: treeRowInset, bottom: 0, trailing: 12))
        .entryRowBackground(isOpen: open == entry.id, guides: treeGuides(depth: depth))
    }

    /// How many entries a collapsed row hides, like the desktop's "(N)".
    static func descendants(_ entries: [Entry]) -> Int {
        entries.reduce(0) { $0 + 1 + descendants($1.children ?? []) }
    }
}

/// A new entry's row: a "New entry" line and, while it's open, a blank
/// form in place (like an entry's edit form) that adds the entry at
/// `slot`. Quiet until tapped: an empty file's row, and, while
/// arranging, those at the start and end of every list. `added` gets the
/// new entry's ID.
private struct AddEntryRow: View {
    @Environment(AppModel.self) private var model
    let slot: AddSlot
    let depth: Int
    @Binding var open: String?
    let added: (String) -> Void
    @State private var draft = CaptureDraft()
    @State private var saving = false
    @State private var error: String?

    private var isOpen: Bool { open == slot.key }
    private var isReference: Bool { slot.file.hasPrefix("reference/") }

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(spacing: 6) {
                Image(systemName: "plus")
                    .font(.caption.weight(.semibold))
                Text("New entry")
                    .font(.subheadline)
            }
            .foregroundStyle(isOpen ? AnyShapeStyle(.tint) : AnyShapeStyle(.tertiary))
            .frame(maxWidth: .infinity, minHeight: entryLineHeight, alignment: .leading)
            .contentShape(Rectangle())
            .onTapGesture { toggle() }
            .accessibilityAddTraits(.isButton)
            .accessibilityLabel("New entry")
            if isOpen {
                InlineForm(draft: $draft, keywords: isReference ? nil : model.captureKeywords,
                           saving: saving, error: error, focusTitle: true, cancel: close) {
                    Task { await save() }
                }
                .transition(.opacity)
            }
        }
        .padding(.leading, CGFloat(depth) * treeIndent + chevronWidth + chevronGap)
        .padding(.bottom, isOpen ? 8 : 0)
        .listRowInsets(EdgeInsets(top: 0, leading: treeRowInset, bottom: 0, trailing: 12))
        .entryRowBackground(isOpen: isOpen, guides: treeGuides(depth: depth))
        .onChange(of: isOpen) { _, nowOpen in
            if nowOpen { reset() }
        }
        .onAppear { if isOpen { reset() } }
    }

    private func toggle() {
        // A tap on the line doesn't throw away a form in progress.
        guard !isOpen else { return }
        withAnimation(.snappy(duration: 0.2)) { open = slot.key }
    }

    private func close() {
        withAnimation(.snappy(duration: 0.2)) { open = nil }
    }

    private func reset() {
        draft = isReference ? CaptureDraft() : model.newCapture()
        error = nil
    }

    private func save() async {
        saving = true
        defer { saving = false }
        draft.createdAt = Date()
        let ok = await model.addEntry(draft, file: slot.file, at: slot.place) { newID in
            withAnimation(.snappy(duration: 0.2)) { added(newID) }
        }
        if !ok { error = model.lastError }
    }
}

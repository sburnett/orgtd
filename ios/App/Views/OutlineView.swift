import SwiftUI

/// The outline: each file's headlines as an expandable tree, a separate
/// Reference section, and search. A row's chevron expands or collapses
/// its children; tapping the row opens its details (and edit form) in
/// place, and swiping it right or left moves it one level deeper or
/// shallower, as desktop ">>" and "<<" do. Every visible list — a file's top level, or an expanded
/// entry's children — starts and ends with a "New entry" row, which
/// opens a form for adding an entry right there. Picking a search result
/// opens the outline at that entry.
struct OutlineView: View {
    @Environment(AppModel.self) private var model
    @State private var query = ""
    @State private var results: [Entry] = []
    @State private var expanded: Set<String> = []
    @State private var target: String?
    @State private var open: String?

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
        .navigationTitle("Outline")
        .searchable(text: $query, prompt: "Search titles, tags, notes")
        .task(id: query) {
            results = query.isEmpty ? [] : await model.search(query)
        }
        .toolbar {
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
        .refreshable { await model.refresh() }
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
    /// entry whose ancestors are all expanded, in outline order, with each
    /// list's "New entry" rows.
    private func nodes(_ entries: [Entry], file: String) -> some View {
        ForEach(TreeRow.visible(entries, in: AddSlot(file: file, parentID: nil, last: false), expanded: expanded)) { row in
            switch row.kind {
            case .entry(let entry):
                TreeRowView(entry: entry, depth: row.depth, isExpanded: expanded.contains(entry.id), open: $open) {
                    withAnimation(.snappy(duration: 0.2)) {
                        if expanded.contains(entry.id) { expanded.remove(entry.id) } else { expanded.insert(entry.id) }
                    }
                }
                // Swipe right: deeper (">>"); swipe left: shallower ("<<").
                .swipeActions(edge: .leading, allowsFullSwipe: true) {
                    if entry.indexPath.last != 0 {
                        Button { shift(entry, by: 1) } label: {
                            Label("Indent", systemImage: "arrow.right.to.line")
                        }
                        .tint(.indigo)
                    }
                }
                .swipeActions(edge: .trailing, allowsFullSwipe: true) {
                    if entry.indexPath.count > 1 {
                        Button { shift(entry, by: -1) } label: {
                            Label("Outdent", systemImage: "arrow.left.to.line")
                        }
                        .tint(.indigo)
                    }
                }
            case .add(let slot):
                AddEntryRow(slot: slot, depth: row.depth, open: $open) { newID in
                    renumber(Renumbering(file: slot.file, removed: nil, inserted: newID))
                    open = newID
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

    /// Entry IDs are index paths, so adding or moving an entry renumbers
    /// others: this carries the expanded rows, and the open one, over.
    private func renumber(_ r: Renumbering) {
        expanded = Set(expanded.map(r.apply))
        if let o = open { open = r.apply(o) }
    }

    /// Leaves search, expands the entry's ancestors, and scrolls to it.
    private func reveal(_ entry: Entry) {
        expanded.formUnion(entry.ancestorIDs)
        open = entry.id
        query = ""
        Task { @MainActor in
            // Let the outline rows exist before scrolling to one.
            try? await Task.sleep(for: .milliseconds(150))
            target = entry.id
        }
    }
}

/// Where a new entry goes: first or last among `parentID`'s children,
/// or among `file`'s top-level entries when it's nil.
struct AddSlot: Hashable {
    let file: String
    let parentID: String?
    let last: Bool

    /// Distinct from every entry ID (those never start with "+").
    var key: String { "+\(last ? "last" : "first"):" + (parentID ?? file + ":") }
}

/// How one change to a file's tree renumbers its entry IDs ("work.org:0.2"
/// is index path [0, 2]): an entry (with its subtree) taken out at
/// `removed`, if any, and an entry — the same one, or a new one — put in
/// at `inserted`, its ID once the change is made.
struct Renumbering {
    let file: String
    let removed: String?
    let inserted: String

    /// `id` as it is after the change. Other files' IDs, and keys that
    /// aren't entry IDs, come back as they were.
    func apply(_ id: String) -> String {
        guard let (f, path) = Entry.parse(id), f == file, let (_, at) = Entry.parse(inserted) else { return id }
        var p = path
        if let removed, let (_, r) = Entry.parse(removed) {
            if p.starts(with: r) {
                return Entry.id(file, at + p.dropFirst(r.count)) // in the moved subtree
            }
            // Later siblings of the removed entry, and their subtrees, move up.
            if p.count >= r.count, p.prefix(r.count - 1).elementsEqual(r.dropLast()), p[r.count - 1] > r[r.count - 1] {
                p[r.count - 1] -= 1
            }
        }
        // The inserted entry's later siblings, and their subtrees, move down.
        let parent = at.dropLast()
        if p.count >= at.count, p.prefix(parent.count).elementsEqual(parent), p[parent.count] >= at[parent.count] {
            p[parent.count] += 1
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

/// One visible line of the tree: an entry, or a "New entry" row.
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

    /// The visible rows of the list `entries`, whose "New entry" rows add
    /// at `slot` (first and last): each entry, then, if it's expanded,
    /// its children's list. An empty list has just the one "New entry".
    static func visible(_ entries: [Entry], in slot: AddSlot, expanded: Set<String>, depth: Int = 0) -> [TreeRow] {
        var rows: [TreeRow] = []
        if !entries.isEmpty {
            rows.append(TreeRow(kind: .add(AddSlot(file: slot.file, parentID: slot.parentID, last: false)), depth: depth))
        }
        for e in entries {
            rows.append(TreeRow(kind: .entry(e), depth: depth))
            if let children = e.children, !children.isEmpty, expanded.contains(e.id) {
                rows += visible(children, in: AddSlot(file: slot.file, parentID: e.id, last: false), expanded: expanded, depth: depth + 1)
            }
        }
        rows.append(TreeRow(kind: .add(AddSlot(file: slot.file, parentID: slot.parentID, last: true)), depth: depth))
        return rows
    }
}

/// The leading part every tree line shares: the row's inset, its depth's
/// indentation, and the column where a chevron goes.
private let treeRowInset: CGFloat = 6
private let treeIndent: CGFloat = 14
private let chevronWidth: CGFloat = 28

/// A row at `depth`'s guide lines: one under each ancestor's chevron, so
/// every expanded entry's subtree hangs from a line that runs down past
/// its last row (the "New entry" row ending its children included).
private func treeGuides(depth: Int) -> [CGFloat] {
    (0..<depth).map { d in treeRowInset + CGFloat(d) * treeIndent + chevronWidth / 2 }
}

/// One line of the tree: indentation, a chevron for an entry with
/// children, then the shared EntryListRow (whose details open in place).
private struct TreeRowView: View {
    @Environment(AppModel.self) private var model
    let entry: Entry
    let depth: Int
    let isExpanded: Bool
    @Binding var open: String?
    let toggle: () -> Void

    private var children: [Entry] { entry.children ?? [] }

    var body: some View {
        HStack(alignment: .top, spacing: 2) {
            if children.isEmpty {
                Color.clear.frame(width: chevronWidth, height: 1)
            } else {
                Button(action: toggle) {
                    Image(systemName: "chevron.right")
                        .font(.footnote.weight(.semibold))
                        .foregroundStyle(.secondary)
                        .rotationEffect(.degrees(isExpanded ? 90 : 0))
                        .animation(.snappy(duration: 0.2), value: isExpanded) // rotates for children, not for details
                        .frame(width: chevronWidth, height: entryLineHeight)
                        .contentShape(Rectangle())
                }
                .buttonStyle(.borderless)
                .accessibilityLabel(isExpanded ? "Collapse" : "Expand")
                .transaction { $0.animation = nil } // still while the row's details open
            }
            EntryListRow(entry: entry, key: entry.id, open: $open, hiddenCount: children.isEmpty || isExpanded ? nil : Self.descendants(children))
        }
        .padding(.leading, CGFloat(depth) * treeIndent)
        .listRowInsets(EdgeInsets(top: 0, leading: treeRowInset, bottom: 0, trailing: 12))
        .entryRowBackground(isOpen: open == entry.id, guides: treeGuides(depth: depth))
    }

    /// How many entries a collapsed row hides, like the desktop's "(N)".
    static func descendants(_ entries: [Entry]) -> Int {
        entries.reduce(0) { $0 + 1 + descendants($1.children ?? []) }
    }
}

/// A list's "New entry" row, at its start or end: quiet until tapped,
/// when it opens a blank form in place (like an entry's edit form) that
/// adds the entry there (mobilecore.AddEntry). `added` gets the new
/// entry's ID.
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
            .accessibilityLabel(slot.last ? "New entry at the end" : "New entry at the start")
            if isOpen {
                InlineForm(draft: $draft, keywords: isReference ? nil : model.captureKeywords,
                           saving: saving, error: error, focusTitle: true, cancel: close) {
                    Task { await save() }
                }
                .transition(.opacity)
            }
        }
        .padding(.leading, CGFloat(depth) * treeIndent + chevronWidth + 2)
        .padding(.bottom, isOpen ? 8 : 0)
        .listRowInsets(EdgeInsets(top: 0, leading: treeRowInset, bottom: 0, trailing: 12))
        .entryRowBackground(isOpen: isOpen, guides: treeGuides(depth: depth))
        .onChange(of: isOpen) { _, nowOpen in
            if nowOpen { reset() }
        }
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
        let ok = await model.addEntry(draft, file: slot.file, parentID: slot.parentID, last: slot.last) { newID in
            withAnimation(.snappy(duration: 0.2)) { added(newID) }
        }
        if !ok { error = model.lastError }
    }
}

import SwiftUI

/// The outline: one tree whose top layer is the files (outline files,
/// then reference ones), each expanding to its headlines, and search. A
/// row's chevron expands or collapses its children (a file's name does
/// too); tapping an entry opens its details (and edit form) in place.
/// Long-pressing an entry enters arranging, until Done: there, dragging
/// an entry moves it among its siblings — a top-level one into any
/// file's top level too — swiping it right or left
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
    /// Files whose entries are hidden (files start expanded).
    @State private var collapsedFiles: Set<String> = []
    @State private var target: String?
    @State private var open: String?
    /// Whether rows are being moved and added (see above).
    @State private var arranging = false
    /// The entry the Edit swipe is editing, in a sheet.
    @State private var editingEntry: Entry?
    /// The entry the state swipe asked to change the state of.
    @State private var settingState: Entry?

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
                    nodes(outline)
                }
            }
            .onChange(of: target) { _, id in
                guard let id else { return }
                withAnimation { proxy.scrollTo(id, anchor: .center) }
                target = nil
            }
        }
        .compactEntryList()
        // Edge to edge on the screen's background, not a floating card:
        // it's one list, and the tree needs the width.
        .listStyle(.plain)
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
        .sheet(item: $settingState) { entry in
            StatePickerSheet(entry: entry)
        }
        .sheet(item: $editingEntry) { entry in
            EditEntrySheet(entry: entry) {
                withAnimation(.snappy(duration: 0.2)) { renumber(Renumbering(file: entry.file, removed: entry.id, inserted: nil)) }
            }
        }
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

                // Collapses everything, files included, if anything is
                // open; otherwise opens everything. The icon shows what a
                // tap will do.
                let expandable = allExpandable
                let files = allFiles
                let anyOpen = !expanded.isDisjoint(with: expandable) || !files.isSubset(of: collapsedFiles)
                Button {
                    withAnimation(.snappy(duration: 0.2)) {
                        expanded = anyOpen ? [] : expandable
                        collapsedFiles = anyOpen ? files : []
                    }
                } label: {
                    Image(systemName: anyOpen ? "rectangle.compress.vertical" : "rectangle.expand.vertical")
                }
                .accessibilityLabel(anyOpen ? "Collapse all" : "Expand all")
                .disabled(!query.isEmpty || files.isEmpty)
            }
            // Sync in a capsule of its own, as on every other tab, so it
            // sits in exactly the same spot: grouped with the buttons above
            // it would share their capsule and be inset differently.
            if #available(iOS 26.0, *) {
                ToolbarSpacer(.fixed, placement: .topBarTrailing)
            }
            ToolbarItem(placement: .topBarTrailing) { SyncButton() }
    }

    /// Every file in the outline.
    private var allFiles: Set<String> {
        guard let outline = model.outline else { return [] }
        return Set((outline.files + outline.reference).map(\.file))
    }

    /// Every entry in the outline that has children, i.e. every entry
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

    /// The rows currently visible: each file, and, if it's expanded, every
    /// entry in it whose ancestors are all expanded, in outline order,
    /// with the "New entry" rows (see TreeRow.outline). One list, so a
    /// drag can cross from one file to another.
    private func nodes(_ outline: OutlineResult) -> some View {
        let rows = TreeRow.outline(outline, arranging: arranging, expanded: expanded, collapsedFiles: collapsedFiles)
        return ForEach(rows) { row in
            switch row.kind {
            case .file(let file):
                FileRowView(file: file, isExpanded: !collapsedFiles.contains(file.file)) {
                    withAnimation(.snappy(duration: 0.2)) {
                        if collapsedFiles.contains(file.file) { collapsedFiles.remove(file.file) } else { collapsedFiles.insert(file.file) }
                    }
                }
                .moveDisabled(true)
            case .entry(let entry):
                let file = entry.file
                TreeRowView(entry: entry, depth: row.depth, isExpanded: expanded.contains(entry.id), arranging: arranging, open: $open,
                            arrange: startArranging,
                            deleted: { renumber(Renumbering(file: file, removed: entry.id, inserted: nil)) }) {
                    withAnimation(.snappy(duration: 0.2)) {
                        if expanded.contains(entry.id) { expanded.remove(entry.id) } else { expanded.insert(entry.id) }
                    }
                }
                // Arranging, swipe right: deeper (">>"); swipe left:
                // shallower ("<<"). Otherwise, right: state; left: edit.
                .swipeActions(edge: .leading, allowsFullSwipe: true) {
                    if arranging {
                        if entry.indexPath.last != 0 {
                            Button { shift(entry, by: 1) } label: {
                                Label("Indent", systemImage: "arrow.right.to.line")
                            }
                            .tint(.indigo)
                        }
                    } else if !entry.isReference {
                        // The first is the full swipe's: DONE, by default.
                        ForEach(quickStates(for: entry), id: \.name) { k in
                            Button {
                                Task { await model.setState(of: entry, to: k.name) }
                            } label: {
                                Text(k.name)
                            }
                            .tint(model.keywordColor(k.name))
                        }
                        Button { settingState = entry } label: {
                            Label("More", systemImage: "ellipsis")
                        }
                        .tint(.gray)
                    }
                }
                .swipeActions(edge: .trailing, allowsFullSwipe: true) {
                    if arranging {
                        if entry.indexPath.count > 1 {
                            Button { shift(entry, by: -1) } label: {
                                Label("Outdent", systemImage: "arrow.left.to.line")
                            }
                            .tint(.indigo)
                        }
                    } else {
                        Button { editingEntry = entry } label: {
                            Label("Edit", systemImage: "pencil")
                        }
                        .tint(.orange)
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

    /// The states a right swipe offers as buttons, besides More (the
    /// sheet of them all): for an open entry, the first done keyword then
    /// the first two active ones; for a done entry, the first three
    /// active ones, since reopening is the likely change. Never its
    /// current state — the next one in order takes its place.
    private func quickStates(for entry: Entry) -> [Keyword] {
        let others = model.status.keywords.filter { $0.name != entry.keyword }
        let active = others.filter { !$0.done }
        if model.keyword(entry.keyword)?.done == true {
            return Array(active.prefix(3))
        }
        let done = others.filter(\.done).prefix(1)
        return Array(done) + Array(active.prefix(3 - done.count))
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
    /// list does nothing, and the row goes back. A top-level entry's
    /// siblings are every file's top-level entries (see moveTopLevel).
    private func move(_ from: IndexSet, to: Int, in rows: [TreeRow]) {
        guard let i = from.first, case .entry(let entry) = rows[i].kind else { return }
        let path = entry.indexPath
        if path.count == 1 {
            moveTopLevel(entry, at: i, to: to, in: rows)
            return
        }
        func sibling(_ k: Int) -> Entry? {
            guard case .entry(let e) = rows[k].kind, e.file == entry.file, e.indexPath.count == path.count,
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

    /// A drag of the top-level `entry` (`rows[i]`) to just before
    /// `rows[to]`: into the file whose rows the drop lands among, before
    /// the first of its top-level entries at or after the drop, or after
    /// the last — or, in a collapsed or empty file, at its end.
    private func moveTopLevel(_ entry: Entry, at i: Int, to: Int, in rows: [TreeRow]) {
        func isFile(_ r: TreeRow) -> Bool { if case .file = r.kind { true } else { false } }
        func topLevel(_ k: Int) -> Entry? {
            guard case .entry(let e) = rows[k].kind, e.indexPath.count == 1 else { return nil }
            return e
        }
        guard let fileRow = rows[..<to].lastIndex(where: isFile), case .file(let dest) = rows[fileRow].kind else { return }
        let end = rows[(fileRow + 1)...].firstIndex(where: isFile) ?? rows.count
        let region = (fileRow + 1)..<end
        let others = region.filter { $0 != i && topLevel($0) != nil }
        let anchor: Entry?, after: Bool
        if let next = others.first(where: { $0 >= to }) {
            (anchor, after) = (topLevel(next), false)
        } else if let last = others.last {
            (anchor, after) = (topLevel(last), true)
        } else {
            (anchor, after) = (nil, false)
        }
        if dest.file == entry.file {
            // Where it already is?
            let order = region.compactMap(topLevel)
            guard let anchor, let k = order.firstIndex(where: { $0.id == entry.id }) else { return }
            if !after && k + 1 < order.count && order[k + 1].id == anchor.id { return }
            if after && k == order.count - 1 { return }
        }
        Task {
            await model.moveEntry(entry, toFile: dest.file, beside: anchor, after: after) { newID in
                withAnimation(.snappy(duration: 0.2)) {
                    if dest.file == entry.file {
                        renumber(Renumbering(file: entry.file, removed: entry.id, inserted: newID))
                        return
                    }
                    // Its own expanded rows go with it to the other file.
                    let moved = expanded.filter { $0 == entry.id || $0.hasPrefix(entry.id + ".") }
                        .map { newID + $0.dropFirst(entry.id.count) }
                    renumber(Renumbering(file: entry.file, removed: entry.id, inserted: nil))
                    renumber(Renumbering(file: dest.file, removed: nil, inserted: newID))
                    expanded.formUnion(moved)
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
        if let (file, _) = Entry.parse(id) { collapsedFiles.remove(file) }
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

/// One visible line of the tree: a file, an entry, or a new entry's row.
struct TreeRow: Identifiable {
    enum Kind {
        case file(OutlineFile)
        case entry(Entry)
        case add(AddSlot)
    }

    let kind: Kind
    let depth: Int

    var id: String {
        switch kind {
        case .file(let f): "=" + f.file // never an entry ID or a slot key
        case .entry(let e): e.id
        case .add(let slot): slot.key
        }
    }

    /// The whole outline's visible rows: each file (outline files, then
    /// reference ones) and, unless it's collapsed, its entries' rows a
    /// level in.
    static func outline(_ outline: OutlineResult, arranging: Bool, expanded: Set<String>, collapsedFiles: Set<String>) -> [TreeRow] {
        var rows: [TreeRow] = []
        for f in outline.files + outline.reference {
            rows.append(TreeRow(kind: .file(f), depth: 0))
            if !collapsedFiles.contains(f.file) {
                rows += visible(f.entries, file: f.file, arranging: arranging, expanded: expanded, depth: 1)
            }
        }
        return rows
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
private let treeRowInset: CGFloat = 10
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

/// A file's line at the top of the tree: a chevron (every file has one —
/// an empty file expands to its "New entry" row) and its name, which
/// toggles it too, with how many entries it hides when collapsed.
private struct FileRowView: View {
    let file: OutlineFile
    let isExpanded: Bool
    let toggle: () -> Void

    private var isReference: Bool { file.file.hasPrefix("reference/") }
    private var name: String {
        isReference ? "Reference · " + file.file.dropFirst("reference/".count) : file.file
    }

    var body: some View {
        HStack(alignment: .center, spacing: chevronGap) {
            Button(action: toggle) {
                Image(systemName: "chevron.right")
                    .font(.footnote.weight(.semibold))
                    .foregroundStyle(.secondary)
                    .rotationEffect(.degrees(isExpanded ? 90 : 0))
                    .frame(width: chevronWidth, height: entryLineHeight)
                    .contentShape(Rectangle())
            }
            .buttonStyle(ChevronButtonStyle())
            .accessibilityLabel(isExpanded ? "Collapse \(name)" : "Expand \(name)")
            HStack(spacing: 6) {
                Image(systemName: isReference ? "books.vertical" : "doc.text")
                    .font(.footnote)
                    .foregroundStyle(.secondary)
                Text(name)
                    .font(.body.weight(.semibold)) // an entry's size: the tree's top layer, set apart by weight
                    .lineLimit(1)
                if !isExpanded && !file.entries.isEmpty {
                    Text("(\(TreeRowView.descendants(file.entries)))")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                Spacer(minLength: 0)
            }
            .frame(minHeight: entryLineHeight)
            .contentShape(Rectangle())
            .onTapGesture(perform: toggle)
            .accessibilityAddTraits(.isHeader)
        }
        .listRowInsets(EdgeInsets(top: 0, leading: treeRowInset, bottom: 0, trailing: 12))
        .entryRowBackground(isOpen: false, plain: true)
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
                         deleted: deleted, longPress: arranging ? nil : arrange, tappable: !arranging, detailActions: false)
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
        .entryRowBackground(isOpen: open == entry.id, guides: treeGuides(depth: depth), plain: true)
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
        .entryRowBackground(isOpen: isOpen, guides: treeGuides(depth: depth), plain: true)
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

/// The State swipe's picker: a sheet from the bottom listing every
/// keyword (in its color, the current one checked) and None, which
/// scrolls however many keywords there are. Picking one sets it and
/// closes the sheet.
private struct StatePickerSheet: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    let entry: Entry

    var body: some View {
        NavigationStack {
            List {
                Section {
                    ForEach(model.status.keywords, id: \.name) { k in
                        row(k.name) { KeywordBadge(name: k.name).font(.body.monospaced()) }
                    }
                    row("") { Text("None").foregroundStyle(.secondary) }
                } header: {
                    Text(entry.title).lineLimit(2).textCase(nil)
                }
            }
            .navigationTitle("State")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { dismiss() }
                }
            }
        }
        .presentationDetents([.medium, .large])
        .presentationDragIndicator(.visible)
    }

    private func row<Label: View>(_ keyword: String, @ViewBuilder label: () -> Label) -> some View {
        Button {
            dismiss()
            if keyword != entry.keyword {
                Task { await model.setState(of: entry, to: keyword) }
            }
        } label: {
            HStack {
                label()
                Spacer()
                if keyword == entry.keyword {
                    Image(systemName: "checkmark").foregroundStyle(.tint)
                }
            }
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .accessibilityAddTraits(keyword == entry.keyword ? .isSelected : [])
    }
}

/// The Edit swipe's form: the same fields, Cancel/Save and Delete as an
/// open row's edit form (InlineEntryEditor), in a sheet from the bottom
/// just as tall as the form, so the list still shows above it (it
/// scrolls once the form is taller than the screen allows). In place,
/// the row would grow while the list is still finishing the swipe,
/// which draws it in the wrong spot for most of a second before it
/// snaps into place. `deleted` runs once a delete is made, for the list
/// to renumber the IDs it holds.
private struct EditEntrySheet: View {
    @Environment(\.dismiss) private var dismiss
    let entry: Entry
    let deleted: () -> Void
    /// The form's height, measured, for the sheet's own.
    @State private var formHeight: CGFloat = 400

    var body: some View {
        ScrollView {
            InlineEntryEditor(entry: entry, deleted: {
                deleted()
                dismiss()
            }) {
                dismiss()
            }
            .padding(.horizontal, 16)
            .padding(.top, 24) // clear of the drag indicator
            .padding(.bottom, 8)
            .onGeometryChange(for: CGFloat.self) { $0.size.height } action: { formHeight = $0 }
        }
        .scrollDismissesKeyboard(.interactively)
        .scrollBounceBehavior(.basedOnSize)
        .presentationDetents([.height(formHeight)])
        .presentationDragIndicator(.visible)
    }
}
